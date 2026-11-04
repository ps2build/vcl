package vcl

type web struct {
	id      int
	key     string
	name    string
	isInt   bool
	pinned  int
	reg     int
	order   int
	lanes   uint8
	lines   map[int]bool
	literal bool
	root    int
	hasDef  bool
	fwd     bool
}

type defSite struct {
	tok   int
	arg   int
	lanes uint8
}

type useSite struct {
	tok   int
	acc   access
	ins   *instr
	token *token
}

type aliasState struct {
	key    string
	name   string
	isInt  bool
	phys   int
	defs   []defSite
	uses   []useSite
	pinned int
	entry  bool
	defOf  map[defKey]int
	cont   []bool
}

type defKey struct {
	tok int
	op  *operand
}

type allocResult struct {
	diags  []diag
	report string
	failed bool
}

type allocator struct {
	toks    []*token
	labels  map[string]int
	aliases []*aliasState
	byKey   map[string]*aliasState
	opts    Options
	pool    [2][]int
	inPins  map[string]int
	outPins map[string]int
	hw      map[string]bool
	res     allocResult
	opWeb   map[*operand]*web
	opsAt   map[defKey]*web
	webs    []*web
}

func (al *allocator) err(line int, msg string) {
	al.res.diags = append(al.res.diags, diag{line: line, msg: msg})
}

func refOf(a access) (key, name string, isInt bool, phys int, ok bool) {
	if a.op == nil || a.special != "" {
		return "", "", false, -1, false
	}
	pre := "f:"
	if a.isInt {
		pre = "i:"
	}
	if a.op.lit {
		return pre + "#" + itoa(a.op.phys), "", a.isInt, a.op.phys, true
	}
	return pre + a.op.name, a.op.name, a.isInt, -1, true
}

func (al *allocator) alias(key, name string, isInt bool, phys int) *aliasState {
	if a, ok := al.byKey[key]; ok {
		return a
	}
	a := &aliasState{key: key, name: name, isInt: isInt, phys: phys, pinned: -1, defOf: map[defKey]int{}}
	al.byKey[key] = a
	al.aliases = append(al.aliases, a)
	return a
}

func (al *allocator) succ(i int) []int {
	t := al.toks[i]
	n := len(al.toks)
	var out []int
	if t.kind == tkInstr && t.ins.f.isBranch {
		f := t.ins.f
		if f.regBranch {
			if f.link && i+1 < n {
				out = append(out, i+1)
			}
			return out
		}
		target := t.ins.args[len(t.ins.args)-1].imm
		if j, ok := al.labels[target]; ok {
			out = append(out, j)
		}
		if (!f.uncond || f.link) && i+1 < n {
			out = append(out, i+1)
		}
		return out
	}
	if i+1 < n {
		out = append(out, i+1)
	}
	return out
}

func (al *allocator) lineOf(i int) int {
	if i >= len(al.toks) {
		i = len(al.toks) - 1
	}
	return al.toks[i].line
}

type bits []uint64

func newBits(n int) bits { return make(bits, (n+63)/64) }
func (b bits) set(i int) { b[i>>6] |= 1 << uint(i&63) }
func (b bits) has(i int) bool {
	return b[i>>6]&(1<<uint(i&63)) != 0
}
func (b bits) or(o bits) bool {
	ch := false
	for i := range b {
		n := b[i] | o[i]
		if n != b[i] {
			b[i] = n
			ch = true
		}
	}
	return ch
}
func (b bits) copyFrom(o bits) { copy(b, o) }
func (b bits) clear() {
	for i := range b {
		b[i] = 0
	}
}

type uf struct{ p []int }

func (u *uf) find(x int) int {
	for u.p[x] != x {
		u.p[x] = u.p[u.p[x]]
		x = u.p[x]
	}
	return x
}
func (u *uf) union(a, b int) {
	a, b = u.find(a), u.find(b)
	if a != b {
		if a < b {
			u.p[b] = a
		} else {
			u.p[a] = b
		}
	}
}

func (al *allocator) collect() {
	for ti, t := range al.toks {
		if t.kind != tkInstr {
			continue
		}
		accs := t.ins.accesses()
		for k := len(accs) - 1; k >= 0; k-- {
			a := accs[k]
			key, name, isInt, phys, ok := refOf(a)
			if !ok {
				continue
			}
			as := al.alias(key, name, isInt, phys)
			if a.write {
				dk := defKey{ti, a.op}
				as.defOf[dk] = len(as.defs)
				as.defs = append(as.defs, defSite{tok: ti, arg: a.arg, lanes: a.lanes})
			}
		}
		for _, a := range accs {
			key, _, _, _, ok := refOf(a)
			if !ok || a.write {
				continue
			}
			as := al.byKey[key]
			if as == nil {
				_, name, isInt, phys, _ := refOf(a)
				as = al.alias(key, name, isInt, phys)
			}
			as.uses = append(as.uses, useSite{tok: ti, acc: a, ins: t.ins, token: t})
		}
	}
}

// reaching computes for one alias the reaching-definition sets at every use.
// It returns, per use, the set of def indexes and whether an undefined path reaches.
func (al *allocator) reaching(as *aliasState, entryDefined bool) (sets []bits, undef []bool) {
	n := len(al.toks)
	nd := len(as.defs) + 1 // last bit is the undefined marker
	words := (nd + 63) / 64
	in := make([][4]bits, n+1)
	for i := range in {
		for l := 0; l < 4; l++ {
			in[i][l] = make(bits, words)
		}
	}
	defAt := map[int][]int{}
	entryIdx := -1
	for di, d := range as.defs {
		if d.tok < 0 {
			entryIdx = di
			continue
		}
		defAt[d.tok] = append(defAt[d.tok], di)
	}
	undefBit := nd - 1
	if entryIdx >= 0 {
		for l := 0; l < 4; l++ {
			in[0][l].set(entryIdx)
		}
	} else if !entryDefined {
		for l := 0; l < 4; l++ {
			in[0][l].set(undefBit)
		}
	}
	work := make([]bool, n)
	for i := range work {
		work[i] = true
	}
	tmp := [4]bits{}
	for l := range tmp {
		tmp[l] = make(bits, words)
	}
	for changed := true; changed; {
		changed = false
		for i := 0; i < n; i++ {
			for l := 0; l < 4; l++ {
				tmp[l].copyFrom(in[i][l])
			}
			if ds, ok := defAt[i]; ok {
				for _, di := range ds {
					m := as.defs[di].lanes
					for l := 0; l < 4; l++ {
						if m&(1<<uint(l)) != 0 {
							tmp[l].clear()
							tmp[l].set(di)
						}
					}
				}
			}
			for _, s := range al.succ(i) {
				for l := 0; l < 4; l++ {
					if in[s][l].or(tmp[l]) {
						changed = true
					}
				}
			}
		}
	}
	for _, u := range as.uses {
		set := make(bits, words)
		un := false
		for l := 0; l < 4; l++ {
			if u.acc.lanes&(1<<uint(l)) != 0 {
				set.or(in[u.tok][l])
			}
		}
		if set.has(undefBit) {
			un = true
		}
		sets = append(sets, set)
		undef = append(undef, un)
	}
	return
}
