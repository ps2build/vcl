package vcl

import (
	"fmt"
	"os"
	"sort"
	"strconv"
)

// loop describes a backward branch and the tokens it encloses.
type loop struct {
	lbl     int // index of the label token
	br      int // index of the branch token
	counted bool
	label   string
}

func cloneTok(t *token) *token {
	nt := *t
	nt.label = ""
	nt.fill = nil
	if t.ins != nil {
		ni := *t.ins
		ni.args = make([]*operand, len(t.ins.args))
		for i, op := range t.ins.args {
			cp := *op
			if op.base != nil {
				b := *op.base
				cp.base = &b
			}
			ni.args[i] = &cp
		}
		nt.ins = &ni
	}
	return &nt
}

func findLoops(toks []*token) []loop {
	labels := map[string]int{}
	for i, t := range toks {
		if t.kind == tkLabel {
			labels[t.label] = i
		}
	}
	var out []loop
	for i, t := range toks {
		if t.kind != tkInstr || !t.ins.f.isBranch || t.ins.f.regBranch {
			continue
		}
		target := t.ins.args[len(t.ins.args)-1].imm
		j, ok := labels[target]
		if !ok || j >= i {
			continue
		}
		l := loop{lbl: j, br: i, label: target}
		has := false
		for _, k := range []int{j, j + 1} {
			if k < len(toks) && toks[k].kind == tkDir && toks[k].dir == "--loopcs" {
				has = true
			}
		}
		l.counted = has && !(t.ins.f.uncond && !t.ins.f.link)
		out = append(out, l)
	}
	return out
}

type inductionUpdate struct {
	idx  int
	step int
}

func updatesOf(toks []*token, from, to int) map[int][]inductionUpdate {
	m := map[int][]inductionUpdate{}
	for i := from; i <= to; i++ {
		t := toks[i]
		if t.kind != tkInstr {
			continue
		}
		f := t.ins.f
		if f.name != "iaddiu" && f.name != "isubiu" {
			continue
		}
		a := t.ins.args
		if a[0].phys != a[1].phys || a[0].phys == 0 {
			continue
		}
		v, ok := decimalImm(a[2].imm)
		if !ok {
			continue
		}
		if f.name == "isubiu" {
			v = -v
		}
		m[a[0].phys] = append(m[a[0].phys], inductionUpdate{i, v})
	}
	return m
}

func nodeOf(t *token) *node { return newNode(t, false) }

func readsQ(t *token) bool { return t.kind == tkInstr && nodeOf(t).reads[resQ] }

// qLiveOut reports whether Q is read after the loop before being rewritten.
func qLiveOut(toks []*token, br int) bool {
	for k := br + 1; k < len(toks); k++ {
		t := toks[k]
		if t.kind != tkInstr {
			continue
		}
		n := nodeOf(t)
		if n.reads[resQ] {
			return true
		}
		if n.writes[resQ] {
			return false
		}
	}
	return false
}

func floatRegsUsed(toks []*token, lo, hi int) map[int]bool {
	used := liveAcross(toks, lo, hi)
	for i := lo; i <= hi; i++ {
		if toks[i].kind != tkInstr {
			continue
		}
		for _, a := range toks[i].ins.accesses() {
			if !a.isInt && a.special == "" && a.op != nil {
				used[a.op.phys] = true
			}
		}
	}
	return used
}

func lowerScratch(used map[int]bool) (int, bool) {
	for r := 31; r >= 1; r-- {
		if !used[r] {
			used[r] = true
			return r, true
		}
	}
	return 0, false
}

func floatAccessLanes(t *token) (reads, writes map[int]uint8) {
	reads, writes = map[int]uint8{}, map[int]uint8{}
	for _, a := range t.ins.accesses() {
		if a.isInt || a.special != "" || a.op == nil {
			continue
		}
		if a.write {
			writes[a.op.phys] |= a.lanes
		} else {
			reads[a.op.phys] |= a.lanes
		}
	}
	return
}

