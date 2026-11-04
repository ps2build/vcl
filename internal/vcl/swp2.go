package vcl

import (
	"fmt"
	"os"
	"strconv"
)

func rawWordCount(toks []*token, opt Options) int {
	e := emitTokens(nil, toks, opt)
	n := 0
	for _, it := range e.items {
		if it.w != nil {
			n++
		}
	}
	return n
}

// wordCount measures one steady-state iteration, including hazards across the back edge.
func wordCount(toks []*token, opt Options) int {
	dup := make([]*token, 0, 2*len(toks))
	for _, t := range toks {
		dup = append(dup, cloneTok(t))
	}
	for _, t := range toks {
		c := cloneTok(t)
		if t.fill != nil {
			c.fill = cloneTok(t.fill)
			c.noPad = false
		}
		dup = append(dup, c)
	}
	return rawWordCount(dup, opt) - rawWordCount(toks, opt)
}

func hasQProducer(toks []*token, from, to int) bool {
	for i := from; i <= to; i++ {
		if toks[i].kind == tkInstr && toks[i].ins.f.writesQ {
			return true
		}
	}
	return false
}

func laneReadsOf(ts []*token) map[int]uint8 {
	m := map[int]uint8{}
	for _, t := range ts {
		r, _ := floatAccessLanes(t)
		for k, v := range r {
			m[k] |= v
		}
	}
	return m
}

type cyclicCand struct {
	cost    int
	split   int
	point   int
	slot    bool
	rots    []rotInfo
	prefix  []*token
	prefixA []*token
	pointA  int
	hasA    bool
	multi   []*token
	mpts    []int
}

type rotInfo struct {
	reg, scratch int
	mask         uint8
}

// planCyclic implements the prefix rotation plan for loops without a Q producer.
type prefixCfg struct {
	splits     func(body []*token) []int
	allowPlain bool
	initial    int // initial best cost; 0 means the unpipelined baseline
	qMode      bool
}

func planCyclic(toks []*token, lp loop, opt Options) ([]*token, bool) {
	return planPrefix(toks, lp, opt, prefixCfg{
		splits: func(body []*token) []int {
			var r []int
			for s := 1; s <= len(body)-2; s++ {
				r = append(r, s)
			}
			return r
		},
	})
}

