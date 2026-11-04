package preproc

import (
	"strconv"
	"strings"
)

var keywords = map[string]bool{
	"equ": true, "assign": true, "reg": true, "assigna": true, "assignc": true,
	"aif": true, "aelse": true, "aendi": true, "arepeat": true, "aendr": true,
	"awhile": true, "aendw": true, "macro": true, "endm": true, "exitm": true,
	"include": true, "end": true, "radix": true, "db": true, "dw": true, "dl": true,
	"data": true, "datab": true, "sdata": true, "sdatab": true, "sdatac": true,
	"sdataz": true, "res": true, "sres": true, "sresc": true, "sresz": true,
	"export": true, "global": true, "print": true, "form": true, "heading": true,
	"page": true, "align": true, "org": true, "program": true, "local": true,
	"alternate": true, "gasp": true, "masp": true, "set": true, "expr": true,
	"ifmode": true, "ifm": true, "elseifmode": true, "elseifm": true,
	"endifmode": true, "endifm": true,
}

func keywordName(word string) (string, bool) {
	if strings.ToLower(word) != word && strings.ToUpper(word) != word {
		return "", false
	}
	name := strings.ToLower(word)
	return name, keywords[name]
}

func (p *Processor) splitLabel(line string) (label, rest string) {
	c := line[0]
	if isBlank(c) {
		return "", strings.TrimLeft(line, " \t")
	}
	if !isIdentStart(c) && c != '\\' {
		return "", line
	}
	end := 1
	for end < len(line) && (isIdent(line[end]) || line[end] == '\\' || line[end] == '&') {
		end++
	}
	label = line[:end]
	rest = line[end:]
	if strings.HasPrefix(rest, ":") {
		rest = rest[1:]
	}
	return label, strings.TrimLeft(rest, " \t")
}

func keywordWord(rest string, prefix byte) (string, bool) {
	if rest == "" || rest[0] != prefix {
		return "", false
	}
	end := 1
	for end < len(rest) && (isLetter(rest[end]) || rest[end] == '_' || rest[end] == '$') {
		end++
	}
	return rest[1:end], true
}

func (p *Processor) processLine(line string) {
	p.sourceLine = line
	if line == "" {
		if p.on() {
			p.out.WriteByte('\n')
		}
		return
	}
	label, rest := p.splitLabel(line)
	if rest == "" {
		if label != "" && p.on() {
			p.out.WriteString(p.symbolPass(label) + ":\n")
		}
		return
	}
	if label != "" && rest[0] == '\\' && p.on() {
		p.strayWrite("blah")
	}
	if word, ok := keywordWord(rest, p.prefix); ok {
		if name, isKeyword := p.keywordFor(word); isKeyword {
			operand := rest[1+len(word):]
			p.keyword(name, label, operand, len(line)-len(rest))
			return
		}
	}
	if !p.on() {
		return
	}
	if len(p.macros) > 0 {
		if m, n := p.macroFor(rest); m != nil {
			p.callMacro(m, rest[n:])
			return
		}
	}
	p.plain(label, rest)
}

func (p *Processor) keywordFor(word string) (string, bool) {
	name, ok := keywordName(word)
	if !ok {
		return "", false
	}
	switch {
	case name == "masp" && !p.gasp:
		return "", false
	case (name == "set" || name == "expr") && p.gasp:
		return "", false
	}
	return name, true
}

var labelKeywords = map[string]bool{
	"db": true, "dw": true, "dl": true, "data": true, "datab": true, "sdata": true,
	"sdatab": true, "sdatac": true, "sdataz": true, "res": true, "sres": true,
	"sresc": true, "sresz": true, "export": true, "global": true, "print": true,
	"form": true, "heading": true, "page": true, "align": true,
}

func (p *Processor) labelPrefix(label string) string {
	if label != "" {
		return p.symbolPass(label) + ":\t"
	}
	return "\t"
}

func (p *Processor) definesSymbol(rest string) bool {
	word, ok := keywordWord(rest, p.prefix)
	if !ok {
		return false
	}
	switch strings.ToLower(word) {
	case "equ", "assign":
		return true
	}
	return false
}

