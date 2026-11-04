package vcl

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

type webKey struct {
	tok int
	op  *operand
}

func (al *allocator) run() {
	toks := al.toks
	n := len(toks)
	firstCode := -1
	for i, t := range toks {
		if t.kind == tkInstr {
			firstCode = i
			break
		}
	}
	al.collect()
	al.checkSpecials()
	var webs []*web
	useWeb := map[*useSite]*web{}
	defWeb := map[webKey]*web{}
	entryWeb := map[string]*web{}
	for _, as := range al.aliases {
		if as.phys >= 0 {
			continue
		}
		if reg, ok := al.inPins[as.key]; ok {
			as.pinned = reg
			as.entry = true
			as.defs = append(as.defs, defSite{tok: -1, arg: 0, lanes: laneAll})
		}
	}
	for _, as := range al.aliases {
		entryDefined := as.entry || (as.phys == 0)
		sets, undef := al.reaching(as, entryDefined)
		nd := len(as.defs)
		p := &uf{p: make([]int, nd)}
		for i := range p.p {
			p.p[i] = i
		}
		for ui, u := range as.uses {
			first := -1
			for di := 0; di < nd; di++ {
				if sets[ui].has(di) {
					if first < 0 {
						first = di
					} else {
						p.union(first, di)
					}
				}
			}
			if undef[ui] && (u.acc.check || nd == 0) && len(al.res.diags) == 0 {
				al.err(u.token.line, al.readErr(u))
			}
		}
		if as.phys >= 0 {
			continue
		}
		ip := &uf{p: make([]int, nd)}
		for i := range ip.p {
			ip.p[i] = i
		}
		cont := make([]bool, nd)
		if !as.isInt {
			var covered uint8
			cur := -1
			for di, d := range as.defs {
				if d.tok < 0 {
					cur = di
					continue
				}
				if cur >= 0 && d.lanes&covered != covered {
					p.union(cur, di)
					ip.union(cur, di)
					cont[di] = true
				}
				covered |= d.lanes
				cur = di
			}
		}
		as.cont = cont
		back := make([]bool, nd)
		for di := range as.defs {
			back[di] = false
		}
		for ui, u := range as.uses {
			for di, d := range as.defs {
				if d.tok >= 0 && sets[ui].has(di) && u.tok <= d.tok {
					back[di] = true
				}
			}
		}
		instKey := map[int]int{}
		instFwd := map[int]bool{}
		for di, d := range as.defs {
			if d.tok < 0 {
				continue
			}
			ord := d.tok*64 + 63 - d.arg
			r := ip.find(di)
			if k, ok := instKey[r]; !ok || ord < k {
				instKey[r] = ord
			}
			if !back[di] {
				instFwd[r] = true
			}
		}
		roots := map[int]*web{}
		get := func(di int) *web {
			r := p.find(di)
			w := roots[r]
			if w == nil {
				w = &web{id: len(webs), key: as.key, name: as.name, isInt: as.isInt, pinned: -1, reg: -1, lines: map[int]bool{}}
				w.order = 1 << 60
				roots[r] = w
				webs = append(webs, w)
			}
			return w
		}
		for di, d := range as.defs {
			w := get(di)
			w.lanes |= d.lanes
			if d.tok >= 0 {
				r := ip.find(di)
				ord := instKey[r]
				if !w.hasDef {
					w.order = ord
					w.fwd = instFwd[r]
				} else if instFwd[r] && (!w.fwd || ord > w.order) {
					w.order = ord
					w.fwd = true
				} else if !instFwd[r] && !w.fwd && ord < w.order {
					w.order = ord
				}
				w.hasDef = true
			}
			if d.tok < 0 {
				w.pinned = as.pinned
				entryWeb[as.key] = w
			}
		}
		for dk, di := range as.defOf {
			defWeb[webKey{dk.tok, dk.op}] = get(di)
		}
		for ui := range as.uses {
			u := &as.uses[ui]
			for di := 0; di < nd; di++ {
				if sets[ui].has(di) {
					useWeb[u] = get(di)
					break
				}
			}
			if w := useWeb[u]; w != nil {
				ord := u.tok*64 + 64
				if as.entry && !w.hasDef && ord < w.order {
					w.order = ord - 1
				}
			}
		}
		if reg, ok := al.outPins[as.key]; ok {
			for _, w := range roots {
				if w.pinned < 0 {
					w.pinned = reg
				}
			}
		}
	}
	nw := len(webs) * 4
	words := (nw + 63) / 64
	if words == 0 {
		words = 1
	}
	liveIn := make([]bits, n+1)
	liveOut := make([]bits, n+1)
	for i := range liveIn {
		liveIn[i] = make(bits, words)
		liveOut[i] = make(bits, words)
	}
	useSet := make([]bits, n)
	killSet := make([]bits, n)
	defSet := make([]bits, n)
	for i := 0; i < n; i++ {
		useSet[i] = make(bits, words)
		killSet[i] = make(bits, words)
		defSet[i] = make(bits, words)
	}
	for _, as := range al.aliases {
		if as.phys >= 0 {
			continue
		}
		for ui := range as.uses {
			u := &as.uses[ui]
			if w := useWeb[u]; w != nil {
				for l := 0; l < 4; l++ {
					useSet[u.tok].set(w.id*4 + l)
				}
			}
		}
		for dk, di := range as.defOf {
			w := defWeb[webKey{dk.tok, dk.op}]
			if w == nil {
				continue
			}
			for l := 0; l < 4; l++ {
				defSet[dk.tok].set(w.id*4 + l)
				if !as.cont[di] {
					killSet[dk.tok].set(w.id*4 + l)
				} else {
					useSet[dk.tok].set(w.id*4 + l)
				}
			}
		}
		if w := entryWeb[as.key]; w != nil && firstCode >= 0 {
			for l := 0; l < 4; l++ {
				defSet[firstCode].set(w.id*4 + l)
			}
		}
	}
	tmp := make(bits, words)
	for changed := true; changed; {
		changed = false
		for i := n - 1; i >= 0; i-- {
			for _, s := range al.succ(i) {
				if liveOut[i].or(liveIn[s]) {
					changed = true
				}
			}
			for k := range tmp {
				tmp[k] = liveOut[i][k]&^killSet[i][k] | useSet[i][k]
			}
			if liveIn[i].or(tmp) {
				changed = true
			}
		}
	}
	for _, w := range webs {
		lo := 1 << 30
		for i := 0; i < n; i++ {
			if anyLane(defSet[i], w.id) && toks[i].line < lo {
				lo = toks[i].line
			}
		}
		for i := 0; i < n; i++ {
			if toks[i].line < lo {
				continue
			}
			if anyLane(liveOut[i], w.id) || anyLane(defSet[i], w.id) || anyLane(useSet[i], w.id) {
				w.lines[toks[i].line] = true
			}
		}
	}
	for i, t := range toks {
		if t.kind != tkInstr || !t.ins.f.isBranch || t.ins.f.regBranch {
			continue
		}
		target := t.ins.args[len(t.ins.args)-1].imm
		j, ok := al.labels[target]
		if !t.ins.f.uncond && i+1 < n {
			for _, w := range webs {
				if anyLane(liveOut[i], w.id) {
					w.lines[toks[i+1].line] = true
				}
			}
		}
		if !ok {
			continue
		}
		if j <= i {
			end := t.line
			if i+1 < n {
				end = toks[i+1].line
			}
			for _, w := range webs {
				if anyLane(liveIn[j], w.id) && anyLane(liveOut[i], w.id) {
					for l := t.line; l <= end; l++ {
						w.lines[l] = true
					}
				}
			}
		} else if t.ins.f.uncond && !t.ins.f.link {
			for _, w := range webs {
				if anyLane(liveOut[i], w.id) {
					for l := t.line + 1; l < toks[j].line; l++ {
						w.lines[l] = true
					}
				}
			}
		}
	}
	al.contExtend(webs, liveOut)
	al.loopCS(webs)
	al.fillGaps(webs)
	al.assign(webs)
	if al.opts.Respread > 0 && !al.res.failed {
		al.respread(webs)
	}
	for _, as := range al.aliases {
		if as.phys >= 0 {
			continue
		}
		for dk := range as.defOf {
			if w := defWeb[webKey{dk.tok, dk.op}]; w != nil && dk.op != nil {
				dk.op.phys = w.reg
			}
		}
		for ui := range as.uses {
			u := &as.uses[ui]
			if w := useWeb[u]; w != nil {
				u.acc.op.phys = w.reg
			}
		}
	}
	al.webs = webs
}

