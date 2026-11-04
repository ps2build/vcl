package vcl

import (
	"regexp"
	"strconv"
	"strings"
)

var pseudoArg = regexp.MustCompile(`(?i)^([A-Za-z_.\[\]][A-Za-z0-9_.\[\]]*)\s*\(\s*v([fi])([0-9]+)\s*\)$`)

func (p *parser) pseudo(n int, base string, c *cursor, raw string) {
	isIn := strings.HasPrefix(base, "in_")
	kind := strings.TrimPrefix(strings.TrimPrefix(base, "in_"), "out_")
	if (isIn && p.region != regEnter) || (!isIn && p.region != regExit) {
		if p.region == regOutside {
			p.die(n, "Code not allowed outside entry point")
			return
		}
		p.err(n, "Invalid operand inside --enter/--endenter block")
		return
	}
	c.skip()
	switch kind {
	case "vf", "vi":
		args, ok, _ := p.splitArgs(n, c, raw, false)
		if !ok {
			return
		}
		if len(args) == 0 {
			p.die(n, "Invalid number of arguments")
			return
		}
		for _, a := range args {
			m := pseudoArg.FindStringSubmatch(a)
			if m == nil {
				p.err(n, "Invalid argument ("+base+" >>> "+a+" <<<)")
				continue
			}
			num, _ := strconv.Atoi(m[3])
			isInt := strings.ToLower(m[2]) == "i"
			if (kind == "vf") == isInt {
				p.err(n, "Invalid argument ("+base+" >>> "+a+" <<<)")
				continue
			}
			key := "f:" + m[1]
			if isInt {
				key = "i:" + m[1]
			}
			if isIn {
				p.inPins[key] = num
			} else {
				if prev, ok := p.inPins[key]; ok && prev != num {
					p.err(n, "Output register conflict")
					continue
				}
				p.outPins[key] = num
			}
		}
	default:
		if !c.done {
			args, ok, _ := p.splitArgs(n, c, raw, false)
			_ = args
			if !ok {
				return
			}
		}
		p.hw[base] = true
	}
	p.toks = append(p.toks, &token{kind: tkDir, line: n, dir: base})
}
