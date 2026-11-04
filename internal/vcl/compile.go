package vcl

import (
	"fmt"
	"os"
	"strings"
)

// Result holds the observable outcome of a compilation.
type Result struct {
	Stdout  string
	Stderr  string
	Exit    int
	Remarks []string
	Slots   []BlockSlots
}

func newParser(opt Options) *parser {
	p := &parser{opt: opt}
	p.inPins = map[string]int{}
	p.outPins = map[string]int{}
	p.hw = map[string]bool{}
	return p
}

// Compile translates VCL source text to scheduled VSM text.
func Compile(src string, opt Options) Result {
	res, cost := searchAlloc(src, opt)
	if opt.NoSearch || opt.Spread || res.Exit != 0 || opt.ShowAlloc || opt.SchedMode != 0 {
		return res
	}
	for mode := 1; mode <= 3; mode++ {
		alt := opt
		alt.SchedMode = mode
		r2, c2 := searchAlloc(src, alt)
		if r2.Exit == 0 && c2 < cost {
			res, cost = r2, c2
		}
	}
	return res
}

func searchAlloc(src string, opt Options) (Result, int) {
	res, cost := compileOnce(src, opt)
	if opt.NoSearch || opt.Spread || res.Exit != 0 || opt.ShowAlloc {
		return res, cost
	}
	best := res
	bestOpt := opt
	for _, w := range []int{3, 5, 8, 12, 18, 31} {
		alt := opt
		alt.Spread = true
		alt.SpreadWidth = w
		r2, c2 := compileOnce(src, alt)
		if r2.Exit == 0 && c2 < cost {
			best, cost, bestOpt = r2, c2, alt
		}
	}
	opt = bestOpt
	n := countLoops(src, opt)
	var chosen []int
	for k := 0; k < n; k++ {
		alt := opt
		alt.Respread = 32
		alt.RespreadLoops = append(append([]int{}, chosen...), k)
		r2, c2 := compileOnce(src, alt)
		if os.Getenv("VCL_DEBUG") != "" {
			fmt.Fprintln(os.Stderr, "search loop", k, r2.Exit, c2, cost)
		}
		if r2.Exit == 0 && c2 < cost {
			best, cost = r2, c2
			chosen = alt.RespreadLoops
		}
	}
	return best, cost
}

func compileOnce(src string, opt Options) (Result, int) {
	p := newParser(opt)
	p.parseSource(src)
	var res Result
	flush := func() {
		var b strings.Builder
		for _, d := range p.diags {
			b.WriteString(d.String())
			b.WriteByte('\n')
		}
		res.Stderr = b.String() + res.Stderr
	}
	if p.fatal {
		flush()
		res.Exit = 1
		return res, 0
	}
	al := &allocator{toks: p.toks, labels: map[string]int{}, byKey: map[string]*aliasState{}, opts: opt,
		inPins: p.inPins, outPins: p.outPins, hw: p.hw}
	for i, t := range al.toks {
		if t.label != "" {
			al.labels[t.label] = i
		}
	}
	al.pool[0] = poolRegs(p.initVFAll, p.initVF, 31)
	al.pool[1] = poolRegs(p.initVIAll, p.initVI, 15)
	al.run()
	diags := append([]diag{}, p.diags...)
	diags = append(diags, al.res.diags...)
	var errb strings.Builder
	for _, d := range diags {
		errb.WriteString(d.String())
		errb.WriteByte('\n')
	}
	if opt.ShowAlloc {
		errb.WriteString(al.report())
	}
	res.Stderr = errb.String()
	if len(al.res.diags) > 0 {
		res.Exit = 1
		return res, 0
	}
	var remarks []string
	opt.Remark = func(s string) { remarks = append(remarks, s) }
	e := buildEmitter(p, al.toks, opt)
	res.Stdout = e.render()
	res.Remarks = remarks
	if opt.WantSlots {
		res.Slots = e.slots()
	}
	if len(diags) > 0 {
		res.Exit = 1
	}
	return res, e.cost()
}

func poolRegs(all bool, set map[int]bool, max int) []int {
	var out []int
	for r := 1; r <= max; r++ {
		if all || set[r] {
			out = append(out, r)
		}
	}
	return out
}

func countLoops(src string, opt Options) int {
	p := newParser(opt)
	p.parseSource(src)
	if p.fatal {
		return 0
	}
	labels := map[string]int{}
	for i, t := range p.toks {
		if t.label != "" {
			labels[t.label] = i
		}
	}
	n := 0
	for i, t := range p.toks {
		if t.kind == tkInstr && t.ins.f.isBranch && !t.ins.f.regBranch {
			if j, ok := labels[t.ins.args[len(t.ins.args)-1].imm]; ok && j <= i {
				n++
			}
		}
	}
	return n
}
