package preproc

import "strings"

type macro struct {
	formals  []string
	defaults []string
	body     string
}

func isSeparator(c byte) bool {
	switch c {
	case ' ', '\t', ',', '"', ';', '(', ')':
		return true
	}
	return false
}

func (p *Processor) macroFor(rest string) (*macro, int) {
	if rest == "" || !isIdentStart(rest[0]) {
		return nil, 0
	}
	end := 1
	for end < len(rest) && isIdent(rest[end]) {
		end++
	}
	return p.macros[strings.ToLower(rest[:end])], end
}

func (p *Processor) defineMacro(label, operand string) {
	line := p.currentLine()
	text := p.kwOperand(operand)
	name := ""
	if label != "" {
		name = label
	} else {
		text = strings.TrimLeft(text, " \t")
		end := 0
		for end < len(text) && isIdent(text[end]) {
			end++
		}
		name, text = text[:end], text[end:]
	}
	parse := p.parseFormals
	if p.gasp {
		parse = p.parseFormalsGasp
	}
	formals, defaults, closed := parse(text)
	body, ok := p.capture("macro", "endm")
	if !ok {
		if p.currentLine() == line {
			line = -1
		}
		p.fail("macro at line " + itoa(line) + ": unexpected end of file in macro definition")
		return
	}
	if !closed {
		p.fail("macro at line " + itoa(line) + ": missing ) after formals")
		return
	}
	if name == "" {
		return
	}
	p.macros[strings.ToLower(name)] = &macro{formals: formals, defaults: defaults, body: body}
}

func (p *Processor) parseFormals(text string) (formals, defaults []string, closed bool) {
	i := 0
	skip := func() {
		for i < len(text) && isBlank(text[i]) {
			i++
		}
	}
	skip()
	paren := i < len(text) && text[i] == '('
	if paren {
		i++
	}
	closed = true
	for {
		skip()
		if paren && i < len(text) && text[i] == ')' {
			i++
			break
		}
		start := i
		for i < len(text) && (i == start && isIdentStart(text[i]) || i > start && isIdent(text[i])) {
			i++
		}
		if i == start {
			if paren {
				closed = false
			}
			break
		}
		name := text[start:i]
		def := ""
		skip()
		if i < len(text) && text[i] == '=' {
			i++
			def, i = p.argValue(text, i)
		}
		skip()
		if i < len(text) && text[i] != ',' {
			if !(paren && text[i] == ')') {
				p.strayWrite("Error: macro needs comma\n")
				i++
			}
		} else if i < len(text) {
			i++
		}
		formals = append(formals, name)
		defaults = append(defaults, def)
	}
	return formals, defaults, closed
}

func (p *Processor) argValue(s string, i int) (string, int) {
	if p.gasp {
		return p.gaspValue(s, i)
	}
	return p.readValue(s, i)
}

func (p *Processor) gaspValue(s string, i int) (string, int) {
	for i < len(s) && isBlank(s[i]) {
		i++
	}
	if i < len(s) && s[i] == '"' {
		end := strings.IndexByte(s[i+1:], '"')
		if end < 0 {
			return s[i+1:], len(s)
		}
		return s[i+1 : i+1+end], i + 1 + end + 1
	}
	start := i
	for i < len(s) && !isBlank(s[i]) && s[i] != ',' && s[i] != p.commentChar {
		i++
	}
	return s[start:i], i
}

func (p *Processor) parseFormalsGasp(text string) (formals, defaults []string, closed bool) {
	i := 0
	skip := func() {
		for i < len(text) && (isBlank(text[i]) || text[i] == ',') {
			i++
		}
	}
	skip()
	paren := i < len(text) && text[i] == '('
	if paren {
		i++
	}
	closed = true
	for {
		skip()
		if i >= len(text) || text[i] == p.commentChar {
			closed = !paren
			break
		}
		if paren && text[i] == ')' {
			break
		}
		start := i
		for i < len(text) && (i == start && isIdentStart(text[i]) || i > start && isIdent(text[i])) {
			i++
		}
		if i == start {
			break
		}
		name := text[start:i]
		def := ""
		if i < len(text) && text[i] == '=' {
			def, i = p.gaspValue(text, i+1)
		}
		formals = append(formals, name)
		defaults = append(defaults, def)
	}
	return formals, defaults, closed
}

