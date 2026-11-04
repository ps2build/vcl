package vcl

import (
	"fmt"
	"strings"
)

func dash(l []string) string {
	if len(l) == 0 {
		return "-"
	}
	return strings.Join(l, "|")
}

type isaRow struct {
	name   string
	pipe   string
	unit   string
	thr    int
	lat    int
	delay  int
	sig    string
	flags  []string
	reads  []string
	writes []string
	mem    string
	bypass []string
}

func isaRows() []isaRow {
	var rows []isaRow
	for _, e := range isaTable {
		rows = append(rows, isaRow{e.mnemonic, e.pipe, e.unit, e.throughput, e.latency, e.delay, e.pattern, e.flags, e.reads, e.writes, e.mem, e.bypass})
	}
	return rows
}

// InstructionTable lists the instruction metadata the compiler uses.
func InstructionTable(asJSON bool) string {
	rows := isaRows()
	var sb strings.Builder
	if !asJSON {
		sb.WriteString("VU instruction metadata\n")
		for _, r := range rows {
			fmt.Fprintf(&sb, "%s pipe=%s unit=%s throughput=%d latency=%d branch_delay=%d signature=%q flags=%s reads=%s writes=%s memory=%s bypass=%s\n",
				r.name, r.pipe, r.unit, r.thr, r.lat, r.delay, r.sig, dash(r.flags), dash(r.reads), dash(r.writes), r.mem, dash(r.bypass))
		}
		return sb.String()
	}
	list := func(l []string) string {
		q := make([]string, len(l))
		for i, s := range l {
			q[i] = "\"" + jsonEscape(s) + "\""
		}
		return "[" + strings.Join(q, ", ") + "]"
	}
	sb.WriteString("{\n  \"instructions\": [\n")
	for i, r := range rows {
		fmt.Fprintf(&sb, "    {\"mnemonic\": \"%s\", \"pipe\": \"%s\", \"unit\": \"%s\", \"throughput\": %d, \"latency\": %d, \"branch_delay_slots\": %d, \"signature\": \"%s\", \"flags\": %s, \"reads\": %s, \"writes\": %s, \"memory\": \"%s\", \"bypass\": %s}",
			jsonEscape(r.name), r.pipe, r.unit, r.thr, r.lat, r.delay, jsonEscape(r.sig), list(r.flags), list(r.reads), list(r.writes), r.mem, list(r.bypass))
		if i+1 < len(rows) {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("  ]\n}\n")
	return sb.String()
}

// InstructionInfo describes one instruction form for tools outside the compiler.
type InstructionInfo struct {
	Name      string
	Pipe      string
	Pattern   string
	Flags     []string
	Broadcast bool
}

// Instructions lists every instruction form the compiler knows.
func Instructions() []InstructionInfo {
	var out []InstructionInfo
	for _, e := range isaTable {
		info := InstructionInfo{Name: e.mnemonic, Pipe: e.pipe, Pattern: e.pattern, Flags: e.parser}
		for _, f := range e.parser {
			if f == "broadcast" {
				info.Broadcast = true
			}
		}
		out = append(out, info)
	}
	return out
}
