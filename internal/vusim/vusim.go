// Package vusim runs scheduled VU1 text on a small latency-aware interpreter.
// It exists to compare the observable behavior of two microprograms.
package vusim

import (
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/ps2build/vcl/internal/dvpas/parse"
)

const memQuads = 1024

type write struct {
	at    int
	apply func(*VM)
	kind  string
	reg   int
	mask  [4]bool
	vals  [4]float32
	vi    bool
}

// Kick records the state captured at an XGKICK.
type Kick struct {
	Addr int
	Mem  [memQuads][4]uint32
}

// VM is the machine state.
type VM struct {
	VF    [32][4]float32
	VI    [16]uint16
	ACC   [4]float32
	Q, P  float32
	I, R  float32
	Mem   [memQuads][4]uint32
	MAC   uint32
	Clip  uint32
	Stat  uint32
	Cycle int

	prog       *parse.Program
	pending    []write
	Kicks      []Kick
	Xtop       uint16
	qReady     int
	pReady     int
	Err        error
	take       bool
	TraceVF    int
	consumer   string
	Done       bool
	Log        map[int][]string
	pcNow      int
	jumpTarget int
	// StaleReads makes a read inside a result's latency window see the old value instead of stalling.
	StaleReads bool
	upperDst   int
	inLower    bool
}

// New prepares a machine for a parsed program.
func New(r io.Reader) (*VM, error) {
	p, err := parse.Parse(r)
	if err != nil {
		return nil, err
	}
	vm := &VM{prog: p, Xtop: 0x40}
	for i := range vm.VF[0] {
		vm.VF[0][i] = 0
	}
	vm.VF[0][3] = 1
	return vm, nil
}

func (vm *VM) schedule(delay int, f func(*VM)) {
	if delay <= 0 {
		f(vm)
		return
	}
	vm.pending = append(vm.pending, write{at: vm.Cycle + delay, apply: f})
}

func (vm *VM) retire() {
	var keep []write
	for _, w := range vm.pending {
		if w.at <= vm.Cycle {
			w.apply(vm)
		} else {
			keep = append(keep, w)
		}
	}
	vm.pending = keep
}

func laneMask(dest string) [4]bool {
	if dest == "" {
		return [4]bool{true, true, true, true}
	}
	var m [4]bool
	for _, c := range dest {
		switch c {
		case 'x':
			m[0] = true
		case 'y':
			m[1] = true
		case 'z':
			m[2] = true
		case 'w':
			m[3] = true
		}
	}
	return m
}

func laneIdx(c byte) int {
	switch c {
	case 'x':
		return 0
	case 'y':
		return 1
	case 'z':
		return 2
	}
	return 3
}

func vfNum(s string) (int, string) {
	s = strings.TrimSpace(s)
	if len(s) < 4 || (s[0] != 'V' && s[0] != 'v') {
		return -1, ""
	}
	n, err := strconv.Atoi(s[2:4])
	if err != nil {
		return -1, ""
	}
	return n, s[4:]
}

func viNum(s string) int {
	s = strings.TrimSpace(s)
	if len(s) < 4 {
		return -1
	}
	n, err := strconv.Atoi(s[2:4])
	if err != nil {
		return -1
	}
	return n
}

func (vm *VM) readVI(n int) int {
	if n <= 0 {
		return 0
	}
	return int(vm.VI[n])
}

// operand value for an FMAC source: returns the four lanes.
func (vm *VM) src(op string, bcLane int) [4]float32 {
	t := strings.TrimSpace(op)
	cons := vm.consumer
	switch strings.ToLower(t) {
	case "q":
		return [4]float32{vm.Q, vm.Q, vm.Q, vm.Q}
	case "i":
		return [4]float32{vm.I, vm.I, vm.I, vm.I}
	case "acc":
		return vm.ACC
	}
	n, rest := vfNum(t)
	if n < 0 {
		return [4]float32{}
	}
	v := vm.bypassed(n, cons)
	if rest != "" {
		l := laneIdx(rest[0])
		return [4]float32{v[l], v[l], v[l], v[l]}
	}
	if bcLane >= 0 {
		return [4]float32{v[bcLane], v[bcLane], v[bcLane], v[bcLane]}
	}
	return v
}

