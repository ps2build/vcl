// Package encode turns parsed VU upper/lower mnemonics into their 32-bit
// machine words.
package encode

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Upper-word field layout:
//
//	bits[31:27] flags (I=16 E=8 M=4 D=2 T=1, combinable)
//	bits[26:25] reserved, always 0
//	bits[24:21] dest write-mask (x=8 y=4 z=2 w=1), unused by D-less ops
//	bits[20:16] T register
//	bits[15:11] S register
//	bits[10:6]  D register, for 3-register ops (add/mul/opmsub/...)
//	bits[10:0]  11-bit opcode, for ops with no D register operand
//	bits[5:0]   6-bit opcode, for 3-register ops
//
// Lower-word field layout (T/S register shift shared with the upper word):
//
//	bits[31:25] 7-bit opcode
//	bits[24:21] dest write-mask, or the top 4 bits of a 15-bit immediate,
//	            or (for the FS/FT-lane pipeline: div/esin/...) a packed
//	            2-bit FT lane (bits[24:23]) + 2-bit FS lane (bits[22:21])
//	bits[20:16] T register
//	bits[15:11] S register
//	bits[10:6]  D register, for integer 3-register ops (iand/ior/...)
//	bits[10:0]  11-bit sub-opcode/immediate/signed branch offset, or the
//	            low 11 bits of a 15-bit immediate
//	bits[5:0]   6-bit sub-opcode, for integer 3-register ops
const (
	destX = 8
	destY = 4
	destZ = 2
	destW = 1
)

const (
	flagI = 16
	flagE = 8
	flagM = 4
	flagD = 2
	flagT = 1
)

// FlagIBit is the upper word's I-flag, already shifted into place - the
// bit assemble.go ORs onto an instruction's upper word when its lower op
// is "loi" (see EncodeLower's "loi" case).
const FlagIBit = flagI << 27

// laneValue maps a single x/y/z/w letter to its 2-bit field encoding, used
// both by dest write-masks (as an OR'd bit) and by FS/FT lane selectors (as
// a 0-3 index) - two different encodings sharing the same letter vocabulary.
func laneIndex(c byte) (uint32, error) {
	switch c | 0x20 { // lowercase
	case 'x':
		return 0, nil
	case 'y':
		return 1, nil
	case 'z':
		return 2, nil
	case 'w':
		return 3, nil
	default:
		return 0, fmt.Errorf("invalid lane character %q", c)
	}
}

// EncodeUpper encodes an upper-pipe instruction. flags is the raw contents
// of a "[...]" suffix on the mnemonic (e.g. "e" from "nop[e]"), dest is the
// raw contents of a ".xyzw"-style suffix, or "" for either.
func EncodeUpper(mnemonic, flags, dest string, operands []string) (uint32, error) {
	f, err := parseFlags(flags)
	if err != nil {
		return 0, err
	}
	base := f << 27

	switch mnemonic {
	case "nop":
		return base | 0x2ff, nil

	case "add", "addi", "addq", "adda", "addai", "addaq",
		"sub", "subi", "subq", "suba", "subai", "subaq",
		"mul", "muli", "mulq", "mula", "mulai", "mulaq",
		"madd", "maddi", "maddq", "madda", "maddai", "maddaq",
		"msub", "msubi", "msubq", "msuba", "msubai", "msubaq":
		return encodeArithFamily(mnemonic, base, dest, operands)

	case "opmsub":
		d, s, t, err := threeVFRegs(dest, operands)
		if err != nil {
			return 0, err
		}
		return base | d.dest<<21 | t.reg<<16 | s.reg<<11 | d.reg<<6 | 0x2e, nil

	case "opmula":
		if len(operands) != 3 {
			return 0, fmt.Errorf("opmula: expected 3 operands (ACC, fs, ft), got %d", len(operands))
		}
		d, err := parseDest(dest)
		if err != nil {
			return 0, err
		}
		s, err := parseVFReg(operands[1])
		if err != nil {
			return 0, err
		}
		t, err := parseVFReg(operands[2])
		if err != nil {
			return 0, err
		}
		return base | d<<21 | t<<16 | s<<11 | 0x2fe, nil

	case "clip", "clipw":
		if len(operands) != 2 {
			return 0, fmt.Errorf("clip: expected 2 operands, got %d", len(operands))
		}
		d, err := parseDest(dest)
		if err != nil {
			return 0, err
		}
		if d != destX|destY|destZ {
			return 0, fmt.Errorf("clip: only a .xyz destination is supported by real hardware")
		}
		s, err := parseVFRegDropLane(operands[0])
		if err != nil {
			return 0, err
		}
		t, err := parseVFRegDropLane(operands[1])
		if err != nil {
			return 0, err
		}
		return base | d<<21 | t<<16 | s<<11 | 0x1ff, nil

	case "max", "mini":
		d, s, t, err := threeVFRegs(dest, operands)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", mnemonic, err)
		}
		opcode := uint32(0x2b)
		if mnemonic == "mini" {
			opcode = 0x2f
		}
		return base | d.dest<<21 | t.reg<<16 | s.reg<<11 | d.reg<<6 | opcode, nil

	case "maxi", "minii":
		if len(operands) != 3 {
			return 0, fmt.Errorf("%s: expected 3 operands, got %d", mnemonic, len(operands))
		}
		want, opcode := "i", uint32(0x1d)
		if mnemonic == "minii" {
			opcode = 0x1f
		}
		if err := expectLiteral(operands[2], want); err != nil {
			return 0, fmt.Errorf("%s: %w", mnemonic, err)
		}
		d, err := parseDest(dest)
		if err != nil {
			return 0, err
		}
		dr, err := parseVFReg(operands[0])
		if err != nil {
			return 0, err
		}
		s, err := parseVFReg(operands[1])
		if err != nil {
			return 0, err
		}
		return base | d<<21 | s<<11 | dr<<6 | opcode, nil

	case "abs", "ftoi0", "ftoi4", "ftoi12", "ftoi15", "itof0", "itof4", "itof12", "itof15":
		if len(operands) != 2 {
			return 0, fmt.Errorf("%s: expected 2 operands, got %d", mnemonic, len(operands))
		}
		opcode := map[string]uint32{
			"abs":   0x1fd,
			"ftoi0": 0x17c, "ftoi4": 0x17d, "ftoi12": 0x17e, "ftoi15": 0x17f,
			"itof0": 0x13c, "itof4": 0x13d, "itof12": 0x13e, "itof15": 0x13f,
		}[mnemonic]
		d, err := parseDest(dest)
		if err != nil {
			return 0, err
		}
		t, err := parseVFReg(operands[0])
		if err != nil {
			return 0, err
		}
		s, err := parseVFReg(operands[1])
		if err != nil {
			return 0, err
		}
		return base | d<<21 | t<<16 | s<<11 | opcode, nil

	default:
		if v, ok, err := encodeBroadcastFamily(mnemonic, base, dest, operands); ok {
			return v, err
		}
		return 0, fmt.Errorf("unsupported upper mnemonic %q (not implemented yet)", mnemonic)
	}
}

