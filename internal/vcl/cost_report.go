package vcl

import (
	"fmt"
	"strings"
)

type kv struct {
	text, json string
	v          uint64
}

// CostTotals are the report-wide figures.
type CostTotals struct {
	Static, Estimated                      uint64
	Issue, Fdiv, Efu, Wait, Waitq, Waitp   uint64
	WStatic, WEstimated                    uint64
	WIssue, WFdiv, WEfu, WWait, WWq, WWp   uint64
	AffStaticBase, AffStaticLoop           uint64
	AffEstBase, AffEstLoop                 uint64
	WSlots, WInstr, WPaired, WNopOnly, WOp uint64
	Slots, Instr, Upper, Lower, Paired     uint64
	SingleUp, SingleLow, NopOnly, NopSlots uint64
	OpLat, LongOps, LongCycles, MaxLat     uint64
	Branch, Waitq2, Waitp2, FdivOps, Efu2  uint64
	EBit, DBit, TBit                       uint64
	WNopSlots                              uint64
}

// Totals sums the blocks.
func (r *CostReport) Totals() CostTotals {
	var t CostTotals
	for _, b := range r.Blocks {
		rep := b.Repeat
		cyc := uint64(b.Cycles)
		est := uint64(b.Estimated())
		t.Static += cyc
		t.Estimated += est
		t.Issue += uint64(b.IssueStall)
		t.Fdiv += uint64(b.FdivStall)
		t.Efu += uint64(b.EfuStall)
		t.Wait += uint64(b.WaitStall)
		t.Waitq += uint64(b.WaitqStall)
		t.Waitp += uint64(b.WaitpStall)
		t.WStatic = wrap32(t.WStatic + cyc*rep)
		t.WEstimated = wrap32(t.WEstimated + est*rep)
		t.WIssue = wrap32(t.WIssue + uint64(b.IssueStall)*rep)
		t.WFdiv = wrap32(t.WFdiv + uint64(b.FdivStall)*rep)
		t.WEfu = wrap32(t.WEfu + uint64(b.EfuStall)*rep)
		t.WWait = wrap32(t.WWait + uint64(b.WaitStall)*rep)
		t.WWq = wrap32(t.WWq + uint64(b.WaitqStall)*rep)
		t.WWp = wrap32(t.WWp + uint64(b.WaitpStall)*rep)
		if !b.Fallback {
			if b.Loop {
				t.AffStaticLoop += cyc
				t.AffEstLoop += est
			} else {
				t.AffStaticBase += cyc
				t.AffEstBase += est
			}
		}
		t.WSlots = wrap32(t.WSlots + 2*cyc*rep)
		t.WInstr = wrap32(t.WInstr + uint64(b.Upper+b.Lower)*rep)
		t.WPaired = wrap32(t.WPaired + uint64(b.Paired)*rep)
		t.WNopOnly = wrap32(t.WNopOnly + uint64(b.NopOnly)*rep)
		t.WOp = wrap32(t.WOp + uint64(b.OpLatency)*rep)
		t.WNopSlots = wrap32(t.WNopSlots + uint64(b.NopSlots)*rep)
		t.Slots += 2 * cyc
		t.Instr += uint64(b.Upper + b.Lower)
		t.Upper += uint64(b.Upper)
		t.Lower += uint64(b.Lower)
		t.Paired += uint64(b.Paired)
		t.SingleUp += uint64(b.SingleUp)
		t.SingleLow += uint64(b.SingleLow)
		t.NopOnly += uint64(b.NopOnly)
		t.NopSlots += uint64(b.NopSlots)
		t.OpLat += uint64(b.OpLatency)
		t.LongOps += uint64(b.LongOps)
		t.LongCycles += uint64(b.LongCycles)
		if uint64(b.MaxLatency) > t.MaxLat {
			t.MaxLat = uint64(b.MaxLatency)
		}
		t.Branch += uint64(b.Branch)
		t.Waitq2 += uint64(b.Waitq)
		t.Waitp2 += uint64(b.Waitp)
		t.FdivOps += uint64(b.FdivOps)
		t.Efu2 += uint64(b.EfuOps)
		t.EBit += uint64(b.EBit)
		t.DBit += uint64(b.DBit)
		t.TBit += uint64(b.TBit)
	}
	return t
}

func affine(base, loop uint64) string { return fmt.Sprintf("%d + %dn", base, loop) }

func (t CostTotals) pressure() uint64 {
	if t.Upper > t.Lower {
		return t.Upper
	}
	return t.Lower
}