func planPrefix(toks []*token, lp loop, opt Options, cfg prefixCfg) ([]*token, bool) {
	L, B := lp.lbl, lp.br
	br := toks[B]
	macLive, clipLive := false, false
	for _, tk := range toks {
		if tk.kind == tkInstr && !tk.raw {
			n := nodeOf(tk)
			if n.reads[resMAC] {
				macLive = true
			}
			if n.reads[resCLIP] {
				clipLive = true
			}
		}
	}
	savedLive := planFlagLive
	planFlagLive = func(res int) bool {
		if res == resMAC {
			return macLive
		}
		return clipLive
	}
	defer func() { planFlagLive = savedLive }()
	if br.ins.f.delay != 1 || br.ins.f.link || br.ins.f.regBranch {
		return nil, false
	}
	if toks[L].kind != tkLabel {
		return nil, false
	}
	var body []*token
	for i := L + 1; i <= B; i++ {
		t := toks[i]
		if t.kind == tkLabel || t.label != "" {
			return nil, false
		}
		if t.kind == tkInstr {
			f := t.ins.f
			if t.raw || t.ins.flags != "" || f.postinc || f.predec || f.xgkick || (f.writesQ && !cfg.qMode) {
				return nil, false
			}
			body = append(body, t)
		}
	}
	upd := updatesOf(toks, L, B)
	if len(upd) == 0 {
		return nil, false
	}
	// baseline cost
	base := make([]*token, 0, len(body))
	for _, t := range body {
		base = append(base, cloneTok(t))
	}
	best := wordCount(base, opt)
	if cfg.initial != 0 {
		best = cfg.initial
	}
	var winner *cyclicCand
	bodyIdx := map[*token]int{}
	for i, t := range body {
		bodyIdx[t] = i
	}
	// positions of induction updates in body order
	type upos struct{ pos, reg, step int }
	var updates []upos
	for i, t := range body {
		if reg, step, ok := bodyUpdate(t); ok {
			updates = append(updates, upos{i, reg, step})
		}
	}
	for _, s := range cfg.splits(body) {
		prefix := body[:s]
		mainPart := body[s:]
		bad := false
		for _, t := range prefix {
			f := t.ins.f
			if f.mem == "store" || f.isBranch || f.xgkick {
				bad = true
			}
		}
		if bad {
			dbgSwp(s, 151)
			continue
		}
		// rotation set
		writes := map[int]uint8{}
		var order []int
		for _, t := range prefix {
			_, w := floatAccessLanes(t)
			for r, m := range w {
				if _, ok := writes[r]; !ok {
					order = append(order, r)
				}
				writes[r] |= m
			}
		}
		if len(order) == 0 && !cfg.allowPlain {
			dbgSwp(s, 166)
			continue
		}
		mainReads := laneReadsOf(mainPart)
		var rots []rotInfo
		used := floatRegsUsed(toks, L, B)
		for _, r := range order {
			m := writes[r]
			_ = mainReads
			// a rotated register must not be read before written inside the prefix
			seenW := uint8(0)
			violated := false
			for _, t := range prefix {
				rd, w := floatAccessLanes(t)
				if rd[r]&^seenW != 0 {
					violated = true
				}
				seenW |= w[r]
			}
			if violated {
				rots = nil
				bad = true
				break
			}
			rots = append(rots, rotInfo{reg: r, mask: m})
		}
		if bad {
			dbgSwp(s, 192)
			continue
		}
		if len(rots) == 0 && !cfg.allowPlain {
			dbgSwp(s, 195)
			continue
		}
		for _, r := range rots {
			used[r.reg] = true
		}
		okScratch := true
		for i := range rots {
			sc, ok := lowerScratch(used)
			if !ok {
				okScratch = false
				break
			}
			rots[i].scratch = sc
		}
		if !okScratch {
			dbgSwp(s, 210)
			continue
		}
		rotMap := map[int]int{}
		for _, r := range rots {
			rotMap[r.reg] = r.scratch
		}
		brNode := nodeOf(br)
		adjust := func(part []*token, pt int) []*token {
			var out []*token
			for _, t := range part {
				c := cloneTok(t)
				for from, to := range rotMap {
					substReg(c, from, to)
				}
				for _, op := range c.ins.args {
					if op.kind == kMem && op.base != nil {
						for _, u := range updates {
							if u.reg == op.base.phys && u.pos >= s+pt {
								if v, err := strconv.Atoi(op.offset); err == nil {
									op.offset = strconv.Itoa(v + u.step)
								}
								break
							}
						}
					}
				}
				out = append(out, c)
			}
			return out
		}
		// prefix must not overwrite what the branch reads or writes
		clash := false
		for _, t := range prefix {
			if dependentAny(nodeOf(t), brNode) {
				clash = true
			}
		}
		if clash {
			dbgSwp(s, 225)
			continue
		}
		// examine insertion points
		startPt := 0
		if cfg.qMode {
			touchesQ := false
			for _, t := range prefix {
				n := nodeOf(t)
				if n.reads[resQ] || n.writes[resQ] || n.reads[resP] || n.writes[resP] {
					touchesQ = true
				}
			}
			if touchesQ {
				for i, t := range mainPart {
					if readsQ(t) && i+1 > startPt {
						startPt = i + 1
					}
				}
				if startPt > len(mainPart)-1 {
					startPt = len(mainPart) - 1
				}
			}
		}
		for pt := startPt; pt <= len(mainPart)-1; pt++ {
			// adjusted prefix copy
			var copyToks []*token
			for _, t := range prefix {
				c := cloneTok(t)
				for from, to := range rotMap {
					substReg(c, from, to)
				}
				for _, op := range c.ins.args {
					if op.kind == kMem && op.base != nil {
						for _, u := range updates {
							if u.reg == op.base.phys && u.pos >= s+pt {
								if v, err := strconv.Atoi(op.offset); err == nil {
									op.offset = strconv.Itoa(v + u.step)
								}
								break
							}
						}
					}
				}
				copyToks = append(copyToks, c)
			}
			if pt < len(mainPart)-1 {
				if !legalPoint(copyToks, mainPart, pt, mainPart[:pt], updates, s) {
					continue
				}
			}
			slot := canSlot(copyToks, mainPart, pt, br)
			// token list for the cost
			var list []*token
			for _, r := range rots {
				list = append(list, mkMove(r.reg, r.scratch, r.mask))
			}
			for i, t := range mainPart {
				if i == pt {
					n := len(copyToks)
					if slot {
						n--
					}
					for _, c := range copyToks[:n] {
						list = append(list, cloneTok(c))
					}
				}
				ct := cloneTok(t)
				if i == len(mainPart)-1 && slot {
					fc := cloneTok(copyToks[len(copyToks)-1])
					ct.fill = fc
					ct.noPad = false
				}
				list = append(list, ct)
			}
			cost := wordCount(list, opt)
			if os.Getenv("VCL_SWPDBG") != "" {
				fmt.Fprintf(os.Stderr, "swp s=%d pt=%d slot=%v cost=%d best=%d\n", s, pt, slot, cost, best)
			}
			if cost < best {
				best = cost
				winner = &cyclicCand{cost: cost, split: s, point: pt, slot: slot, rots: rots, prefix: copyToks}
				winner.prefix = copyToks
			}
		}
		if cfg.qMode && startPt > 0 {
			qi := -1
			for i, t := range prefix {
				n := nodeOf(t)
				if n.reads[resQ] || n.writes[resQ] || n.reads[resP] || n.writes[resP] {
					qi = i
					break
				}
			}
			if qi > 0 {
				for pt2 := startPt; pt2 <= len(mainPart)-1; pt2++ {
					bToks := adjust(prefix[qi:], pt2)
					if pt2 < len(mainPart)-1 && !legalPoint(bToks, mainPart, pt2, mainPart[:pt2], updates, s) {
						continue
					}
					slot := canSlot(bToks, mainPart, pt2, br)
					for pt1 := 0; pt1 <= pt2; pt1++ {
						aToks := adjust(prefix[:qi], pt1)
						if pt1 < len(mainPart)-1 && !legalPoint(aToks, mainPart, pt1, mainPart[:pt1], updates, s) {
							continue
						}
						var list []*token
						for _, r := range rots {
							list = append(list, mkMove(r.reg, r.scratch, r.mask))
						}
						for i, t := range mainPart {
							if i == pt1 {
								for _, c := range aToks {
									list = append(list, cloneTok(c))
								}
							}
							if i == pt2 {
								n := len(bToks)
								if slot {
									n--
								}
								for _, c := range bToks[:n] {
									list = append(list, cloneTok(c))
								}
							}
							ct := cloneTok(t)
							if i == len(mainPart)-1 && slot {
								ct.fill = cloneTok(bToks[len(bToks)-1])
								ct.noPad = false
							}
							list = append(list, ct)
						}
						cost := wordCount(list, opt)
						if os.Getenv("VCL_SWPDBG") != "" {
							fmt.Fprintf(os.Stderr, "swpA s=%d pt1=%d pt2=%d cost=%d best=%d\n", s, pt1, pt2, cost, best)
						}
						if cost < best {
							best = cost
							winner = &cyclicCand{cost: cost, split: s, point: pt2, slot: slot, rots: rots, prefix: bToks, prefixA: aToks, pointA: pt1, hasA: true}
						}
					}
				}
			}
		}
		{
			pts := make([]int, len(prefix))
			prev := 0
			ok := true
			var placed []*token
			for k, t := range prefix {
				found := -1
				for pt := prev; pt <= len(mainPart)-1; pt++ {
					c := adjust([]*token{t}, pt)
					if pt < len(mainPart)-1 && !legalPoint(c, mainPart, pt, mainPart[:pt], updates, s) {
						continue
					}
					found = pt
					placed = append(placed, c[0])
					break
				}
				if found < 0 {
					ok = false
					break
				}
				pts[k] = found
				prev = found
			}
			if ok {
				var list []*token
				for _, r := range rots {
					list = append(list, mkMove(r.reg, r.scratch, r.mask))
				}
				for i, t := range mainPart {
					for k, c := range placed {
						if pts[k] == i {
							list = append(list, cloneTok(c))
						}
					}
					list = append(list, cloneTok(t))
				}
				cost := wordCount(list, opt)
				if os.Getenv("VCL_SWPDBG") != "" {
					fmt.Fprintf(os.Stderr, "swpG s=%d cost=%d best=%d pts=%v\n", s, cost, best, pts)
				}
				if cost < best {
					best = cost
					winner = &cyclicCand{cost: cost, split: s, rots: rots, multi: placed, mpts: pts}
				}
			}
		}
	}
	if winner == nil {
		return nil, false
	}
	s := winner.split
	mainPart := body[s:]
	var out []*token
	out = append(out, &token{kind: tkLabel, label: lp.label + "__PROLOG", line: toks[L].line})
	rotMap := map[int]int{}
	for _, r := range winner.rots {
		rotMap[r.reg] = r.scratch
	}
	for _, t := range body[:s] {
		c := cloneTok(t)
		for from, to := range rotMap {
			substReg(c, from, to)
		}
		out = append(out, c)
	}
	out = append(out, &token{kind: tkLabel, label: lp.label, line: toks[L].line})
	for _, r := range winner.rots {
		out = append(out, mkMove(r.reg, r.scratch, r.mask))
	}
	for i, t := range mainPart {
		for k, c := range winner.multi {
			if winner.mpts[k] == i {
				out = append(out, cloneTok(c))
			}
		}
		if winner.hasA && i == winner.pointA {
			for _, c := range winner.prefixA {
				out = append(out, cloneTok(c))
			}
		}
		if i == winner.point {
			n := len(winner.prefix)
			if winner.slot {
				n--
			}
			for _, c := range winner.prefix[:n] {
				out = append(out, cloneTok(c))
			}
		}
		ct := cloneTok(t)
		if i == len(mainPart)-1 && winner.slot {
			ct.fill = cloneTok(winner.prefix[len(winner.prefix)-1])
			ct.noPad = false
		}
		out = append(out, ct)
	}
	return out, true
}