// contExtend holds values that live across a --cont through the block that follows.
func (al *allocator) contExtend(webs []*web, liveOut []bits) {
	toks := al.toks
	n := len(toks)
	for i, t := range toks {
		if t.kind != tkDir || t.dir != "--cont" {
			continue
		}
		end := i + 1
		for end < n {
			d := toks[end]
			if d.kind == tkDir && (d.dir == "--cont" || d.dir == "--exit") {
				break
			}
			end++
		}
		last := toks[n-1].line
		if end-1 >= 0 && end-1 < n {
			last = toks[end-1].line
		}
		for _, w := range webs {
			if anyLane(liveOut[i], w.id) {
				for l := t.line; l <= last; l++ {
					w.lines[l] = true
				}
			}
		}
	}
}

// loopCS widens float lifetimes across loops marked with --LoopCS.
func (al *allocator) loopCS(webs []*web) {
	toks := al.toks
	for i, t := range toks {
		if t.kind != tkInstr || !t.ins.f.isBranch || t.ins.f.regBranch {
			continue
		}
		target := t.ins.args[len(t.ins.args)-1].imm
		j, ok := al.labels[target]
		if !ok || j > i {
			continue
		}
		has := false
		for _, k := range []int{j, j + 1} {
			if k < len(toks) && toks[k].kind == tkDir && toks[k].dir == "--loopcs" {
				has = true
			}
		}
		if !has {
			continue
		}
		lo, hi := toks[j].line, t.line
		var over []*web
		defer func() {
			if os.Getenv("VCL_DEBUG") != "" {
				fmt.Fprintln(os.Stderr, "loopcs", lo, hi, len(over), len(al.pool[0]))
			}
		}()
		for _, w := range webs {
			if w.isInt {
				continue
			}
			for l := range w.lines {
				if l >= lo && l <= hi {
					over = append(over, w)
					break
				}
			}
		}
		if len(over) > len(al.pool[0]) {
			if al.opts.CSSlack == 0 {
				continue
			}
			keep := len(al.pool[0]) - al.opts.CSSlack
			if keep <= 0 {
				continue
			}
			count := map[string]int{}
			for _, w := range over {
				count[w.name]++
			}
			sort.SliceStable(over, func(i, j int) bool {
				ci, cj := count[over[i].name], count[over[j].name]
				if ci != cj {
					return ci > cj
				}
				return over[i].order < over[j].order
			})
			over = over[:keep]
		}
		for _, w := range over {
			for l := lo; l <= hi; l++ {
				w.lines[l] = true
			}
		}
	}
}