func pct(a, b uint64) string {
	if b == 0 {
		return "0.00"
	}
	return fmt.Sprintf("%.2f", float64(a)*100/float64(b))
}

func (t CostTotals) headline() []kv {
	return []kv{
		{"static_cycles", "", t.Static}, {"estimated_total_cycles", "", t.Estimated},
		{"issue_stall_cycles", "", t.Issue}, {"fdiv_issue_stall_cycles", "", t.Fdiv}, {"efu_issue_stall_cycles", "", t.Efu},
		{"wait_stall_cycles", "", t.Wait}, {"waitq_stall_cycles", "", t.Waitq}, {"waitp_stall_cycles", "", t.Waitp},
		{"weighted_static_cycles", "", t.WStatic}, {"weighted_estimated_total_cycles", "", t.WEstimated},
		{"weighted_issue_stall_cycles", "", t.WIssue}, {"weighted_fdiv_issue_stall_cycles", "", t.WFdiv}, {"weighted_efu_issue_stall_cycles", "", t.WEfu},
		{"weighted_wait_stall_cycles", "", t.WWait}, {"weighted_waitq_stall_cycles", "", t.WWq}, {"weighted_waitp_stall_cycles", "", t.WWp},
	}
}

func (t CostTotals) affineLines() (static, estimated string) {
	return affine(t.AffStaticBase, t.AffStaticLoop), affine(t.AffEstBase, t.AffEstLoop)
}

func (t CostTotals) weightedMix() []kv {
	return []kv{
		{"weighted_instruction_slots", "", t.WSlots}, {"weighted_instructions", "", t.WInstr},
		{"weighted_paired_cycles", "", t.WPaired}, {"weighted_nop_only_cycles", "", t.WNopOnly},
		{"weighted_operation_latency_cycles", "", t.WOp},
	}
}

func (r *CostReport) blockText(b *BlockCost) string {
	return fmt.Sprintf("  %s: cycles=%d repeat=%d weighted_cycles=%d upper=%d lower=%d paired=%d single_upper=%d single_lower=%d nop_only=%d nop_slots=%d issue_stall=%d fdiv_issue_stall=%d efu_issue_stall=%d wait_stall=%d waitq_stall=%d waitp_stall=%d estimated_cycles=%d op_latency=%d long_ops=%d long_cycles=%d\n",
		b.Label, b.Cycles, b.Repeat, b.wcycles(), b.Upper, b.Lower, b.Paired, b.SingleUp, b.SingleLow, b.NopOnly, b.NopSlots,
		b.IssueStall, b.FdivStall, b.EfuStall, b.WaitStall, b.WaitqStall, b.WaitpStall, b.Estimated(), b.OpLatency, b.LongOps, b.LongCycles)
}

type topList struct {
	text, json string
	key        func(*BlockCost) uint64
	filter     func(*BlockCost) bool
	fields     func(*BlockCost) []kv
	sortIdx    []int
}

func u(v int) uint64 { return uint64(v) }