func bodyUpdate(t *token) (reg, step int, ok bool) {
	f := t.ins.f
	if f.name != "iaddiu" && f.name != "isubiu" {
		return 0, 0, false
	}
	a := t.ins.args
	if a[0].phys != a[1].phys || a[0].phys == 0 {
		return 0, 0, false
	}
	v, ok2 := decimalImm(a[2].imm)
	if !ok2 {
		return 0, 0, false
	}
	if f.name == "isubiu" {
		v = -v
	}
	return a[0].phys, v, true
}

func legalPoint(prefix []*token, mainPart []*token, pt int, before []*token, updates interface{}, split int) bool {
	// (2) the prefix must not read registers written by main instructions before the point
	prefWrites := map[int]uint8{}
	for _, c := range prefix {
		rd, w := floatAccessLanes(c)
		for r, m := range rd {
			var wr uint8
			for _, b := range before {
				_, bw := floatAccessLanes(b)
				wr |= bw[r]
			}
			if m&wr&^prefWrites[r] != 0 {
				if os.Getenv("VCL_SWPDBG") == "3" {
					fmt.Fprintf(os.Stderr, "rule2 %s reg %d pt %d\n", c.ins.text(false), r, pt)
				}
				return false
			}
		}
		for _, id := range intReads(c) {
			if id == 0 {
				continue
			}
			if _, _, isUpd := firstUpdateOf(mainPart, id); isUpd {
				continue
			}
			for _, b := range before {
				if contains(intWrites(b), id) {
					return false
				}
			}
		}
		for r, m := range w {
			prefWrites[r] |= m
		}
	}
	// (1) the prefix moves in front of every main instruction from the point to the branch
	for i := pt; i < len(mainPart)-1; i++ {
		m := mainPart[i]
		for _, c := range prefix {
			if conflict(c, m) {
				if os.Getenv("VCL_SWPDBG") == "2" {
					fmt.Fprintf(os.Stderr, "conflict %s <> %s\n", c.ins.text(false), m.ins.text(false))
				}
				return false
			}
		}
	}
	return true
}

