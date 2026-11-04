package preproc

import (
	"strconv"
	"strings"
)

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isIdent(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9'
}

func isBlank(c byte) bool {
	return c == ' ' || c == '\t'
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func isLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func floatLength(s string) (int, bool) {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	if i >= len(s) || s[i] != '.' {
		return 0, false
	}
	i++
	frac := i
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	if i == frac {
		return 0, false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		k := j
		for k < len(s) && isDigit(s[k]) {
			k++
		}
		if k > j {
			i = k
		}
	}
	return i, true
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func (p *Processor) operand(s string) string {
	return p.numberPass(p.symbolPass(s))
}

func (p *Processor) kwOperand(s string) string {
	if p.gasp {
		return p.gaspNumberPass(p.symbolPass(s))
	}
	return p.operand(s)
}

func (p *Processor) gaspNumberPass(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case isIdentStart(c):
			j := i + 1
			for j < len(s) && isIdent(s[j]) {
				j++
			}
			if j < len(s) && s[j] == '\'' {
				i = p.gaspBased(&b, s, i, j)
				continue
			}
			b.WriteString(s[i:j])
			i = j
		case isDigit(c):
			i = p.decimalNumber(&b, s, i)
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

func (p *Processor) gaspBased(b *strings.Builder, s string, start, quote int) int {
	letter := s[quote-1] | 0x20
	base := map[byte]uint32{'b': 2, 'q': 8, 'h': 16, 'd': 10}[letter]
	if quote-start != 1 || (base == 0 && letter != 'a') {
		p.fail("Illegal base character " + string(s[quote-1]) + ".")
		b.WriteString(s[start:quote] + "0")
		return quote + 1
	}
	i := quote + 1
	if letter == 'a' {
		if i < len(s) {
			b.WriteString(itoa(int(s[i])))
			i++
		}
		return i
	}
	var v uint32
	for i < len(s) && digitValue(s[i]) < base {
		v = v*base + digitValue(s[i])
		i++
	}
	b.WriteString(itoa32(int32(v)))
	return i
}

func (p *Processor) symbolPass(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == p.commentChar:
			b.WriteString(s[i:])
			return b.String()
		case c == '\\':
			i = p.symbolBackslash(&b, s, i)
		case c == '.':
			if n, ok := p.stringFunction(&b, s, i); ok {
				i = n
			} else {
				b.WriteByte(c)
				i++
			}
		case isIdentStart(c):
			j := i + 1
			for j < len(s) && isIdent(s[j]) {
				j++
			}
			if v, ok := p.symbols[s[i:j]]; ok {
				b.WriteString(v)
			} else {
				b.WriteString(s[i:j])
			}
			i = j
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

func (p *Processor) symbolBackslash(b *strings.Builder, s string, i int) int {
	if i+1 >= len(s) {
		b.WriteByte('\\')
		return i + 1
	}
	switch n := s[i+1]; {
	case n == '(':
		end := strings.IndexByte(s[i+2:], ')')
		if end < 0 {
			b.WriteString(s[i:])
			return len(s)
		}
		b.WriteString(s[i : i+2+end+1])
		return i + 2 + end + 1
	case n == '&' || n == '$':
		j := i + 2
		for j < len(s) && isIdent(s[j]) {
			j++
		}
		name := s[i+2 : j]
		if j < len(s) && s[j] == '\'' {
			j++
		}
		v, ok := p.vars[name]
		if !ok {
			if n == '&' {
				p.warn("Can't find preprocessor variable " + name + ".")
			} else {
				v = "0"
			}
		}
		b.WriteString(v)
		return j
	case isLetter(n) || n == '_' || n == '$':
		j := i + 1
		for j < len(s) && (isLetter(s[j]) || s[j] == '_' || s[j] == '$') {
			j++
		}
		if word := s[i+1 : j]; word == "expr" || word == "EXPR" {
			return p.backslashExpr(b, s, j)
		}
		b.WriteString(s[i:j])
		return j
	}
	b.WriteByte('\\')
	return i + 1
}

func (p *Processor) backslashExpr(b *strings.Builder, s string, i int) int {
	e := &exprParser{p: p, s: s, pos: i}
	e.skip()
	var v value
	if e.pos < len(s) && s[e.pos] == '(' {
		e.pos++
		v = e.or()
		if e.peek() == ')' {
			e.pos++
		}
	} else {
		v = e.or()
	}
	if v.float {
		b.WriteString(strconv.FormatFloat(v.f, 'f', 6, 64))
	}
	return e.pos
}

func (p *Processor) numberPass(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == p.commentChar:
			b.WriteString(s[i:])
			return b.String()
		case c == '\\' && i+1 < len(s) && s[i+1] == '(':
			end := strings.IndexByte(s[i+2:], ')')
			if end < 0 {
				b.WriteString(s[i+2:])
				return b.String()
			}
			b.WriteString(s[i+2 : i+2+end])
			i += 2 + end + 1
		case c == '0' && i+1 < len(s) && isBaseLetter(s[i+1]):
			i = p.prefixedNumber(&b, s, i)
		case isIdentStart(c):
			j := i + 1
			for j < len(s) && isIdent(s[j]) {
				j++
			}
			b.WriteString(s[i:j])
			i = j
		case isDigit(c) || c == '.':
			if n, ok := floatLength(s[i:]); ok {
				b.WriteString(s[i : i+n])
				i += n
				continue
			}
			if c == '.' {
				b.WriteByte(c)
				i++
				continue
			}
			i = p.decimalNumber(&b, s, i)
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(s) && s[j] != c {
				j++
			}
			b.WriteString(s[i:j])
			i = j
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

func isBaseLetter(c byte) bool {
	switch c | 0x20 {
	case 'b', 'q', 'h', 'd', 'a':
		return true
	}
	return false
}

func (p *Processor) prefixedNumber(b *strings.Builder, s string, i int) int {
	start := i + 2
	if start >= len(s) || isBlank(s[start]) || s[start] == ',' || s[start] == p.commentChar {
		b.WriteString(s[i:start])
		return start
	}
	letter := s[i+1] | 0x20
	if letter == 'a' {
		end := start
		for end < len(s) && !isBlank(s[end]) && s[end] != ',' && s[end] != ')' {
			end++
		}
		var v uint32
		for _, ch := range []byte(s[start:end]) {
			v = v<<8 + uint32(int32(int8(ch)))
		}
		b.WriteString(itoa32(int32(v)))
		return end
	}
	base := map[byte]uint32{'b': 2, 'q': 8, 'd': 10, 'h': 16}[letter]
	end := start
	var v uint32
	for end < len(s) {
		d := digitValue(s[end])
		if d >= base {
			break
		}
		v = v*base + d
		end++
	}
	b.WriteString(itoa32(int32(v)))
	return end
}

func digitValue(c byte) uint32 {
	switch {
	case c >= '0' && c <= '9':
		return uint32(c - '0')
	case c >= 'a' && c <= 'f':
		return uint32(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return uint32(c-'A') + 10
	}
	return 99
}

func (p *Processor) decimalNumber(b *strings.Builder, s string, i int) int {
	base := uint32(p.radix)
	var v uint32
	j := i
	for j < len(s) {
		d := digitValue(s[j])
		if d >= base {
			break
		}
		v = v*base + d
		j++
	}
	if j == i {
		b.WriteByte(s[i])
		return i + 1
	}
	b.WriteString(itoa32(int32(v)))
	for j < len(s) && isIdent(s[j]) {
		b.WriteByte(s[j])
		j++
	}
	return j
}