// broadcastFamily gives one add/sub/madd/msub/max/mini/mul base mnemonic's
// opcode nibble (the VF-dest form's bits[5:2]) and, where real hardware has
// one, its ACC-accumulating form's 9-bit base value (bits[10:2]) - real VU
// hardware has no ACC-write broadcast form for max/mini.
type broadcastFamily struct {
	nibble  uint32
	hasAcc  bool
	accBase uint32
}

var broadcastFamilies = map[string]broadcastFamily{
	"add":  {nibble: 0x0, hasAcc: true, accBase: 0x0f},
	"sub":  {nibble: 0x1, hasAcc: true, accBase: 0x1f},
	"madd": {nibble: 0x2, hasAcc: true, accBase: 0x2f},
	"msub": {nibble: 0x3, hasAcc: true, accBase: 0x3f},
	"max":  {nibble: 0x4},
	"mini": {nibble: 0x5},
	"mul":  {nibble: 0x6, hasAcc: true, accBase: 0x6f},
}

func encodeBroadcastFamily(mnemonic string, base uint32, dest string, operands []string) (result uint32, ok bool, err error) {
	for name, fam := range broadcastFamilies {
		suffix, hasPrefix := strings.CutPrefix(mnemonic, name)
		if !hasPrefix || suffix == "" {
			continue
		}
		acc, letter := false, suffix
		if fam.hasAcc {
			if s, ok := strings.CutPrefix(suffix, "a"); ok && s != "" {
				acc, letter = true, s
			}
		}
		if len(letter) != 1 {
			continue
		}
		bc, err := laneIndex(letter[0])
		if err != nil {
			continue
		}

		if len(operands) != 3 {
			return 0, true, fmt.Errorf("%s: expected 3 operands, got %d", mnemonic, len(operands))
		}
		d, err := parseDest(dest)
		if err != nil {
			return 0, true, err
		}
		s, err := parseVFReg(operands[1])
		if err != nil {
			return 0, true, err
		}
		t, err := parseVFBCReg(operands[2], bc)
		if err != nil {
			return 0, true, err
		}
		if acc {
			return base | d<<21 | t<<16 | s<<11 | (fam.accBase<<2 | bc), true, nil
		}
		dr, err := parseVFReg(operands[0])
		if err != nil {
			return 0, true, err
		}
		return base | d<<21 | t<<16 | s<<11 | dr<<6 | (fam.nibble<<2 | bc), true, nil
	}
	return 0, false, nil
}

// expectLiteral checks that operand is the fixed placeholder token want
// (case-insensitive) - real syntax for subi/subq/subai/subaq's broadcast
// source operand, contributing no bits of its own.
func expectLiteral(operand, want string) error {
	if !strings.EqualFold(strings.TrimSpace(operand), want) {
		return fmt.Errorf("expected literal %q, got %q", want, operand)
	}
	return nil
}

// arithFamily gives the six opcodes shared by every {add,sub,mul,madd,msub}
// mnemonic family: a plain 3-VF-register form, an "i"/"q" broadcast form
// (2 real VF registers plus the literal placeholder token), and an
// ACC-accumulating version of each of those three.
type arithFamily struct {
	base, immI, immQ, acc, accI, accQ uint32
}

var arithFamilies = map[string]arithFamily{
	"add":  {0x28, 0x22, 0x20, 0x2bc, 0x23e, 0x23c},
	"sub":  {0x2c, 0x26, 0x24, 0x2fc, 0x27e, 0x27c},
	"mul":  {0x2a, 0x1e, 0x1c, 0x2be, 0x1fe, 0x1fc},
	"madd": {0x29, 0x23, 0x21, 0x2bd, 0x23f, 0x23d},
	"msub": {0x2d, 0x27, 0x25, 0x2fd, 0x27f, 0x27d},
}