func substReg(t *token, from, to int) {
	for _, op := range t.ins.args {
		if op.kind == kVF && op.phys == from {
			op.phys = to
		}
	}
}

func mkMove(dst, src int, mask uint8) *token {
	f := forms["move"][0]
	d := &operand{kind: kVF, phys: dst, spec: f.args[0], text: vfName(dst)}
	s := &operand{kind: kVF, phys: src, spec: f.args[1], text: vfName(src)}
	ins := &instr{f: f, word: "move", mask: mask, hasMask: true, args: []*operand{d, s}}
	return &token{kind: tkInstr, ins: ins}
}

func invertBranch(t *token, target string) *token {
	nt := cloneTok(t)
	if nt.ins.f.name == "ibne" {
		nt.ins.f = forms["ibeq"][0]
	} else {
		nt.ins.f = forms["ibne"][0]
	}
	nt.ins.word = nt.ins.f.name
	last := nt.ins.args[len(nt.ins.args)-1]
	last.imm = target
	last.text = target
	return nt
}

type swpPlan struct {
	out []*token
}

// planSingle builds the single-producer pipelined replacement for a loop.
func planSingle(toks []*token, lp loop) ([]*token, bool) {
	L, B := lp.lbl, lp.br
	br := toks[B]
	if br.ins.f.delay != 1 || br.ins.f.link || br.ins.f.regBranch {
		return nil, false
	}
	// body must be label, optional directive, instructions
	var body []int
	for i := L + 1; i <= B; i++ {
		t := toks[i]
		if t.kind == tkLabel || t.label != "" {
			return nil, false
		}
		if t.kind == tkInstr {
			if t.raw || t.ins.flags != "" {
				return nil, false
			}
			body = append(body, i)
		}
	}
	// token at L must be label only
	if toks[L].kind != tkLabel {
		return nil, false
	}
	var prods []int
	for pos, i := range body {
		f := toks[i].ins.f
		if f.writesQ {
			prods = append(prods, pos)
		}
		if f.xgkick || f.postinc || f.predec {
			return nil, false
		}
	}
	if len(prods) != 1 {
		return nil, false
	}
	P := prods[0]
	var cons []int
	for pos := P + 1; pos < len(body); pos++ {
		if readsQ(toks[body[pos]]) && body[pos] != B {
			cons = append(cons, pos)
		}
	}
	if len(cons) == 0 {
		return nil, false
	}
	pf := toks[body[P]].ins.f
	lat := pf.latency
	c1, cl := cons[0], cons[len(cons)-1]
	last := len(body) - 1 // branch position
	prefix := P
	gap := c1 - P - 1
	suffix := last - c1 - 1
	if prefix+suffix < lat {
		return nil, false
	}
	if gap >= lat {
		return nil, false
	}
	if suffix+1+prefix < lat {
		return nil, false
	}
	if last-cl-1+1+prefix < lat {
		return nil, false
	}
	qlive := qLiveOut(toks, B)
	upd := updatesOf(toks, L, B)
	if len(upd) == 0 {
		return nil, false
	}
	prolog := body[:c1]
	mainSet := body[c1:]
	prefetch := body[:c1]
	var pre []int
	for _, i := range prefetch {
		if i != body[P] {
			pre = append(pre, i)
		}
	}
	// prefetch emittability
	step := func(reg int) (int, bool) {
		if u := upd[reg]; len(u) > 0 {
			return u[0].step, true
		}
		return 0, false
	}
	prefWrites := map[int]uint8{}
	for _, i := range pre {
		t := toks[i]
		f := t.ins.f
		if f.mem != "none" && !(f.mem == "load") {
			return nil, false
		}
		reads, writes := floatAccessLanes(t)
		_ = reads
		for r, m := range writes {
			prefWrites[r] |= m
		}
		for _, a := range t.ins.accesses() {
			if a.isInt && !a.write && a.op != nil {
				if _, isInd := upd[a.op.phys]; isInd {
					if f.mem != "load" {
						return nil, false
					}
					if _, ok := step(a.op.phys); !ok {
						return nil, false
					}
				}
			}
		}
	}
	// rotation
	type rotation struct {
		reg, scratch int
		mask         uint8
	}
	carriedMask := map[int]uint8{}
	var order []int
	note := func(r int, m uint8) {
		if m == 0 {
			return
		}
		if _, ok := carriedMask[r]; !ok {
			order = append(order, r)
		}
		carriedMask[r] |= m
	}
	for _, cpos := range cons {
		ct := toks[body[cpos]]
		cr, cw := floatAccessLanes(ct)
		for r, m := range cr {
			var wr uint8
			for _, i := range body[:cpos] {
				_, w := floatAccessLanes(toks[i])
				wr |= w[r]
			}
			note(r, m&wr)
		}
		for r, m := range cw {
			var rd uint8
			for _, i := range body[cpos+1:] {
				rr, _ := floatAccessLanes(toks[i])
				rd |= rr[r]
			}
			note(r, m&rd)
		}
	}
	used := floatRegsUsed(toks, L, B)
	var rots []rotation
	for _, r := range order {
		if prefWrites[r]&carriedMask[r] == 0 && prefWrites[r] == 0 {
			continue
		}
		rots = append(rots, rotation{reg: r, mask: carriedMask[r]})
	}
	for _, rt := range rots {
		used[rt.reg] = true
	}
	for i := range rots {
		s, ok := lowerScratch(used)
		if !ok {
			return nil, false
		}
		rots[i].scratch = s
	}
	rotated := map[int]bool{}
	for _, rt := range rots {
		rotated[rt.reg] = true
	}
	// suffix instructions must not clobber prefetch results (outside rotated registers)
	for _, i := range body[cl:] {
		r, w := floatAccessLanes(toks[i])
		for reg, m := range prefWrites {
			if rotated[reg] {
				continue
			}
			if m&(r[reg]|w[reg]) != 0 && i != body[cl] {
				return nil, false
			}
		}
		_ = w
		if i == body[cl] {
			for reg, m := range prefWrites {
				if rotated[reg] {
					continue
				}
				if m&(r[reg]|w[reg]) != 0 {
					return nil, false
				}
			}
		}
	}
	// rotated registers written by the suffix make the plan invalid
	for _, i := range body[cl:] {
		_, w := floatAccessLanes(toks[i])
		for reg := range rotated {
			if w[reg] != 0 {
				return nil, false
			}
		}
	}
	// delayed stores
	delayed := map[int]bool{}
	canDelay := br.ins.f.name == "ibne" || br.ins.f.name == "ibeq"
	var stores []int
	for _, i := range body[c1:last] {
		if plainStore(toks[i]) {
			stores = append(stores, i)
		}
	}
	nextOff := map[int]int{}
	okDelay := canDelay && len(stores) > 0 && !qlive
	for _, i := range stores {
		t := toks[i]
		base, off, ok := storeParts(t)
		if !ok {
			okDelay = false
			break
		}
		us := upd[base]
		if len(us) == 0 {
			okDelay = false
			break
		}
		s := 0
		for _, u := range us {
			if u.idx > i {
				s += u.step
			}
		}
		nextOff[i] = off - s
		// stored value must stay intact
		_, rd := floatAccessLanes(t)
		_ = rd
		for _, reg := range storeFloatRegs(t) {
			if rotated[reg] || prefWrites[reg] != 0 {
				okDelay = false
			}
			for _, j := range body[c1:last] {
				if j > i {
					_, w := floatAccessLanes(toks[j])
					if w[reg] != 0 {
						okDelay = false
					}
				}
			}
		}
		for _, reg := range storeDataRegs(t) {
			for _, j := range body[c1:last] {
				if j > i && contains(intWrites(toks[j]), reg) {
					okDelay = false
				}
			}
		}
		// loads that might alias
		for _, j := range body[c1:last] {
			if j > i && toks[j].ins.f.mem == "load" {
				b2, o2, ok2 := storeParts(toks[j])
				if !ok2 || (b2 == base && o2 == off) {
					okDelay = false
				}
			}
		}
	}
	if okDelay {
		for _, i := range stores {
			delayed[i] = true
		}
	}
	// producer in delay slot?
	prodTok := toks[body[P]]
	inSlot := false
	if qlive {
		if len(pre) != 0 || len(rots) != 0 || prodTok.ins.f.mem != "none" {
			return nil, false
		}
		pr, pw := floatAccessLanes(prodTok)
		_ = pw
		loopW := map[int]uint8{}
		loopR := map[int]uint8{}
		for _, i := range body {
			r, w := floatAccessLanes(toks[i])
			for k, v := range r {
				loopR[k] |= v
			}
			for k, v := range w {
				loopW[k] |= v
			}
		}
		for k, v := range pr {
			if v&loopR[k] != 0 && loopW[k] != 0 {
				return nil, false
			}
		}
		for _, id := range intReads(prodTok) {
			if _, ok := upd[id]; ok {
				return nil, false
			}
		}
	}
	if !qlive && br.ins.f.delay == 1 && !prodTok.ins.f.isBranch && prodTok.ins.f.mem == "none" {
		inSlot = true
		pn := nodeOf(prodTok)
		for _, i := range body[cl+1 : last] {
			in := nodeOf(toks[i])
			if dependentAny(pn, in) {
				inSlot = false
			}
		}
		bn := nodeOf(br)
		if dependentAny(pn, bn) {
			inSlot = false
		}
		if len(rots) > 0 {
			// the producer reads rotated registers through scratch; keep it in the delay slot only if
			// no rotation move overwrites what it reads
		}
	}
	// build the replacement
	lblP := lp.label + "__PROLOG"
	lblD := lp.label + "__DRAIN"
	var out []*token
	out = append(out, &token{kind: tkLabel, label: lblP, line: toks[L].line})
	for _, i := range prolog {
		out = append(out, cloneTok(toks[i]))
	}
	buildRange := func(invert bool) []*token {
		var r []*token
		for pos := c1; pos <= last; pos++ {
			i := body[pos]
			t := toks[i]
			if delayed[i] && pos != last {
				continue
			}
			if pos == last {
				for _, rt := range rots {
					r = append(r, mkMove(rt.reg, rt.scratch, rt.mask))
				}
				nb := cloneTok(t)
				if invert {
					nb = invertBranch(t, lblD)
				}
				if inSlot {
					pc := cloneTok(prodTok)
					for _, rt := range rots {
						substReg(pc, rt.reg, rt.scratch)
					}
					nb.fill = pc
					nb.noPad = false
				}
				r = append(r, nb)
				continue
			}
			r = append(r, cloneTok(t))
			if pos == cl {
				for _, pi := range pre {
					pc := cloneTok(toks[pi])
					for _, rt := range rots {
						substReg(pc, rt.reg, rt.scratch)
					}
					for _, op := range pc.ins.args {
						if op.kind == kMem && op.base != nil {
							if s, ok := step(op.base.phys); ok {
								if v, err := strconv.Atoi(op.offset); err == nil {
									op.offset = strconv.Itoa(v + s)
								}
							}
						}
					}
					r = append(r, pc)
				}
				if !inSlot {
					pc := cloneTok(prodTok)
					for _, rt := range rots {
						substReg(pc, rt.reg, rt.scratch)
					}
					r = append(r, pc)
				}
			}
		}
		return r
	}
	delayedCopies := func() []*token {
		var r []*token
		idx := make([]int, 0)
		for i := range delayed {
			idx = append(idx, i)
		}
		sort.Ints(idx)
		for _, i := range idx {
			c := cloneTok(toks[i])
			for _, op := range c.ins.args {
				if op.kind == kMem {
					op.offset = strconv.Itoa(nextOff[i])
				}
			}
			r = append(r, c)
		}
		return r
	}
	if len(delayed) > 0 {
		out = append(out, buildRange(true)...)
	}
	out = append(out, &token{kind: tkLabel, label: lp.label, line: toks[L].line})
	out = append(out, delayedCopies()...)
	out = append(out, buildRange(false)...)
	if len(delayed) > 0 || qlive {
		out = append(out, &token{kind: tkLabel, label: lblD, line: toks[B].line})
		out = append(out, delayedCopies()...)
		if qlive {
			out = append(out, cloneTok(prodTok))
		}
	}
	_ = mainSet
	return out, true
}

