package preproc

import (
	"strconv"
	"strings"
)

var dataSizes = map[byte]int32{'b': 1, 'w': 2, 'l': 4}

var dataDirectives = map[string]string{"db": ".byte", "dw": ".short", "dl": ".long"}

func sizeDirective(size int32) string {
	switch size {
	case 1:
		return ".byte"
	case 2:
		return ".short"
	}
	return ".long"
}

func (p *Processor) dataDirective(name, label, operand string) {
	p.out.WriteString(p.labelPrefix(label))
	if _, ok := dataDirectives[name]; ok {
		p.emitData(dataDirectives[name], p.kwOperand(operand))
		return
	}
	size, operand := p.splitSize(name, operand)
	text := p.kwOperand(operand)
	switch name {
	case "data":
		p.emitData(sizeDirective(size), text)
	case "datab":
		p.emitDatab(size, text)
	case "res", "sres":
		p.emitRes(size, text, 0)
	case "sresc", "sresz":
		p.emitRes(size, text, 1)
	case "sdata":
		p.emitSdata(text, false, false)
	case "sdatac":
		p.emitSdata(text, true, false)
	case "sdataz":
		p.emitSdata(text, false, true)
	case "sdatab":
		p.emitSdatab(text)
	case "export", "global":
		p.out.WriteString(".global\t" + text + "\n")
	case "print":
		p.emitPrint(text)
	case "heading":
		p.out.WriteString(".title\t" + strings.TrimLeft(text, " \t") + "\n")
	case "page":
		p.out.WriteString(".eject\n")
	case "form":
		p.emitForm(text)
	case "align":
		p.emitAlign(text)
	}
}

func (p *Processor) splitSize(name, operand string) (int32, string) {
	switch name {
	case "data", "datab", "res", "sres", "sresc", "sresz":
	default:
		return 4, operand
	}
	if len(operand) < 2 || operand[0] != '.' || !isLetter(operand[1]) {
		return 4, operand
	}
	size, ok := dataSizes[operand[1]|0x20]
	if !ok {
		p.fail("size must be one of b, w or l, is " + string(operand[1]) + ".")
		size = 4
	}
	return size, operand[2:]
}

func (p *Processor) emitData(directive, text string) {
	var b strings.Builder
	e := &exprParser{p: p, s: text}
	for {
		e.skip()
		if e.pos >= len(text) {
			break
		}
		if text[e.pos] == p.commentChar {
			b.WriteString(text[e.pos:])
			break
		}
		v := e.or()
		b.WriteString(v.text())
		if e.peek() == ',' {
			e.pos++
			b.WriteByte(',')
		}
	}
	p.out.WriteString(directive + "\t" + b.String() + "\n")
}

func (p *Processor) emitDatab(size int32, text string) {
	e := &exprParser{p: p, s: text}
	count := e.or()
	if !count.absolute() {
		p.fail("datab repeat must be constant.")
		count = value{}
	}
	if e.peek() == ',' {
		e.pos++
	}
	fill := e.or()
	if !fill.absolute() {
		p.fail("datab data must be absolute.")
		fill = value{}
	}
	p.out.WriteString(".fill\t" + itoa32(count.c) + "," + itoa32(size) + "," + itoa32(fill.c) + "\n")
}

func (p *Processor) emitRes(size int32, text string, extra int32) {
	e := &exprParser{p: p, s: text}
	for {
		v := e.or()
		if !v.absolute() {
			p.fail("res needs absolute expression for fill count.")
			v = value{}
		}
		p.out.WriteString(".space\t" + itoa32((v.c+extra)*size) + "\n")
		if e.peek() != ',' {
			return
		}
		e.pos++
	}
}

func (p *Processor) stringItems(text string) []byte {
	var codes []byte
	i := 0
	for i < len(text) {
		c := text[i]
		switch {
		case isBlank(c) || c == ',':
			i++
		case c == p.commentChar:
			return codes
		case c == '"':
			i++
			for i < len(text) {
				if text[i] == '"' {
					if i+1 < len(text) && text[i+1] == '"' {
						codes = append(codes, '"')
						i += 2
						continue
					}
					i++
					break
				}
				codes = append(codes, text[i])
				i++
			}
		default:
			for i < len(text) && !isBlank(text[i]) && text[i] != ',' && text[i] != p.commentChar && text[i] != '"' {
				codes = append(codes, text[i])
				i++
			}
		}
	}
	return codes
}

func joinCodes(codes []int) string {
	parts := make([]string, len(codes))
	for i, c := range codes {
		parts[i] = strconv.Itoa(c)
	}
	return strings.Join(parts, ",")
}

func (p *Processor) emitSdata(text string, withLength, withZero bool) {
	var codes []int
	items := p.stringItems(text)
	if withLength {
		if len(items) > 255 {
			p.fail("string for SDATAC longer than 255 characters (" + strconv.Itoa(len(items)) + ").")
		}
		codes = append(codes, len(items))
	}
	for _, c := range items {
		codes = append(codes, int(c))
	}
	if withZero {
		codes = append(codes, 0)
	}
	p.out.WriteString(".byte\t" + joinCodes(codes) + "\n")
}

