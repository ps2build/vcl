package vsm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/bits"
	"strings"
	"sync"

	"github.com/ps2build/vcl/internal/dvpas"
	"github.com/ps2build/vcl/internal/vcl"
)

const upperFlagMask = 0xF8000000

type bitField struct {
	pos     []int
	signPos int
	signed  bool
}

func (f bitField) extract(w uint32) int {
	v := 0
	for k, p := range f.pos {
		v |= int(w>>uint(p)&1) << uint(k)
	}
	if f.signed {
		if w>>uint(f.signPos)&1 == 1 {
			v -= 1 << uint(len(f.pos))
		}
	}
	return v
}

type operand struct {
	kind   string
	fixReg int
	reg    bitField
	lane   bitField
	imm    bitField
	fix    string
}

type decodeForm struct {
	name    string
	pipe    string
	mask    uint32
	match   uint32
	dest    bool
	destPos [4]int
	ops     []operand
	lane    string
}

var (
	tableOnce sync.Once
	upperTab  []*decodeForm
	lowerTab  []*decodeForm
)

func encodeOne(upper, lower string) (lo, up uint32, err error) {
	src := ".vu\nL0:\n" + upper + " " + lower + "\n"
	var obj bytes.Buffer
	if err = dvpas.Assemble(strings.NewReader(src), &obj); err != nil {
		return 0, 0, err
	}
	o, err := ReadObject(obj.Bytes())
	if err != nil {
		return 0, 0, err
	}
	code := o.Section(".vutext")
	if len(code) < 8 {
		return 0, 0, fmt.Errorf("short object")
	}
	return binary.LittleEndian.Uint32(code), binary.LittleEndian.Uint32(code[4:]), nil
}

type slotSpec struct {
	kind  string
	fixed string
}

func parseSlots(pattern string) []slotSpec {
	if pattern == "" {
		return nil
	}
	var out []slotSpec
	for _, op := range strings.Split(pattern, ",") {
		alts := strings.Split(op, "|")
		pick := alts[0]
		for _, a := range alts {
			if strings.HasPrefix(a, "imm(") {
				pick = a
			}
		}
		parts := strings.Split(pick, ":")
		head := parts[0]
		flags := map[string]bool{}
		for _, f := range parts[1:] {
			flags[f] = true
		}
		var s slotSpec
		switch head {
		case "vf":
			switch {
			case flags["bc"]:
				s.kind = "vfbc"
			case flags["flag"]:
				s.kind = "vflane"
			case flags["wcomp"]:
				s.kind = "vfw"
			default:
				s.kind = "vf"
			}
		case "vi":
			s.kind = "vi"
		case "acc":
			s.kind, s.fixed = "fixed", "ACC"
		case "q":
			s.kind, s.fixed = "fixed", "Q"
		case "p":
			s.kind, s.fixed = "fixed", "P"
		case "i":
			s.kind, s.fixed = "fixed", "I"
		case "r":
			s.kind, s.fixed = "fixed", "R"
		case "imm":
			switch {
			case flags["branch"]:
				s.kind = "branch"
			case flags["raw"]:
				s.kind = "loi"
			default:
				s.kind = "imm"
			}
		case "(vi)":
			switch {
			case flags["postinc"]:
				s.kind = "post"
			case flags["predec"]:
				s.kind = "pre"
			default:
				s.kind = "memplain"
			}
		case "imm(vi)":
			s.kind = "mem"
		default:
			s.kind = "unknown"
		}
		out = append(out, s)
	}
	return out
}

type probeVal struct {
	reg  int
	lane int
	imm  int
	base int
}

const lanes = "xyzw"

func renderOperands(slots []slotSpec, vals []probeVal, bcLane string) []string {
	var out []string
	for i, s := range slots {
		v := vals[i]
		switch s.kind {
		case "vf":
			out = append(out, fmt.Sprintf("VF%02d", v.reg))
		case "vfbc":
			out = append(out, fmt.Sprintf("VF%02d%s", v.reg, bcLane))
		case "vflane":
			out = append(out, fmt.Sprintf("VF%02d%c", v.reg, lanes[v.lane]))
		case "vfw":
			out = append(out, fmt.Sprintf("VF%02dw", v.reg))
		case "vi":
			out = append(out, fmt.Sprintf("VI%02d", v.reg))
		case "fixed":
			out = append(out, s.fixed)
		case "imm":
			out = append(out, fmt.Sprint(v.imm))
		case "branch":
			out = append(out, "L0")
		case "loi":
			out = append(out, "1.0")
		case "post":
			out = append(out, fmt.Sprintf("(VI%02d++)", v.reg))
		case "pre":
			out = append(out, fmt.Sprintf("(--VI%02d)", v.reg))
		case "memplain":
			out = append(out, fmt.Sprintf("(VI%02d)", v.reg))
		case "mem":
			out = append(out, fmt.Sprintf("%d(VI%02d)", v.imm, v.reg))
		}
	}
	return out
}

