package preproc

import "strings"

type value struct {
	c     int32
	plus  string
	minus string
	float bool
	f     float64
}

type exprParser struct {
	p   *Processor
	s   string
	pos int
}

func (v value) absolute() bool {
	return v.plus == "" && v.minus == ""
}

func (v value) text() string {
	var b strings.Builder
	b.WriteString(v.plus)
	if v.c != 0 {
		if b.Len() > 0 {
			b.WriteByte('+')
		}
		b.WriteString(itoa32(v.c))
	}
	if v.minus != "" {
		b.WriteString("-" + v.plus)
	}
	if b.Len() == 0 {
		return "0"
	}
	return b.String()
}

func (p *Processor) evalText(s string) (value, string) {
	e := &exprParser{p: p, s: s}
	v := e.or()
	e.skip()
	return v, e.s[e.pos:]
}

func (e *exprParser) skip() {
	for e.pos < len(e.s) && isBlank(e.s[e.pos]) {
		e.pos++
	}
}

func (e *exprParser) peek() byte {
	e.skip()
	if e.pos >= len(e.s) {
		return 0
	}
	return e.s[e.pos]
}

func (e *exprParser) or() value {
	v := e.and()
	for {
		c := e.peek()
		if c != '|' && c != '~' {
			return v
		}
		e.pos++
		v = e.absoluteOp(c, v, e.and())
	}
}

func (e *exprParser) and() value {
	v := e.sum()
	for e.peek() == '&' {
		e.pos++
		v = e.absoluteOp('&', v, e.sum())
	}
	return v
}

func (e *exprParser) sum() value {
	v := e.product()
	for {
		switch e.peek() {
		case '+':
			e.pos++
			v = e.add(v, e.product())
		case '-':
			e.pos++
			v = e.add(v, negate(e.product()))
		default:
			return v
		}
	}
}

func (e *exprParser) product() value {
	v := e.unary()
	for {
		switch e.peek() {
		case '*':
			e.pos++
			v = e.absoluteOp('*', v, e.unary())
		case '/':
			e.pos++
			v = e.absoluteOp('/', v, e.unary())
		default:
			return v
		}
	}
}

func (e *exprParser) unary() value {
	switch e.peek() {
	case '-':
		e.pos++
		return negate(e.unary())
	case '+':
		e.pos++
		return e.unary()
	case '~':
		e.pos++
		v := e.unary()
		if !v.absolute() {
			e.p.fail("the ~ operator cannot take non-absolute arguments.")
		}
		return value{c: ^v.c, plus: v.plus, minus: v.minus, float: v.float, f: v.f}
	case '(':
		e.pos++
		v := e.or()
		if e.peek() == ')' {
			e.pos++
		} else {
			e.p.fail("misplaced closing parens.")
		}
		return v
	}
	return e.primary()
}

func negate(v value) value {
	return value{c: -v.c, plus: v.minus, minus: v.plus, float: v.float, f: v.f}
}

func (e *exprParser) add(a, b value) value {
	r := value{c: a.c + b.c, plus: a.plus, minus: a.minus, float: a.float, f: a.f}
	if b.plus != "" {
		if a.plus != "" {
			e.p.fail("can't add two relocatable expressions")
		}
		r.plus = b.plus
	}
	if b.minus != "" {
		r.minus = b.minus
	}
	return r
}

func (e *exprParser) absoluteOp(op byte, a, b value) value {
	if !a.absolute() || !b.absolute() {
		e.p.fail("the " + string(op) + " operator cannot take non-absolute arguments.")
	}
	r := value{plus: a.plus, minus: a.minus, float: a.float, f: a.f}
	if r.plus == "" && r.minus == "" {
		r.plus, r.minus = b.plus, b.minus
	}
	switch op {
	case '*':
		r.c = a.c * b.c
	case '/':
		if b.c == 0 && b.absolute() {
			e.p.fail("attempt to divide by zero.")
			r.c = a.c
		} else if b.c == -1 {
			r.c = -a.c
		} else {
			r.c = a.c / b.c
		}
	case '&':
		r.c = a.c & b.c
	case '|':
		r.c = a.c | b.c
	case '~':
		r.c = a.c ^ b.c
	}
	return r
}

func (e *exprParser) primary() value {
	e.skip()
	if e.pos >= len(e.s) {
		e.p.fail("can't find primary in expression.")
		return value{}
	}
	c := e.s[e.pos]
	switch {
	case c == '"':
		e.pos++
		for e.pos < len(e.s) {
			if e.s[e.pos] == '"' {
				if e.pos+1 < len(e.s) && e.s[e.pos+1] == '"' {
					e.pos += 2
					continue
				}
				e.pos++
				break
			}
			e.pos++
		}
		e.p.fail("string where expression expected.")
		return value{}
	case isIdentStart(c):
		start := e.pos
		for e.pos < len(e.s) && isIdent(e.s[e.pos]) {
			e.pos++
		}
		return value{plus: e.s[start:e.pos]}
	case c >= '0' && c <= '9' || c == '.':
		if n, ok := floatLength(e.s[e.pos:]); ok {
			f := parseFloat(e.s[e.pos : e.pos+n])
			e.pos += n
			return value{float: true, f: f}
		}
		if c == '.' {
			break
		}
		var v uint32
		for e.pos < len(e.s) && e.s[e.pos] >= '0' && e.s[e.pos] <= '9' {
			v = v*10 + uint32(e.s[e.pos]-'0')
			e.pos++
		}
		return value{c: int32(v)}
	}
	e.p.fail("can't find primary in expression.")
	e.pos++
	return value{}
}