func fz(f float32) float32 {
	if f != f {
		return 0
	}
	if f > math.MaxFloat32 {
		return math.MaxFloat32
	}
	if f < -math.MaxFloat32 {
		return -math.MaxFloat32
	}
	return f
}

func macBits(res [4]float32, mask [4]bool) uint32 {
	var m uint32
	for l := 0; l < 4; l++ {
		if !mask[l] {
			continue
		}
		bit := uint(3 - l)
		if res[l] == 0 {
			m |= 1 << (bit + 4)
		}
		if math.Signbit(float64(res[l])) {
			m |= 1 << (bit + 8)
		}
	}
	return m
}

type fn func(a, b, acc float32) float32

var ops = map[string]struct {
	f     fn
	toAcc bool
	useA  bool
}{}

func baseOp(name string) (kind string, acc bool, bc int, third string) {
	n := strings.ToLower(name)
	bc = -1
	for _, b := range []string{"madd", "msub", "mul", "add", "sub", "max", "mini"} {
		if !strings.HasPrefix(n, b) {
			continue
		}
		rest := n[len(b):]
		if strings.HasPrefix(rest, "a") && (b != "max" && b != "mini") {
			acc = true
			rest = rest[1:]
		}
		switch rest {
		case "":
			return b, acc, -1, "v"
		case "i":
			return b, acc, -1, "i"
		case "q":
			return b, acc, -1, "q"
		case "x", "y", "z", "w":
			return b, acc, laneIdx(rest[0]), "bc"
		}
	}
	return "", false, -1, ""
}

func ordKey(f float32) int64 {
	b := math.Float32bits(f)
	if b&0x80000000 != 0 {
		return -int64(b & 0x7fffffff)
	}
	return int64(b)
}

func apply(kind string, a, b, acc float32) float32 {
	switch kind {
	case "add":
		return a + b
	case "sub":
		return a - b
	case "mul":
		return a * b
	case "madd":
		return acc + a*b
	case "msub":
		return acc - a*b
	case "max":
		if ordKey(a) > ordKey(b) {
			return a
		}
		return b
	case "mini":
		if ordKey(a) < ordKey(b) {
			return a
		}
		return b
	}
	return 0
}

func ftoi(v float32, shift uint) uint32 {
	x := float64(v) * float64(uint32(1)<<shift)
	if x > 2147483647 {
		return 0x7fffffff
	}
	if x < -2147483648 {
		return 0x80000000
	}
	return uint32(int32(x))
}

func (vm *VM) setVF(n int, mask [4]bool, vals [4]float32, delay int) {
	vm.setVFKind(n, mask, vals, delay, "")
}

func (vm *VM) setVFKind(n int, mask [4]bool, vals [4]float32, delay int, kind string) {
	if n <= 0 {
		return
	}
	if vm.inLower && n == vm.upperDst {
		return
	}
	m := mask
	v := vals
	if delay <= 0 {
		for l := 0; l < 4; l++ {
			if m[l] {
				vm.VF[n][l] = v[l]
			}
		}
		return
	}
	vm.pending = append(vm.pending, write{at: vm.Cycle + delay, kind: kind, reg: n, mask: m, vals: v, apply: func(vm *VM) {
		for l := 0; l < 4; l++ {
			if m[l] {
				vm.VF[n][l] = v[l]
			}
		}
	}})
}

// bypassed returns register n as seen by a consumer that can use a result early.
func (vm *VM) bypassed(n int, consumer string) [4]float32 {
	v := vm.VF[n]
	for _, w := range vm.pending {
		if w.reg != n || w.kind == "" {
			continue
		}
		credit := 0
		switch {
		case w.kind == "load" && strings.HasPrefix(consumer, "ftoi"):
			credit = 3
		case w.kind == "load" && consumer == "minii":
			credit = 2
		case w.kind == "ftoi" && consumer == "mtir":
			credit = 3
		}
		if credit > 0 && w.at-credit <= vm.Cycle {
			for l := 0; l < 4; l++ {
				if w.mask[l] {
					v[l] = w.vals[l]
				}
			}
		}
	}
	return v
}