func storeFloatRegs(t *token) []int {
	var r []int
	for _, op := range t.ins.args {
		if op.kind == kVF {
			r = append(r, op.phys)
		}
	}
	return r
}

var planFlagLive = func(int) bool { return true }

func dependentAny(a, b *node) bool {
	f := planFlagLive
	return dependent(a, b, f) || dependent(b, a, f)
}

// applySWP rewrites eligible loops; the second result reports whether any plan was applied.
func applySWP(toks []*token, opt Options) ([]*token, bool) {
	loops := findLoops(toks)
	applied := false
	// process from the last loop so earlier indexes stay valid
	sort.Slice(loops, func(i, j int) bool { return loops[i].lbl > loops[j].lbl })
	for _, lp := range loops {
		if !lp.counted {
			opt.remark("loop %s: not software pipelined (not a counted loop)", lp.label)
			continue
		}
		kind := "single producer"
		repl, ok := planSingle(toks, lp)
		if !ok && !hasQProducer(toks, lp.lbl, lp.br) {
			kind = "prefix rotation"
			repl, ok = planCyclic(toks, lp, opt)
		}
		if !ok && countQProducers(toks, lp.lbl, lp.br) > 1 {
			kind = "multiple Q producers"
			repl, ok = planMultiQ(toks, lp, opt)
		}
		if !ok {
			opt.remark("loop %s: not software pipelined (no plan lowers the cost)", lp.label)
			continue
		}
		opt.remark("loop %s: software pipelined (%s)", lp.label, kind)
		nt := make([]*token, 0, len(toks))
		nt = append(nt, toks[:lp.lbl]...)
		nt = append(nt, repl...)
		nt = append(nt, toks[lp.br+1:]...)
		toks = nt
		applied = true
	}
	return toks, applied
}

