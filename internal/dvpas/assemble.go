// Package dvpas assembles .vsm VU microcode sources into relocatable ELF
// objects.
package dvpas

import (
	"encoding/binary"
	"fmt"
	"io"
	"sort"

	"github.com/ps2build/vcl/internal/dvpas/elfout"
	"github.com/ps2build/vcl/internal/dvpas/encode"
	"github.com/ps2build/vcl/internal/dvpas/parse"
)

// Assemble reads a .vsm source from r and writes a relocatable ELF object
// to w.
func Assemble(r io.Reader, w io.Writer) error {
	prog, err := parse.Parse(r)
	if err != nil {
		return err
	}

	size := 0
	for _, inst := range prog.Instructions {
		if inst.PaddingBytes > 0 {
			size += inst.PaddingBytes
		} else {
			size += 8
		}
	}
	if rem := uint32(size) % prog.Align; rem != 0 {
		size += int(prog.Align - rem)
	}

	code := make([]byte, size)
	var offset uint32
	for _, inst := range prog.Instructions {
		if inst.PaddingBytes > 0 {
			offset += uint32(inst.PaddingBytes)
			continue
		}

		upper, err := encode.EncodeUpper(inst.UpperMnemonic, inst.UpperFlags, inst.UpperDest, inst.UpperOperands)
		if err != nil {
			return fmt.Errorf("line %d: %w", inst.Line, err)
		}
		bt := &encode.BranchTarget{InstrOffset: offset, Labels: prog.Labels}
		lower, err := encode.EncodeLower(inst.LowerMnemonic, inst.LowerDest, inst.LowerOperands, bt)
		if err != nil {
			return fmt.Errorf("line %d: %w", inst.Line, err)
		}
		if inst.LowerMnemonic == "loi" {
			upper |= encode.FlagIBit
		}
		binary.LittleEndian.PutUint32(code[offset:], lower)
		binary.LittleEndian.PutUint32(code[offset+4:], upper)
		offset += 8
	}

	names := make([]string, 0, len(prog.Labels))
	for name := range prog.Labels {
		names = append(names, name)
	}
	sort.Strings(names)

	obj := &elfout.Object{Code: code, Align: prog.Align}
	for _, name := range names {
		obj.Symbols = append(obj.Symbols, elfout.Symbol{
			Name:   name,
			Offset: prog.Labels[name],
			Global: prog.Globals[name],
		})
	}

	return elfout.Write(w, obj)
}
