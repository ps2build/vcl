package vsm

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
)

// DumpOptions selects what Dump prints.
type DumpOptions struct {
	Headers      bool
	Symbols      bool
	Relocs       bool
	Contents     bool
	Disassemble  bool
	Section      string
	Start, Stop  uint32
	HasStop      bool
	NoRawInsn    bool
	NoAddresses  bool
	FileName     string
	AssemblyOnly bool
}

// Dump renders the requested views of the object as text.
func (o *Object) Dump(opt DumpOptions) (string, error) {
	var sb strings.Builder
	opt.AssemblyOnly = opt.Disassemble && opt.NoAddresses && opt.NoRawInsn
	if !opt.AssemblyOnly {
		fmt.Fprintf(&sb, "%s:\tfile format elf32-vu\n\n", opt.FileName)
	}
	if opt.Headers {
		sb.WriteString("Sections:\nIdx Name          Size     VMA      Align Type\n")
		for i, s := range o.Sections {
			fmt.Fprintf(&sb, "%3d %-13s %08x %08x 2**%d %s\n", i, s.Name, s.Size, s.Addr, log2(s.Align), s.Type)
		}
		sb.WriteString("\n")
	}
	if opt.Symbols {
		sb.WriteString("SYMBOL TABLE:\n")
		for _, s := range o.Symbols {
			scope := "l"
			if s.Global {
				scope = "g"
			}
			kind := " "
			if s.Kind == "section" {
				kind = "d"
			}
			sec := s.Section
			if sec == "" {
				sec = "*UND*"
			}
			fmt.Fprintf(&sb, "%08x %s     %s %s\t%08x %s\n", s.Value, scope, kind, sec, s.Size, s.Name)
		}
		sb.WriteString("\n")
	}
	if opt.Relocs {
		if len(o.Relocs) == 0 {
			sb.WriteString("RELOCATION RECORDS: none\n\n")
		}
		for _, r := range o.Relocs {
			fmt.Fprintf(&sb, "%08x %s %s\n", r.Offset, r.Type, r.Symbol)
		}
	}
	for _, s := range o.Sections {
		if opt.Section != "" && s.Name != opt.Section {
			continue
		}
		if opt.Contents && s.Data != nil {
			fmt.Fprintf(&sb, "Contents of section %s:\n", s.Name)
			dumpHex(&sb, s.Data, s.Addr, opt)
			sb.WriteString("\n")
		}
		if opt.Disassemble && s.Name == ".vutext" {
			if err := o.disassembleSection(&sb, s, opt); err != nil {
				return sb.String(), err
			}
		}
	}
	return sb.String(), nil
}

func log2(v uint32) int {
	n := 0
	for v > 1 {
		v >>= 1
		n++
	}
	return n
}

func clip(data []byte, base uint32, opt DumpOptions) (lo, hi uint32) {
	lo, hi = 0, uint32(len(data))
	if opt.Start > base && opt.Start-base > lo {
		lo = opt.Start - base
	}
	if opt.HasStop && opt.Stop > base && opt.Stop-base < hi {
		hi = opt.Stop - base
	}
	if lo > hi {
		lo = hi
	}
	return lo, hi
}

func dumpHex(sb *strings.Builder, data []byte, base uint32, opt DumpOptions) {
	lo, hi := clip(data, base, opt)
	for off := lo &^ 15; off < hi; off += 16 {
		fmt.Fprintf(sb, " %04x", base+off)
		var ascii strings.Builder
		for i := uint32(0); i < 16; i++ {
			if i%4 == 0 {
				sb.WriteByte(' ')
			}
			if off+i < uint32(len(data)) && off+i >= lo && off+i < hi {
				b := data[off+i]
				fmt.Fprintf(sb, "%02x", b)
				if b >= 32 && b < 127 {
					ascii.WriteByte(b)
				} else {
					ascii.WriteByte('.')
				}
			} else {
				sb.WriteString("  ")
				ascii.WriteByte(' ')
			}
		}
		fmt.Fprintf(sb, "  %s\n", ascii.String())
	}
}