func probeBuild(info vcl.InstructionInfo, lane string) *decodeForm {
	slots := parseSlots(info.Pattern)
	hasDest := false
	for _, f := range info.Flags {
		if f == "dest" {
			hasDest = true
		}
	}
	if strings.Contains(info.Pattern, ":dest") && info.Pipe == "lower" {
		hasDest = true
	}
	mnemonic := info.Name + lane
	build := func(vals []probeVal, dest string) (uint32, bool) {
		text := mnemonic
		if hasDest && dest != "" {
			text += "." + dest
		}
		if ops := renderOperands(slots, vals, lane); len(ops) > 0 {
			text += " " + strings.Join(ops, ", ")
		}
		var lo, up uint32
		var err error
		if info.Pipe == "upper" {
			lo, up, err = encodeOne(text, "nop")
			return up, err == nil
		}
		lo, up, err = encodeOne("nop", text)
		_ = up
		return lo, err == nil
	}
	base := make([]probeVal, len(slots))
	baseDest := "xyzw"
	baseWord, ok := build(base, baseDest)
	if !ok {
		baseDest = "xyz"
		baseWord, ok = build(base, baseDest)
	}
	if !ok {
		for i := range base {
			base[i].reg = 1
		}
		baseDest = "xyzw"
		baseWord, ok = build(base, baseDest)
	}
	if !ok {
		return nil
	}
	f := &decodeForm{name: mnemonic, pipe: info.Pipe, dest: hasDest, lane: lane}
	f.destPos = [4]int{24, 23, 22, 21}
	variable := uint32(0)
	clone := func() []probeVal { return append([]probeVal{}, base...) }
	if hasDest {
		for _, d := range []string{"x", "y", "z", "w", "xy", "zw"} {
			if w, ok := build(base, d); ok {
				variable |= w ^ baseWord
			}
		}
	}
	for i, s := range slots {
		op := operand{kind: s.kind, fix: s.fixed, fixReg: base[i].reg}
		switch s.kind {
		case "vf", "vfbc", "vfw", "vi", "post", "pre", "memplain", "mem", "vflane":
			for k := 0; k < 5; k++ {
				v := clone()
				v[i].reg = 1 << uint(k)
				if w, ok := build(v, baseDest); ok && w != baseWord {
					diff := w ^ baseWord
					variable |= diff
					if bits.OnesCount32(diff) == 1 {
						op.reg.pos = append(op.reg.pos, bits.TrailingZeros32(diff))
					}
				}
			}
			if s.kind == "vflane" {
				for k := 1; k <= 2; k++ {
					v := clone()
					v[i].lane = k
					if w, ok := build(v, baseDest); ok && w != baseWord {
						diff := w ^ baseWord
						variable |= diff
						if bits.OnesCount32(diff) == 1 {
							op.lane.pos = append(op.lane.pos, bits.TrailingZeros32(diff))
						}
					}
				}
			}
		}
		if s.kind == "imm" || s.kind == "mem" {
			var positions []int
			for k := 0; k < 24; k++ {
				v := clone()
				v[i].imm = 1 << uint(k)
				w, ok := build(v, baseDest)
				if !ok || w == baseWord {
					break
				}
				diff := w ^ baseWord
				variable |= diff
				if bits.OnesCount32(diff) != 1 {
					break
				}
				positions = append(positions, bits.TrailingZeros32(diff))
			}
			op.imm.pos = positions
			v := clone()
			v[i].imm = -1
			if w, ok := build(v, baseDest); ok && w != baseWord {
				diff := w ^ baseWord
				variable |= diff
				for _, p := range positions {
					diff &^= 1 << uint(p)
				}
				if bits.OnesCount32(diff) == 1 {
					op.imm.signed = true
					op.imm.signPos = bits.TrailingZeros32(diff)
				}
			}
			if s.kind == "mem" && !op.imm.signed {
				op.imm.signed = false
			}
		}
		if s.kind == "branch" {
			for k := 0; k < 10; k++ {
				op.imm.pos = append(op.imm.pos, k)
			}
			op.imm.signed, op.imm.signPos = true, 10
			variable |= 0x7ff
		}
		f.ops = append(f.ops, op)
	}
	f.mask = ^variable
	f.match = baseWord & f.mask
	if info.Pipe == "upper" {
		f.mask &^= upperFlagMask
		f.match &^= upperFlagMask
	}
	return f
}

func buildTables() {
	for _, info := range vcl.Instructions() {
		if info.Pipe == "nop" {
			continue
		}
		var lanesToTry []string
		if info.Broadcast {
			lanesToTry = []string{"x", "y", "z", "w"}
		} else {
			lanesToTry = []string{""}
		}
		for _, l := range lanesToTry {
			f := probeBuild(info, l)
			if f == nil {
				continue
			}
			if info.Pipe == "upper" {
				upperTab = append(upperTab, f)
			} else {
				lowerTab = append(lowerTab, f)
			}
		}
	}
	lo, up, err := encodeOne("nop", "nop")
	if err == nil {
		upperTab = append(upperTab, &decodeForm{name: "nop", pipe: "upper", mask: 0xffffffff &^ upperFlagMask, match: up &^ upperFlagMask})
		lowerTab = append(lowerTab, &decodeForm{name: "nop", pipe: "lower", mask: 0xffffffff, match: lo})
	}
}

