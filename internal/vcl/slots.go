package vcl

import (
	"fmt"
	"strings"
)

// SlotInfo describes one issue word of the scheduled program.
type SlotInfo struct {
	Index   int
	Cycle   int
	Upper   string
	Lower   string
	Padding string
}

// BlockSlots lists the issue words after one label.
type BlockSlots struct {
	Label string
	Slots []SlotInfo
}

func (e *emitter) slots() []BlockSlots {
	var blocks []BlockSlots
	cycle := 0
	for _, it := range e.items {
		if it.w == nil {
			if n := len(it.text); n > 0 && it.text[n-1] == ':' && it.text[0] != ' ' {
				blocks = append(blocks, BlockSlots{Label: it.text[:n-1]})
			}
			continue
		}
		if len(blocks) == 0 {
			blocks = append(blocks, BlockSlots{Label: "<entry>"})
		}
		b := &blocks[len(blocks)-1]
		s := SlotInfo{Index: len(b.Slots), Cycle: cycle}
		if w := it.w; w.upper != nil {
			s.Upper = w.upper.ins.f.name
		}
		if w := it.w; w.lower != nil {
			s.Lower = w.lower.ins.f.name
		}
		if it.w.upper == nil && it.w.lower == nil {
			switch strings.TrimSpace(it.w.text[1]) {
			case "waitq":
				s.Padding = "waitq"
			case "waitp":
				s.Padding = "waitp"
			default:
				if it.w.text[0] == "" && it.w.text[1] == "" {
					s.Padding = "nop"
				}
			}
		}
		b.Slots = append(b.Slots, s)
		cycle++
	}
	return blocks
}

func slotName(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// SlotsText renders the issue words as plain text.
func SlotsText(blocks []BlockSlots) string {
	var sb strings.Builder
	sb.WriteString("VSM schedule slots\n")
	total := 0
	for _, b := range blocks {
		total += len(b.Slots)
	}
	fmt.Fprintf(&sb, "program_cycle_count=%d\n", total)
	for i, b := range blocks {
		fmt.Fprintf(&sb, "block %d label=%s slots=%d\n", i, b.Label, len(b.Slots))
		for _, s := range b.Slots {
			pad := s.Padding
			if pad == "" {
				pad = "none"
			}
			fmt.Fprintf(&sb, "  slot %d cycle=%d upper=%s lower=%s padding=%s\n", s.Index, s.Cycle, slotName(s.Upper), slotName(s.Lower), pad)
		}
	}
	return sb.String()
}

func jsonString(s string) string {
	if s == "" {
		return "null"
	}
	return "\"" + jsonEscape(s) + "\""
}

// SlotsJSON renders the issue words as JSON.
func SlotsJSON(blocks []BlockSlots) string {
	var sb strings.Builder
	total := 0
	for _, b := range blocks {
		total += len(b.Slots)
	}
	fmt.Fprintf(&sb, "{\n  \"program_cycle_count\": %d,\n  \"blocks\": [\n", total)
	for i, b := range blocks {
		fmt.Fprintf(&sb, "    {\"index\": %d, \"label\": \"%s\", \"slots\": [\n", i, jsonEscape(b.Label))
		for k, s := range b.Slots {
			pad := s.Padding
			if pad == "" {
				pad = "none"
			}
			fmt.Fprintf(&sb, "      {\"index\": %d, \"cycle\": %d, \"upper\": %s, \"lower\": %s, \"padding\": \"%s\"}", s.Index, s.Cycle, jsonString(s.Upper), jsonString(s.Lower), pad)
			if k+1 < len(b.Slots) {
				sb.WriteString(",")
			}
			sb.WriteString("\n")
		}
		sb.WriteString("    ]}")
		if i+1 < len(blocks) {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("  ]\n}\n")
	return sb.String()
}