// encodeArithFamily handles every add/sub/mul/madd/msub mnemonic variant:
// the base name is the plain 3-VF-register form; "i"/"q" suffixes select
// the broadcast form (real syntax's third operand becomes the literal
// placeholder token "i"/"q" instead of a register); an "a" before that
// selects the ACC-accumulating form (whose destination is the implicit ACC
// register, so its first operand is the literal placeholder token "ACC").
func encodeArithFamily(mnemonic string, base uint32, dest string, operands []string) (uint32, error) {
	for name, fam := range arithFamilies {
		suffix, ok := strings.CutPrefix(mnemonic, name)
		if !ok {
			continue
		}
		switch suffix {
		case "":
			d, s, t, err := threeVFRegs(dest, operands)
			if err != nil {
				return 0, fmt.Errorf("%s: %w", mnemonic, err)
			}
			return base | d.dest<<21 | t.reg<<16 | s.reg<<11 | d.reg<<6 | fam.base, nil

		case "i", "q":
			opcode, want := fam.immI, "i"
			if suffix == "q" {
				opcode, want = fam.immQ, "q"
			}
			if len(operands) != 3 {
				return 0, fmt.Errorf("%s: expected 3 operands, got %d", mnemonic, len(operands))
			}
			if err := expectLiteral(operands[2], want); err != nil {
				return 0, fmt.Errorf("%s: %w", mnemonic, err)
			}
			d, err := parseDest(dest)
			if err != nil {
				return 0, err
			}
			dr, err := parseVFReg(operands[0])
			if err != nil {
				return 0, err
			}
			s, err := parseVFReg(operands[1])
			if err != nil {
				return 0, err
			}
			return base | d<<21 | s<<11 | dr<<6 | opcode, nil

		case "a":
			if len(operands) != 3 {
				return 0, fmt.Errorf("%s: expected 3 operands (ACC, fs, ft), got %d", mnemonic, len(operands))
			}
			d, err := parseDest(dest)
			if err != nil {
				return 0, err
			}
			s, err := parseVFReg(operands[1])
			if err != nil {
				return 0, err
			}
			t, err := parseVFReg(operands[2])
			if err != nil {
				return 0, err
			}
			return base | d<<21 | t<<16 | s<<11 | fam.acc, nil

		case "ai", "aq":
			opcode, want := fam.accI, "i"
			if suffix == "aq" {
				opcode, want = fam.accQ, "q"
			}
			if len(operands) != 3 {
				return 0, fmt.Errorf("%s: expected 3 operands (ACC, fs, i/q), got %d", mnemonic, len(operands))
			}
			if err := expectLiteral(operands[2], want); err != nil {
				return 0, fmt.Errorf("%s: %w", mnemonic, err)
			}
			d, err := parseDest(dest)
			if err != nil {
				return 0, err
			}
			s, err := parseVFReg(operands[1])
			if err != nil {
				return 0, err
			}
			return base | d<<21 | s<<11 | opcode, nil
		}
	}
	return 0, fmt.Errorf("unsupported arithmetic mnemonic %q", mnemonic)
}

type vfOperand struct {
	reg  uint32
	dest uint32 // only meaningful for the first (destination) operand
}

// threeVFRegs parses the common "Vd, Vs, Vt" three-VF-register upper-op
// shape (add/mul/opmsub), returning the destination's own dest write-mask
// alongside all three register numbers.
func threeVFRegs(dest string, operands []string) (d, s, t vfOperand, err error) {
	if len(operands) != 3 {
		return d, s, t, fmt.Errorf("expected 3 operands, got %d", len(operands))
	}
	dm, err := parseDest(dest)
	if err != nil {
		return d, s, t, err
	}
	dr, err := parseVFReg(operands[0])
	if err != nil {
		return d, s, t, err
	}
	sr, err := parseVFReg(operands[1])
	if err != nil {
		return d, s, t, err
	}
	tr, err := parseVFReg(operands[2])
	if err != nil {
		return d, s, t, err
	}
	return vfOperand{reg: dr, dest: dm}, vfOperand{reg: sr}, vfOperand{reg: tr}, nil
}

// BranchTarget resolves a label to a signed PC-relative instruction offset,
// the way EncodeLower needs it for "b" and friends.
type BranchTarget struct {
	// InstrOffset is this instruction's own byte offset within .vutext.
	InstrOffset uint32
	// Labels maps label name to byte offset within .vutext.
	Labels map[string]uint32
}

