package vcl

import (
	"fmt"
	"os"
	"strings"
)

type item struct {
	text string
	w    *word
}

type emitter struct {
	p       *parser
	toks    []*token
	opts    Options
	items   []item
	clock   int
	ready   map[int]int
	prod    map[int]*form
	issued  map[int]int
	capFill bool
	lastUp  bool
	hasLast bool
	macRead bool
	macLast int
	tokIdx  map[*token]int
	// state
	codeSince  bool
	pending    string
	entryWaits map[string]map[int]int
	backWaits  map[string]map[int]int
	fwdWaits   map[string]map[int]int
	seen       map[string]bool
	termBr     bool
	flagH      int
	stallAdj   int
}

func isCandidate(t *token) bool {
	if t.kind != tkInstr || t.raw {
		return false
	}
	ins := t.ins
	f := ins.f
	if f.postinc || f.predec || f.isBranch || f.nop || f.xgkick || f.waitQ || f.waitP {
		return false
	}
	if ins.flags != "" {
		return false
	}
	if readsFlags(f) {
		return false
	}
	return true
}

func readsFlags(f *form) bool {
	return f.reads["mac"] || f.reads["clip"] || f.name == "fsand" || f.name == "fseq" || f.name == "fsor"
}

func (e *emitter) line(s string) { e.items = append(e.items, item{text: s}) }

func (e *emitter) word(wd *word) {
	e.items = append(e.items, item{w: wd})
	e.clock++
}

func nopWord() *word { return &word{} }

func (e *emitter) pads(k int) {
	for i := 0; i < k; i++ {
		e.word(nopWord())
	}
}

// hazards returns the cycles node n must wait at the current clock, split by cause.
func (e *emitter) hazards(n *node) (reg, q, p int) {
	f := n.ins.f
	e.flagH = 0
	for k := range n.reads {
		r, ok := e.ready[k]
		if !ok {
			continue
		}
		if pf := e.prod[k]; pf != nil {
			r -= bypassCredit(pf, f)
		}
		if e.capFill {
			if c := e.issued[k] + 4; c < r {
				r = c
			}
		}
		d := r - e.clock
		if e.opts.Interlock && (k == resMAC || k == resCLIP) {
			d = r - (e.clock - e.stallAdj)
		}
		switch k {
		case resQ:
			if d > q {
				q = d
			}
		case resP:
			if d > p {
				p = d
			}
		case resMAC, resCLIP:
			if e.opts.Interlock {
				if d > e.flagH {
					e.flagH = d
				}
			} else if d > reg {
				reg = d
			}
		default:
			if d > reg {
				reg = d
			}
		}
	}
	return
}

func bypassCredit(prod, cons *form) int {
	switch {
	case cons.name == "mtir" && prod.bypassMtir:
		return prod.latency
	case strings.HasPrefix(cons.name, "ftoi") && prod.bypassFtoi:
		return prod.latency
	case cons.name == "minii" && prod.bypassMini:
		return 2
	}
	return 0
}

