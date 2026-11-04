package vcl

import (
	"fmt"
	"sort"
	"strings"
)

// CostWeights maps a block label to its repeat count.
type CostWeights map[string]uint64

// PS2GLWeights weights the four renderer loops by 100.
func PS2GLWeights() CostWeights {
	return CostWeights{
		"xform_loop_lid":          100,
		"dir_light_vert_loop_lid": 100,
		"pt_light_vert_loop_lid":  100,
		"final_loop_lid":          100,
	}
}

// BlockCost holds the figures for one labelled block.
type BlockCost struct {
	Label      string
	Canonical  string
	Repeat     uint64
	Loop       bool
	Fallback   bool
	Cycles     int
	Upper      int
	Lower      int
	Paired     int
	SingleUp   int
	SingleLow  int
	NopOnly    int
	NopSlots   int
	IssueStall int
	FdivStall  int
	EfuStall   int
	WaitStall  int
	WaitqStall int
	WaitpStall int
	OpLatency  int
	LongOps    int
	LongCycles int
	MaxLatency int
	Branch     int
	Waitq      int
	Waitp      int
	FdivOps    int
	EfuOps     int
	EBit       int
	DBit       int
	TBit       int
}

// Estimated is the block's cycle count including stalls.
func (b *BlockCost) Estimated() int { return b.Cycles + b.IssueStall + b.WaitStall }

func (b *BlockCost) wcycles() uint64    { return wrap32(uint64(b.Cycles) * b.Repeat) }
func (b *BlockCost) westimated() uint64 { return wrap32(uint64(b.Estimated()) * b.Repeat) }

// wrap32 wraps weighted figures to the 32-bit unsigned range.
func wrap32(v uint64) uint64 { return v & 0xffffffff }

// CostReport is the analysis of one VSM text.
type CostReport struct {
	Input          string
	Blocks         []*BlockCost
	Unknown        int
	SlotMismatches int
	Ignored        int
}

type costSlot struct {
	name string
	form *form
}

func costForm(word string, upperCtx bool) (*form, string, bool) {
	w := strings.ToLower(word)
	if i := strings.IndexByte(w, '['); i >= 0 {
		w = w[:i]
	}
	if i := strings.IndexByte(w, '.'); i >= 0 {
		w = w[:i]
	}
	if w == "" {
		return nil, "", false
	}
	if fs, ok := forms[w]; ok {
		return fs[0], w, true
	}
	if n := len(w); n > 1 && strings.IndexByte("xyzw", w[n-1]) >= 0 {
		if fs, ok := forms[w[:n-1]]; ok && fs[0].upper {
			return fs[0], w[:n-1], true
		}
	}
	return nil, w, false
}

func isWordChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '[' || c == ']'
}

type costWord struct {
	up, lo       *form
	upText       string
	loText       string
	ebit, dbit   bool
	tbit         bool
	upperActive  bool
	mismatchSlot bool
}

