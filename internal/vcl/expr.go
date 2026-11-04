package vcl

import (
	"math"
	"strings"
)

// exprResult is the outcome of evaluating an arithmetic operand.
type exprResult struct {
	value    float64
	ok       bool
	parenFn  bool
	consumed int
}

type exprParser struct {
	s       string
	pos     int
	failed  bool
	parenFn bool
}

func (p *exprParser) skip() {
	for p.pos < len(p.s) && p.s[p.pos] == ' ' {
		p.pos++
	}
}

func (p *exprParser) peek() byte {
	p.skip()
	if p.pos < len(p.s) {
		return p.s[p.pos]
	}
	return 0
}

var mathFuncs = []struct {
	name  string
	arity int
}{
	{"atan2", 2}, {"log10", 1}, {"asin", 1}, {"acos", 1}, {"atan", 1}, {"sinh", 1}, {"cosh", 1},
	{"sqrt", 1}, {"abs", 1}, {"exp", 1}, {"sin", 1}, {"cos", 1}, {"tan", 1}, {"pow", 2}, {"log", 1}, {"pi", 0},
}

func callMath(name string, a []float64) float64 {
	switch name {
	case "abs":
		return math.Abs(a[0])
	case "exp":
		return math.Exp(a[0])
	case "asin":
		return math.Asin(a[0])
	case "sin":
		return math.Sin(a[0])
	case "sinh":
		return math.Sinh(a[0])
	case "acos":
		return math.Acos(a[0])
	case "cos":
		return math.Cos(a[0])
	case "cosh":
		return math.Cosh(a[0])
	case "atan":
		return math.Atan(a[0])
	case "atan2":
		return math.Atan2(a[0], a[1])
	case "tan":
		return math.Tan(a[0])
	case "pow":
		return math.Pow(a[0], a[1])
	case "log":
		return math.Log(a[0])
	case "log10":
		return math.Log10(a[0])
	case "sqrt":
		return math.Sqrt(a[0])
	case "pi":
		return math.Pi
	}
	return 0
}

func (p *exprParser) expr() float64 {
	v := p.term()
	for !p.failed {
		c := p.peek()
		if c == '+' {
			p.pos++
			v += p.term()
		} else if c == '-' {
			p.pos++
			v -= p.term()
		} else {
			break
		}
	}
	return v
}

func (p *exprParser) term() float64 {
	v := p.unary()
	for !p.failed {
		c := p.peek()
		switch c {
		case '*':
			p.pos++
			v *= p.unary()
		case '/':
			p.pos++
			v /= p.unary()
		case '%':
			p.pos++
			v = math.Mod(v, p.unary())
		default:
			return v
		}
	}
	return v
}

func (p *exprParser) unary() float64 {
	c := p.peek()
	if c == '+' || c == '-' {
		return p.signed()
	}
	if c >= 'a' && c <= 'z' {
		return p.function()
	}
	return p.primary()
}

func (p *exprParser) signed() float64 {
	c := p.peek()
	p.pos++
	var v float64
	n := p.peek()
	if n == '+' || n == '-' {
		v = p.signed()
	} else {
		v = p.primary()
	}
	if c == '-' {
		return -v
	}
	return v
}

func (p *exprParser) function() float64 {
	rest := p.s[p.pos:]
	for _, f := range mathFuncs {
		if !strings.HasPrefix(rest, f.name) {
			continue
		}
		p.pos += len(f.name)
		var args []float64
		if p.peek() == '(' {
			p.pos++
			p.parenFn = true
			if p.peek() != ')' {
				for {
					args = append(args, p.expr())
					if p.failed {
						return 0
					}
					if p.peek() == ',' {
						p.pos++
						continue
					}
					break
				}
			}
			if p.peek() != ')' {
				p.failed = true
				return 0
			}
			p.pos++
		} else {
			c := p.peek()
			if c != 0 && c != ')' {
				args = append(args, p.primary())
			}
		}
		if p.failed || len(args) != f.arity {
			p.failed = true
			return 0
		}
		return callMath(f.name, args)
	}
	p.failed = true
	return 0
}

func (p *exprParser) primary() float64 {
	c := p.peek()
	if c == '(' {
		p.pos++
		v := p.expr()
		if p.failed {
			return 0
		}
		if p.peek() != ')' {
			p.failed = true
			return 0
		}
		p.pos++
		return v
	}
	if (c >= '0' && c <= '9') || c == '.' {
		return p.number()
	}
	p.failed = true
	return 0
}

func (p *exprParser) number() float64 {
	start := p.pos
	dots := 0
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		if c >= '0' && c <= '9' {
			p.pos++
		} else if c == '.' {
			dots++
			if dots > 1 {
				p.failed = true
				return 0
			}
			p.pos++
		} else {
			break
		}
	}
	if p.pos < len(p.s) && (p.s[p.pos] == 'e' || p.s[p.pos] == 'E') {
		q := p.pos + 1
		if q < len(p.s) && (p.s[q] == '+' || p.s[q] == '-') {
			q++
		}
		d := q
		for d < len(p.s) && p.s[d] >= '0' && p.s[d] <= '9' {
			d++
		}
		if d == q {
			p.failed = true
			return 0
		}
		p.pos = d
	}
	return parseFloat(p.s[start:p.pos])
}

func evalExpr(s string) exprResult {
	p := &exprParser{s: s}
	v := p.expr()
	p.skip()
	if p.failed || p.pos != len(p.s) {
		return exprResult{ok: false, parenFn: p.parenFn}
	}
	return exprResult{value: v, ok: true, parenFn: p.parenFn}
}

func floatBits(v float64) uint32 {
	if math.IsNaN(v) {
		return 0xffc00000
	}
	return math.Float32bits(float32(v))
}

func truncInt32(v float64) int32 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v >= 2147483648 || v <= -2147483649 {
		return math.MinInt32
	}
	return int32(v)
}