// EncodeLower encodes a lower-pipe instruction. dest is the raw contents of
// a ".xyzw"-style suffix on the mnemonic, or "". bt is only consulted by
// branch mnemonics (nil is fine otherwise).
func EncodeLower(mnemonic, dest string, operands []string, bt *BranchTarget) (uint32, error) {
	switch mnemonic {
	case "nop":
		return 0x8000033c, nil
	case "waitq":
		return 0x800003bf, nil
	case "waitp":
		return 0x800007bf, nil

	case "loi":
		if len(operands) != 1 {
			return 0, fmt.Errorf("loi: expected 1 operand, got %d", len(operands))
		}
		text := strings.TrimSpace(operands[0])
		if strings.HasPrefix(text, "0x") || strings.HasPrefix(text, "0X") {
			bits, err := strconv.ParseUint(text[2:], 16, 32)
			if err != nil {
				return 0, fmt.Errorf("loi: invalid hex literal %q: %w", operands[0], err)
			}
			return uint32(bits), nil
		}
		f, err := strconv.ParseFloat(text, 32)
		if err != nil {
			return 0, fmt.Errorf("loi: invalid float literal %q: %w", operands[0], err)
		}
		return math.Float32bits(float32(f)), nil

	case "iaddiu":
		if len(operands) != 3 {
			return 0, fmt.Errorf("iaddiu: expected 3 operands, got %d", len(operands))
		}
		it, err := parseIReg(operands[0])
		if err != nil {
			return 0, err
		}
		is, err := parseIReg(operands[1])
		if err != nil {
			return 0, err
		}
		imm, err := parseImmediate(operands[2])
		if err != nil {
			return 0, err
		}
		imm &= 0x7fff
		return (0x08 << 25) | (it << 16) | (is << 11) | ((imm >> 11) << 21) | (imm & 0x7ff), nil

	case "iswr":
		if len(operands) != 2 {
			return 0, fmt.Errorf("iswr: expected 2 operands, got %d", len(operands))
		}
		it, err := parseIReg(operands[0])
		if err != nil {
			return 0, err
		}
		is, err := parseIRegParen(operands[1])
		if err != nil {
			return 0, err
		}
		d, err := parseDest(dest)
		if err != nil {
			return 0, err
		}
		return (0x40 << 25) | (d << 21) | (it << 16) | (is << 11) | 0x3ff, nil

	case "lq":
		if len(operands) != 2 {
			return 0, fmt.Errorf("lq: expected 2 operands, got %d", len(operands))
		}
		d, err := parseDest(dest)
		if err != nil {
			return 0, err
		}
		t, err := parseVFReg(operands[0])
		if err != nil {
			return 0, err
		}
		imm, s, err := parseOffsetIReg(operands[1])
		if err != nil {
			return 0, err
		}
		return (d << 21) | (t << 16) | (s << 11) | imm, nil

	case "sq":
		if len(operands) != 2 {
			return 0, fmt.Errorf("sq: expected 2 operands, got %d", len(operands))
		}
		d, err := parseDest(dest)
		if err != nil {
			return 0, err
		}
		s, err := parseVFReg(operands[0])
		if err != nil {
			return 0, err
		}
		imm, t, err := parseOffsetIReg(operands[1])
		if err != nil {
			return 0, err
		}
		return (0x01 << 25) | (d << 21) | (t << 16) | (s << 11) | imm, nil

	case "xtop":
		if len(operands) != 1 {
			return 0, fmt.Errorf("xtop: expected 1 operand, got %d", len(operands))
		}
		t, err := parseIReg(operands[0])
		if err != nil {
			return 0, err
		}
		return (0x40 << 25) | (t << 16) | 0x6bc, nil

	case "fmand":
		t, s, err := twoIRegs(operands)
		if err != nil {
			return 0, fmt.Errorf("fmand: %w", err)
		}
		return (0x1a << 25) | (t << 16) | (s << 11), nil

	case "fcand":
		if len(operands) != 2 {
			return 0, fmt.Errorf("fcand: expected 2 operands, got %d", len(operands))
		}
		imm, err := parseImmediate(operands[1])
		if err != nil {
			return 0, err
		}
		return (0x12 << 25) | (imm & 0xffffff), nil

	case "iand", "ior":
		var sub uint32 = 0x34
		if mnemonic == "ior" {
			sub = 0x35
		}
		if len(operands) != 3 {
			return 0, fmt.Errorf("%s: expected 3 operands, got %d", mnemonic, len(operands))
		}
		d, err := parseIReg(operands[0])
		if err != nil {
			return 0, err
		}
		s, err := parseIReg(operands[1])
		if err != nil {
			return 0, err
		}
		t, err := parseIReg(operands[2])
		if err != nil {
			return 0, err
		}
		return (0x40 << 25) | (t << 16) | (s << 11) | (d << 6) | sub, nil

	case "mfir":
		if len(operands) != 2 {
			return 0, fmt.Errorf("mfir: expected 2 operands, got %d", len(operands))
		}
		d, err := parseDest(dest)
		if err != nil {
			return 0, err
		}
		t, err := parseVFReg(operands[0])
		if err != nil {
			return 0, err
		}
		s, err := parseIReg(operands[1])
		if err != nil {
			return 0, err
		}
		return (0x40 << 25) | (d << 21) | (t << 16) | (s << 11) | 0x3fd, nil

	case "div", "rsqrt":
		if len(operands) != 3 {
			return 0, fmt.Errorf("%s: expected 3 operands (Q, fs, ft), got %d", mnemonic, len(operands))
		}
		sub := uint32(0x3bc)
		if mnemonic == "rsqrt" {
			sub = 0x3be
		}
		fs, err := parseVFLaneReg(operands[1])
		if err != nil {
			return 0, err
		}
		ft, err := parseVFLaneReg(operands[2])
		if err != nil {
			return 0, err
		}
		return (0x40 << 25) | fs.ffstreg(true) | ft.ffstreg(false) | sub, nil

	case "esin", "eatan", "eexp", "ercpr", "ersqrt", "esqrt":
		if len(operands) != 2 {
			return 0, fmt.Errorf("%s: expected 2 operands (P, fs), got %d", mnemonic, len(operands))
		}
		sub, ok := map[string]uint32{
			"eatan": 0x7fd, "eexp": 0x7fe, "ercpr": 0x7be,
			"ersqrt": 0x7bd, "esin": 0x7fc, "esqrt": 0x7bc,
		}[mnemonic]
		if !ok {
			return 0, fmt.Errorf("unsupported P-pipeline mnemonic %q", mnemonic)
		}
		fs, err := parseVFLaneReg(operands[1])
		if err != nil {
			return 0, err
		}
		return (0x40 << 25) | fs.ffstreg(true) | sub, nil

	case "b":
		if len(operands) != 1 {
			return 0, fmt.Errorf("b: expected 1 operand, got %d", len(operands))
		}
		off, err := branchOffset(bt, operands[0])
		if err != nil {
			return 0, fmt.Errorf("b: %w", err)
		}
		return (0x20 << 25) | off, nil

	case "ibeq", "ibne":
		if len(operands) != 3 {
			return 0, fmt.Errorf("%s: expected 3 operands, got %d", mnemonic, len(operands))
		}
		op := uint32(0x28)
		if mnemonic == "ibne" {
			op = 0x29
		}
		t, err := parseIReg(operands[0])
		if err != nil {
			return 0, err
		}
		s, err := parseIReg(operands[1])
		if err != nil {
			return 0, err
		}
		off, err := branchOffset(bt, operands[2])
		if err != nil {
			return 0, fmt.Errorf("%s: %w", mnemonic, err)
		}
		return (op << 25) | (t << 16) | (s << 11) | off, nil

	case "ibltz", "ibgez", "ibgtz", "iblez":
		if len(operands) != 2 {
			return 0, fmt.Errorf("%s: expected 2 operands, got %d", mnemonic, len(operands))
		}
		op := map[string]uint32{"ibltz": 0x2c, "ibgtz": 0x2d, "iblez": 0x2e, "ibgez": 0x2f}[mnemonic]
		s, err := parseIReg(operands[0])
		if err != nil {
			return 0, err
		}
		off, err := branchOffset(bt, operands[1])
		if err != nil {
			return 0, fmt.Errorf("%s: %w", mnemonic, err)
		}
		return (op << 25) | (s << 11) | off, nil

	case "jr":
		if len(operands) != 1 {
			return 0, fmt.Errorf("jr: expected 1 operand, got %d", len(operands))
		}
		s, err := parseIReg(operands[0])
		if err != nil {
			return 0, err
		}
		return (0x24 << 25) | (s << 11), nil

	case "jalr":
		if len(operands) != 2 {
			return 0, fmt.Errorf("jalr: expected 2 operands, got %d", len(operands))
		}
		t, err := parseIReg(operands[0])
		if err != nil {
			return 0, err
		}
		s, err := parseIReg(operands[1])
		if err != nil {
			return 0, err
		}
		return (0x25 << 25) | (t << 16) | (s << 11), nil

	case "iaddi":
		if len(operands) != 3 {
			return 0, fmt.Errorf("iaddi: expected 3 operands, got %d", len(operands))
		}
		t, err := parseIReg(operands[0])
		if err != nil {
			return 0, err
		}
		s, err := parseIReg(operands[1])
		if err != nil {
			return 0, err
		}
		imm, err := parseImmediate(operands[2])
		if err != nil {
			return 0, err
		}
		imm &= 0x1f
		return (0x40 << 25) | (t << 16) | (s << 11) | (imm << 6) | 0x32, nil

	case "isubiu":
		if len(operands) != 3 {
			return 0, fmt.Errorf("isubiu: expected 3 operands, got %d", len(operands))
		}
		t, err := parseIReg(operands[0])
		if err != nil {
			return 0, err
		}
		s, err := parseIReg(operands[1])
		if err != nil {
			return 0, err
		}
		imm, err := parseImmediate(operands[2])
		if err != nil {
			return 0, err
		}
		imm &= 0x7fff
		return (0x09 << 25) | (t << 16) | (s << 11) | ((imm >> 11) << 21) | (imm & 0x7ff), nil

	case "ilw", "isw":
		if len(operands) != 2 {
			return 0, fmt.Errorf("%s: expected 2 operands, got %d", mnemonic, len(operands))
		}
		op := uint32(0x04)
		if mnemonic == "isw" {
			op = 0x05
		}
		d, err := parseDest(dest)
		if err != nil {
			return 0, err
		}
		t, err := parseIReg(operands[0])
		if err != nil {
			return 0, err
		}
		imm, s, err := parseOffsetIReg(operands[1])
		if err != nil {
			return 0, err
		}
		return (op << 25) | (d << 21) | (t << 16) | (s << 11) | imm, nil

	case "lqi", "lqd":
		if len(operands) != 2 {
			return 0, fmt.Errorf("%s: expected 2 operands, got %d", mnemonic, len(operands))
		}
		sub := uint32(0x37c)
		if mnemonic == "lqd" {
			sub = 0x37e
		}
		d, err := parseDest(dest)
		if err != nil {
			return 0, err
		}
		t, err := parseVFReg(operands[0])
		if err != nil {
			return 0, err
		}
		s, err := parseIncDecReg(operands[1])
		if err != nil {
			return 0, err
		}
		return (0x40 << 25) | (d << 21) | (t << 16) | (s << 11) | sub, nil

	case "sqi", "sqd":
		if len(operands) != 2 {
			return 0, fmt.Errorf("%s: expected 2 operands, got %d", mnemonic, len(operands))
		}
		sub := uint32(0x37d)
		if mnemonic == "sqd" {
			sub = 0x37f
		}
		d, err := parseDest(dest)
		if err != nil {
			return 0, err
		}
		s, err := parseVFReg(operands[0])
		if err != nil {
			return 0, err
		}
		t, err := parseIncDecReg(operands[1])
		if err != nil {
			return 0, err
		}
		return (0x40 << 25) | (d << 21) | (t << 16) | (s << 11) | sub, nil

	case "mtir":
		if len(operands) != 2 {
			return 0, fmt.Errorf("mtir: expected 2 operands, got %d", len(operands))
		}
		t, err := parseIReg(operands[0])
		if err != nil {
			return 0, err
		}
		fs, err := parseVFLaneReg(operands[1])
		if err != nil {
			return 0, err
		}
		return (0x40 << 25) | (t << 16) | fs.ffstreg(true) | 0x3fc, nil

	case "move", "mr32":
		if len(operands) != 2 {
			return 0, fmt.Errorf("%s: expected 2 operands, got %d", mnemonic, len(operands))
		}
		sub := uint32(0x33c)
		if mnemonic == "mr32" {
			sub = 0x33d
		}
		d, err := parseDest(dest)
		if err != nil {
			return 0, err
		}
		t, err := parseVFReg(operands[0])
		if err != nil {
			return 0, err
		}
		s, err := parseVFReg(operands[1])
		if err != nil {
			return 0, err
		}
		return (0x40 << 25) | (d << 21) | (t << 16) | (s << 11) | sub, nil

	case "eatanxy", "eatanxz", "eleng", "erleng", "ersadd", "esadd", "esum":
		// Real syntax always writes "P" as the first operand (a fixed
		// placeholder, like div/esin's "Q") - these don't have a dest
		// suffix; their write-mask is fixed per mnemonic instead.
		if len(operands) != 2 {
			return 0, fmt.Errorf("%s: expected 2 operands (P, fs), got %d", mnemonic, len(operands))
		}
		if err := expectLiteral(operands[0], "p"); err != nil {
			return 0, fmt.Errorf("%s: %w", mnemonic, err)
		}
		ds := map[string][2]uint32{
			"eatanxy": {0xc, 0x77c}, "eatanxz": {0xa, 0x77d},
			"eleng": {0xe, 0x73e}, "erleng": {0xe, 0x73f},
			"ersadd": {0xe, 0x73d}, "esadd": {0xe, 0x73c}, "esum": {0xf, 0x77e},
		}[mnemonic]
		s, err := parseVFReg(operands[1])
		if err != nil {
			return 0, err
		}
		return (0x40 << 25) | (ds[0] << 21) | (s << 11) | ds[1], nil

	case "bal":
		if len(operands) != 2 {
			return 0, fmt.Errorf("bal: expected 2 operands, got %d", len(operands))
		}
		t, err := parseIReg(operands[0])
		if err != nil {
			return 0, err
		}
		off, err := branchOffset(bt, operands[1])
		if err != nil {
			return 0, fmt.Errorf("bal: %w", err)
		}
		return (0x21 << 25) | (t << 16) | off, nil

	case "fceq", "fcor":
		if len(operands) != 2 {
			return 0, fmt.Errorf("%s: expected 2 operands (VI01, imm), got %d", mnemonic, len(operands))
		}
		if err := expectLiteral(operands[0], "vi01"); err != nil {
			return 0, fmt.Errorf("%s: %w", mnemonic, err)
		}
		op := uint32(0x10)
		if mnemonic == "fcor" {
			op = 0x13
		}
		imm, err := parseImmediate(operands[1])
		if err != nil {
			return 0, err
		}
		return (op << 25) | (imm & 0xffffff), nil

	case "fcget":
		if len(operands) != 1 {
			return 0, fmt.Errorf("fcget: expected 1 operand, got %d", len(operands))
		}
		t, err := parseIReg(operands[0])
		if err != nil {
			return 0, err
		}
		return (0x1c << 25) | (t << 16), nil

	case "fcset":
		if len(operands) != 1 {
			return 0, fmt.Errorf("fcset: expected 1 operand, got %d", len(operands))
		}
		imm, err := parseImmediate(operands[0])
		if err != nil {
			return 0, err
		}
		return (0x11 << 25) | (imm & 0xffffff), nil

	case "fmeq", "fmor":
		if len(operands) != 2 {
			return 0, fmt.Errorf("%s: expected 2 operands, got %d", mnemonic, len(operands))
		}
		op := uint32(0x18)
		if mnemonic == "fmor" {
			op = 0x1b
		}
		t, s, err := twoIRegs(operands)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", mnemonic, err)
		}
		return (op << 25) | (t << 16) | (s << 11), nil

	case "iadd", "isub":
		if len(operands) != 3 {
			return 0, fmt.Errorf("%s: expected 3 operands, got %d", mnemonic, len(operands))
		}
		sub := uint32(0x30)
		if mnemonic == "isub" {
			sub = 0x31
		}
		d, err := parseIReg(operands[0])
		if err != nil {
			return 0, err
		}
		s, err := parseIReg(operands[1])
		if err != nil {
			return 0, err
		}
		t, err := parseIReg(operands[2])
		if err != nil {
			return 0, err
		}
		return (0x40 << 25) | (t << 16) | (s << 11) | (d << 6) | sub, nil

	case "ilwr":
		if len(operands) != 2 {
			return 0, fmt.Errorf("ilwr: expected 2 operands, got %d", len(operands))
		}
		d, err := parseDest(dest)
		if err != nil {
			return 0, err
		}
		t, err := parseIReg(operands[0])
		if err != nil {
			return 0, err
		}
		s, err := parseIRegParen(operands[1])
		if err != nil {
			return 0, err
		}
		return (0x40 << 25) | (d << 21) | (t << 16) | (s << 11) | 0x3fe, nil

	case "mfp", "rget", "rnext":
		if len(operands) != 2 {
			return 0, fmt.Errorf("%s: expected 2 operands, got %d", mnemonic, len(operands))
		}
		want, sub := "p", uint32(0x67c)
		switch mnemonic {
		case "rget":
			want, sub = "r", 0x43d
		case "rnext":
			want, sub = "r", 0x43c
		}
		if err := expectLiteral(operands[1], want); err != nil {
			return 0, fmt.Errorf("%s: %w", mnemonic, err)
		}
		d, err := parseDest(dest)
		if err != nil {
			return 0, err
		}
		t, err := parseVFReg(operands[0])
		if err != nil {
			return 0, err
		}
		return (0x40 << 25) | (d << 21) | (t << 16) | sub, nil

	case "rinit", "rxor":
		if len(operands) != 2 {
			return 0, fmt.Errorf("%s: expected 2 operands (R, fs), got %d", mnemonic, len(operands))
		}
		if err := expectLiteral(operands[0], "r"); err != nil {
			return 0, fmt.Errorf("%s: %w", mnemonic, err)
		}
		sub := uint32(0x43e)
		if mnemonic == "rxor" {
			sub = 0x43f
		}
		fs, err := parseVFLaneReg(operands[1])
		if err != nil {
			return 0, err
		}
		return (0x40 << 25) | fs.ffstreg(true) | sub, nil

	case "sqrt":
		if len(operands) != 2 {
			return 0, fmt.Errorf("sqrt: expected 2 operands (Q, ft), got %d", len(operands))
		}
		if err := expectLiteral(operands[0], "q"); err != nil {
			return 0, fmt.Errorf("sqrt: %w", err)
		}
		ft, err := parseVFLaneReg(operands[1])
		if err != nil {
			return 0, err
		}
		return (0x40 << 25) | ft.ffstreg(false) | 0x3bd, nil

	case "xgkick":
		if len(operands) != 1 {
			return 0, fmt.Errorf("xgkick: expected 1 operand, got %d", len(operands))
		}
		s, err := parseIReg(operands[0])
		if err != nil {
			return 0, err
		}
		return (0x40 << 25) | (s << 11) | 0x6fc, nil

	case "fsand", "fseq", "fsor":
		if len(operands) != 2 {
			return 0, fmt.Errorf("%s: expected 2 operands, got %d", mnemonic, len(operands))
		}
		op := map[string]uint32{"fsand": 0x16, "fseq": 0x14, "fsor": 0x17}[mnemonic]
		t, err := parseIReg(operands[0])
		if err != nil {
			return 0, err
		}
		imm, err := parseUint(operands[1], 12)
		if err != nil {
			return 0, err
		}
		return (op << 25) | (t << 16) | ((imm >> 11) << 21) | (imm & 0x7ff), nil

	case "fsset":
		if len(operands) != 1 {
			return 0, fmt.Errorf("fsset: expected 1 operand, got %d", len(operands))
		}
		imm, err := parseUint(operands[0], 12)
		if err != nil {
			return 0, err
		}
		return (0x15 << 25) | ((imm >> 11) << 21) | (imm & 0x7c0), nil

	case "xitop":
		if len(operands) != 1 {
			return 0, fmt.Errorf("xitop: expected 1 operand, got %d", len(operands))
		}
		t, err := parseIReg(operands[0])
		if err != nil {
			return 0, err
		}
		return (0x40 << 25) | (t << 16) | 0x6bd, nil

	default:
		return 0, fmt.Errorf("unsupported lower mnemonic %q (not implemented yet)", mnemonic)
	}
}