func (p *Processor) plain(label, rest string) {
	if label != "" && p.definesSymbol(rest) {
		p.out.WriteString(label + ":\t" + p.operand(rest) + "\n")
		return
	}
	p.out.WriteString(p.labelPrefix(label))
	text := p.operand(rest)
	if i := strings.IndexByte(text, 0); i >= 0 {
		text = text[:i]
	}
	p.out.WriteString(text)
	p.out.WriteByte('\n')
}

func (p *Processor) keyword(name, label, operand string, col int) {
	if !p.on() {
		switch name {
		case "aif":
			p.levels = append(p.levels, level{})
		case "aelse", "aendi":
			p.branch(name)
		case "endifmode", "endifm":
			if len(p.levels) > 0 {
				p.levels = p.levels[:len(p.levels)-1]
			}
		case "elseifmode", "elseifm":
			p.elseifmode()
		default:
			if labelKeywords[name] {
				p.out.WriteString(p.labelPrefix(label))
			}
		}
		return
	}
	switch name {
	case "equ", "assign":
		p.defineSymbol(name, label, operand)
	case "reg":
		p.defineReg(label, operand)
	case "assigna":
		p.defineNumeric(label, operand)
	case "assignc":
		p.defineString(label, operand)
	case "aif":
		p.aif(operand)
	case "aelse", "aendi":
		p.branch(name)
	case "arepeat":
		p.arepeat(operand)
	case "aendr":
		p.fail("AENDR without a AREPEAT.")
	case "awhile":
		p.awhile(operand)
	case "aendw":
		p.fail("AENDW without a AENDW.")
	case "macro":
		p.defineMacro(label, operand)
	case "endm":
		p.fail(".ENDM without a matching .MACRO.")
	case "exitm":
		p.exitm()
	case "include":
		p.include(operand)
	case "end":
		p.ended = true
	case "radix":
		p.setRadix(operand)
	case "local":
		p.report(severityError, "LOCAL outside of MACRO", false)
	case "program", "set", "expr":
	case "alternate":
		p.macros = map[string]*macro{}
	case "gasp":
		p.gasp = true
		p.prefix = '.'
	case "masp":
		p.gasp = false
		p.prefix = p.basePrefix
	case "ifmode", "ifm":
		p.ifmode(operand)
	case "elseifmode", "elseifm":
		p.elseifmode()
	case "endifmode", "endifm":
		if len(p.levels) > 0 {
			p.levels = p.levels[:len(p.levels)-1]
		}
	case "org":
		p.fail("ORG command not allowed.")
		p.out.WriteString(p.labelPrefix(label))
		p.out.WriteString(sliceFrom(p.kwOperand(operand), col+1))
		p.out.WriteByte('\n')
	case "align":
		if p.gasp {
			p.dataDirective(name, label, operand)
		} else {
			p.alignMasp(label, operand, col)
		}
	default:
		p.dataDirective(name, label, operand)
	}
}

func sliceFrom(s string, i int) string {
	if i >= len(s) {
		return ""
	}
	return s[i:]
}

func (p *Processor) alignMasp(label, operand string, col int) {
	prefix := p.labelPrefix(label)
	p.out.WriteString(prefix)
	p.out.WriteString(prefix)
	p.out.WriteString(sliceFrom(p.kwOperand(operand), col))
	p.out.WriteByte('\n')
}

func (p *Processor) setRadix(operand string) {
	text := strings.TrimLeft(p.kwOperand(operand), " \t")
	if text == "" {
		p.report(severityError, "radix is  must be one of b, q, d or h", false)
		return
	}
	switch text[0] | 0x20 {
	case 'b':
		p.radix = 2
	case 'q':
		p.radix = 8
	case 'd':
		p.radix = 10
	case 'h':
		p.radix = 16
	default:
		p.report(severityError, "radix is "+string(text[0])+" must be one of b, q, d or h", false)
	}
}