func topLists() []topList {
	return []topList{
		{"top_weighted_blocks", "top_weighted_blocks", func(b *BlockCost) uint64 { return b.wcycles() }, nil,
			func(b *BlockCost) []kv {
				return []kv{
					{"weighted_cycles", "weighted_cycles", b.wcycles()}, {"cycles", "cycles", u(b.Cycles)}, {"repeat", "repeat", b.Repeat},
					{"paired", "paired_cycles", u(b.Paired)}, {"nop_only", "nop_only_cycles", u(b.NopOnly)},
					{"weighted_nop_only", "weighted_nop_only_cycles", wrap32(u(b.NopOnly) * b.Repeat)},
					{"nop_slots", "nop_slots", u(b.NopSlots)}, {"weighted_nop_slots", "weighted_nop_slots", wrap32(u(b.NopSlots) * b.Repeat)},
					{"estimated_cycles", "estimated_cycles", u(b.Estimated())}, {"weighted_estimated_cycles", "weighted_estimated_cycles", b.westimated()},
					{"issue_stall", "issue_stall_cycles", u(b.IssueStall)}, {"weighted_issue_stall", "weighted_issue_stall_cycles", wrap32(u(b.IssueStall) * b.Repeat)},
					{"wait_stall", "wait_stall_cycles", u(b.WaitStall)}, {"weighted_wait_stall", "weighted_wait_stall_cycles", wrap32(u(b.WaitStall) * b.Repeat)},
				}
			}, []int{0, 1}},
		{"top_weighted_estimated_blocks", "top_weighted_estimated_blocks", func(b *BlockCost) uint64 { return b.westimated() }, nil,
			func(b *BlockCost) []kv {
				return []kv{
					{"weighted_estimated_cycles", "weighted_estimated_cycles", b.westimated()}, {"estimated_cycles", "estimated_cycles", u(b.Estimated())},
					{"repeat", "repeat", b.Repeat}, {"weighted_cycles", "weighted_cycles", b.wcycles()}, {"cycles", "cycles", u(b.Cycles)},
					{"weighted_issue_stall", "weighted_issue_stall_cycles", wrap32(u(b.IssueStall) * b.Repeat)}, {"issue_stall", "issue_stall_cycles", u(b.IssueStall)},
					{"weighted_wait_stall", "weighted_wait_stall_cycles", wrap32(u(b.WaitStall) * b.Repeat)}, {"wait_stall", "wait_stall_cycles", u(b.WaitStall)},
				}
			}, []int{0, 3}},
		{"top_weighted_idle_blocks", "top_weighted_idle_blocks", func(b *BlockCost) uint64 { return wrap32(u(b.NopSlots) * b.Repeat) }, nil,
			func(b *BlockCost) []kv {
				return []kv{
					{"weighted_nop_slots", "weighted_nop_slots", wrap32(u(b.NopSlots) * b.Repeat)}, {"nop_slots", "nop_slots", u(b.NopSlots)},
					{"repeat", "repeat", b.Repeat}, {"weighted_cycles", "weighted_cycles", b.wcycles()}, {"cycles", "cycles", u(b.Cycles)},
					{"weighted_nop_only", "weighted_nop_only_cycles", wrap32(u(b.NopOnly) * b.Repeat)}, {"nop_only", "nop_only_cycles", u(b.NopOnly)},
				}
			}, []int{0, 3}},
		{"top_weighted_wait_blocks", "top_weighted_wait_blocks", func(b *BlockCost) uint64 { return wrap32(u(b.WaitStall) * b.Repeat) },
			func(b *BlockCost) bool { return b.WaitStall > 0 },
			func(b *BlockCost) []kv {
				return []kv{
					{"weighted_wait_stall", "weighted_wait_stall_cycles", wrap32(u(b.WaitStall) * b.Repeat)}, {"wait_stall", "wait_stall_cycles", u(b.WaitStall)},
					{"repeat", "repeat", b.Repeat}, {"weighted_estimated_cycles", "weighted_estimated_cycles", b.westimated()}, {"estimated_cycles", "estimated_cycles", u(b.Estimated())},
					{"weighted_issue_stall", "weighted_issue_stall_cycles", wrap32(u(b.IssueStall) * b.Repeat)}, {"issue_stall", "issue_stall_cycles", u(b.IssueStall)},
					{"weighted_cycles", "weighted_cycles", b.wcycles()}, {"cycles", "cycles", u(b.Cycles)},
				}
			}, []int{0, 1}},
	}
}

func (r *CostReport) selectTop(tl topList) []*BlockCost {
	var bs []*BlockCost
	for _, b := range r.Blocks {
		if tl.filter == nil || tl.filter(b) {
			bs = append(bs, b)
		}
	}
	return sortBlocks(bs, tl.fields, tl.sortIdx)
}

