package preproc

import "strings"

type level struct {
	on       bool
	parentOn bool
	elseSeen bool
}

func (p *Processor) aif(operand string) {
	cond := p.condition(p.kwOperand(operand))
	p.levels = append(p.levels, level{on: cond, parentOn: true})
}

func (p *Processor) branch(name string) {
	if name == "aendi" {
		if len(p.levels) == 0 {
			p.fail("AENDI without AIF.")
			return
		}
		p.levels = p.levels[:len(p.levels)-1]
		return
	}
	if len(p.levels) == 0 {
		p.fail("AELSE without AIF.")
		p.levels = append(p.levels, level{parentOn: true, elseSeen: true})
		return
	}
	top := &p.levels[len(p.levels)-1]
	if top.elseSeen {
		p.fail("Multiple AELSEs in AIF.")
	}
	top.elseSeen = true
	if top.parentOn {
		top.on = !top.on
	}
}

func (p *Processor) condition(text string) bool {
	e := &exprParser{p: p, s: text}
	e.skip()
	if e.pos < len(text) && text[e.pos] == '"' {
		return p.stringCondition(e)
	}
	l := e.or()
	op, ok := e.operator()
	if !ok {
		return false
	}
	e.skip()
	if e.pos < len(text) && text[e.pos] == '"' {
		p.warn("String compared against expression.")
		return false
	}
	r := e.or()
	if !l.absolute() || !r.absolute() {
		p.fail("Conditional operator must have absolute operands.")
	}
	return compare(op, cmpInt(l.c, r.c))
}

func (e *exprParser) operator() (string, bool) {
	e.skip()
	end := min(e.pos+2, len(e.s))
	op := strings.ToLower(e.s[e.pos:end])
	e.pos = end
	switch op {
	case "eq", "ne", "lt", "le", "gt", "ge":
		return op, true
	}
	e.p.fail("Comparison operator must be one of EQ, NE, LT, LE, GT or GE.")
	return "", false
}

func (p *Processor) stringCondition(e *exprParser) bool {
	l := e.stringOperand()
	op, ok := e.operator()
	if !ok {
		return false
	}
	e.skip()
	r := ""
	if e.pos < len(e.s) && (e.s[e.pos] == '"' || e.s[e.pos] == '<') {
		r = e.stringOperand()
	}
	if op != "eq" && op != "ne" {
		p.fail("Comparison operator for strings must be EQ or NE")
		return false
	}
	return (l == r) == (op == "eq")
}

func (e *exprParser) stringOperand() string {
	var b strings.Builder
	for e.pos < len(e.s) {
		switch e.s[e.pos] {
		case '"':
			e.pos++
			for e.pos < len(e.s) {
				if e.s[e.pos] == '"' {
					if e.pos+1 < len(e.s) && e.s[e.pos+1] == '"' {
						b.WriteByte('"')
						e.pos += 2
						continue
					}
					e.pos++
					break
				}
				b.WriteByte(e.s[e.pos])
				e.pos++
			}
		case '<':
			e.pos++
			v := e.or()
			if !v.absolute() {
				e.p.fail("Character code in string must be absolute expression.")
			}
			if e.peek() == '>' {
				e.pos++
			} else {
				e.p.fail("Missing > for character code.")
			}
			b.WriteByte(byte(v.c))
		default:
			return b.String()
		}
	}
	return b.String()
}

func cmpInt(a, b int32) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func compare(op string, c int) bool {
	switch op {
	case "eq":
		return c == 0
	case "ne":
		return c != 0
	case "lt":
		return c < 0
	case "le":
		return c <= 0
	case "gt":
		return c > 0
	case "ge":
		return c >= 0
	}
	return false
}