func (vm *VM) execUpper(in parse.Instruction) {
	name := strings.ToLower(in.UpperMnemonic)
	vm.consumer = name
	if strings.HasPrefix(name, "minii") {
		vm.consumer = "minii"
	}
	ops := in.UpperOperands
	mask := laneMask(in.UpperDest)
	switch name {
	case "nop":
		return
	case "abs":
		n, _ := vfNum(ops[0])
		s := vm.src(ops[1], -1)
		var r [4]float32
		for l := range r {
			r[l] = float32(math.Abs(float64(s[l])))
		}
		vm.setVF(n, mask, r, 4)
		vm.setMAC(r, mask)
		return
	case "ftoi0", "ftoi4", "ftoi12", "ftoi15", "itof0", "itof4", "itof12", "itof15":
		n, _ := vfNum(ops[0])
		sh := map[string]uint{"0": 0, "4": 4, "12": 12, "15": 15}[name[4:]]
		s := vm.srcBits(ops[1])
		var out [4]uint32
		var f [4]float32
		for l := 0; l < 4; l++ {
			if name[:4] == "ftoi" {
				out[l] = ftoi(math.Float32frombits(s[l]), sh)
			} else {
				f[l] = float32(int32(s[l])) / float32(uint32(1)<<sh)
			}
		}
		mk := mask
		if n > 0 {
			var vals [4]float32
			for l := 0; l < 4; l++ {
				if name[:4] == "ftoi" {
					vals[l] = math.Float32frombits(out[l])
				} else {
					vals[l] = f[l]
				}
			}
			kind := ""
			if name[:4] == "ftoi" {
				kind = "ftoi"
			}
			vm.setVFKind(n, mk, vals, 4, kind)
		}
		return
	case "clip", "clipw", "cliplw":
		s := vm.src(ops[0], -1)
		w := float32(math.Abs(float64(vm.src(ops[1], 3)[3])))
		var b uint32
		if s[0] > w {
			b |= 1
		}
		if s[0] < -w {
			b |= 2
		}
		if s[1] > w {
			b |= 4
		}
		if s[1] < -w {
			b |= 8
		}
		if s[2] > w {
			b |= 16
		}
		if s[2] < -w {
			b |= 32
		}
		vm.schedule(4, func(vm *VM) { vm.Clip = ((vm.Clip << 6) | b) & 0xffffff })
		return
	case "opmula", "opmsub":
		a := vm.src(ops[len(ops)-2], -1)
		b := vm.src(ops[len(ops)-1], -1)
		r := [4]float32{a[1]*b[2] - 0, a[2] * b[0], a[0] * b[1], 0}
		if name == "opmula" {
			vm.schedule(1, func(vm *VM) {
				vm.ACC[0], vm.ACC[1], vm.ACC[2] = a[1]*b[2], a[2]*b[0], a[0]*b[1]
			})
			vm.setMAC(r, [4]bool{true, true, true, false})
		} else {
			n, _ := vfNum(ops[0])
			res := [4]float32{vm.ACC[0] - a[1]*b[2], vm.ACC[1] - a[2]*b[0], vm.ACC[2] - a[0]*b[1], 0}
			vm.setVF(n, [4]bool{true, true, true, false}, res, 4)
			vm.setMAC(res, [4]bool{true, true, true, false})
		}
		return
	}
	kind, acc, bc, third := baseOp(name)
	if kind == "" {
		vm.Err = fmt.Errorf("unknown upper %s", name)
		return
	}
	var dst int
	var a, b [4]float32
	if acc {
		a = vm.src(ops[1], -1)
		b = vm.src(ops[2], bc)
	} else {
		dst, _ = vfNum(ops[0])
		a = vm.src(ops[1], -1)
		b = vm.src(ops[2], bc)
	}
	_ = third
	var r [4]float32
	for l := 0; l < 4; l++ {
		r[l] = apply(kind, a[l], b[l], vm.ACC[l])
		if kind != "max" && kind != "mini" {
			r[l] = fz(r[l])
		}
	}
	if acc {
		mk := mask
		rr := r
		vm.schedule(1, func(vm *VM) {
			for l := 0; l < 4; l++ {
				if mk[l] {
					vm.ACC[l] = rr[l]
				}
			}
		})
	} else {
		vm.setVF(dst, mask, r, 4)
	}
	vm.setMAC(r, mask)
}

