package vcl

import "strconv"

func (t *token) plain() bool {
	return t.kind == tkInstr && !t.raw && t.label == "" && t.ins.flags == ""
}

func fillable(t *token) bool {
	if t.kind != tkInstr || t.raw {
		return false
	}
	f := t.ins.f
	return f.isBranch && f.delay == 1 && !f.link && !f.regBranch && t.ins.flags == ""
}

func branchReads(t *token, reg int) bool {
	for _, a := range t.ins.accesses() {
		if a.isInt && !a.write && a.op != nil && a.op.phys == reg && a.special == "" {
			return true
		}
	}
	return false
}

func intWrites(t *token) []int {
	var r []int
	for _, a := range t.ins.accesses() {
		if a.isInt && a.write && a.op != nil && a.special == "" {
			r = append(r, a.op.phys)
		}
	}
	return r
}

func intReads(t *token) []int {
	var r []int
	for _, a := range t.ins.accesses() {
		if a.isInt && !a.write && a.op != nil && a.special == "" {
			r = append(r, a.op.phys)
		}
	}
	return r
}

func fillCandidate(t *token) bool {
	if !t.plain() {
		return false
	}
	f := t.ins.f
	if f.unit != "ialu" || f.latency > 1 || f.isBranch || readsFlags(f) {
		return false
	}
	if len(f.readsRes) > 0 || len(f.writesRes) > 0 {
		return false
	}
	return true
}

func selfAdd(t *token) (reg, step int, ok bool) {
	if !t.plain() || t.ins.f.name != "iaddiu" {
		return 0, 0, false
	}
	a := t.ins.args
	if a[0].phys != a[1].phys || a[0].phys == 0 {
		return 0, 0, false
	}
	v, err := strconv.Atoi(a[2].imm)
	if err != nil {
		return 0, 0, false
	}
	return a[0].phys, v, true
}

func plainStore(t *token) bool {
	if !t.plain() {
		return false
	}
	f := t.ins.f
	return f.mem == "store" && !f.postinc && !f.predec && f.name != "xgkick"
}

func storeParts(t *token) (base int, off int, ok bool) {
	for _, op := range t.ins.args {
		if op.kind == kMem && op.base != nil {
			v, err := strconv.Atoi(op.offset)
			if err != nil {
				return 0, 0, false
			}
			return op.base.phys, v, true
		}
	}
	return 0, 0, false
}

func storeDataRegs(t *token) []int {
	var r []int
	for _, op := range t.ins.args {
		if op.kind == kVI {
			r = append(r, op.phys)
		}
	}
	return r
}

func contains(l []int, v int) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// applyFills moves instructions into branch delay slots.
func applyFills(toks []*token, labels map[string]int) []*token {
	var out []*token
	n := len(toks)
	removed := make([]bool, n)
	for i := 0; i < n; i++ {
		if removed[i] {
			continue
		}
		t := toks[i]
		out = append(out, t)
		if !fillable(t) {
			continue
		}
		args := t.ins.args
		target := args[len(args)-1].imm
		// kinds 1 and 2: a store followed by a pointer increment
		if len(out) >= 3 {
			a, s := out[len(out)-2], out[len(out)-3]
			if reg, step, ok := selfAdd(a); ok && branchReads(t, reg) && plainStore(s) {
				base, off, ok2 := storeParts(s)
				if ok2 && !contains(storeDataRegs(s), reg) {
					if base == reg {
						ns := *s
						ni := *s.ins
						ni.args = append([]*operand(nil), s.ins.args...)
						for k, op := range ni.args {
							if op.kind == kMem {
								cp := *op
								cp.offset = strconv.Itoa(off - step)
								ni.args[k] = &cp
							}
						}
						ns.ins = &ni
						t.fill = &ns
						out = append(out[:len(out)-3], a, t)
						continue
					}
					t.fill = s
					out = append(out[:len(out)-3], a, t)
					continue
				}
			}
		}
		// kind 3: the previous instruction
		if len(out) >= 2 {
			c := out[len(out)-2]
			if fillCandidate(c) {
				ok := true
				for _, w := range intWrites(c) {
					if w != 0 && branchReads(t, w) {
						ok = false
					}
				}
				if ok {
					t.fill = c
					out = append(out[:len(out)-2], t)
					continue
				}
			}
		}
		// kind 4: the next instruction when the target overwrites its result
		if !t.ins.f.uncond && i+1 < n {
			c := toks[i+1]
			if fillCandidate(c) {
				j, ok := labels[target]
				if ok && j > i {
					ws := intWrites(c)
					if len(ws) == 1 && ws[0] != 0 && deadFrom(toks, j, ws[0]) {
						t.fill = c
						removed[i+1] = true
					}
				}
			}
		}
	}
	return out
}

// deadFrom scans linearly from a target for the first access of an integer register.
func deadFrom(toks []*token, from int, reg int) bool {
	for k := from; k < len(toks); k++ {
		t := toks[k]
		if t.kind != tkInstr {
			continue
		}
		if contains(intReads(t), reg) {
			return false
		}
		if contains(intWrites(t), reg) {
			return true
		}
	}
	return true
}