func (al *allocator) readErr(u useSite) string {
	box := ""
	kind := "integer"
	if !u.acc.isInt {
		kind = "float"
		box = " " + laneBox(u.acc.lanes)
	}
	return "Read-attempt from uninitialized " + kind + " register (" + instrText(u.ins, u.acc.arg) + box + ")"
}

func instrText(ins *instr, blame int) string {
	var b strings.Builder
	b.WriteString(ins.word)
	for i, a := range ins.args {
		if i == 0 {
			b.WriteByte(' ')
		} else {
			b.WriteString(", ")
		}
		if i == blame {
			b.WriteString(">>> " + a.text + " <<<")
		} else {
			b.WriteString(a.text)
		}
	}
	return b.String()
}

type litUse struct {
	reg   int
	lines map[int]bool
	order int
}

func (al *allocator) assign(webs []*web) {
	sort.SliceStable(webs, func(i, j int) bool { return webs[i].order < webs[j].order })
	taken := [2]map[int][]*web{{}, {}}
	litLines := map[int]map[int]bool{}
	for _, t := range al.toks {
		if t.kind != tkInstr {
			continue
		}
		for _, a := range t.ins.accesses() {
			if a.isInt && a.op != nil && a.op.lit && a.special == "" {
				if litLines[a.op.phys] == nil {
					litLines[a.op.phys] = map[int]bool{}
				}
				litLines[a.op.phys][t.line] = true
			}
		}
	}
	fl := map[int]map[int]bool{}
	for _, t := range al.toks {
		if t.kind != tkInstr {
			continue
		}
		for _, a := range t.ins.accesses() {
			if !a.isInt && a.op != nil && a.op.lit && a.op.phys > 0 && a.special == "" {
				if fl[a.op.phys] == nil {
					fl[a.op.phys] = map[int]bool{}
				}
				fl[a.op.phys][t.line] = true
			}
		}
	}
	// integer webs of one name share a register
	group := map[string][]*web{}
	for _, w := range webs {
		if w.isInt {
			group[w.key] = append(group[w.key], w)
		}
	}
	conflict := func(ws []*web, kind, reg int) bool {
		for _, w := range ws {
			for _, o := range taken[kind][reg] {
				for l := range w.lines {
					if o.lines[l] {
						return true
					}
				}
			}
			lit := litLines[reg]
			if kind == 0 {
				lit = fl[reg]
			}
			for l := range w.lines {
				if lit[l] {
					return true
				}
			}
		}
		return false
	}
	for _, w := range webs {
		if w.pinned >= 0 {
			kind := 0
			if w.isInt {
				kind = 1
			}
			w.reg = w.pinned
			taken[kind][w.reg] = append(taken[kind][w.reg], w)
		}
	}
	for _, w := range webs {
		if w.pinned >= 0 || w.reg >= 0 {
			continue
		}
		kind := 0
		ws := []*web{w}
		if w.isInt {
			kind = 1
			ws = nil
			for _, g := range group[w.key] {
				if g.pinned < 0 && g.reg < 0 {
					ws = append(ws, g)
				}
			}
		}
		placed := false
		pool := al.pool[kind]
		if al.opts.SpreadWidth > 0 && kind == 0 {
			var free []int
			for _, r := range pool {
				if !conflict(ws, kind, r) {
					free = append(free, r)
					if len(free) == al.opts.SpreadWidth {
						break
					}
				}
			}
			if len(free) > 0 {
				pool = al.spreadOrder(free, taken[kind])
			}
		}
		for _, r := range pool {
			if !conflict(ws, kind, r) {
				for _, x := range ws {
					x.reg = r
					taken[kind][r] = append(taken[kind][r], x)
				}
				placed = true
				break
			}
		}
		if !placed {
			al.res.failed = true
			if al.opts.ShowAlloc {
				kn := "FLOAT"
				if kind == 1 {
					kn = "INTEGER"
				}
				al.res.diags = append(al.res.diags, diag{plain: true, msg: "Failed to allocate " + kn + " register for alias"})
			}
			al.res.diags = append(al.res.diags, diag{plain: true, msg: "Register allocation ran out of registers"})
			return
		}
	}
	_ = fmt.Sprint
}