func slotCounts(p *parser, toks []*token, opt Options) (int, map[string]int) {
	e := emitTokens(p, toks, opt)
	total := 0
	blocks := map[string]int{}
	cur := ""
	for _, it := range e.items {
		if it.w == nil {
			if len(it.text) > 0 && it.text[len(it.text)-1] == ':' && it.text[0] != ' ' {
				cur = it.text
			}
			continue
		}
		total++
		blocks[cur]++
	}
	return total, blocks
}

func isUpdateTok(t *token) (reg, step int, ok bool) {
	if t.kind != tkInstr || t.label != "" || t.raw || t.ins.flags != "" {
		return 0, 0, false
	}
	f := t.ins.f
	if f.name != "iaddiu" && f.name != "isubiu" {
		return 0, 0, false
	}
	a := t.ins.args
	if a[0].phys != a[1].phys || a[0].phys == 0 {
		return 0, 0, false
	}
	v, ok2 := decimalImm(a[2].imm)
	if !ok2 || v == 0 {
		return 0, 0, false
	}
	if f.name == "isubiu" {
		v = -v
	}
	return a[0].phys, v, true
}

// advanceStoreBases moves pointer updates in front of the stores that precede them.
func advanceStoreBases(p *parser, toks []*token, opt Options) []*token {
	work := make([]*token, len(toks))
	copy(work, toks)
	changed := false
	for i := 1; i < len(work); i++ {
		reg, step, ok := isUpdateTok(work[i])
		if !ok {
			continue
		}
		var stores []int
		for j := i - 1; j >= 0; j-- {
			t := work[j]
			if t.kind != tkInstr || t.label != "" && j != 0 && len(stores) == 0 && false {
				break
			}
			if t.kind != tkInstr {
				break
			}
			if t.fill != nil || t.ins.f.isBranch || t.ins.f.xgkick {
				break
			}
			if plainStore(t) {
				base, _, okp := storeParts(t)
				if okp && base == reg && !contains(storeDataRegs(t), reg) {
					stores = append(stores, j)
					if t.label != "" {
						break
					}
					continue
				}
			}
			n := nodeOf(t)
			if n.reads[viBase+reg] || n.writes[viBase+reg] {
				break
			}
			if t.label != "" {
				break
			}
		}
		if len(stores) == 0 {
			continue
		}
		first := stores[len(stores)-1]
		upd := work[i]
		for _, j := range stores {
			ns := cloneTok(work[j])
			ns.label = work[j].label
			for _, op := range ns.ins.args {
				if op.kind == kMem {
					v, _ := strconv.Atoi(op.offset)
					op.offset = strconv.Itoa(v - step)
				}
			}
			work[j] = ns
		}
		copy(work[first+1:i+1], work[first:i])
		work[first] = upd
		changed = true
	}
	if !changed {
		return toks
	}
	t0, b0 := slotCounts(p, toks, opt)
	t1, b1 := slotCounts(p, work, opt)
	if os.Getenv("VCL_DEBUG") != "" {
		fmt.Fprintln(os.Stderr, "advance:", t0, t1, b0, b1)
	}
	if t1 > t0 {
		return toks
	}
	for k, v := range b1 {
		if v > b0[k] {
			if os.Getenv("VCL_DEBUG") != "" {
				fmt.Fprintln(os.Stderr, "advance rejected on", k, b0[k], v)
			}
			if os.Getenv("VCL_FORCE") == "" {
				return toks
			}
		}
	}
	return work
}