func (vm *VM) setMAC(r [4]float32, mask [4]bool) {
	b := macBits(r, mask)
	vm.schedule(4, func(vm *VM) { vm.MAC = b })
}

func (vm *VM) srcBits(op string) [4]uint32 {
	n, rest := vfNum(op)
	var out [4]uint32
	if n < 0 {
		return out
	}
	bv := vm.bypassed(n, vm.consumer)
	for l := 0; l < 4; l++ {
		out[l] = math.Float32bits(bv[l])
	}
	if rest != "" {
		l := laneIdx(rest[0])
		for k := range out {
			out[k] = out[l]
		}
	}
	return out
}

func parseOffset(s string, vm *VM) (int, int) {
	s = strings.TrimSpace(s)
	open := strings.IndexByte(s, '(')
	off := 0
	if open > 0 {
		off, _ = strconv.Atoi(strings.TrimSpace(s[:open]))
	}
	reg := viNum(strings.Trim(s[open+1:], ")+- "))
	return off, reg
}

func (vm *VM) memIdx(v int) int { return ((v % memQuads) + memQuads) % memQuads }

// Run executes until the end marker or the limits; it returns false on timeout.
func (vm *VM) Run(maxCycles, maxKicks int) bool { return vm.RunAt(0, maxCycles, maxKicks) }

// RunAt starts execution at a word index.
func (vm *VM) RunAt(pc, maxCycles, maxKicks int) bool {
	pendingJump := -1
	delayLeft := 0
	endLeft := -1
	n := len(vm.prog.Instructions)
	for vm.Cycle < maxCycles {
		if pc >= n || pc < 0 {
			return true
		}
		in := vm.prog.Instructions[pc]
		vm.retire()
		if in.PaddingBytes > 0 {
			pc++
			continue
		}
		vm.take = false
		vm.pcNow = pc
		switch strings.ToLower(in.LowerMnemonic) {
		case "waitq":
			if vm.qReady > vm.Cycle {
				vm.Cycle = vm.qReady
				vm.retire()
			}
		case "waitp":
			if vm.pReady > vm.Cycle {
				vm.Cycle = vm.pReady
				vm.retire()
			}
		}
		if vm.TraceVF > 0 {
			fmt.Printf("c%d pc%d up=%s %v lo=%s %v | VF%d=%v VI1=%v\n", vm.Cycle, pc, in.UpperMnemonic, in.UpperOperands, in.LowerMnemonic, in.LowerOperands, vm.TraceVF, vm.VF[vm.TraceVF], vm.VI[1])
		}
		if !vm.StaleReads {
			vm.interlock(in)
		}
		vm.upperDst = -1
		if len(in.UpperOperands) > 0 {
			if n, _ := vfNum(in.UpperOperands[0]); n > 0 {
				vm.upperDst = n
			}
		}
		vm.inLower = true
		stall := vm.execLower(in)
		vm.inLower = false
		vm.execUpper(in)
		if vm.Err != nil {
			return false
		}
		vm.Cycle += 1 + stall
		next := pc + 1
		if delayLeft > 0 {
			delayLeft--
			if delayLeft == 0 {
				next = pendingJump
				pendingJump = -1
			}
		}
		if vm.take {
			pendingJump = vm.jumpTarget
			delayLeft = 1
		}
		if endLeft >= 0 {
			endLeft--
			if endLeft < 0 {
				vm.Cycle += 4
				vm.retire()
				return true
			}
		}
		if strings.Contains(strings.ToUpper(in.UpperFlags), "E") && endLeft < 0 {
			endLeft = 1
			endLeft--
			if endLeft < 0 {
				endLeft = 0
			}
		}
		if len(vm.Kicks) >= maxKicks {
			return true
		}
		pc = next
	}
	return false
}

// Entry returns the word index of a label, or -1.
func (vm *VM) Entry(name string) int { return vm.label(name) }

func (vm *VM) label(name string) int {
	if off, ok := vm.prog.Labels[strings.TrimSpace(name)]; ok {
		return int(off / 8)
	}
	return -1
}