func branchOffset(bt *BranchTarget, label string) (uint32, error) {
	if bt == nil {
		return 0, fmt.Errorf("no branch-target context available")
	}
	targetOffset, ok := bt.Labels[label]
	if !ok {
		return 0, fmt.Errorf("undefined label %q", label)
	}
	off := int32(targetOffset)/8 - (int32(bt.InstrOffset)/8 + 1)
	if off < -1024 || off > 1023 {
		return 0, fmt.Errorf("branch target %q is out of the 11-bit signed range", label)
	}
	return uint32(off) & 0x7ff, nil
}

// parseIncDecReg parses lq/sq's auto-increment/decrement address operands:
// "(viN++)" or "(--viN)".
func parseIncDecReg(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, "++)") {
		return parseIReg(s[1 : len(s)-3])
	}
	if strings.HasPrefix(s, "(--") && strings.HasSuffix(s, ")") {
		return parseIReg(s[3 : len(s)-1])
	}
	return 0, fmt.Errorf("expected (viN++) or (--viN), got %q", s)
}

type laneReg struct {
	reg  uint32
	lane uint32
}

func (r laneReg) ffstreg(isSource bool) uint32 {
	if isSource {
		return (r.lane&3)<<21 | (r.reg&0x1f)<<11
	}
	return (r.lane&3)<<23 | (r.reg&0x1f)<<16
}