func (p *Processor) defineSymbol(kind, label, operand string) {
	text := p.kwOperand(operand)
	v, _ := p.evalText(text)
	if label == "" {
		return
	}
	if kind == "assign" {
		if _, exists := p.symbols[label]; exists {
			p.fail("redefinition not allowed")
		}
	}
	p.symbols[label] = v.text()
}

func (p *Processor) defineReg(label, operand string) {
	text := strings.TrimLeft(p.kwOperand(operand), " \t")
	if text == "" || text[0] != '(' {
		p.fail("misplaced ( .")
		if text != "" {
			text = text[1:]
		}
	} else {
		text = strings.TrimLeft(text[1:], " \t")
	}
	if i := strings.IndexByte(text, ')'); i >= 0 {
		text = text[:i]
	}
	if label != "" {
		p.symbols[p.symbolPass(label)] = text
	}
}

func (p *Processor) defineNumeric(label, operand string) {
	name := p.symbolPass(label)
	text := p.symbolPass(p.kwOperand(operand))
	v, _ := p.evalText(text)
	if !v.absolute() {
		p.fail(".ASSIGNA needs constant expression argument.")
		v = value{}
	}
	if name == "" {
		p.fail(".ASSIGNA without label.")
		return
	}
	p.vars[name] = itoa32(v.c)
}

func (p *Processor) defineString(label, operand string) {
	name := p.symbolPass(label)
	if name == "" {
		p.fail(".ASSIGNS without label.")
		return
	}
	s := strings.TrimLeft(operand, " \t")
	value := ""
	if s != "" && s[0] == '"' {
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			if s[i] == '"' {
				if i+1 < len(s) && s[i+1] == '"' {
					b.WriteByte('"')
					i++
					continue
				}
				break
			}
			b.WriteByte(s[i])
		}
		value = b.String()
	}
	p.vars[name] = value
}

func (p *Processor) exitm() {
	if t := p.top(); t != nil && t.kind != kindFile {
		p.popStream()
	}
}

func (p *Processor) countExpansion() {
	p.expansions++
	if p.expansions > maxExpansions && !p.noLimit {
		p.fatal("Unreasonable expansion (-u turns off check).")
	}
}

func (p *Processor) include(operand string) {
	text := p.kwOperand(operand)
	name, ok := firstString(text)
	if !ok {
		p.fatal("Can't open include file `'.")
	}
	data, opened, err := p.openInclude(name)
	if err != nil {
		p.fatal("Can't open include file `" + name + "'.")
	}
	p.push(kindFile, opened, string(data))
}

func firstString(s string) (string, bool) {
	s = strings.TrimLeft(s, " \t")
	if s == "" || s[0] != '"' {
		return "", false
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		if s[i] == '"' {
			if i+1 < len(s) && s[i+1] == '"' {
				b.WriteByte('"')
				i++
				continue
			}
			break
		}
		b.WriteByte(s[i])
	}
	return b.String(), true
}

func (p *Processor) openInclude(name string) ([]byte, string, error) {
	for _, dir := range p.includeDirs {
		full := dir + "/" + name
		if data, err := p.readFile(full); err == nil {
			return data, full, nil
		}
	}
	data, err := p.readFile(name)
	return data, name, err
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func (p *Processor) ifmode(operand string) {
	text := strings.TrimLeft(operand, " \t")
	if len(text) < 4 {
		p.fatal("UUU2.")
	}
	var matches bool
	switch strings.ToLower(text[:4]) {
	case "gasp":
		matches = p.gasp
	case "masp":
		matches = !p.gasp
	default:
		p.fatal("UUU3.")
	}
	p.levels = append(p.levels, level{on: matches, parentOn: true})
}

func (p *Processor) elseifmode() {
	if len(p.levels) == 0 {
		return
	}
	top := &p.levels[len(p.levels)-1]
	if top.elseSeen {
		p.fail("Multiple ELSEs in IFMODE.")
	}
	top.elseSeen = true
	if top.parentOn {
		top.on = !top.on
	}
}