func rangesOf(lines map[int]bool) []lineRange {
	var ls []int
	for l := range lines {
		ls = append(ls, l)
	}
	sort.Ints(ls)
	var out []lineRange
	for _, l := range ls {
		if len(out) > 0 && l <= out[len(out)-1].hi+1 {
			out[len(out)-1].hi = l
			continue
		}
		out = append(out, lineRange{l, l})
	}
	return out
}

type lineRange struct{ lo, hi int }

func fmtRanges(r []lineRange) string {
	var parts []string
	for _, x := range r {
		parts = append(parts, fmt.Sprintf("[%d-%d]", x.lo, x.hi))
	}
	return strings.Join(parts, ", ")
}

func (al *allocator) report() string {
	var b strings.Builder
	var fl, in int
	var fp, ip int
	for _, w := range al.webs {
		if w.isInt {
			in++
			if w.pinned >= 0 {
				ip++
			}
		} else {
			fl++
			if w.pinned >= 0 {
				fp++
			}
		}
	}
	lit := map[int]map[int]bool{}
	var litOrder []int
	for _, t := range al.toks {
		if t.kind != tkInstr {
			continue
		}
		for _, a := range t.ins.accesses() {
			if a.isInt && a.op != nil && a.op.lit && a.special == "" {
				if lit[a.op.phys] == nil {
					lit[a.op.phys] = map[int]bool{}
					litOrder = append(litOrder, a.op.phys)
				}
				lit[a.op.phys][t.line] = true
			}
		}
	}
	in += len(lit)
	ip += len(lit)
	b.WriteString("\n=== Register Allocation Debug ===\n")
	fmt.Fprintf(&b, "Float registers: %d available, %d needed (total aliases: %d, preallocated: %d)\n", len(al.pool[0]), fl-fp, fl, fp)
	fmt.Fprintf(&b, "Integer registers: %d available, %d needed (total aliases: %d, preallocated: %d)\n", len(al.pool[1]), in-ip, in, ip)
	b.WriteString("\n=== Alias Ranges ===\n")
	for _, w := range al.webs {
		kind := "FLOAT"
		if w.isInt {
			kind = "INT"
		}
		pre := ""
		if w.pinned >= 0 {
			pre = fmt.Sprintf(" [prealloc: V%s%02d]", map[bool]string{false: "F", true: "I"}[w.isInt], w.pinned)
		}
		nm := ""
		if os.Getenv("VCL_DEBUG") != "" {
			nm = "(" + w.name + ")"
		}
		fmt.Fprintf(&b, "%s%s:%s ranges: %s\n", kind, nm, pre, fmtRanges(rangesOf(w.lines)))
	}
	for _, r := range litOrder {
		fmt.Fprintf(&b, "INT: [prealloc: VI%02d] ranges: %s\n", r, fmtRanges(rangesOf(lit[r])))
	}
	b.WriteString("====================\n")
	return b.String()
}