func (e *emitter) hazard(n *node) int {
	r, q, p := e.hazards(n)
	return maxInt(maxInt(r, e.flagH), maxInt(q, p))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (e *emitter) issue(n *node, at int) {
	f := n.ins.f
	if f.latency > 1 {
		for k := range n.writes {
			if k == resMAC || k == resCLIP || k == resACC || k == resI || k == resR {
				continue
			}
			e.ready[k] = at + f.latency + 1
			if e.opts.Interlock && (k == resQ || k == resP) {
				e.ready[k] = at + f.latency
			}
			e.prod[k] = f
			e.issued[k] = at
		}
	}
	if n.writes[resMAC] {
		e.ready[resMAC] = at + 4
		if e.opts.Interlock {
			e.ready[resMAC] = at - e.stallAdj + 4
		}
	}
	if n.writes[resCLIP] {
		e.ready[resCLIP] = at + 4
		if e.opts.Interlock {
			e.ready[resCLIP] = at - e.stallAdj + 4
		}
	}
}

func (w *word) set(n *node, asMax bool) {
	txt := n.ins.text(asMax)
	if n.upper || n.ins.f.nop {
		w.upper = n
		w.text[0] = txt
	} else {
		w.lower = n
		w.text[1] = txt
	}
}

func (w *word) render() string {
	up, lo := w.text[0], w.text[1]
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

// prepare makes room for n: it emits padding or wait words and reports a wait to place in n's own word.
func (e *emitter) prepare(n *node) (own string, at int) {
	for {
		r, q, p := e.hazards(n)
		if e.flagH > 0 {
			e.pads(e.flagH)
			continue
		}
		d := maxInt(r, maxInt(q, p))
		if d <= 0 {
			return "", e.clock
		}
		if r >= q && r >= p {
			if e.opts.Interlock {
				e.clock += r
				e.stallAdj += r
			} else {
				e.pads(r)
			}
			continue
		}
		if q > 1 && q >= p {
			if n.upper {
				return "waitq", e.clock + q
			}
			e.waitWord("waitq", q)
			continue
		}
		if p > 1 {
			if n.upper {
				return "waitp", e.clock + p
			}
			e.waitWord("waitp", p)
			continue
		}
		e.pads(maxInt(q, p))
	}
}

func (e *emitter) waitWord(kind string, gap int) {
	w := &word{}
	w.text[1] = kind
	e.items = append(e.items, item{w: w})
	e.clock += gap + 1
}

// scheduleSegment orders a run of candidate tokens and appends words.
func (e *emitter) scheduleSegment(seg []*token, after int) {
	macLive := e.flagLive(after, resMAC)
	clipLive := e.flagLive(after, resCLIP)
	nodes := make([]*node, len(seg))
	for i, t := range seg {
		nodes[i] = newNode(t, e.moveAsMax(t))
	}
	n := len(nodes)
	deps := make([][]int, n)
	predOf := make([][]int, n)
	lastMAC, lastCLIP := -1, -1
	idxOf := map[*node]int{}
	for i, nd := range nodes {
		idxOf[nd] = i
		if nd.writes[resMAC] {
			lastMAC = i
		}
		if nd.writes[resCLIP] {
			lastCLIP = i
		}
	}
	flagFor := func(later int) func(int) bool {
		return func(res int) bool {
			if res == resMAC {
				return macLive && later == lastMAC
			}
			return clipLive && later == lastCLIP
		}
	}
	for j := 0; j < n; j++ {
		for i := 0; i < j; i++ {
			if dependent(nodes[i], nodes[j], flagFor(j)) {
				deps[i] = append(deps[i], j)
				predOf[j] = append(predOf[j], i)
			}
		}
	}
	prio := computePriorities(nodes, deps)
	done := make([]bool, n)
	issued := 0
	for issued < n {
		var ready []int
		for i := 0; i < n; i++ {
			if done[i] {
				continue
			}
			ok := true
			for _, p := range predOf[i] {
				if !done[p] {
					ok = false
					break
				}
			}
			if ok {
				ready = append(ready, i)
			}
		}
		score := func(i int) int {
			nd := nodes[i]
			h := e.hazard(nd)
			switch e.opts.SchedMode {
			case 1:
				return i + 1000*h - 60*prio[i]
			case 2:
				return i + 1000*h - 200*prio[i]
			case 3:
				return i + 1000*h - 20*prio[i] - 40*nd.lat()
			}
			s := i + 1000*h
			if nd.writesQ || nd.writesP {
				s -= 500
			}
			if nd.isLoad {
				s -= 300
			}
			if e.hasLast && e.lastUp != nd.selUp {
				s -= 100
			}
			if h == 0 {
				s -= 20 * prio[i]
			}
			return s
		}
		best := ready[0]
		for _, i := range ready[1:] {
			if score(i) < score(best) {
				best = i
			}
		}
		prim := nodes[best]
		delay := e.hazard(prim)
		if os.Getenv("VCL_TRACE") == "deps" && best == 5 {
			for i := 0; i < n; i++ {
				if done[i] {
					continue
				}
				var pend []string
				for _, p := range predOf[i] {
					if !done[p] {
						pend = append(pend, fmt.Sprintf("%d:%s", p, nodes[p].ins.text(false)))
					}
				}
				fmt.Fprintf(os.Stderr, "  node %d %s waits %v\n", i, nodes[i].ins.text(false), pend)
			}
		}
		if os.Getenv("VCL_TRACE") != "" {
			fmt.Fprintf(os.Stderr, "clock %d pick %d %s\n", e.clock, best, prim.ins.text(false))
			for _, i := range ready {
				fmt.Fprintf(os.Stderr, "    ready %d %s score %d haz %d prio %d\n", i, nodes[i].ins.text(false), score(i), e.hazard(nodes[i]), prio[i])
			}
		}
		partner := -1
		for _, i := range ready {
			if i == best {
				continue
			}
			c := nodes[i]
			if c.upper == prim.upper {
				continue
			}
			if dependent(prim, c, flagFor(maxInt(idxOf[prim], idxOf[c]))) || dependent(c, prim, flagFor(maxInt(idxOf[prim], idxOf[c]))) {
				continue
			}
			if e.hazard(c) > delay {
				continue
			}
			if writeClash(prim, c) {
				continue
			}
			if prim.writes[resMAC] && c.reads[resMAC] || c.writes[resMAC] && prim.reads[resMAC] {
				continue
			}
			if partner < 0 || score(i) < score(partner) {
				partner = i
			}
		}
		own, at := e.prepare(prim)
		e.flushLabel()
		w := &word{}
		w.set(prim, e.isMove(prim))
		if own != "" {
			w.text[1] = own
			partner = -1
		}
		e.issue(prim, at)
		done[best] = true
		issued++
		if partner >= 0 {
			pn := nodes[partner]
			w.set(pn, e.isMove(pn))
			e.issue(pn, at)
			done[partner] = true
			issued++
		}
		e.lastUp = prim.selUp
		e.hasLast = true
		e.items = append(e.items, item{w: w})
		e.clock = at + 1
	}
}

func (e *emitter) isMove(n *node) bool {
	return n.ins.f.name == "move" && n.upper
}

func (e *emitter) moveAsMax(t *token) bool {
	f := t.ins.f
	if f.name != "move" {
		return false
	}
	if t.ins.mask&laneW != 0 || t.ins.mask == 0 {
		return false
	}
	if e.macRead && e.tokIdx[t] < e.macLast {
		return false
	}
	return true
}

func generate(p *parser, toks []*token, opt Options) string {
	e := buildEmitter(p, toks, opt)
	return e.render()
}

func buildEmitter(p *parser, toks []*token, opt Options) *emitter {
	labels := map[string]int{}
	for i, t := range toks {
		if t.label != "" {
			labels[t.label] = i
		}
	}
	toks = mergeAdds(toks)
	labels = map[string]int{}
	for i, t := range toks {
		if t.label != "" {
			labels[t.label] = i
		}
	}
	swpApplied := false
	if !opt.NoPipeline {
		toks, swpApplied = applySWP(toks, opt)
		labels = map[string]int{}
		for i, t := range toks {
			if t.kind == tkLabel || t.label != "" {
				labels[t.label] = i
			}
		}
	}
	if !swpApplied {
		toks = applyFills(toks, labels)
	} else {
		toks = advanceStoreBases(p, toks, opt)
	}
	return emitTokens(p, toks, opt)
}

func emitTokens(p *parser, toks []*token, opt Options) *emitter {
	var waits map[string]map[int]int
	var e *emitter
	for iter := 0; iter < 4; iter++ {
		e = emitTokensWith(p, toks, opt, waits)
		if sameWaits(waits, e.backWaits) {
			break
		}
		waits = e.backWaits
	}
	return e
}

func sameWaits(a, b map[string]map[int]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, m := range a {
		n := b[k]
		if len(m) != len(n) {
			return false
		}
		for r, v := range m {
			if n[r] != v {
				return false
			}
		}
	}
	return true
}

func emitTokensWith(p *parser, toks []*token, opt Options, waits map[string]map[int]int) *emitter {
	e := &emitter{entryWaits: waits, backWaits: map[string]map[int]int{}, fwdWaits: map[string]map[int]int{}, seen: map[string]bool{}, p: p, toks: toks, opts: opt, ready: map[int]int{}, prod: map[int]*form{}, issued: map[int]int{}}
	e.macLast = -1
	e.tokIdx = map[*token]int{}
	for i, t := range toks {
		e.tokIdx[t] = i
		if t.kind == tkInstr && readsFlags(t.ins.f) {
			e.macRead = true
			e.macLast = i
		}
	}
	n := len(toks)
	for i := 0; i < n; i++ {
		t := toks[i]
		switch t.kind {
		case tkLabel:
			e.line(t.label + ":")
			e.termBr = false
			e.enterLabel(t.label)
		case tkDir:
			e.directive(t)
		case tkInstr:
			hiding := e.termBr && t.label == ""
			start := len(e.items)
			if hiding {
				defer func() {}()
			}
			if t.label != "" {
				e.pending = t.label
				e.enterLabel(t.label)
				e.termBr = false
			}
			if isCandidate(t) {
				j := i + 1
				for j < n && isCandidate(toks[j]) && toks[j].label == "" {
					j++
				}
				e.scheduleSegment(toks[i:j], j)
				e.codeSince = true
				e.termBr = false
				if hiding {
					e.hide(start)
					e.termBr = true
				}
				i = j - 1
			} else {
				e.single(t)
				if hiding {
					e.hide(start)
					e.termBr = true
				}
			}
		}
	}
	if e.codeSince && !e.termBr {
		e.endPair()
	}
	return e
}

func (e *emitter) single(t *token) {
	ins := t.ins
	f := ins.f
	nd := newNode(t, false)
	e.codeSince = true
	e.termBr = false
	if f.isBranch && !f.link && !f.regBranch && (t.fill == nil || t.noPad) {
		if idx := e.tailPair(nd); idx >= 0 {
			if !f.uncond && !(idx > 0 && e.items[idx-1].w != nil && e.items[idx-1].w.isNop()) {
				e.items = append(e.items[:idx], append([]item{{w: nopWord()}}, e.items[idx:]...)...)
			}
			e.delaySlot(t)
			e.recordBack(t)
			if f.uncond {
				e.termBr = true
			}
			return
		}
	} else if !f.isBranch && (f.xgkick || readsFlags(f)) {
		if e.tailPair(nd) >= 0 {
			return
		}
	}
	if f.isBranch {
		own, at := e.prepare(nd)
		_ = own
		if t.fill != nil && !t.noPad {
			fn := newNode(t.fill, false)
			saved := e.clock
			e.clock = at + 1
			e.capFill = true
			fh := e.hazard(fn)
			e.capFill = false
			e.clock = saved
			if (!f.uncond || f.link || f.regBranch) && !e.prevIsNopPair() {
				fh--
			}
			if fh > 0 {
				e.pads(fh)
				at += fh
			}
		}
		if !f.uncond || f.link || f.regBranch {
			if !e.prevIsNopPair() {
				e.items = append(e.items, item{w: nopWord()})
			}
		}
		e.flushLabel()
		wd := &word{}
		wd.set(nd, false)
		e.issue(nd, at)
		e.items = append(e.items, item{w: wd})
		e.clock = at + 1
		e.delaySlot(t)
		e.recordBack(t)
		if f.uncond && !f.link {
			e.termBr = true
		}
		return
	}
	own, at := e.prepare(nd)
	_ = own
	e.flushLabel()
	wd := &word{}
	wd.set(nd, false)
	e.issue(nd, at)
	e.hasLast, e.lastUp = true, nd.upper
	e.items = append(e.items, item{w: wd})
	e.clock = at + 1
}

func (e *emitter) prevIsNopPair() bool {
	for k := len(e.items) - 1; k >= 0; k-- {
		it := e.items[k]
		if it.w != nil {
			return it.w.isNop()
		}
		return false
	}
	return false
}

func (e *emitter) endPair() {
	e.items = append(e.items, item{w: &word{text: [2]string{"nop[E]", ""}}})
	e.items = append(e.items, item{w: nopWord()})
	e.codeSince = false
}

func (e *emitter) directive(t *token) {
	switch t.dir {
	case "--cont":
		e.endPair()
		e.clock++
	case "--exit":
		if e.codeSince && !e.termBr {
			e.endPair()
		}
		e.codeSince = false
	case ".global":
		e.line(strings.Repeat(" ", 20) + ".global " + strings.Join(t.dargs, ", "))
	case ".rawloop", ".endrawloop":
		e.line(strings.Repeat(" ", 20) + t.dir + " ")
	}
}

func (e *emitter) render() string {
	var b strings.Builder
	p := e.p
	if !p.hasVU || p.hasName {
		b.WriteString("\t\t.vu\n\t\t.align 4\n")
		if p.hasName {
			b.WriteString("\t\t.global\t" + p.progName + "_CodeStart\n")
			b.WriteString("\t\t.global\t" + p.progName + "_CodeEnd\n")
			b.WriteString(p.progName + "_CodeStart:\n")
		}
	}
	for _, it := range e.items {
		if it.w != nil {
			b.WriteString(it.w.render())
		} else {
			b.WriteString(it.text)
		}
		b.WriteByte('\n')
	}
	if p.hasName {
		b.WriteString("\t\t.align 4\n" + p.progName + "_CodeEnd:\n")
	}
	return b.String()
}

// flagLive reports whether a flag written before token index 'from' may still be read.
func (e *emitter) flagLive(from int, res int) bool {
	for k := from; k < len(e.toks); k++ {
		t := e.toks[k]
		if t.kind == tkDir {
			if t.dir == "--exit" || t.dir == "--cont" {
				return false
			}
			continue
		}
		if t.kind != tkInstr {
			continue
		}
		f := t.ins.f
		if res == resMAC && (f.reads["mac"] || f.name == "fsand" || f.name == "fseq" || f.name == "fsor") {
			return true
		}
		if res == resCLIP && f.reads["clip"] {
			return true
		}
		if res == resMAC && f.writes["mac"] || res == resCLIP && f.writes["clip"] {
			return false
		}
	}
	return false
}

func (w *word) isNop() bool {
	return (w.text[0] == "" || w.text[0] == "nop") && (w.text[1] == "" || w.text[1] == "nop")
}

// mergeAdds folds adjacent integer additions into one.
func mergeAdds(toks []*token) []*token {
	var out []*token
	for _, t := range toks {
		if len(out) > 0 && t.kind == tkInstr && t.label == "" && t.ins.flags == "" && !t.raw {
			p := out[len(out)-1]
			if p.kind == tkInstr && p.ins.flags == "" && !p.raw && p.ins.f.name == "iaddiu" && t.ins.f.name == "iaddiu" {
				pa, ta := p.ins.args, t.ins.args
				if pa[0].phys == ta[0].phys && ta[0].phys == ta[1].phys && ta[0].phys != 0 {
					a, ok1 := decimalImm(pa[2].imm)
					b, ok2 := decimalImm(ta[2].imm)
					if ok1 && ok2 && a+b != 0 && a+b >= -32768 && a+b <= 32767 {
						np := *p
						ni := *p.ins
						ni.args = append([]*operand(nil), p.ins.args...)
						cp := *ni.args[2]
						cp.imm = itoa(a + b)
						ni.args[2] = &cp
						np.ins = &ni
						out[len(out)-1] = &np
						continue
					}
				}
			}
		}
		out = append(out, t)
	}
	return out
}

func decimalImm(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	i := 0
	if s[0] == '-' || s[0] == '+' {
		i = 1
	}
	if i >= len(s) {
		return 0, false
	}
	for j := i; j < len(s); j++ {
		if s[j] < '0' || s[j] > '9' {
			return 0, false
		}
	}
	v := 0
	for j := i; j < len(s); j++ {
		v = v*10 + int(s[j]-'0')
		if v > 1<<40 {
			return 0, false
		}
	}
	if s[0] == '-' {
		v = -v
	}
	return v, true
}

func (e *emitter) flushLabel() {
	if e.pending != "" {
		e.line(e.pending + ":")
		e.pending = ""
	}
}

// tailPair merges nd into the lower slot of the previous word when that is safe.
func (e *emitter) tailPair(nd *node) int {
	k := len(e.items) - 1
	if k < 0 || e.items[k].w == nil || e.pending != "" {
		return -1
	}
	w := e.items[k].w
	if w.upper == nil || w.lower != nil || w.text[1] != "" || w.upper.ins.flags != "" || w.upper.ins.f.nop {
		return -1
	}
	if dependent(w.upper, nd, func(int) bool { return true }) || dependent(nd, w.upper, func(int) bool { return true }) {
		return -1
	}
	if writeClash(w.upper, nd) {
		return -1
	}
	saved := e.clock
	e.clock--
	h := e.hazard(nd)
	e.clock = saved
	if h > 0 {
		return -1
	}
	w.set(nd, false)
	e.issue(nd, saved-1)
	return k
}

// hide drops instruction words emitted after a terminal branch but keeps their padding.
func (e *emitter) hide(start int) {
	out := e.items[:start]
	for _, it := range e.items[start:] {
		if it.w != nil && it.w.text[0] == "" && it.w.text[1] == "" {
			out = append(out, it)
		}
	}
	e.items = out
}

func (e *emitter) delaySlot(t *token) {
	if t.fill != nil {
		fn := newNode(t.fill, false)
		fw := &word{}
		fw.set(fn, false)
		e.issue(fn, e.clock)
		e.items = append(e.items, item{w: fw})
		e.clock++
		return
	}
	e.items = append(e.items, item{w: nopWord()})
}

// cost estimates run time: loop bodies count a hundred times.
func (e *emitter) cost() int {
	loops := map[string]bool{}
	seen := map[string]bool{}
	for _, t := range e.toks {
		if t.kind == tkLabel {
			seen[t.label] = true
		}
		if t.kind == tkInstr && t.ins.f.isBranch && !t.ins.f.regBranch {
			target := t.ins.args[len(t.ins.args)-1].imm
			if seen[target] {
				loops[target] = true
			}
		}
	}
	start := map[string]int{}
	for k, it := range e.items {
		if it.w == nil {
			if n := len(it.text); n > 0 && it.text[n-1] == ':' && it.text[0] != ' ' {
				start[it.text[:n-1]] = k
			}
		}
	}
	type span struct{ lo, hi int }
	var spans []span
	for k, it := range e.items {
		if it.w == nil || it.w.text[1] == "" {
			continue
		}
		f := strings.Fields(strings.ReplaceAll(it.w.text[1], ",", " "))
		if len(f) < 2 {
			continue
		}
		m := strings.ToLower(f[0])
		if !strings.HasPrefix(m, "ib") && m != "b" {
			continue
		}
		target := f[len(f)-1]
		if loops[target] {
			if lo, ok := start[target]; ok && lo < k {
				spans = append(spans, span{lo, k + 1})
			}
		}
	}
	total := 0
	for k, it := range e.items {
		if it.w == nil {
			continue
		}
		depth := 0
		for _, sp := range spans {
			if k >= sp.lo && k <= sp.hi {
				depth++
			}
		}
		w := 1
		for d := 0; d < depth && d < 3; d++ {
			w *= 100
		}
		total += w
	}
	return total
}

func (e *emitter) enterLabel(name string) {
	e.seen[name] = true
	for k, w := range e.fwdWaits[name] {
		if r := e.clock + w; r > e.ready[k] {
			e.ready[k] = r
		}
	}
	for k, w := range e.entryWaits[name] {
		if r := e.clock + w; r > e.ready[k] {
			e.ready[k] = r
			if e.prod[k] == nil {
				e.prod[k] = nil
			}
		}
	}
}

// recordBack remembers what is still pending when a backward branch re-enters its loop.
func (e *emitter) recordBack(t *token) {
	if t.kind != tkInstr || !t.ins.f.isBranch || t.ins.f.regBranch {
		return
	}
	target := t.ins.args[len(t.ins.args)-1].imm
	store := e.backWaits
	if !e.seen[target] {
		store = e.fwdWaits
	}
	m := store[target]
	if m == nil {
		m = map[int]int{}
		store[target] = m
	}
	for k, r := range e.ready {
		if w := r - e.clock; w > 0 && w > m[k] {
			m[k] = w
		}
	}
}