func (vm *VM) execLower(in parse.Instruction) int {
	name := strings.ToLower(in.LowerMnemonic)
	ops := in.LowerOperands
	mask := laneMask(in.LowerDest)
	setVI := func(n int, v int, d int) {
		if n <= 0 {
			return
		}
		if d <= 0 {
			vm.VI[n] = uint16(v)
			return
		}
		vm.pending = append(vm.pending, write{at: vm.Cycle + d, reg: n, vi: true, apply: func(vm *VM) { vm.VI[n] = uint16(v) }})
	}
	switch name {
	case "nop":
	case "waitq", "waitp":
	case "loi":
		v, _ := parseFloatBits(ops[0])
		vm.I = v
	case "iadd", "isub", "iand", "ior":
		a, b := vm.readVI(viNum(ops[1])), vm.readVI(viNum(ops[2]))
		r := 0
		switch name {
		case "iadd":
			r = a + b
		case "isub":
			r = a - b
		case "iand":
			r = a & b
		case "ior":
			r = a | b
		}
		setVI(viNum(ops[0]), r, 1)
	case "iaddiu", "iaddi", "isubiu":
		a := vm.readVI(viNum(ops[1]))
		imm := evalImm(ops[2])
		if name == "isubiu" {
			imm = -imm
		}
		setVI(viNum(ops[0]), a+imm, 1)
	case "move", "mr32", "mfir":
		n, _ := vfNum(ops[0])
		var r [4]float32
		switch name {
		case "move":
			s := vm.src(ops[1], -1)
			r = s
		case "mr32":
			s := vm.src(ops[1], -1)
			r = [4]float32{s[1], s[2], s[3], s[0]}
		case "mfir":
			v := float32(math.Float32frombits(uint32(int32(int16(vm.readVI(viNum(ops[1])))))))
			r = [4]float32{v, v, v, v}
		}
		vm.setVF(n, mask, r, 5)
	case "mtir":
		vm.consumer = "mtir"
		s := vm.srcBits(ops[1])
		setVI(viNum(ops[0]), int(s[0]&0xffff), 1)
		_ = s
		n, rest := vfNum(ops[1])
		if n >= 0 && rest != "" {
			bv := vm.bypassed(n, "mtir")
			setVI(viNum(ops[0]), int(math.Float32bits(bv[laneIdx(rest[0])])&0xffff), 1)
		}
	case "lq", "lqi", "lqd":
		n, _ := vfNum(ops[0])
		off, reg := parseOffset(ops[1], vm)
		base := vm.readVI(reg)
		addr := base + off
		if name == "lqd" {
			base--
			addr = base
			setVI(reg, base, 1)
		}
		if name == "lqi" {
			addr = base
			setVI(reg, base+1, 1)
		}
		q := vm.Mem[vm.memIdx(addr)]
		var r [4]float32
		for l := range r {
			r[l] = math.Float32frombits(q[l])
		}
		vm.setVFKind(n, mask, r, 4, "load")
	case "sq", "sqi", "sqd":
		n, _ := vfNum(ops[0])
		off, reg := parseOffset(ops[1], vm)
		base := vm.readVI(reg)
		addr := base + off
		if name == "sqd" {
			base--
			addr = base
			setVI(reg, base, 1)
		}
		if name == "sqi" {
			addr = base
			setVI(reg, base+1, 1)
		}
		idx := vm.memIdx(addr)
		for l := 0; l < 4; l++ {
			if mask[l] {
				vm.Mem[idx][l] = math.Float32bits(vm.VF[n][l])
			}
		}
		if vm.Log != nil {
			vm.Log[idx] = append(vm.Log[idx], fmt.Sprintf("c%d pc%d %s %v", vm.Cycle, vm.pcNow, in.LowerMnemonic+"."+in.LowerDest, vm.VF[n]))
		}
	case "ilw", "isw":
		n := viNum(ops[0])
		open := strings.IndexByte(ops[1], ')')
		_ = open
		off, reg := parseOffset(ops[1], vm)
		addr := vm.readVI(reg) + off
		l := 0
		for i := 0; i < 4; i++ {
			if mask[i] {
				l = i
			}
		}
		idx := vm.memIdx(addr)
		if name == "ilw" {
			setVI(n, int(vm.Mem[idx][l]&0xffff), 5)
		} else {
			vm.Mem[idx][l] = uint32(vm.readVI(n))
		}
	case "div", "sqrt", "rsqrt":
		var v float32
		switch name {
		case "div":
			a := vm.src(ops[1], -1)
			b := vm.src(ops[2], -1)
			la, lb := fieldLane(ops[1]), fieldLane(ops[2])
			v = fz(a[la] / nz(b[lb]))
		case "sqrt":
			a := vm.src(ops[1], -1)
			v = float32(math.Sqrt(math.Abs(float64(a[fieldLane(ops[1])]))))
		case "rsqrt":
			a := vm.src(ops[1], -1)
			b := vm.src(ops[2], -1)
			v = fz(a[fieldLane(ops[1])] / nz(float32(math.Sqrt(math.Abs(float64(b[fieldLane(ops[2])]))))))
		}
		lat := 7
		if name == "rsqrt" {
			lat = 13
		}
		vm.qReady = vm.Cycle + lat
		vm.schedule(lat, func(vm *VM) { vm.Q = v })
	case "fcset", "fsset":
		if name == "fcset" {
			c := uint32(evalImm(ops[0]))
			vm.schedule(4, func(vm *VM) { vm.Clip = c & 0xffffff })
		}
	case "fcand", "fceq", "fcor":
		c := vm.Clip
		m := uint32(evalImm(ops[1]))
		var r uint32
		switch name {
		case "fcand":
			if c&m != 0 {
				r = 1
			}
		case "fceq":
			if c&0xffffff == m {
				r = 1
			}
		case "fcor":
			if (c|m)&0xffffff == 0xffffff {
				r = 1
			}
		}
		setVI(viNum(ops[0]), int(r), 1)
	case "fmand", "fmeq", "fmor":
		m := uint32(vm.readVI(viNum(ops[1])))
		var r uint32
		switch name {
		case "fmand":
			r = vm.MAC & m
		case "fmeq":
			if vm.MAC&0xffff == m {
				r = 1
			}
		case "fmor":
			r = vm.MAC | m
		}
		setVI(viNum(ops[0]), int(r&0xffff), 1)
	case "fsand", "fseq", "fsor":
		setVI(viNum(ops[0]), 0, 1)
	case "fcget":
		setVI(viNum(ops[0]), int(vm.Clip&0xfff), 1)
	case "xtop", "xitop":
		setVI(viNum(ops[0]), int(vm.Xtop), 1)
	case "xgkick":
		k := Kick{Addr: vm.readVI(viNum(ops[0]))}
		k.Mem = vm.Mem
		vm.Kicks = append(vm.Kicks, k)
	case "b", "bal", "ibeq", "ibne", "ibgez", "ibgtz", "iblez", "ibltz", "jr", "jalr":
		take := false
		target := -1
		switch name {
		case "b":
			take, target = true, vm.label(ops[0])
		case "bal":
			take, target = true, vm.label(ops[1])
			setVI(viNum(ops[0]), 0, 1)
		case "ibeq":
			take = vm.readVI(viNum(ops[0])) == vm.readVI(viNum(ops[1]))
			target = vm.label(ops[2])
		case "ibne":
			take = vm.readVI(viNum(ops[0])) != vm.readVI(viNum(ops[1]))
			target = vm.label(ops[2])
		case "ibgez", "ibgtz", "iblez", "ibltz":
			v := int(int16(vm.readVI(viNum(ops[0]))))
			target = vm.label(ops[1])
			switch name {
			case "ibgez":
				take = v >= 0
			case "ibgtz":
				take = v > 0
			case "iblez":
				take = v <= 0
			case "ibltz":
				take = v < 0
			}
		}
		if take {
			vm.take = true
			vm.jumpTarget = target
		}
	case "mfp":
		n, _ := vfNum(ops[0])
		v := vm.P
		vm.setVF(n, mask, [4]float32{v, v, v, v}, 5)
	case "esadd", "ersadd", "eleng", "erleng", "esum", "ercpr", "ersqrt", "esin", "eatan", "eexp", "eatanxy", "eatanxz":
		a := vm.src(ops[1], -1)
		l := fieldLane(ops[1])
		sq := a[0]*a[0] + a[1]*a[1] + a[2]*a[2]
		var v float32
		lat := 11
		switch name {
		case "esadd":
			v = sq
		case "ersadd":
			v, lat = fz(1/nz(sq)), 18
		case "eleng":
			v, lat = float32(math.Sqrt(float64(sq))), 18
		case "erleng":
			v, lat = fz(1/nz(float32(math.Sqrt(float64(sq))))), 24
		case "esum":
			v, lat = a[0]+a[1]+a[2]+a[3], 12
		case "ercpr":
			v, lat = fz(1/nz(a[0])), 12
		case "ersqrt":
			v, lat = fz(1/nz(float32(math.Sqrt(math.Abs(float64(a[l])))))), 18
		case "esin":
			v, lat = float32(math.Sin(float64(a[l]))), 29
		case "eatan":
			v, lat = float32(math.Atan(float64(a[l]))), 54
		case "eexp":
			v, lat = float32(math.Exp(-float64(a[l]))), 44
		case "eatanxy":
			v, lat = float32(math.Atan2(float64(a[1]), float64(a[0]))), 54
		case "eatanxz":
			v, lat = float32(math.Atan2(float64(a[2]), float64(a[0]))), 54
		}
		v = fz(v)
		vm.pReady = vm.Cycle + lat
		vm.schedule(lat, func(vm *VM) { vm.P = v })
	case "ilwr", "iswr":
		n := viNum(ops[0])
		reg := viNum(strings.Trim(ops[1], "() "))
		idx := vm.memIdx(vm.readVI(reg))
		l := 0
		for i := 0; i < 4; i++ {
			if mask[i] {
				l = i
			}
		}
		if name == "ilwr" {
			setVI(n, int(vm.Mem[idx][l]&0xffff), 5)
		} else {
			vm.Mem[idx][l] = uint32(vm.readVI(n))
		}
	case "rinit", "rxor":
		vm.R = float32(1)
	case "rget", "rnext":
		n, _ := vfNum(ops[0])
		vm.setVF(n, mask, [4]float32{vm.R, vm.R, vm.R, vm.R}, 5)
	default:
		vm.Err = fmt.Errorf("unknown lower %s", name)
	}
	return 0
}