func twoIRegs(operands []string) (t, s uint32, err error) {
	if len(operands) != 2 {
		return 0, 0, fmt.Errorf("expected 2 operands, got %d", len(operands))
	}
	t, err = parseIReg(operands[0])
	if err != nil {
		return 0, 0, err
	}
	s, err = parseIReg(operands[1])
	if err != nil {
		return 0, 0, err
	}
	return t, s, nil
}

// parseOffsetIReg parses a "N(viM)" memory operand, as used by lq/sq.
func parseOffsetIReg(s string) (imm, reg uint32, err error) {
	open := strings.IndexByte(s, '(')
	if open < 0 || !strings.HasSuffix(s, ")") {
		return 0, 0, fmt.Errorf("expected offset(viN), got %q", s)
	}
	imm, err = parseSigned(strings.TrimSpace(s[:open]), 11)
	if err != nil {
		return 0, 0, err
	}
	reg, err = parseIReg(s[open+1 : len(s)-1])
	if err != nil {
		return 0, 0, err
	}
	return imm, reg, nil
}

func parseFlags(s string) (uint32, error) {
	var v uint32
	for _, c := range strings.ToLower(s) {
		switch c {
		case 'i':
			v |= flagI
		case 'e':
			v |= flagE
		case 'm':
			v |= flagM
		case 'd':
			v |= flagD
		case 't':
			v |= flagT
		default:
			return 0, fmt.Errorf("invalid flag character %q", c)
		}
	}
	return v, nil
}