func (al *allocator) checkSpecials() {
	written := map[string]bool{}
	for _, t := range al.toks {
		if t.kind != tkInstr {
			continue
		}
		for _, a := range t.ins.accesses() {
			if a.special == "" || a.special == "acc" {
				continue
			}
			if a.write {
				continue
			}
			if !written[a.special] && !al.hw["in_hw_"+a.special] && len(al.res.diags) == 0 {
				name := map[string]string{"q": "Q", "p": "P", "i": "I", "r": "R"}[a.special]
				al.err(t.line, "Read-attempt from uninitialized "+name+" register ("+instrText(t.ins, a.arg)+")")
			}
		}
		for _, a := range t.ins.accesses() {
			if a.special != "" && a.write {
				written[a.special] = true
			}
		}
		for _, r := range t.ins.f.writesRes {
			written[r] = true
		}
	}
}

// fillGaps closes holes that contain no source tokens (blank and comment lines).
func (al *allocator) fillGaps(webs []*web) {
	has := map[int]bool{}
	for _, t := range al.toks {
		has[t.line] = true
	}
	for _, w := range webs {
		var ls []int
		for l := range w.lines {
			ls = append(ls, l)
		}
		sort.Ints(ls)
		for i := 0; i+1 < len(ls); i++ {
			a, b := ls[i], ls[i+1]
			if b-a <= 1 {
				continue
			}
			empty := true
			for l := a + 1; l < b; l++ {
				if has[l] {
					empty = false
					break
				}
			}
			if empty {
				for l := a + 1; l < b; l++ {
					w.lines[l] = true
				}
			}
		}
	}
}