func nz(f float32) float32 {
	if f == 0 {
		return 1e-30
	}
	return f
}

func fieldLane(op string) int {
	_, rest := vfNum(op)
	if rest == "" {
		return 3
	}
	return laneIdx(rest[0])
}

func parseFloatBits(s string) (float32, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "0x") {
		u, err := strconv.ParseUint(s[2:], 16, 32)
		return math.Float32frombits(uint32(u)), err
	}
	f, err := strconv.ParseFloat(s, 32)
	return float32(f), err
}

func evalImm(s string) int {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "-0x") {
		neg := strings.HasPrefix(s, "-")
		u, _ := strconv.ParseInt(strings.TrimPrefix(strings.TrimPrefix(s, "-"), "0x"), 16, 64)
		if neg {
			return -int(u)
		}
		return int(u)
	}
	v, _ := strconv.Atoi(s)
	return v
}

var regMention = regexp.MustCompile("(?i)(vf|vi)([0-9]+)")

// interlock stalls until every VF/VI register the word mentions has no write in flight.
func (vm *VM) interlock(in parse.Instruction) {
	var text []string
	text = append(text, in.UpperOperands...)
	text = append(text, in.LowerOperands...)
	wait := 0
	for _, t := range text {
		for _, m := range regMention.FindAllStringSubmatch(t, -1) {
			n, _ := strconv.Atoi(m[2])
			if n == 0 {
				continue
			}
			vi := strings.EqualFold(m[1], "vi")
			for _, w := range vm.pending {
				if w.reg == n && w.vi == vi && (vi || w.mask != [4]bool{}) && w.at-vm.Cycle > wait {
					wait = w.at - vm.Cycle
				}
			}
		}
	}
	if wait > 0 {
		vm.Cycle += wait
		vm.retire()
	}
}