func parseDest(s string) (uint32, error) {
	if s == "" {
		return destX | destY | destZ | destW, nil
	}
	var v uint32
	for _, c := range strings.ToLower(s) {
		switch c {
		case 'x':
			v |= destX
		case 'y':
			v |= destY
		case 'z':
			v |= destZ
		case 'w':
			v |= destW
		default:
			return 0, fmt.Errorf("invalid dest character %q", c)
		}
	}
	return v, nil
}

func parseIReg(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	if len(s) < 3 || !strings.HasPrefix(strings.ToLower(s), "vi") {
		return 0, fmt.Errorf("expected an integer register (vi0-vi31), got %q", s)
	}
	n, err := strconv.Atoi(s[2:])
	if err != nil || n < 0 || n > 31 {
		return 0, fmt.Errorf("invalid integer register %q", s)
	}
	return uint32(n), nil
}

func parseIRegParen(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "(") || !strings.HasSuffix(s, ")") {
		return 0, fmt.Errorf("expected (viN), got %q", s)
	}
	return parseIReg(s[1 : len(s)-1])
}

// parseVFReg parses a plain "VF01"-style float register, no lane suffix.
func parseVFReg(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	if len(s) < 3 || !strings.HasPrefix(strings.ToLower(s), "vf") {
		return 0, fmt.Errorf("expected a VF register (vf0-vf31), got %q", s)
	}
	n, err := strconv.Atoi(s[2:])
	if err != nil || n < 0 || n > 31 {
		return 0, fmt.Errorf("invalid VF register %q", s)
	}
	return uint32(n), nil
}