func countQProducers(toks []*token, from, to int) int {
	n := 0
	for i := from; i <= to; i++ {
		if toks[i].kind == tkInstr && toks[i].ins.f.writesQ {
			n++
		}
	}
	return n
}

// liveAcross lists float registers whose values are live when entering or leaving a loop.
func liveAcross(toks []*token, lo, hi int) map[int]bool {
	n := len(toks)
	labels := map[string]int{}
	for i, t := range toks {
		if t.kind == tkLabel || t.label != "" {
			labels[t.label] = i
		}
	}
	in := make([]uint32, n+1)
	out := make([]uint32, n+1)
	use := make([]uint32, n)
	kill := make([]uint32, n)
	for i, t := range toks {
		if t.kind != tkInstr {
			continue
		}
		for _, a := range t.ins.accesses() {
			if a.isInt || a.special != "" || a.op == nil || a.op.phys <= 0 {
				continue
			}
			bit := uint32(1) << uint(a.op.phys)
			if a.write {
				if a.lanes == laneAll {
					kill[i] |= bit
				}
			} else {
				use[i] |= bit
			}
		}
	}
	succ := func(i int) []int {
		t := toks[i]
		var r []int
		if t.kind == tkInstr && t.ins.f.isBranch {
			f := t.ins.f
			if f.regBranch {
				if f.link && i+1 < n {
					r = append(r, i+1)
				}
				return r
			}
			if j, ok := labels[t.ins.args[len(t.ins.args)-1].imm]; ok {
				r = append(r, j)
			}
			if (!f.uncond || f.link) && i+1 < n {
				r = append(r, i+1)
			}
			return r
		}
		if i+1 < n {
			r = append(r, i+1)
		}
		return r
	}
	for changed := true; changed; {
		changed = false
		for i := n - 1; i >= 0; i-- {
			var o uint32
			for _, s := range succ(i) {
				o |= in[s]
			}
			ni := o&^kill[i] | use[i]
			if o != out[i] || ni != in[i] {
				out[i], in[i] = o, ni
				changed = true
			}
		}
	}
	res := map[int]bool{}
	for r := 1; r < 32; r++ {
		bit := uint32(1) << uint(r)
		if in[lo]&bit != 0 || out[hi]&bit != 0 {
			res[r] = true
		}
	}
	return res
}

func (o Options) remark(format string, a ...any) {
	if o.Remark != nil {
		o.Remark(fmt.Sprintf(format, a...))
	}
}