// AnalyzeCost parses compiled VSM text and returns its cost figures.
func AnalyzeCost(name, text string, weights CostWeights) *CostReport {
	rep := &CostReport{Input: name}
	var cur *BlockCost
	var pendingLabels []string
	startBlock := func(label string) {
		cur = &BlockCost{Label: label, Repeat: 1}
		rep.Blocks = append(rep.Blocks, cur)
	}
	text = strings.ReplaceAll(text, "\r", "")
	var clock int
	var qReady, pReady, fdivFree, efuFree int
	for _, raw := range strings.Split(text, "\n") {
		line := raw
		if i := strings.IndexByte(line, ';'); i >= 0 {
			line = line[:i]
		}
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		if trim[0] == '.' {
			continue
		}
		if strings.HasSuffix(trim, ":") && !strings.ContainsAny(trim, " \t") {
			label := strings.TrimSuffix(trim, ":")
			if cur != nil && cur.Cycles == 0 {
				cur.Label = label
				continue
			}
			startBlock(label)
			continue
		}
		var words []string
		var positions []int
		for i := 0; i < len(trim); {
			if !isWordChar(trim[i]) {
				i++
				continue
			}
			j := i
			for j < len(trim) && isWordChar(trim[j]) {
				j++
			}
			words = append(words, trim[i:j])
			positions = append(positions, i)
			i = j
		}
		var found []costSlot
		var rawFound []string
		for _, w := range words {
			if f, nm, ok := costForm(w, false); ok {
				found = append(found, costSlot{nm, f})
				rawFound = append(rawFound, w)
			}
		}
		if len(found) == 0 {
			rep.Ignored++
			continue
		}
		if cur == nil {
			startBlock("<entry>")
		}
		var up, lo *costSlot
		var upRaw, loRaw string
		if len(found) == 1 {
			if found[0].form.upper || found[0].form.nop {
				up, upRaw = &found[0], rawFound[0]
			} else {
				lo, loRaw = &found[0], rawFound[0]
			}
		} else {
			up, upRaw = &found[0], rawFound[0]
			lo, loRaw = &found[1], rawFound[1]
		}
		cur.Cycles++
		w := costWord{}
		var hasE, hasD, hasT bool
		for _, rw := range rawFound {
			if i := strings.IndexByte(rw, '['); i >= 0 && i+1 < len(rw) {
				switch strings.ToUpper(rw[i+1 : i+2]) {
				case "E":
					hasE = true
				case "D":
					hasD = true
				case "T":
					hasT = true
				}
			}
		}
		if hasE {
			cur.EBit++
		}
		if hasD {
			cur.DBit++
		}
		if hasT {
			cur.TBit++
		}
		upIsNop := up == nil || up.form.nop
		loIsNop := lo == nil || lo.form.nop
		if up != nil && !up.form.upper && !up.form.nop {
			rep.SlotMismatches++
		}
		if lo != nil && !lo.form.lower && !lo.form.nop {
			rep.SlotMismatches++
		}
		if up != nil {
			w.up = up.form
		}
		if lo != nil {
			w.lo = lo.form
		}
		_, _ = loRaw, upRaw
		slotsNop := 0
		if upIsNop {
			slotsNop++
		} else {
			cur.Upper++
		}
		if loIsNop {
			slotsNop++
		} else {
			cur.Lower++
		}
		cur.NopSlots += slotsNop
		switch {
		case !upIsNop && !loIsNop:
			cur.Paired++
		case !upIsNop:
			cur.SingleUp++
		case !loIsNop:
			cur.SingleLow++
		default:
			cur.NopOnly++
		}
		w.upperActive = !upIsNop
		cost := func(f *form, isUpperSlot bool) {
			if f == nil || f.nop {
				return
			}
			cur.OpLatency += f.latency
			if f.latency > 1 {
				cur.LongOps++
				cur.LongCycles += f.latency - 1
			}
			if f.latency > cur.MaxLatency {
				cur.MaxLatency = f.latency
			}
			if f.isBranch {
				cur.Branch++
			}
		}
		cost(w.up, true)
		cost(w.lo, false)
		producer := func(f *form) (q, p bool) {
			if f == nil || f.nop {
				return false, false
			}
			return f.unit == "fdiv" && !f.waitQ && !f.waitP, f.unit == "efu" && !f.waitP && !f.waitQ
		}
		qs := []*form{w.up, w.lo}
		for _, f := range qs {
			q, p := producer(f)
			if q {
				cur.FdivOps++
				if clock < fdivFree {
					s := fdivFree - clock
					cur.IssueStall += s
					cur.FdivStall += s
					clock += s
				}
			}
			if p {
				cur.EfuOps++
				if clock < efuFree {
					s := efuFree - clock
					cur.IssueStall += s
					cur.EfuStall += s
					clock += s
				}
			}
		}
		for _, f := range qs {
			if f == nil || f.nop {
				continue
			}
			if f.waitQ {
				cur.Waitq++
				resume := clock + 1
				if w.upperActive {
					resume = clock
				}
				if qReady > resume {
					s := qReady - resume
					cur.WaitStall += s
					cur.WaitqStall += s
					clock += s
				}
			}
			if f.waitP {
				cur.Waitp++
				resume := clock + 1
				if w.upperActive {
					resume = clock
				}
				if pReady > resume {
					s := pReady - resume
					cur.WaitStall += s
					cur.WaitpStall += s
					clock += s
				}
			}
		}
		for _, f := range qs {
			q, p := producer(f)
			if q {
				fdivFree = clock + f.throughput
				qReady = clock + f.latency + 1
			}
			if p {
				efuFree = clock + f.throughput
				pReady = clock + f.latency + 1
			}
		}
		clock++
	}
	_ = pendingLabels
	var kept []*BlockCost
	for _, b := range rep.Blocks {
		if b.Cycles > 0 {
			kept = append(kept, b)
		}
	}
	rep.Blocks = kept
	rep.applyWeights(weights)
	return rep
}

