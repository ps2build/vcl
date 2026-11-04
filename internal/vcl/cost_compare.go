package vcl

import (
	"fmt"
	"sort"
	"strings"
)

// CostComparison relates a candidate report to a baseline.
type CostComparison struct {
	Base, Cand *CostReport
}

type metricRow struct {
	name       string
	base, cand int64
}

func (c *CostComparison) metrics() []metricRow {
	b, d := c.Base.Totals(), c.Cand.Totals()
	row := func(n string, x, y uint64) metricRow { return metricRow{n, int64(x), int64(y)} }
	return []metricRow{
		row("static_cycles", b.Static, d.Static), row("estimated_total_cycles", b.Estimated, d.Estimated),
		row("issue_stall_cycles", b.Issue, d.Issue), row("fdiv_issue_stall_cycles", b.Fdiv, d.Fdiv), row("efu_issue_stall_cycles", b.Efu, d.Efu),
		row("wait_stall_cycles", b.Wait, d.Wait), row("waitq_stall_cycles", b.Waitq, d.Waitq), row("waitp_stall_cycles", b.Waitp, d.Waitp),
		row("weighted_static_cycles", b.WStatic, d.WStatic), row("weighted_estimated_total_cycles", b.WEstimated, d.WEstimated),
		row("weighted_issue_stall_cycles", b.WIssue, d.WIssue), row("weighted_fdiv_issue_stall_cycles", b.WFdiv, d.WFdiv),
		row("weighted_efu_issue_stall_cycles", b.WEfu, d.WEfu), row("weighted_wait_stall_cycles", b.WWait, d.WWait),
		row("weighted_waitq_stall_cycles", b.WWq, d.WWq), row("weighted_waitp_stall_cycles", b.WWp, d.WWp),
		row("weighted_instructions", b.WInstr, d.WInstr), row("weighted_paired_cycles", b.WPaired, d.WPaired),
		row("weighted_nop_only_cycles", b.WNopOnly, d.WNopOnly), row("weighted_nop_slots", b.WNopSlots, d.WNopSlots),
		row("affine_static_base_cycles", b.AffStaticBase, d.AffStaticBase), row("affine_static_loop_cycles", b.AffStaticLoop, d.AffStaticLoop),
		row("affine_estimated_base_cycles", b.AffEstBase, d.AffEstBase), row("affine_estimated_loop_cycles", b.AffEstLoop, d.AffEstLoop),
		row("instructions", b.Instr, d.Instr), row("upper_instructions", b.Upper, d.Upper), row("lower_instructions", b.Lower, d.Lower),
		row("paired_cycles", b.Paired, d.Paired), row("nop_only_cycles", b.NopOnly, d.NopOnly), row("nop_slots", b.NopSlots, d.NopSlots),
		row("operation_latency_cycles", b.OpLat, d.OpLat), row("long_latency_ops", b.LongOps, d.LongOps),
		row("long_latency_cycles", b.LongCycles, d.LongCycles), row("max_op_latency", b.MaxLat, d.MaxLat),
	}
}

type labelPair struct {
	label      string
	base, cand *BlockCost
}

func (c *CostComparison) pairs() []labelPair {
	zero := func(l string) *BlockCost { return &BlockCost{Label: l} }
	byLabel := func(r *CostReport) map[string]*BlockCost {
		m := map[string]*BlockCost{}
		for _, b := range r.Blocks {
			if _, ok := m[b.Canonical]; !ok {
				m[b.Canonical] = b
			}
		}
		return m
	}
	bm, cm := byLabel(c.Base), byLabel(c.Cand)
	var order []string
	seen := map[string]bool{}
	for _, b := range c.Base.Blocks {
		if !seen[b.Canonical] {
			seen[b.Canonical] = true
			order = append(order, b.Canonical)
		}
	}
	for _, b := range c.Cand.Blocks {
		if !seen[b.Canonical] {
			seen[b.Canonical] = true
			order = append(order, b.Canonical)
		}
	}
	sort.Strings(order)
	var out []labelPair
	for _, l := range order {
		p := labelPair{label: l, base: bm[l], cand: cm[l]}
		if p.base == nil {
			p.base = zero(l)
		}
		if p.cand == nil {
			p.cand = zero(l)
		}
		out = append(out, p)
	}
	return out
}