// Text renders the report as plain text.
func (r *CostReport) Text() string {
	t := r.Totals()
	var sb strings.Builder
	sb.WriteString("VSM cost report\n")
	fmt.Fprintf(&sb, "input: %s\n", r.Input)
	w := func(items []kv) {
		for _, it := range items {
			fmt.Fprintf(&sb, "%s: %d\n", it.text, it.v)
		}
	}
	w(t.headline())
	as, ae := t.affineLines()
	fmt.Fprintf(&sb, "affine_static_cycles: %s\n", as)
	fmt.Fprintf(&sb, "affine_static_base_cycles: %d\naffine_static_loop_cycles: %d\n", t.AffStaticBase, t.AffStaticLoop)
	fmt.Fprintf(&sb, "affine_estimated_cycles: %s\n", ae)
	fmt.Fprintf(&sb, "affine_estimated_base_cycles: %d\naffine_estimated_loop_cycles: %d\n", t.AffEstBase, t.AffEstLoop)
	w(t.weightedMix())
	w([]kv{{"instruction_slots", "", t.Slots}, {"instructions", "", t.Instr}, {"upper_instructions", "", t.Upper}, {"lower_instructions", "", t.Lower},
		{"paired_cycles", "", t.Paired}, {"single_upper_cycles", "", t.SingleUp}, {"single_lower_cycles", "", t.SingleLow},
		{"nop_only_cycles", "", t.NopOnly}, {"nop_slots", "", t.NopSlots}})
	fmt.Fprintf(&sb, "slot_utilization_percent: %s\n", pct(t.Instr, t.Slots))
	fmt.Fprintf(&sb, "pairing_efficiency_percent: %s\n", pct(t.Paired, t.Static))
	w([]kv{{"slot_pressure_min_cycles", "", t.pressure()}, {"excess_cycles_over_slot_pressure", "", t.Static - t.pressure()},
		{"operation_latency_cycles", "", t.OpLat}, {"long_latency_ops", "", t.LongOps}, {"long_latency_cycles", "", t.LongCycles},
		{"max_op_latency", "", t.MaxLat}, {"branch_cycles", "", t.Branch}, {"waitq_cycles", "", t.Waitq2}, {"waitp_cycles", "", t.Waitp2},
		{"fdiv_ops", "", t.FdivOps}, {"efu_ops", "", t.Efu2}, {"e_bit_cycles", "", t.EBit}, {"d_bit_cycles", "", t.DBit}, {"t_bit_cycles", "", t.TBit},
		{"unknown_instructions", "", u(r.Unknown)}, {"slot_mismatches", "", u(r.SlotMismatches)}, {"ignored_lines", "", u(r.Ignored)}})
	sb.WriteString("blocks:\n")
	for _, b := range r.Blocks {
		sb.WriteString(r.blockText(b))
	}
	for _, tl := range topLists() {
		sb.WriteString(tl.text + ":\n")
		for _, b := range r.selectTop(tl) {
			fmt.Fprintf(&sb, "  %s:", b.Label)
			for _, f := range tl.fields(b) {
				fmt.Fprintf(&sb, " %s=%d", f.text, f.v)
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func jsonEscape(s string) string {
	var sb strings.Builder
	for _, c := range s {
		switch c {
		case '"':
			sb.WriteString("\\\"")
		case '\\':
			sb.WriteString("\\\\")
		case '\n':
			sb.WriteString("\\n")
		case '\t':
			sb.WriteString("\\t")
		default:
			sb.WriteRune(c)
		}
	}
	return sb.String()
}

func objectJSON(label string, fields []kv) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "{\"label\": \"%s\"", jsonEscape(label))
	for _, f := range fields {
		fmt.Fprintf(&sb, ", \"%s\": %d", f.json, f.v)
	}
	sb.WriteString("}")
	return sb.String()
}

// JSON renders the report as JSON.
func (r *CostReport) JSON() string {
	t := r.Totals()
	var sb strings.Builder
	sb.WriteString("{\n")
	fmt.Fprintf(&sb, "  \"input\": \"%s\",\n", jsonEscape(r.Input))
	num := func(name string, v uint64) { fmt.Fprintf(&sb, "  \"%s\": %d,\n", name, v) }
	for _, it := range t.headline() {
		num(it.text, it.v)
	}
	as, ae := t.affineLines()
	fmt.Fprintf(&sb, "  \"affine_static_cycles\": \"%s\",\n", as)
	num("affine_static_base_cycles", t.AffStaticBase)
	num("affine_static_loop_cycles", t.AffStaticLoop)
	fmt.Fprintf(&sb, "  \"affine_estimated_cycles\": \"%s\",\n", ae)
	num("affine_estimated_base_cycles", t.AffEstBase)
	num("affine_estimated_loop_cycles", t.AffEstLoop)
	for _, it := range t.weightedMix() {
		num(it.text, it.v)
	}
	for _, it := range []kv{{"instruction_slots", "", t.Slots}, {"instructions", "", t.Instr}, {"upper_instructions", "", t.Upper}, {"lower_instructions", "", t.Lower},
		{"paired_cycles", "", t.Paired}, {"single_upper_cycles", "", t.SingleUp}, {"single_lower_cycles", "", t.SingleLow},
		{"nop_only_cycles", "", t.NopOnly}, {"nop_slots", "", t.NopSlots}, {"slot_pressure_min_cycles", "", t.pressure()},
		{"excess_cycles_over_slot_pressure", "", t.Static - t.pressure()}, {"operation_latency_cycles", "", t.OpLat},
		{"long_latency_ops", "", t.LongOps}, {"long_latency_cycles", "", t.LongCycles}, {"max_op_latency", "", t.MaxLat},
		{"branch_cycles", "", t.Branch}, {"waitq_cycles", "", t.Waitq2}, {"waitp_cycles", "", t.Waitp2}, {"fdiv_ops", "", t.FdivOps},
		{"efu_ops", "", t.Efu2}, {"e_bit_cycles", "", t.EBit}, {"d_bit_cycles", "", t.DBit}, {"t_bit_cycles", "", t.TBit},
		{"unknown_instructions", "", u(r.Unknown)}, {"slot_mismatches", "", u(r.SlotMismatches)}, {"ignored_lines", "", u(r.Ignored)}} {
		num(it.text, it.v)
	}
	var labels []string
	for _, b := range r.Blocks {
		labels = append(labels, "\""+jsonEscape(b.Label)+"\"")
	}
	fmt.Fprintf(&sb, "  \"label_order\": [%s],\n", strings.Join(labels, ", "))
	sb.WriteString("  \"cost_by_label\": {\n")
	for i, b := range r.Blocks {
		role := "base"
		if b.Fallback {
			role = "excluded"
		} else if b.Loop {
			role = "loop"
		}
		sb0, sl0, eb0, el0 := u(b.Cycles), uint64(0), u(b.Estimated()), uint64(0)
		if b.Loop {
			sb0, sl0, eb0, el0 = 0, u(b.Cycles), 0, u(b.Estimated())
		}
		if b.Fallback {
			sb0, sl0, eb0, el0 = 0, 0, 0, 0
		}
		fmt.Fprintf(&sb, "    \"%s\": {\"canonical_label\": \"%s\", \"affine_role\": \"%s\", \"repeat\": %d, \"static_cycles\": %d, \"estimated_cycles\": %d, \"weighted_static_cycles\": %d, \"weighted_estimated_cycles\": %d, \"instructions\": %d, \"upper_instructions\": %d, \"lower_instructions\": %d, \"paired_cycles\": %d, \"single_upper_cycles\": %d, \"single_lower_cycles\": %d, \"nop_only_cycles\": %d, \"nop_slots\": %d, \"issue_stall_cycles\": %d, \"fdiv_issue_stall_cycles\": %d, \"efu_issue_stall_cycles\": %d, \"wait_stall_cycles\": %d, \"waitq_stall_cycles\": %d, \"waitp_stall_cycles\": %d, \"affine\": {\"static_base_cycles\": %d, \"static_loop_cycles\": %d, \"estimated_base_cycles\": %d, \"estimated_loop_cycles\": %d}}",
			jsonEscape(b.Label), jsonEscape(b.Canonical), role, b.Repeat, b.Cycles, b.Estimated(), b.wcycles(), b.westimated(),
			b.Upper+b.Lower, b.Upper, b.Lower, b.Paired, b.SingleUp, b.SingleLow, b.NopOnly, b.NopSlots,
			b.IssueStall, b.FdivStall, b.EfuStall, b.WaitStall, b.WaitqStall, b.WaitpStall, sb0, sl0, eb0, el0)
		if i+1 < len(r.Blocks) {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("  },\n  \"blocks\": [\n")
	for i, b := range r.Blocks {
		fmt.Fprintf(&sb, "    {\"label\": \"%s\", \"cycles\": %d, \"repeat\": %d, \"weighted_cycles\": %d, \"upper_instructions\": %d, \"lower_instructions\": %d, \"paired_cycles\": %d, \"single_upper_cycles\": %d, \"single_lower_cycles\": %d, \"nop_only_cycles\": %d, \"nop_slots\": %d, \"issue_stall_cycles\": %d, \"fdiv_issue_stall_cycles\": %d, \"efu_issue_stall_cycles\": %d, \"wait_stall_cycles\": %d, \"waitq_stall_cycles\": %d, \"waitp_stall_cycles\": %d, \"estimated_cycles\": %d, \"operation_latency_cycles\": %d, \"long_latency_ops\": %d, \"long_latency_cycles\": %d}",
			jsonEscape(b.Label), b.Cycles, b.Repeat, b.wcycles(), b.Upper, b.Lower, b.Paired, b.SingleUp, b.SingleLow, b.NopOnly, b.NopSlots,
			b.IssueStall, b.FdivStall, b.EfuStall, b.WaitStall, b.WaitqStall, b.WaitpStall, b.Estimated(), b.OpLatency, b.LongOps, b.LongCycles)
		if i+1 < len(r.Blocks) {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("  ],\n")
	tls := topLists()
	for i, tl := range tls {
		fmt.Fprintf(&sb, "  \"%s\": [\n", tl.json)
		top := r.selectTop(tl)
		for k, b := range top {
			fmt.Fprintf(&sb, "    %s", objectJSON(b.Label, tl.fields(b)))
			if k+1 < len(top) {
				sb.WriteString(",")
			}
			sb.WriteString("\n")
		}
		if len(top) == 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("  ]")
		if i+1 < len(tls) {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}
