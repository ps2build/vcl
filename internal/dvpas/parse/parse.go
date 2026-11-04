// Package parse reads a .vsm source and produces the instruction stream for
// its .vu (VU microcode) sub-format. The .dmadata, .dmapackvif and .endgif
// sub-formats are not supported.
package parse

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Instruction is one paired upper+lower VU microcode instruction (one
// 64-bit VLIW word), or - when PaddingBytes is non-zero - a run of raw
// zero-padding bytes emitted by an ".align" directive instead.
type Instruction struct {
	PaddingBytes int

	UpperMnemonic string
	UpperFlags    string // raw contents of "[...]" after the mnemonic, "" if none
	UpperDest     string // raw contents after '.' on the upper mnemonic, "" if none
	UpperOperands []string

	LowerMnemonic string
	LowerDest     string // raw contents after '.' in e.g. "iswr.x", "" if none
	LowerOperands []string

	Line int
}

// Program is one parsed .vsm source.
type Program struct {
	Instructions []Instruction
	Labels       map[string]uint32 // symbol name -> byte offset within .vutext
	Globals      map[string]bool
	Align        uint32
}

var upperArity = map[string]int{
	"nop": 0, "opmsub": 3, "opmula": 3, "clipw": 2,
	"add": 3, "addi": 3, "addq": 3, "adda": 3, "addai": 3, "addaq": 3,
	"sub": 3, "subi": 3, "subq": 3, "suba": 3, "subai": 3, "subaq": 3,
	"mul": 3, "muli": 3, "mulq": 3, "mula": 3, "mulai": 3, "mulaq": 3,
	"madd": 3, "maddi": 3, "maddq": 3, "madda": 3, "maddai": 3, "maddaq": 3,
	"msub": 3, "msubi": 3, "msubq": 3, "msuba": 3, "msubai": 3, "msubaq": 3,
	"abs": 2, "ftoi0": 2, "ftoi4": 2, "ftoi12": 2, "ftoi15": 2,
	"itof0": 2, "itof4": 2, "itof12": 2, "itof15": 2,
	"max": 3, "maxi": 3, "mini": 3, "minii": 3,
}

func init() {
	for _, base := range []string{"add", "sub", "madd", "msub", "max", "mini", "mul"} {
		for _, letter := range []string{"x", "y", "z", "w"} {
			upperArity[base+letter] = 3
		}
	}
	for _, base := range []string{"add", "sub", "madd", "msub", "mul"} {
		for _, letter := range []string{"x", "y", "z", "w"} {
			upperArity[base+"a"+letter] = 3
		}
	}
}

var lowerArity = map[string]int{
	"nop": 0, "waitq": 0, "waitp": 0, "loi": 1,
	"iaddiu": 3, "iswr": 2, "lq": 2, "sq": 2, "xtop": 1,
	"fmand": 2, "fcand": 2, "iand": 3, "ior": 3, "mfir": 2,
	"div": 3, "rsqrt": 3,
	"esin": 2, "eatan": 2, "eexp": 2, "ercpr": 2, "ersqrt": 2, "esqrt": 2,
	"b":    1,
	"ibeq": 3, "ibne": 3, "ibltz": 2, "ibgez": 2, "ibgtz": 2, "iblez": 2,
	"jr": 1, "jalr": 2,
	"iaddi": 3, "isubiu": 3, "ilw": 2, "isw": 2,
	"lqi": 2, "lqd": 2, "sqi": 2, "sqd": 2,
	"mtir": 2, "move": 2, "mr32": 2,
	"eatanxy": 2, "eatanxz": 2, "eleng": 2, "erleng": 2, "ersadd": 2, "esadd": 2, "esum": 2,
	"bal": 2, "fceq": 2, "fcget": 1, "fcor": 2, "fcset": 1, "fmeq": 2, "fmor": 2,
	"iadd": 3, "isub": 3, "ilwr": 2,
	"mfp": 2, "rget": 2, "rinit": 2, "rnext": 2, "rxor": 2, "sqrt": 2, "xgkick": 1, "xitop": 1,
	"fsand": 2, "fseq": 2, "fsor": 2, "fsset": 1,
}

// Parse reads a .vu-mode .vsm source from r.
func Parse(r io.Reader) (*Program, error) {
	p := &Program{Labels: map[string]uint32{}, Globals: map[string]bool{}, Align: 8}
	sc := bufio.NewScanner(r)
	var offset uint32
	lineNo := 0
	sawVU := false

	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") {
			continue
		}
		if line == ".vu" {
			sawVU = true
			continue
		}
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == ".align" {
			n, err := strconv.Atoi(fields[1])
			if err != nil {
				return nil, fmt.Errorf("line %d: bad .align operand: %w", lineNo, err)
			}
			alignTo := uint32(1) << uint(n)
			if alignTo > p.Align {
				p.Align = alignTo
			}
			pad := (alignTo - (offset % alignTo)) % alignTo
			if pad > 0 {
				p.Instructions = append(p.Instructions, Instruction{PaddingBytes: int(pad), Line: lineNo})
				offset += pad
			}
			continue
		}
		if fields := strings.Fields(line); len(fields) == 2 && (fields[0] == ".global" || fields[0] == ".globl") {
			p.Globals[fields[1]] = true
			continue
		}
		if name, ok := strings.CutSuffix(line, ":"); ok && !strings.ContainsAny(name, " \t") {
			p.Labels[name] = offset
			continue
		}
		if !sawVU {
			return nil, fmt.Errorf("line %d: only .vu mode is implemented so far (missing a .vu directive before the first instruction)", lineNo)
		}
		inst, err := parseInstruction(line, lineNo)
		if err != nil {
			return nil, err
		}
		p.Instructions = append(p.Instructions, inst)
		offset += 8
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return p, nil
}