func canonicalLabel(label string) string {
	l := strings.ReplaceAll(label, "__MAIN_LOOP", "")
	if i := strings.LastIndex(l, "_vcl_"); i >= 0 {
		tail := l[i+len("_vcl_"):]
		if tail == "adcLoop_done_lid" {
			for _, k := range []string{"EXPL_vu1_fast_nolights_", "EXPL_vu1_fast_", "EXPL_vu1_scei_"} {
				if strings.HasPrefix(l, k) {
					return "xform_loop_lid"
				}
			}
		}
		if strings.HasSuffix(tail, "_lid") {
			return tail
		}
		return label
	}
	for _, k := range PS2GLLoopLabels {
		if strings.HasSuffix(l, k) {
			return k
		}
	}
	return label
}

// PS2GLLoopLabels lists the renderer loop labels.
var PS2GLLoopLabels = []string{"xform_loop_lid", "dir_light_vert_loop_lid", "pt_light_vert_loop_lid", "final_loop_lid"}

func (r *CostReport) applyWeights(w CostWeights) {
	for _, b := range r.Blocks {
		b.Canonical = canonicalLabel(b.Label)
		b.Fallback = strings.Contains(b.Label, "__FALLBACK")
		if n, ok := w[b.Canonical]; ok {
			b.Repeat = n
			b.Loop = true
		}
	}
}

func sortBlocks(bs []*BlockCost, fields func(*BlockCost) []kv, idx []int) []*BlockCost {
	out := append([]*BlockCost{}, bs...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := fields(out[i]), fields(out[j])
		for _, k := range idx {
			if a[k].v != b[k].v {
				return a[k].v > b[k].v
			}
		}
		return out[i].Label < out[j].Label
	})
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

// ParseCostWeights parses a comma-separated list of "ps2gl" or "label=count" items.
// Counts must be positive decimal numbers that fit in 32 bits.
func ParseCostWeights(spec string) (CostWeights, error) {
	w := CostWeights{}
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if item == "ps2gl" || item == "ps2gl-100" {
			for k, n := range PS2GLWeights() {
				w[k] = n
			}
			continue
		}
		i := strings.IndexByte(item, '=')
		if i <= 0 {
			return nil, fmt.Errorf("bad loop weight %q: expected label=count or ps2gl", item)
		}
		var n uint64
		for _, c := range item[i+1:] {
			if c < '0' || c > '9' {
				return nil, fmt.Errorf("bad loop weight %q: the count must be a positive decimal number", item)
			}
			n = n*10 + uint64(c-'0')
			if n > 0xffffffff {
				return nil, fmt.Errorf("bad loop weight %q: the count is too large", item)
			}
		}
		if n == 0 || i+1 == len(item) {
			return nil, fmt.Errorf("bad loop weight %q: the count must be positive", item)
		}
		w[item[:i]] = n
	}
	return w, nil
}