func (o *Object) disassembleSection(sb *strings.Builder, s Section, opt DumpOptions) error {
	named := map[uint32][]Symbol{}
	for _, sym := range o.Labels(s.Name) {
		named[sym.Value] = append(named[sym.Value], sym)
	}
	synthetic := map[int]bool{}
	label := func(pc int) string {
		if syms, ok := named[uint32(pc*8)]; ok {
			return syms[0].Name
		}
		synthetic[pc] = true
		return fmt.Sprintf("L_%04x", pc*8)
	}
	words := Disassemble(s.Data, label)
	for len(words) > 0 && words[len(words)-1].Upper == 0 && words[len(words)-1].Lower == 0 {
		words = words[:len(words)-1]
	}
	lo, hi := clip(s.Data, s.Addr, opt)
	if !opt.AssemblyOnly {
		fmt.Fprintf(sb, "Disassembly of section %s:\n", s.Name)
	} else {
		fmt.Fprintf(sb, "\t\t.vu\n\t\t.align %d\n", log2(maxAlign(s.Align)))
		var globals []string
		for _, sym := range o.Labels(s.Name) {
			if sym.Global {
				globals = append(globals, sym.Name)
			}
		}
		sort.Strings(globals)
		for _, g := range globals {
			fmt.Fprintf(sb, "\t\t.global\t%s\n", g)
		}
	}
	for pc, w := range words {
		off := uint32(pc * 8)
		if off < lo || off >= hi {
			continue
		}
		if syms, ok := named[off]; ok {
			for _, sym := range syms {
				if opt.AssemblyOnly || opt.NoAddresses {
					fmt.Fprintf(sb, "%s:\n", sym.Name)
				} else {
					fmt.Fprintf(sb, "\n%08x <%s>:\n", s.Addr+off, sym.Name)
				}
			}
		} else if synthetic[pc] {
			if opt.AssemblyOnly || opt.NoAddresses {
				fmt.Fprintf(sb, "L_%04x:\n", off)
			} else {
				fmt.Fprintf(sb, "\n%08x <L_%04x>:\n", s.Addr+off, off)
			}
		}
		line := w.Render()
		if w.Unknown {
			line = fmt.Sprintf("; unknown instruction %08x %08x", w.Upper, w.Lower)
		}
		var prefix string
		if !opt.NoAddresses {
			prefix = fmt.Sprintf("%8x:\t", s.Addr+off)
		}
		if !opt.NoRawInsn {
			var raw [8]byte
			binary.LittleEndian.PutUint32(raw[:], w.Lower)
			binary.LittleEndian.PutUint32(raw[4:], w.Upper)
			prefix += fmt.Sprintf("%02x %02x %02x %02x %02x %02x %02x %02x\t", raw[0], raw[1], raw[2], raw[3], raw[4], raw[5], raw[6], raw[7])
		}
		sb.WriteString(prefix + strings.TrimLeft(line, " ") + "\n")
	}
	end := uint32(len(words) * 8)
	for _, sym := range o.Labels(s.Name) {
		if sym.Value < end || sym.Value < lo {
			continue
		}
		if opt.AssemblyOnly || opt.NoAddresses {
			if sym.Value > end {
				for e := uint(3); e <= 12; e++ {
					if (end+1<<e-1)&^(1<<e-1) == sym.Value {
						fmt.Fprintf(sb, "\t\t.align %d\n", e)
						break
					}
				}
				end = sym.Value
			}
			fmt.Fprintf(sb, "%s:\n", sym.Name)
		} else {
			fmt.Fprintf(sb, "\n%08x <%s>:\n", s.Addr+sym.Value, sym.Name)
		}
	}
	if opt.AssemblyOnly {
		return nil
	}
	sb.WriteString("\n")
	return nil
}

func maxAlign(a uint32) uint32 {
	if a < 8 {
		return 8
	}
	return a
}