func firstUpdateOf(mainPart []*token, reg int) (int, int, bool) {
	for i, t := range mainPart {
		if r, s, ok := bodyUpdate(t); ok && r == reg {
			return i, s, true
		}
	}
	return 0, 0, false
}

func conflict(prefixInstr, m *token) bool {
	// an induction update is hopped over by a plain memory access based on it
	if reg, _, ok := bodyUpdate(m); ok {
		pf := prefixInstr.ins.f
		if (pf.mem == "load" || pf.mem == "store") && !pf.postinc && !pf.predec {
			for _, op := range prefixInstr.ins.args {
				if op.kind == kMem && op.base != nil && op.base.phys == reg && !contains(intWrites(prefixInstr), reg) {
					// the prefix may only touch the pointer as a base register
					pn := nodeOf(prefixInstr)
					mn := nodeOf(m)
					delete(pn.reads, viBase+reg)
					return dependentAny(pn, mn)
				}
			}
		}
	}
	return dependentAny(nodeOf(prefixInstr), nodeOf(m))
}

func canSlot(prefix []*token, mainPart []*token, pt int, br *token) bool {
	if len(prefix) == 0 || br.ins.f.delay != 1 || br.ins.f.link || br.ins.f.regBranch {
		return false
	}
	lt := prefix[len(prefix)-1]
	f := lt.ins.f
	if f.mem != "none" || f.isBranch || f.waitQ || f.waitP || f.xgkick {
		return false
	}
	n := nodeOf(lt)
	if n.reads[resQ] || n.reads[resP] {
		return false
	}
	if dependentAny(n, nodeOf(br)) {
		return false
	}
	for i := pt; i < len(mainPart)-1; i++ {
		if dependentAny(n, nodeOf(mainPart[i])) {
			return false
		}
	}
	// instructions of the prefix after nothing else follow it, so only the branch matters
	return true
}

