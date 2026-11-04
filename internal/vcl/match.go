package vcl

import (
	"strings"
)

// tryMatch matches args to one candidate form. On failure it returns the failing index and the box text.
func (p *parser) tryMatch(cd candidate, args []string, mask uint8, hasMask bool) (*instr, int, string) {
	f := cd.f
	ins := &instr{f: f, mask: mask, hasMask: hasMask, bcLane: cd.bcLane}
	if f.xyzOnly {
		if hasMask && mask != laneX|laneY|laneZ {
			return nil, 0, laneBox(laneX | laneY | laneZ)
		}
		ins.mask = laneX | laneY | laneZ
	} else if !hasMask {
		ins.mask = laneAll
	}
	// the destination field of a register operand supplies the mask when none was written
	if !hasMask {
		for i, spec := range f.args {
			if p.syntax && i > 0 {
				break
			}
			if !spec.dest || (spec.kind != kVF && spec.kind != kACC) {
				continue
			}
			if m, ok := p.operandFields(args[i], spec); ok {
				ins.mask = m
				ins.hasMask = true
				break
			}
		}
	}
	boxFor := func(op *operand) string {
		if op != nil && op.hasFld {
			return laneBox(op.fields)
		}
		if hasMask {
			return laneBox(mask)
		}
		return ""
	}
	for i, spec := range f.args {
		op, ok := p.matchArg(ins, spec, args[i])
		if !ok {
			var blame *operand
			if o2, ok2 := p.regName(args[i], spec.kind != kVI, true); ok2 {
				blame = o2
			}
			return nil, i, boxFor(blame)
		}
		op.spec = spec
		ins.args = append(ins.args, op)
	}
	return ins, 0, ""
}

// operandFields returns the field selection carried by a register operand.
func (p *parser) operandFields(text string, spec argSpec) (uint8, bool) {
	text = trimBlank(text)
	if spec.kind == kACC {
		if p.syntax {
			return 0, false
		}
		b, m, ok := splitOldField(text)
		if ok && strings.EqualFold(b, "acc") {
			return m, true
		}
		return 0, false
	}
	op, ok := p.regName(text, true, true)
	if !ok || !op.hasFld {
		return 0, false
	}
	return op.fields, true
}

func (p *parser) matchArg(ins *instr, spec argSpec, text string) (*operand, bool) {
	if len(spec.alts) > 0 {
		if op, ok := p.matchOne(ins, spec, text); ok {
			return op, true
		}
		return p.matchArg(ins, spec.alts[0], text)
	}
	return p.matchOne(ins, spec, text)
}

func (p *parser) matchOne(ins *instr, spec argSpec, text string) (*operand, bool) {
	text = trimBlank(text)
	if text == "" {
		return nil, false
	}
	switch spec.kind {
	case kVF:
		return p.matchVF(ins, spec, text)
	case kVI:
		op, ok := p.regName(text, false, false)
		if !ok {
			return nil, false
		}
		op.kind = kVI
		return op, true
	case kACC:
		if spec.dest {
			if m, ok := p.operandFields(text, spec); ok {
				if m != ins.mask {
					return nil, false
				}
				return &operand{kind: kACC, text: text, phys: -1, special: "acc", fields: m, hasFld: true}, true
			}
		}
		fallthrough
	case kQ, kP, kI, kR:
		low := strings.ToLower(text)
		name := map[argKind]string{kACC: "acc", kQ: "q", kP: "p", kI: "i", kR: "r"}[spec.kind]
		if low != name {
			return nil, false
		}
		return &operand{kind: spec.kind, text: text, phys: -1, special: name}, true
	case kImm:
		return p.matchImm(spec, text)
	case kMem:
		return p.matchMem(ins, spec, text)
	}
	return nil, false
}

func (p *parser) matchVF(ins *instr, spec argSpec, text string) (*operand, bool) {
	op, ok := p.regName(text, true, true)
	if !ok {
		return nil, false
	}
	op.kind = kVF
	switch {
	case spec.bc:
		if ins.bcLane != 0 {
			if op.hasFld && op.fields != ins.bcLane {
				return nil, false
			}
		} else if !op.hasFld || popcount(op.fields) != 1 {
			return nil, false
		}
	case spec.flag:
		if !op.hasFld || popcount(op.fields) != 1 {
			return nil, false
		}
	case spec.wcomp:
		if op.hasFld && popcount(op.fields) != 1 {
			return nil, false
		}
	case spec.dest:
		if op.hasFld && op.fields != ins.mask {
			return nil, false
		}
	default:
		if op.hasFld {
			return nil, false
		}
	}
	return op, true
}

func popcount(m uint8) int {
	n := 0
	for ; m != 0; m >>= 1 {
		n += int(m & 1)
	}
	return n
}

func (p *parser) matchImm(spec argSpec, text string) (*operand, bool) {
	op := &operand{kind: kImm, text: text, phys: -1}
	switch {
	case spec.branch:
		op.imm = text
	case spec.raw:
		op.imm = evalLoi(text)
	case spec.eval:
		s, ok, bad := evalImmediate(text)
		if bad || !ok {
			return nil, false
		}
		op.imm = s
	default:
		op.imm = text
	}
	return op, true
}

func (p *parser) matchMem(ins *instr, spec argSpec, text string) (*operand, bool) {
	op := &operand{kind: kMem, text: text, phys: -1}
	if spec.postinc || spec.predec {
		t := strings.ToLower(text)
		var reg string
		switch {
		case spec.postinc && strings.HasPrefix(t, "(") && strings.HasSuffix(t, "++)"):
			reg = text[1 : len(text)-3]
		case spec.predec && strings.HasPrefix(t, "(--") && strings.HasSuffix(t, ")"):
			reg = text[3 : len(text)-1]
		default:
			return nil, false
		}
		b, ok := p.regName(trimBlank(reg), false, false)
		if !ok {
			return nil, false
		}
		b.kind = kVI
		op.base = b
		op.postinc, op.predec = spec.postinc, spec.predec
		return op, true
	}
	// optional trailing field letters (ilw / isw style) are handled by the caller form
	open := strings.LastIndexByte(text, '(')
	if open < 0 || !strings.HasSuffix(text, ")") {
		return nil, false
	}
	inner := text[open+1 : len(text)-1]
	if strings.ContainsAny(inner, " \t") {
		return nil, false
	}
	b, ok := p.regName(inner, false, false)
	if !ok {
		return nil, false
	}
	b.kind = kVI
	op.base = b
	off := trimBlank(text[:open])
	if off == "" {
		if spec.zero || spec.eval {
			off = "0"
		}
	} else {
		r := evalExpr(off)
		switch {
		case r.parenFn:
			return nil, false
		case r.ok:
			off = itoa(int(truncInt32(r.value)))
		default:
			if !symbolLike(off) {
				return nil, false
			}
		}
	}
	op.offset = off
	return op, true
}

func symbolLike(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '.' || ch == '$') {
			return false
		}
	}
	return true
}