func anyLane(b bits, id int) bool {
	return b.has(id*4) || b.has(id*4+1) || b.has(id*4+2) || b.has(id*4+3)
}

// spreadOrder lists registers with the least recently released first.
func (al *allocator) spreadOrder(pool []int, taken map[int][]*web) []int {
	type cand struct{ r, last int }
	var cs []cand
	for _, r := range pool {
		last := 0
		for _, w := range taken[r] {
			for l := range w.lines {
				if l > last {
					last = l
				}
			}
		}
		cs = append(cs, cand{r, last})
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].last < cs[j].last })
	out := make([]int, len(cs))
	for i, c := range cs {
		out[i] = c.r
	}
	return out
}

func webBounds(w *web) (int, int) {
	lo, hi := 1<<30, 0
	for l := range w.lines {
		if l < lo {
			lo = l
		}
		if l > hi {
			hi = l
		}
	}
	return lo, hi
}

// respread moves short-lived float values to registers that were idle the longest.
func (al *allocator) respread(webs []*web) {
	byReg := map[int][]*web{}
	for _, w := range webs {
		if !w.isInt && w.reg > 0 {
			byReg[w.reg] = append(byReg[w.reg], w)
		}
	}
	fl := map[int]map[int]bool{}
	for _, t := range al.toks {
		if t.kind != tkInstr {
			continue
		}
		for _, a := range t.ins.accesses() {
			if !a.isInt && a.op != nil && a.op.lit && a.op.phys > 0 && a.special == "" {
				if fl[a.op.phys] == nil {
					fl[a.op.phys] = map[int]bool{}
				}
				fl[a.op.phys][t.line] = true
			}
		}
	}
	gapOf := func(w *web, reg int) (int, bool) {
		lo, _ := webBounds(w)
		gap := 1 << 20
		for _, o := range byReg[reg] {
			if o == w {
				continue
			}
			for l := range w.lines {
				if o.lines[l] {
					return 0, false
				}
			}
			for l := range fl[reg] {
				if w.lines[l] {
					return 0, false
				}
			}
			_, ohi := webBounds(o)
			if ohi < lo && lo-ohi < gap {
				gap = lo - ohi
			}
		}
		return gap, true
	}
	order := append([]*web(nil), webs...)
	sort.SliceStable(order, func(i, j int) bool { return order[i].order < order[j].order })
	regions := al.loopRegions()
	for _, w := range order {
		if w.isInt || w.pinned >= 0 || w.reg <= 0 {
			continue
		}
		if len(al.opts.RespreadLoops) > 0 {
			lo, hi := webBounds(w)
			in := false
			for _, k := range al.opts.RespreadLoops {
				if k < len(regions) && lo >= regions[k][0] && hi <= regions[k][1] {
					in = true
				}
			}
			if !in {
				continue
			}
		}
		cur, _ := gapOf(w, w.reg)
		if cur >= al.opts.Respread {
			continue
		}
		bestReg, bestGap := w.reg, cur
		for _, r := range al.pool[0] {
			if r == w.reg {
				continue
			}
			g, ok := gapOf(w, r)
			if ok && g > bestGap {
				bestReg, bestGap = r, g
			}
		}
		if bestReg != w.reg {
			old := byReg[w.reg]
			for i, o := range old {
				if o == w {
					byReg[w.reg] = append(old[:i:i], old[i+1:]...)
					break
				}
			}
			w.reg = bestReg
			byReg[bestReg] = append(byReg[bestReg], w)
		}
	}
}

// loopRegions lists the source line spans of backward branches, in program order.
func (al *allocator) loopRegions() [][2]int {
	var out [][2]int
	for i, t := range al.toks {
		if t.kind != tkInstr || !t.ins.f.isBranch || t.ins.f.regBranch {
			continue
		}
		j, ok := al.labels[t.ins.args[len(t.ins.args)-1].imm]
		if ok && j <= i {
			out = append(out, [2]int{al.toks[j].line, t.line})
		}
	}
	return out
}

// LoopRegionCount reports how many loops a program has.