func (p *Processor) emitSdatab(text string) {
	e := &exprParser{p: p, s: text}
	count := e.or()
	if !count.absolute() {
		p.fail("Must have absolute SDATAB repeat count.")
		count = value{c: 1}
	} else if count.c <= 0 {
		p.fail("Must have positive SDATAB repeat count (" + itoa32(count.c) + ").")
		count = value{c: 1}
	}
	if e.peek() == ',' {
		e.pos++
	}
	var codes []int
	for _, c := range p.stringItems(text[e.pos:]) {
		codes = append(codes, int(c))
	}
	for i := int32(0); i < count.c; i++ {
		if i > 0 {
			p.out.WriteByte('\t')
		}
		p.out.WriteString(".byte\t" + joinCodes(codes) + "\n")
	}
}

func (p *Processor) emitPrint(text string) {
	for _, w := range strings.Fields(text) {
		switch w {
		case "list":
			p.out.WriteString(".list\n")
		case "nolist":
			p.out.WriteString(".nolist\n")
		}
	}
}

func (p *Processor) emitForm(text string) {
	lin, col := value{c: 60}, value{c: 132}
	for _, w := range strings.Fields(text) {
		key, expr, ok := strings.Cut(w, "=")
		if !ok {
			continue
		}
		v, _ := p.evalText(expr)
		switch strings.ToLower(key) {
		case "lin":
			if !v.absolute() {
				p.fail("form LIN= needs absolute expresssion.")
				v = value{}
			}
			lin = v
		case "col":
			if !v.absolute() {
				p.fail("form COL= needs absolute expresssion.")
				v = value{}
			}
			col = v
		}
	}
	p.out.WriteString(".psize " + itoa32(lin.c) + "," + itoa32(col.c) + "\n")
}

func (p *Processor) emitAlign(text string) {
	e := &exprParser{p: p, s: text}
	n := e.or()
	if !n.absolute() {
		p.fail("align needs absolute expression.")
		n = value{}
	}
	out := ".align\t" + itoa32(n.c)
	if e.peek() == ',' {
		e.pos++
		f := e.or()
		if !f.absolute() {
			p.fail(".align needs absolute fill value.")
			f = value{}
		}
		out += "," + itoa32(f.c)
	}
	p.out.WriteString(out + "\n")
}

func (p *Processor) stringFunction(b *strings.Builder, s string, i int) (int, bool) {
	rest := strings.ToLower(s[i+1:])
	for _, name := range []string{"substr", "instr", "len"} {
		if !strings.HasPrefix(rest, name) {
			continue
		}
		end := i + 1 + len(name)
		if name != "len" && end >= len(s) {
			return 0, false
		}
		return p.runStringFunction(b, name, s, end), true
	}
	return 0, false
}

func (p *Processor) runStringFunction(b *strings.Builder, name, s string, i int) int {
	if i >= len(s) || s[i] != '(' {
		p.fail("misplaced ( .")
		b.WriteString("0")
		return i
	}
	args, end, closed := splitCallArgs(s, i+1)
	if !closed {
		p.fail("misplaced ).")
	}
	arg := func(n int) string {
		if n < len(args) {
			return p.stringArg(args[n])
		}
		return ""
	}
	num := func(n int) (int32, bool) {
		if n >= len(args) {
			return 0, false
		}
		v, _ := p.evalText(p.symbolPass(args[n]))
		return v.c, true
	}
	switch name {
	case "len":
		b.WriteString(strconv.Itoa(len(arg(0))))
	case "instr":
		start, _ := num(2)
		hay, needle := arg(0), arg(1)
		pos := -1
		if start >= 0 && int(start) <= len(hay) {
			if k := strings.Index(hay[start:], needle); k >= 0 {
				pos = int(start) + k
			}
		}
		b.WriteString(strconv.Itoa(pos))
	case "substr":
		text := arg(0)
		from, _ := num(1)
		n, _ := num(2)
		if from < 0 || n < 0 || int(from)+int(n) > len(text) {
			b.WriteString(" ")
		} else {
			b.WriteString(`"` + text[from:from+n] + `"`)
		}
	}
	for end < len(s) && isBlank(s[end]) {
		end++
	}
	return end
}

func splitCallArgs(s string, i int) ([]string, int, bool) {
	var args []string
	start := i
	depth := 0
	var quote byte
	for ; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			if depth == 0 {
				return append(args, s[start:i]), i + 1, true
			}
			depth--
		case c == ',' && depth == 0:
			args = append(args, s[start:i])
			start = i + 1
		}
	}
	return append(args, s[start:]), len(s), false
}

func (p *Processor) stringArg(arg string) string {
	arg = strings.TrimSpace(arg)
	if strings.HasPrefix(arg, `"`) {
		return strings.ReplaceAll(strings.TrimSuffix(arg[1:], `"`), `""`, `"`)
	}
	return p.symbolPass(arg)
}