// parseVFRegDropLane parses "VF08" or "VF08w" and returns just the
// register number, discarding any trailing lane letter.
func parseVFRegDropLane(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	if len(s) > 0 {
		switch s[len(s)-1] | 0x20 {
		case 'x', 'y', 'z', 'w':
			s = s[:len(s)-1]
		}
	}
	return parseVFReg(s)
}

func parseVFBCReg(s string, bc uint32) (uint32, error) {
	s = strings.TrimSpace(s)
	if len(s) > 0 {
		switch s[len(s)-1] | 0x20 {
		case 'x', 'y', 'z', 'w':
			lane, err := laneIndex(s[len(s)-1])
			if err != nil {
				return 0, err
			}
			if lane != bc {
				return 0, fmt.Errorf("register lane %q does not match the mnemonic's broadcast lane", s[len(s)-1:])
			}
			s = s[:len(s)-1]
		}
	}
	return parseVFReg(s)
}

// parseVFLaneReg parses "VF01w"-style register-with-required-lane operands
// (div/esin/rsqrt/... "FS"/"FT" operands).
func parseVFLaneReg(s string) (laneReg, error) {
	s = strings.TrimSpace(s)
	if len(s) < 4 {
		return laneReg{}, fmt.Errorf("expected a lane-suffixed VF register (e.g. vf01w), got %q", s)
	}
	lane, err := laneIndex(s[len(s)-1])
	if err != nil {
		return laneReg{}, err
	}
	reg, err := parseVFReg(s[:len(s)-1])
	if err != nil {
		return laneReg{}, err
	}
	return laneReg{reg: reg, lane: lane}, nil
}

func parseSigned(s string, bits int) (uint32, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 0, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid immediate %q: %w", s, err)
	}
	if n < -(1<<(bits-1)) || n >= 1<<bits {
		return 0, fmt.Errorf("immediate %d out of range for a %d-bit field", n, bits)
	}
	return uint32(n) & (1<<bits - 1), nil
}

func parseUint(s string, bits int) (uint32, error) {
	n, err := parseImmediate(s)
	if err != nil {
		return 0, err
	}
	max := uint32(1) << bits
	if n >= max {
		return 0, fmt.Errorf("immediate %d out of range for a %d-bit field", n, bits)
	}
	return n, nil
}

func parseImmediate(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	n, err := strconv.ParseInt(s, 0, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid immediate %q: %w", s, err)
	}
	return uint32(n), nil
}
