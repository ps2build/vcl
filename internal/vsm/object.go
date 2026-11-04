package vsm

import (
	"bytes"
	"debug/elf"
	"fmt"
	"sort"
	"strings"
)

// Section is one section of a VU object.
type Section struct {
	Name  string
	Type  string
	Flags string
	Addr  uint32
	Size  uint32
	Align uint32
	Data  []byte
}

// Symbol is one symbol of a VU object.
type Symbol struct {
	Name    string
	Value   uint32
	Size    uint32
	Section string
	Global  bool
	Kind    string
}

// Reloc is one relocation entry.
type Reloc struct {
	Section string
	Offset  uint32
	Type    string
	Symbol  string
}

// Object is a parsed VU object file.
type Object struct {
	Sections []Section
	Symbols  []Symbol
	Relocs   []Reloc
}

// ReadObject parses an ELF32 object produced by the assembler.
func ReadObject(data []byte) (*Object, error) {
	f, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	o := &Object{}
	for _, s := range f.Sections {
		if s.Type == elf.SHT_NULL {
			continue
		}
		var body []byte
		if s.Type != elf.SHT_NOBITS {
			body, err = s.Data()
			if err != nil {
				return nil, err
			}
		}
		o.Sections = append(o.Sections, Section{
			Name: s.Name, Type: strings.TrimPrefix(s.Type.String(), "SHT_"), Flags: flagString(s.Flags),
			Addr: uint32(s.Addr), Size: uint32(s.Size), Align: uint32(s.Addralign), Data: body,
		})
	}
	syms, err := f.Symbols()
	if err == nil {
		for _, s := range syms {
			if s.Name == "" && elf.ST_TYPE(s.Info) != elf.STT_SECTION {
				continue
			}
			sec := ""
			if int(s.Section) > 0 && int(s.Section) < len(f.Sections) {
				sec = f.Sections[s.Section].Name
			}
			kind := "notype"
			if elf.ST_TYPE(s.Info) == elf.STT_SECTION {
				kind = "section"
			}
			o.Symbols = append(o.Symbols, Symbol{Name: s.Name, Value: uint32(s.Value), Size: uint32(s.Size), Section: sec, Global: elf.ST_BIND(s.Info) == elf.STB_GLOBAL, Kind: kind})
		}
	}
	return o, nil
}

func flagString(f elf.SectionFlag) string {
	var b strings.Builder
	if f&elf.SHF_WRITE != 0 {
		b.WriteByte('W')
	}
	if f&elf.SHF_ALLOC != 0 {
		b.WriteByte('A')
	}
	if f&elf.SHF_EXECINSTR != 0 {
		b.WriteByte('X')
	}
	return b.String()
}

// Section returns the data of the named section, or nil.
func (o *Object) Section(name string) []byte {
	for _, s := range o.Sections {
		if s.Name == name {
			return s.Data
		}
	}
	return nil
}

// Labels returns the named non-section symbols of a section sorted by address.
func (o *Object) Labels(section string) []Symbol {
	var out []Symbol
	for _, s := range o.Symbols {
		if s.Section == section && s.Kind != "section" && s.Name != "" {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	return out
}

// IsInstructionLine reports whether a VSM text line holds an instruction word.
func IsInstructionLine(line string) bool {
	if i := strings.IndexByte(line, ';'); i >= 0 {
		line = line[:i]
	}
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, ".") {
		return false
	}
	if strings.HasSuffix(t, ":") && !strings.ContainsAny(t, " \t") {
		return false
	}
	return true
}

func hexWord(v uint32) string { return fmt.Sprintf("%08x", v) }
