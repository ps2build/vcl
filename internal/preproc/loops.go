package preproc

import "strings"

func (p *Processor) scanBlock(line, open, closeName string) int {
	i := 0
	skipBlanks := func() {
		for i < len(line) && isBlank(line[i]) {
			i++
		}
	}
	skipBlanks()
	for i < len(line) && isIdent(line[i]) {
		i++
	}
	if i < len(line) && line[i] == ':' {
		i++
	}
	skipBlanks()
	if i >= len(line) || line[i] != p.prefix {
		return 0
	}
	rest := strings.ToLower(line[i+1:])
	for _, k := range []struct {
		name  string
		delta int
	}{{open, 1}, {closeName, -1}} {
		if strings.HasPrefix(rest, k.name) {
			after := rest[len(k.name):]
			if after == "" || !isLetter(after[0]) && !isDigit(after[0]) {
				return k.delta
			}
		}
	}
	return 0
}

func (p *Processor) capture(open, closeName string) (string, bool) {
	depth := 1
	var body strings.Builder
	for {
		line, ok := p.readLine()
		if !ok {
			return "", false
		}
		if delta := p.scanBlock(line, open, closeName); delta != 0 {
			depth += delta
			if depth == 0 {
				return body.String(), true
			}
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
}

func (p *Processor) currentLine() int {
	if t := p.top(); t != nil {
		return t.nl
	}
	return 0
}

func (p *Processor) arepeat(operand string) {
	text := p.symbolPass(p.kwOperand(operand))
	v, _ := p.evalText(text)
	count := v.c
	if !v.absolute() {
		p.fail("AREPEAT must have absolute operand.")
		count = 0
	}
	line := p.currentLine()
	body, ok := p.capture("arepeat", "aendr")
	if !ok {
		p.fatal("AREPEAT without a AENDR at " + itoa(line) + ".")
	}
	if count <= 0 {
		return
	}
	p.countExpansion()
	injected := body
	if count > 1 {
		injected += "\t.AREPEAT\t" + itoa32(count-1) + "\n" + body + "\t.AENDR\n"
	}
	p.push(kindLoop, p.kwOperand(operand), injected)
}

func (p *Processor) awhile(operand string) {
	line := p.currentLine()
	source := p.sourceLine
	body, ok := p.capture("awhile", "aendw")
	if !ok {
		p.fatal("AWHILE without a AENDW at " + itoa(line) + ".")
	}
	if !p.condition(p.symbolPass(operand)) {
		return
	}
	p.countExpansion()
	p.push(kindLoop, p.symbolPass(operand), body+source+"\n"+body+"\t.AENDW\n")
}
