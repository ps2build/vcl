package vcl

import (
	"strings"
)

var bcBase = map[string]bool{
	"add": true, "sub": true, "mul": true, "madd": true, "msub": true, "max": true, "mini": true,
	"adda": true, "suba": true, "mula": true, "madda": true, "msuba": true,
}

type candidate struct {
	f      *form
	bcLane uint8
	name   string
}

func laneOf(c byte) uint8 {
	switch c {
	case 'x':
		return laneX
	case 'y':
		return laneY
	case 'z':
		return laneZ
	case 'w':
		return laneW
	}
	return 0
}

func (p *parser) candidates(base string) []candidate {
	var out []candidate
	if fs, ok := forms[base]; ok {
		for _, f := range fs {
			if f.broadcast && !p.syntax {
				continue
			}
			out = append(out, candidate{f: f, name: base})
		}
		if p.syntax && bcBase[base] {
			// simplified forms
			for _, extra := range simplified(base) {
				for _, f := range forms[extra] {
					out = append(out, candidate{f: f, name: extra})
				}
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if len(base) > 1 {
		last := base[len(base)-1]
		stem := base[:len(base)-1]
		if lane := laneOf(last); lane != 0 && bcBase[stem] {
			for _, f := range forms[stem] {
				if f.broadcast {
					return []candidate{{f: f, bcLane: lane, name: base}}
				}
			}
		}
	}
	return nil
}

func simplified(base string) []string {
	acc := map[string]string{"add": "adda", "sub": "suba", "mul": "mula", "madd": "madda", "msub": "msuba"}
	switch base {
	case "add", "sub", "mul", "madd", "msub", "max", "mini":
		var r []string
		r = append(r, base+"i", base+"q")
		if a, ok := acc[base]; ok {
			r = append(r, a, a+"i", a+"q")
		}
		return r
	}
	return nil
}

type instrWord struct {
	base    string
	flags   string
	fields  string
	hasFld  bool
	badFld  bool
	badFlag bool
}

func decodeWord(word string) instrWord {
	var w instrWord
	i := strings.IndexAny(word, "[.")
	if i < 0 {
		w.base = word
		return w
	}
	w.base = word[:i]
	rest := word[i:]
	if rest[0] == '[' {
		k := strings.IndexByte(rest, ']')
		if k < 0 {
			w.badFlag = true
			return w
		}
		w.flags = rest[1:k]
		rest = rest[k+1:]
	}
	if rest != "" {
		if rest[0] == '.' {
			w.fields = rest[1:]
			w.hasFld = w.fields != ""
		} else {
			w.badFld = true
		}
	}
	return w
}

func canonFlags(s string) (string, bool) {
	var e, d, t bool
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case 'E', 'e':
			e = true
		case 'D', 'd':
			d = true
		case 'T', 't':
			t = true
		default:
			return "", false
		}
	}
	out := ""
	if e {
		out += "E"
	}
	if d {
		out += "D"
	}
	if t {
		out += "T"
	}
	return out, true
}

func (p *parser) instruction(n int, label, word string, c *cursor, raw string) {
	w := decodeWord(word)
	base := strings.ToLower(w.base)
	if strings.HasPrefix(base, "in_") || strings.HasPrefix(base, "out_") {
		p.pseudo(n, base, c, raw)
		return
	}
	cands := p.candidates(base)
	if len(cands) == 0 {
		p.die(n, "Unknown operand '"+w.base+"'")
		return
	}
	if w.badFlag {
		p.die(n, "Invalid bit-flag")
		return
	}
	flags, ok := canonFlags(w.flags)
	if !ok {
		p.die(n, "Invalid bit-flag")
		return
	}
	var mask uint8
	hasMask := false
	if w.badFld {
		p.die(n, "Invalid destination-field")
		return
	}
	if w.fields != "" {
		m, ok := parseFields(w.fields)
		if !ok {
			p.die(n, "Invalid destination-field")
			return
		}
		mask, hasMask = m, true
	}
	c.skip()
	if len(cands[0].f.args) == 0 {
		if !c.done {
			p.die(n, "Invalid characters")
			return
		}
		ins := &instr{f: cands[0].f, word: word, flags: flags, mask: laneAll}
		p.pushInstr(n, label, ins, raw)
		return
	}
	args, ok, open := p.splitArgs(n, c, raw, false)
	if !ok {
		return
	}
	if len(args) == 0 {
		p.die(n, "Invalid number of arguments")
		return
	}
	typed := w.base
	if hasMask {
		typed += "." + w.fields
	}
	ins, matched := p.match(n, cands, typed, args, mask, hasMask)
	if open {
		p.die(n, "Unbalanced parentheses")
		return
	}
	if !matched || ins == nil {
		return
	}
	ins.flags = flags
	ins.fieldText = w.fields
	ins.word = typed
	p.pushInstr(n, label, ins, raw)
}

func (p *parser) pushInstr(n int, label string, ins *instr, raw string) {
	if !p.codeAllowed(n) {
		return
	}
	if label != "" {
		if p.seenLabels[label] {
			p.die(n, "Duplicate label '"+label+"'")
			return
		}
		p.seenLabels[label] = true
	}
	p.toks = append(p.toks, &token{kind: tkInstr, line: n, label: label, ins: ins, src: raw, raw: p.vsmMode})
}

func (p *parser) codeAllowed(n int) bool {
	switch p.region {
	case regOutside:
		p.die(n, "Code not allowed outside entry point")
		return false
	case regEnter:
		p.die(n, "Invalid operand inside --enter/--endenter block")
		return false
	case regExit:
		p.die(n, "Invalid operand inside --exit/--endexit block")
		return false
	}
	return true
}

func (p *parser) fail(n int, ins string, args []string, idx int, box string) {
	var b strings.Builder
	b.WriteString(ins)
	for i, a := range args {
		if i == 0 {
			b.WriteByte(' ')
		} else {
			b.WriteString(", ")
		}
		if i == idx {
			b.WriteString(">>> " + a + " <<<")
		} else {
			b.WriteString(a)
		}
	}
	if box != "" {
		b.WriteString(" " + box)
	}
	p.err(n, "Invalid argument ("+b.String()+")")
}

func (p *parser) match(n int, cands []candidate, typed string, args []string, mask uint8, hasMask bool) (*instr, bool) {
	var lastIdx = -1
	var lastBox string
	tried := false
	for _, cd := range cands {
		if len(cd.f.args) != len(args) {
			continue
		}
		tried = true
		ins, idx, box := p.tryMatch(cd, args, mask, hasMask)
		if ins != nil {
			return ins, true
		}
		lastIdx, lastBox = idx, box
	}
	if !tried {
		p.err(n, "Wrong number of arguments")
		return nil, true
	}
	p.fail(n, typed, args, lastIdx, lastBox)
	return nil, false
}