// planMultiQ builds the prefix plan for loops with several Q producers.
func planMultiQ(toks []*token, lp loop, opt Options) ([]*token, bool) {
	L, B := lp.lbl, lp.br
	if qLiveOut(toks, B) || toks[L].kind != tkLabel {
		return nil, false
	}
	var body []*token
	for i := L + 1; i <= B; i++ {
		if toks[i].kind == tkInstr {
			body = append(body, toks[i])
		}
	}
	var prods []int
	for i, t := range body {
		if t.ins.f.writesQ {
			prods = append(prods, i)
		}
	}
	if len(prods) < 2 {
		return nil, false
	}
	// stages: each producer with the readers up to the next producer
	type stage struct {
		prod        int
		readers     []int
		lastReader  int
		firstReader int
	}
	var stages []stage
	for k, p := range prods {
		end := len(body) - 1
		if k+1 < len(prods) {
			end = prods[k+1]
		}
		st := stage{prod: p, firstReader: -1, lastReader: -1}
		for i := p + 1; i < end; i++ {
			if readsQ(body[i]) {
				st.readers = append(st.readers, i)
				if st.firstReader < 0 {
					st.firstReader = i
				}
				st.lastReader = i
			}
		}
		stages = append(stages, st)
	}
	hasReader := false
	for _, st := range stages {
		if len(st.readers) > 0 {
			hasReader = true
		}
		if len(st.readers) == 0 {
			return nil, false
		}
	}
	if !hasReader {
		return nil, false
	}
	last := len(body) - 1
	lat := body[stages[0].prod].ins.f.latency
	// first stage insertion gap
	ins0 := (last - 1 - stages[0].lastReader) + 1 + stages[0].prod
	if ins0 < lat {
		return nil, false
	}
	cfg := prefixCfg{
		allowPlain: true,
		qMode:      true,
		splits: func(b []*token) []int {
			seen := map[int]bool{}
			var r []int
			add := func(s int) {
				if s >= 1 && s <= last-1 && !seen[s] {
					seen[s] = true
					r = append(r, s)
				}
			}
			if stages[0].prod != 0 {
				add(stages[0].prod)
			}
			add(stages[0].prod + 1)
			if stages[0].firstReader >= 0 {
				add(stages[0].firstReader)
			}
			for _, st := range stages {
				if st.lastReader >= 0 && st.lastReader < last {
					add(st.lastReader + 1)
				}
			}
			return r
		},
	}
	return planPrefix(toks, lp, opt, cfg)
}

func dbgSwp(s, line int) {
	if os.Getenv("VCL_SWPDBG") != "" {
		fmt.Fprintf(os.Stderr, "swp reject s=%d at %d\n", s, line)
	}
}