func (p *Processor) readValue(s string, i int) (string, int) {
	for i < len(s) && isBlank(s[i]) {
		i++
	}
	var b strings.Builder
	pendingBlank := false
	for i < len(s) {
		c := s[i]
		if c == ',' || c == p.commentChar {
			break
		}
		if isBlank(c) {
			pendingBlank = true
			i++
			continue
		}
		if pendingBlank {
			b.WriteByte(' ')
			pendingBlank = false
		}
		switch {
		case c == '\\':
			if i+1 < len(s) {
				b.WriteByte(s[i+1])
				i += 2
			} else {
				b.WriteByte(c)
				i++
			}
		case c == '"' || c == '\'':
			b.WriteByte(c)
			i++
			for i < len(s) && s[i] != c {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				b.WriteByte(s[i])
				i++
			}
			if i < len(s) {
				b.WriteByte(c)
				i++
			}
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), i
}

func (p *Processor) callMacro(m *macro, argText string) {
	p.countExpansion()
	values, ok := p.bindArguments(m, argText)
	if !ok {
		return
	}
	text, complete := p.expandBody(m, values)
	_ = complete
	p.push(kindMacro, "macro expansion", text)
	p.macroCount++
}

func (p *Processor) bindArguments(m *macro, s string) (map[string]string, bool) {
	actual := map[string]string{}
	index := map[string]bool{}
	for _, f := range m.formals {
		index[f] = true
	}
	next := 0
	keywordSeen := false
	positionalSeen := false
	i := 0
	skip := func() {
		for i < len(s) && isBlank(s[i]) {
			i++
		}
	}
	skip()
	for i < len(s) && s[i] != p.commentChar {
		j := i
		for j < len(s) && !isSeparator(s[j]) && s[j] != p.commentChar && s[j] != '=' {
			j++
		}
		var value string
		if j < len(s) && s[j] == '=' {
			k := i
			for k < len(s) && isIdent(s[k]) {
				k++
			}
			if k != j || i == j || isDigit(s[i]) {
				p.fail("confusion in formal parameters")
				return nil, false
			}
			name := s[i:j]
			if !index[name] {
				p.fail("macro formal argument does not exist")
				return nil, false
			}
			value, i = p.argValue(s, j+1)
			actual[name] = value
			keywordSeen = true
		} else {
			if keywordSeen {
				p.fail("can't mix positional and keyword arguments")
				return nil, false
			}
			value, i = p.argValue(s, i)
			if next >= len(m.formals) {
				p.fail("too many positional arguments")
				return nil, false
			}
			actual[m.formals[next]] = value
			next++
			positionalSeen = true
		}
		skip()
		if i < len(s) && s[i] == ',' {
			i++
		}
		skip()
	}
	_ = positionalSeen
	values := make(map[string]string, len(m.formals))
	for k, name := range m.formals {
		if v := actual[name]; v != "" {
			values[name] = v
		} else {
			values[name] = m.defaults[k]
		}
	}
	return values, true
}

func (p *Processor) expandBody(m *macro, values map[string]string) (string, bool) {
	body := m.body
	var b strings.Builder
	inQuote := false
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case c == '"':
			inQuote = !inQuote
			b.WriteByte(c)
		case c == p.commentChar && !inQuote && i+1 < len(body) && body[i+1] == p.commentChar:
			i = skipLine(body, i)
			b.WriteByte('\n')
		case c == '&':
			j := i + 1
			for j < len(body) && (j == i+1 && isIdentStart(body[j]) || j > i+1 && isIdent(body[j])) {
				j++
			}
			name := body[i+1 : j]
			if v, ok := values[name]; ok && name != "" {
				b.WriteString(v)
			} else {
				b.WriteByte('&')
			}
			if j < len(body) && body[j] == '&' {
				j++
			}
			i = j - 1
		case c == '\\' && i+1 < len(body):
			n, complete := p.expandBackslash(&b, body, i, values)
			if !complete {
				return b.String(), false
			}
			i = n
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), true
}

func skipLine(body string, i int) int {
	for i < len(body) && body[i] != '\n' {
		i++
	}
	return i
}

func (p *Processor) expandBackslash(b *strings.Builder, body string, i int, values map[string]string) (int, bool) {
	n := body[i+1]
	switch {
	case n == p.commentChar:
		return skipLine(body, i+1) - 1 + 0, true
	case n == '(':
		end := strings.IndexByte(body[i+2:], ')')
		if end < 0 {
			p.fail("missplaced )")
			b.WriteString(body[i+2:])
			return len(body), false
		}
		b.WriteString(body[i+2 : i+2+end])
		return i + 2 + end, true
	case n == '@':
		b.WriteString(itoa(p.macroCount))
		return i + 1, true
	case n == '&':
		b.WriteString("\\&")
		return i + 1, true
	case isIdentStart(n):
		j := i + 2
		for j < len(body) && isIdent(body[j]) {
			j++
		}
		name := body[i+1 : j]
		if j < len(body) && body[j] == '\'' {
			j++
		}
		if v, ok := values[name]; ok {
			b.WriteString(v)
		} else {
			b.WriteString("\\" + name)
		}
		return j - 1, true
	}
	b.WriteByte('\\')
	return i, true
}