// tokenize splits a line into words on both whitespace and commas (a
// paren'd address operand like "0(vi00)" contains neither, so it always
// survives as one token).
func tokenize(line string) []string {
	var tokens []string
	for _, field := range strings.Fields(line) {
		for _, t := range strings.Split(field, ",") {
			if t != "" {
				tokens = append(tokens, t)
			}
		}
	}
	return tokens
}

func parseInstruction(line string, lineNo int) (Instruction, error) {
	tokens := tokenize(line)
	if len(tokens) < 2 {
		return Instruction{}, fmt.Errorf("line %d: expected an upper op and a lower op, got %q", lineNo, line)
	}

	upperMnemonic, upperFlags, upperDest := splitMnemonic(tokens[0])
	upperN, ok := upperArity[upperMnemonic]
	if !ok {
		return Instruction{}, fmt.Errorf("line %d: unknown upper mnemonic %q", lineNo, upperMnemonic)
	}
	rest := tokens[1:]
	if len(rest) < upperN {
		return Instruction{}, fmt.Errorf("line %d: %q expects %d operand(s), not enough tokens on the line", lineNo, upperMnemonic, upperN)
	}
	upperOperands, rest := rest[:upperN], rest[upperN:]

	if len(rest) < 1 {
		return Instruction{}, fmt.Errorf("line %d: missing a lower op after %q", lineNo, upperMnemonic)
	}
	lowerMnemonic, _, lowerDest := splitMnemonic(rest[0])
	lowerN, ok := lowerArity[lowerMnemonic]
	if !ok {
		return Instruction{}, fmt.Errorf("line %d: unknown lower mnemonic %q", lineNo, lowerMnemonic)
	}
	rest = rest[1:]
	if len(rest) != lowerN {
		return Instruction{}, fmt.Errorf("line %d: %q expects %d operand(s), got %d", lineNo, lowerMnemonic, lowerN, len(rest))
	}

	return Instruction{
		UpperMnemonic: upperMnemonic,
		UpperFlags:    upperFlags,
		UpperDest:     upperDest,
		UpperOperands: upperOperands,
		LowerMnemonic: lowerMnemonic,
		LowerDest:     lowerDest,
		LowerOperands: rest,
		Line:          lineNo,
	}, nil
}

// splitMnemonic splits one op token (e.g. "nop[e]" or "iswr.x") into its
// lowercased base mnemonic, optional "[...]" flags, and optional ".xyzw"
// dest suffix.
func splitMnemonic(tok string) (mnemonic, flags, dest string) {
	mnemonic = tok
	if i := strings.IndexByte(mnemonic, '['); i >= 0 && strings.HasSuffix(mnemonic, "]") {
		flags = mnemonic[i+1 : len(mnemonic)-1]
		mnemonic = mnemonic[:i]
	}
	if i := strings.IndexByte(mnemonic, '.'); i >= 0 {
		dest = mnemonic[i+1:]
		mnemonic = mnemonic[:i]
	}
	return strings.ToLower(mnemonic), flags, dest
}