type compareList struct {
	text, json string
	value      func(*BlockCost) uint64
	extra      []struct {
		text, json string
		value      func(*BlockCost) uint64
	}
}

func compareLists() []compareList {
	type ex = struct {
		text, json string
		value      func(*BlockCost) uint64
	}
	return []compareList{
		{"top_weighted_estimated_blocks", "weighted_estimated_cycles", func(b *BlockCost) uint64 { return b.westimated() },
			[]ex{{"estimated_cycles", "estimated_cycles", func(b *BlockCost) uint64 { return u(b.Estimated()) }}}},
		{"top_weighted_idle_blocks", "weighted_nop_slots", func(b *BlockCost) uint64 { return wrap32(u(b.NopSlots) * b.Repeat) },
			[]ex{{"nop_slots", "nop_slots", func(b *BlockCost) uint64 { return u(b.NopSlots) }}}},
		{"top_weighted_wait_blocks", "weighted_wait_stall_cycles", func(b *BlockCost) uint64 { return wrap32(u(b.WaitStall) * b.Repeat) },
			[]ex{{"wait_stall_cycles", "wait_stall_cycles", func(b *BlockCost) uint64 { return u(b.WaitStall) }}}},
	}
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func (c *CostComparison) topFor(l compareList) []labelPair {
	var ps []labelPair
	for _, p := range c.pairs() {
		if int64(l.value(p.cand))-int64(l.value(p.base)) != 0 {
			ps = append(ps, p)
		}
	}
	sort.SliceStable(ps, func(i, j int) bool {
		di := abs64(int64(l.value(ps[i].cand)) - int64(l.value(ps[i].base)))
		dj := abs64(int64(l.value(ps[j].cand)) - int64(l.value(ps[j].base)))
		if di != dj {
			return di > dj
		}
		if ci, cj := l.value(ps[i].cand), l.value(ps[j].cand); ci != cj {
			return ci > cj
		}
		return ps[i].label < ps[j].label
	})
	if len(ps) > 8 {
		ps = ps[:8]
	}
	return ps
}

// Text renders the comparison as plain text.
func (c *CostComparison) Text() string {
	var sb strings.Builder
	sb.WriteString("VSM cost comparison\n")
	fmt.Fprintf(&sb, "baseline: %s\ncandidate: %s\nmetrics:\n", c.Base.Input, c.Cand.Input)
	for _, m := range c.metrics() {
		fmt.Fprintf(&sb, "  %s: baseline=%d candidate=%d delta=%d\n", m.name, m.base, m.cand, m.cand-m.base)
	}
	for _, l := range compareLists() {
		sb.WriteString(l.text + ":\n")
		ps := c.topFor(l)
		if len(ps) == 0 {
			sb.WriteString("  (no block deltas)\n")
		}
		for _, p := range ps {
			bv, cv := l.value(p.base), l.value(p.cand)
			fmt.Fprintf(&sb, "  %s: baseline_%s=%d candidate_%s=%d delta=%d", p.label, l.json, bv, l.json, cv, int64(cv)-int64(bv))
			for _, e := range l.extra {
				fmt.Fprintf(&sb, " baseline_%s=%d candidate_%s=%d", e.text, e.value(p.base), e.text, e.value(p.cand))
			}
			fmt.Fprintf(&sb, " baseline_repeat=%d candidate_repeat=%d\n", p.base.Repeat, p.cand.Repeat)
		}
	}
	return sb.String()
}

func (r *CostReport) compareObject() string {
	t := r.Totals()
	return fmt.Sprintf("{\"input\": \"%s\"", jsonEscape(r.Input)) + func() string {
		var sb strings.Builder
		for _, m := range (&CostComparison{Base: r, Cand: r}).metrics() {
			fmt.Fprintf(&sb, ", \"%s\": %d", m.name, m.base)
		}
		_ = t
		return sb.String()
	}() + "}"
}

func blockCompareJSON(b *BlockCost, label string) string {
	if b.Label != "" {
		label = b.Label
	}
	return fmt.Sprintf("{\"label\": \"%s\", \"static_cycles\": %d, \"estimated_cycles\": %d, \"repeat\": %d, \"weighted_static_cycles\": %d, \"weighted_estimated_cycles\": %d, \"nop_slots\": %d, \"weighted_nop_slots\": %d, \"wait_stall_cycles\": %d, \"weighted_wait_stall_cycles\": %d, \"issue_stall_cycles\": %d, \"weighted_issue_stall_cycles\": %d}",
		jsonEscape(label), b.Cycles, b.Estimated(), b.Repeat, b.wcycles(), b.westimated(), b.NopSlots, wrap32(u(b.NopSlots)*b.Repeat),
		b.WaitStall, wrap32(u(b.WaitStall)*b.Repeat), b.IssueStall, wrap32(u(b.IssueStall)*b.Repeat))
}

// JSON renders the comparison as JSON.
func (c *CostComparison) JSON() string {
	var sb strings.Builder
	sb.WriteString("{\n")
	fmt.Fprintf(&sb, "  \"baseline\": %s,\n  \"candidate\": ", c.Base.compareObject())
	cand := &CostComparison{Base: c.Cand, Cand: c.Cand}
	sb.WriteString(cand.Base.compareObject())
	sb.WriteString(",\n  \"delta\": {")
	for i, m := range c.metrics() {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "\"%s\": %d", m.name, m.cand-m.base)
	}
	sb.WriteString("},\n  \"label_comparisons\": [\n")
	ps := c.pairs()
	for i, p := range ps {
		d := int64(p.cand.westimated()) - int64(p.base.westimated())
		dn := int64(wrap32(u(p.cand.NopSlots)*p.cand.Repeat)) - int64(wrap32(u(p.base.NopSlots)*p.base.Repeat))
		dw := int64(wrap32(u(p.cand.WaitStall)*p.cand.Repeat)) - int64(wrap32(u(p.base.WaitStall)*p.base.Repeat))
		fmt.Fprintf(&sb, "    {\"label\": \"%s\", \"baseline\": %s, \"candidate\": %s, \"delta\": {\"weighted_estimated_cycles\": %d, \"weighted_nop_slots\": %d, \"weighted_wait_stall_cycles\": %d}}",
			jsonEscape(p.label), blockCompareJSON(p.base, p.label), blockCompareJSON(p.cand, p.label), d, dn, dw)
		if i+1 < len(ps) {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("  ],\n")
	lists := compareLists()
	for i, l := range lists {
		fmt.Fprintf(&sb, "  \"%s\": [\n", l.text)
		top := c.topFor(l)
		for k, p := range top {
			bv, cv := l.value(p.base), l.value(p.cand)
			fmt.Fprintf(&sb, "    {\"label\": \"%s\", \"baseline_%s\": %d, \"candidate_%s\": %d, \"delta_%s\": %d", jsonEscape(p.label), l.json, bv, l.json, cv, l.json, int64(cv)-int64(bv))
			for _, e := range l.extra {
				fmt.Fprintf(&sb, ", \"baseline_%s\": %d, \"candidate_%s\": %d", e.json, e.value(p.base), e.json, e.value(p.cand))
			}
			fmt.Fprintf(&sb, ", \"baseline_repeat\": %d, \"candidate_repeat\": %d}", p.base.Repeat, p.cand.Repeat)
			if k+1 < len(top) {
				sb.WriteString(",")
			}
			sb.WriteString("\n")
		}
		if len(top) == 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("  ]")
		if i+1 < len(lists) {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

// MarkdownHeader is the two-line table header of the markdown comparison.
func MarkdownHeader() string {
	cols := []string{"baseline", "candidate", "baseline static", "candidate static", "static delta", "baseline estimated", "candidate estimated", "estimated delta", "estimated ratio",
		"baseline weighted static", "candidate weighted static", "weighted static delta", "baseline weighted estimated", "candidate weighted estimated", "weighted estimated delta", "weighted estimated ratio",
		"baseline affine estimated", "candidate affine estimated", "affine estimated base delta", "affine estimated loop delta",
		"baseline issue stall", "candidate issue stall", "issue stall delta", "baseline wait stall", "candidate wait stall", "wait stall delta",
		"baseline paired", "candidate paired", "paired delta"}
	var sb strings.Builder
	sb.WriteString("| " + strings.Join(cols, " | ") + " |\n")
	sb.WriteString("|---|---|")
	for i := 2; i < len(cols); i++ {
		sb.WriteString("---:|")
	}
	sb.WriteString("\n")
	return sb.String()
}

func signed(v int64) string {
	if v > 0 {
		return fmt.Sprintf("+%d", v)
	}
	return fmt.Sprintf("%d", v)
}

func ratio(base, cand uint64) string {
	if base == 0 {
		return "0.00x"
	}
	return fmt.Sprintf("%.2fx", float64(cand)/float64(base))
}

func mdEscape(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

// MarkdownRow is one table row for the comparison.
func (c *CostComparison) MarkdownRow() string {
	b, d := c.Base.Totals(), c.Cand.Totals()
	d64 := func(x, y uint64) string { return signed(int64(y) - int64(x)) }
	cells := []string{
		mdEscape(c.Base.Input), mdEscape(c.Cand.Input),
		fmt.Sprint(b.Static), fmt.Sprint(d.Static), d64(b.Static, d.Static),
		fmt.Sprint(b.Estimated), fmt.Sprint(d.Estimated), d64(b.Estimated, d.Estimated), ratio(b.Estimated, d.Estimated),
		fmt.Sprint(b.WStatic), fmt.Sprint(d.WStatic), d64(b.WStatic, d.WStatic),
		fmt.Sprint(b.WEstimated), fmt.Sprint(d.WEstimated), d64(b.WEstimated, d.WEstimated), ratio(b.WEstimated, d.WEstimated),
		affine(b.AffEstBase, b.AffEstLoop), affine(d.AffEstBase, d.AffEstLoop), d64(b.AffEstBase, d.AffEstBase), d64(b.AffEstLoop, d.AffEstLoop),
		fmt.Sprint(b.Issue), fmt.Sprint(d.Issue), d64(b.Issue, d.Issue),
		fmt.Sprint(b.Wait), fmt.Sprint(d.Wait), d64(b.Wait, d.Wait),
		fmt.Sprint(b.Paired), fmt.Sprint(d.Paired), d64(b.Paired, d.Paired),
	}
	return "| " + strings.Join(cells, " | ") + " |\n"
}

// Markdown renders a one-row table for the comparison.
func (c *CostComparison) Markdown() string { return MarkdownHeader() + c.MarkdownRow() }

// CostMetric returns a named figure of a report; both dash and underscore spellings are accepted.
func CostMetric(r *CostReport, name string) (uint64, bool) {
	t := r.Totals()
	switch strings.ReplaceAll(name, "-", "_") {
	case "static", "static_cycles":
		return t.Static, true
	case "estimated", "estimated_total_cycles":
		return t.Estimated, true
	case "weighted_static", "weighted_static_cycles":
		return t.WStatic, true
	case "weighted_estimated", "weighted_estimated_total_cycles":
		return t.WEstimated, true
	case "affine_static_base", "affine_static_base_cycles":
		return t.AffStaticBase, true
	case "affine_static_loop", "affine_static_loop_cycles":
		return t.AffStaticLoop, true
	case "affine_estimated_base", "affine_estimated_base_cycles":
		return t.AffEstBase, true
	case "affine_estimated_loop", "affine_estimated_loop_cycles":
		return t.AffEstLoop, true
	}
	return 0, false
}

// CostRegression describes a metric where the candidate is slower than the baseline.
type CostRegression struct {
	Metric           string
	Baseline, Cand   uint64
	BaseName, CandIn string
}

// CheckCost reports a regression when the candidate figure exceeds the baseline's.
func CheckCost(base, cand *CostReport, metric string) (*CostRegression, bool) {
	a, ok := CostMetric(base, metric)
	if !ok {
		return nil, false
	}
	b, _ := CostMetric(cand, metric)
	if b > a {
		return &CostRegression{metric, a, b, base.Input, cand.Input}, true
	}
	return nil, true
}