func pick(tab []*decodeForm, w uint32) *decodeForm {
	var best *decodeForm
	for _, f := range tab {
		if w&f.mask == f.match {
			if best == nil || bits.OnesCount32(f.mask) > bits.OnesCount32(best.mask) {
				best = f
			}
		}
	}
	return best
}

func (f *decodeForm) destString(w uint32) string {
	var b strings.Builder
	for i, c := range lanes {
		if w>>uint(f.destPos[i])&1 == 1 {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// Decoded is one disassembled instruction word.
type Decoded struct {
	Upper, Lower string
	Target       int
	HasTarget    bool
}

func (f *decodeForm) format(w uint32, pc int, label func(int) string, upperPipe bool) (string, int, bool) {
	var b strings.Builder
	b.WriteString(f.name)
	if upperPipe {
		var fl []byte
		if w&(8<<27) != 0 {
			fl = append(fl, 'E')
		}
		if w&(2<<27) != 0 {
			fl = append(fl, 'D')
		}
		if w&(4<<27) != 0 {
			fl = append(fl, 'M')
		}
		if w&(1<<27) != 0 {
			fl = append(fl, 'T')
		}
		if len(fl) > 0 {
			b.WriteString("[" + string(fl) + "]")
		}
	}
	if f.dest {
		d := f.destString(w)
		if upperPipe || d != "xyzw" {
			if d == "" {
				d = "xyzw"
			}
			b.WriteString("." + d)
		}
	}
	var parts []string
	target, hasTarget := 0, false
	regOf := func(op operand) int {
		if len(op.reg.pos) == 0 {
			return op.fixReg
		}
		return op.reg.extract(w)
	}
	for _, op := range f.ops {
		switch op.kind {
		case "vf":
			parts = append(parts, fmt.Sprintf("VF%02d", regOf(op)))
		case "vfbc":
			parts = append(parts, fmt.Sprintf("VF%02d%s", regOf(op), f.lane))
		case "vflane":
			parts = append(parts, fmt.Sprintf("VF%02d%c", regOf(op), lanes[op.lane.extract(w)]))
		case "vfw":
			parts = append(parts, fmt.Sprintf("VF%02dw", regOf(op)))
		case "vi":
			parts = append(parts, fmt.Sprintf("VI%02d", regOf(op)))
		case "fixed":
			parts = append(parts, op.fix)
		case "imm":
			parts = append(parts, fmt.Sprint(op.imm.extract(w)))
		case "branch":
			target, hasTarget = pc+1+op.imm.extract(w), true
			parts = append(parts, label(target))
		case "post":
			parts = append(parts, fmt.Sprintf("(VI%02d++)", regOf(op)))
		case "pre":
			parts = append(parts, fmt.Sprintf("(--VI%02d)", regOf(op)))
		case "memplain":
			parts = append(parts, fmt.Sprintf("(VI%02d)", regOf(op)))
		case "mem":
			parts = append(parts, fmt.Sprintf("%d(VI%02d)", op.imm.extract(w), regOf(op)))
		}
	}
	if len(parts) > 0 {
		b.WriteString(" " + strings.Join(parts, ", "))
	}
	return b.String(), target, hasTarget
}

// Word is one decoded 64-bit instruction.
type Word struct {
	Lower, Upper uint32
	UpperText    string
	LowerText    string
	Target       int
	HasTarget    bool
	Unknown      bool
}

// Disassemble decodes code (8 bytes per instruction, lower word first).
func Disassemble(code []byte, label func(int) string) []Word {
	tableOnce.Do(buildTables)
	var out []Word
	for pc := 0; (pc+1)*8 <= len(code); pc++ {
		lo := binary.LittleEndian.Uint32(code[pc*8:])
		up := binary.LittleEndian.Uint32(code[pc*8+4:])
		w := Word{Lower: lo, Upper: up}
		uf := pick(upperTab, up)
		if uf == nil {
			w.Unknown = true
		} else {
			w.UpperText, _, _ = uf.format(up&^(16<<27), pc, label, true)
		}
		if up&(16<<27) != 0 {
			w.LowerText = fmt.Sprintf("loi 0x%08x", lo)
		} else if lf := pick(lowerTab, lo); lf == nil {
			w.Unknown = true
		} else {
			w.LowerText, w.Target, w.HasTarget = lf.format(lo, pc, label, false)
		}
		out = append(out, w)
	}
	return out
}

// ProbeForms reports how many upper and lower forms the decoder derived.
func ProbeForms() (upper, lower int) {
	tableOnce.Do(buildTables)
	return len(upperTab), len(lowerTab)
}

// Render lays out one instruction word as VSM text.
func (w Word) Render() string {
	up, lo := w.UpperText, w.LowerText
	if up == "" {
		up = "nop"
	}
	if lo == "" {
		lo = "nop"
	}
	line := strings.Repeat(" ", 20) + up
	if len(up) < 32 {
		line += strings.Repeat(" ", 32-len(up))
	} else {
		line += " "
	}
	return line + lo
}

func vclInstructions() []vcl.InstructionInfo { return vcl.Instructions() }
