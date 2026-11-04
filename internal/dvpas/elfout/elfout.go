// Package elfout writes the relocatable ELF32 MIPS N32/R5900 object produced
// by the VU assembler. It emits a .vutext PROGBITS section and a symbol table.
package elfout

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	etREL    = 1
	emMIPS   = 8
	eeFlags  = 0x20920024 // cpic, abi2, 5900, mips3 - matches the real EE toolchain's own objects
	ehSize   = 52
	shEntSz  = 40
	symEntSz = 16
)

const (
	shtNULL     = 0
	shtPROGBITS = 1
	shtSYMTAB   = 2
	shtSTRTAB   = 3
)

const (
	shfWRITE = 1
	shfALLOC = 2
	shfEXEC  = 4
)

const (
	stbLOCAL  = 0
	stbGLOBAL = 1
)

const (
	sttNOTYPE  = 0
	sttSECTION = 3
)

// Symbol is one global or local symbol defined within Code.
type Symbol struct {
	Name   string
	Offset uint32
	Global bool
}

// Object is the minimal relocatable-object model this writer supports.
type Object struct {
	Code    []byte // .vutext contents
	Symbols []Symbol
	// Align is .vutext's section alignment; 8 is used if left at 0.
	Align uint32
}

type strtab struct {
	buf    []byte
	offset map[string]uint32
}

func newStrtab() *strtab {
	return &strtab{buf: []byte{0}, offset: map[string]uint32{"": 0}}
}

func (s *strtab) add(name string) uint32 {
	if off, ok := s.offset[name]; ok {
		return off
	}
	off := uint32(len(s.buf))
	s.buf = append(s.buf, name...)
	s.buf = append(s.buf, 0)
	s.offset[name] = off
	return off
}

// Write emits obj as a real ELF32 little-endian relocatable object to w.
func Write(w io.Writer, obj *Object) error {
	shstr := newStrtab()
	nameVutext := shstr.add(".vutext")
	nameSymtab := shstr.add(".symtab")
	nameStrtab := shstr.add(".strtab")
	nameShstrtab := shstr.add(".shstrtab")

	str := newStrtab()

	vutextAlign := obj.Align
	if vutextAlign == 0 {
		vutextAlign = 8
	}

	// Symbol 0 (STN_UNDEF) and a local section symbol for .vutext always
	// come first; sh_info on .symtab must point one past the last local.
	type symEnt struct {
		nameOff uint32
		value   uint32
		info    byte
		shndx   uint16
	}
	syms := []symEnt{
		{0, 0, 0, 0},
		{0, 0, (stbLOCAL << 4) | sttSECTION, 1},
	}
	for _, s := range obj.Symbols {
		if s.Global {
			continue
		}
		syms = append(syms, symEnt{str.add(s.Name), s.Offset, (stbLOCAL << 4) | sttNOTYPE, 1})
	}
	firstGlobal := uint32(len(syms))
	for _, s := range obj.Symbols {
		if !s.Global {
			continue
		}
		syms = append(syms, symEnt{str.add(s.Name), s.Offset, (stbGLOBAL << 4) | sttNOTYPE, 1})
	}

	symtabBuf := make([]byte, 0, len(syms)*symEntSz)
	for _, s := range syms {
		var e [symEntSz]byte
		binary.LittleEndian.PutUint32(e[0:4], s.nameOff)
		binary.LittleEndian.PutUint32(e[4:8], s.value)
		binary.LittleEndian.PutUint32(e[8:12], 0) // st_size
		e[12] = s.info
		e[13] = 0 // st_other
		binary.LittleEndian.PutUint16(e[14:16], s.shndx)
		symtabBuf = append(symtabBuf, e[:]...)
	}

	type section struct {
		name         uint32
		typ, flags   uint32
		data         []byte
		link, info   uint32
		align, entsz uint32
	}
	sections := []section{
		{}, // SHN_UNDEF
		{name: nameVutext, typ: shtPROGBITS, flags: shfALLOC | shfEXEC, data: obj.Code, align: vutextAlign},
		{name: nameSymtab, typ: shtSYMTAB, data: symtabBuf, link: 3, info: firstGlobal, align: 4, entsz: symEntSz},
		{name: nameStrtab, typ: shtSTRTAB, data: str.buf, align: 1},
		{name: nameShstrtab, typ: shtSTRTAB, data: shstr.buf, align: 1},
	}

	offset := uint32(ehSize)
	offsets := make([]uint32, len(sections))
	for i, s := range sections {
		if s.typ == shtNULL {
			continue
		}
		if s.align > 1 {
			offset = alignUp(offset, s.align)
		}
		offsets[i] = offset
		offset += uint32(len(s.data))
	}
	shoff := alignUp(offset, 4)

	var out []byte
	emit := func(b []byte) { out = append(out, b...) }

	var eh [ehSize]byte
	copy(eh[0:4], []byte{0x7f, 'E', 'L', 'F'})
	eh[4] = 1 // ELFCLASS32
	eh[5] = 1 // ELFDATA2LSB
	eh[6] = 1 // EV_CURRENT
	binary.LittleEndian.PutUint16(eh[16:18], etREL)
	binary.LittleEndian.PutUint16(eh[18:20], emMIPS)
	binary.LittleEndian.PutUint32(eh[20:24], 1) // e_version
	binary.LittleEndian.PutUint32(eh[32:36], shoff)
	binary.LittleEndian.PutUint32(eh[36:40], eeFlags)
	binary.LittleEndian.PutUint16(eh[40:42], ehSize)
	binary.LittleEndian.PutUint16(eh[46:48], shEntSz)
	binary.LittleEndian.PutUint16(eh[48:50], uint16(len(sections)))
	binary.LittleEndian.PutUint16(eh[50:52], uint16(len(sections)-1)) // .shstrtab is always last
	emit(eh[:])

	for i, s := range sections {
		if s.typ == shtNULL {
			continue
		}
		for uint32(len(out)) < offsets[i] {
			out = append(out, 0)
		}
		emit(s.data)
	}
	for uint32(len(out)) < shoff {
		out = append(out, 0)
	}

	for i, s := range sections {
		var sh [shEntSz]byte
		binary.LittleEndian.PutUint32(sh[0:4], s.name)
		binary.LittleEndian.PutUint32(sh[4:8], s.typ)
		binary.LittleEndian.PutUint32(sh[8:12], s.flags)
		binary.LittleEndian.PutUint32(sh[16:20], offsets[i])
		binary.LittleEndian.PutUint32(sh[20:24], uint32(len(s.data)))
		binary.LittleEndian.PutUint32(sh[24:28], s.link)
		binary.LittleEndian.PutUint32(sh[28:32], s.info)
		binary.LittleEndian.PutUint32(sh[32:36], s.align)
		binary.LittleEndian.PutUint32(sh[36:40], s.entsz)
		emit(sh[:])
	}

	if _, err := w.Write(out); err != nil {
		return fmt.Errorf("writing ELF object: %w", err)
	}
	return nil
}

func alignUp(v, align uint32) uint32 {
	if align == 0 {
		return v
	}
	return (v + align - 1) &^ (align - 1)
}
