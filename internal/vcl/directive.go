package vcl

import (
	"strconv"
	"strings"
)

type region int

const (
	regOutside region = iota
	regEnter
	regCode
	regExit
)

func itoa(n int) string { return strconv.Itoa(n) }

func (p *parser) directive(n int, label, word string, c *cursor, raw string) {
	name := strings.ToLower(word)
	zeroArg := map[string]bool{
		".vu": true, "--enter": true, "--endenter": true, "--exit": true, "--endexit": true,
		"--barrier": true, "--cont": true, ".vsm": true, ".endvsm": true, ".raw": true, ".endraw": true,
		".rawloop": true, ".endrawloop": true, ".init_vf_all": true, ".init_vi_all": true,
	}
	multi := map[string]bool{
		".init_vf": true, ".init_vi": true, ".rem_vf": true, ".rem_vi": true, ".mpg": true,
		".name": true, ".syntax": true, ".global": true, "--loopcs": true, "--loopextra": true, "--loopabs": true, "--cpp": true, "--gasp": true,
	}
	if !zeroArg[name] && !multi[name] {
		p.die(n, "Unknown operand '"+word+"'")
		return
	}
	var args []string
	c.skip()
	if zeroArg[name] {
		if !c.done {
			p.die(n, "Invalid characters")
			return
		}
	} else if !c.done {
		a, ok, _ := p.splitArgs(n, c, raw, true)
		if !ok {
			return
		}
		args = a
	}
	switch name {
	case ".name":
		if len(args) == 0 {
			p.die(n, "Invalid number of arguments")
			return
		}
		if len(args) != 1 {
			p.errToken(n, "Wrong number of arguments")
			return
		}
		p.progName, p.hasName = args[0], true
	case ".vu":
		p.hasVU = true
	case ".syntax":
		if len(args) != 1 {
			p.errToken(n, "Wrong number of arguments")
			return
		}
		switch strings.ToLower(args[0]) {
		case "new":
			p.syntax = true
		case "old":
			p.syntax = false
		default:
			p.errToken(n, "Invalid argument (.syntax >>> "+args[0]+" <<<)")
		}
	case ".init_vf", ".init_vi":
		p.initList(n, name, args)
	case ".init_vf_all":
		if p.vfListed {
			p.errToken(n, "Cannot mix .init_vf_all and .init_vf")
			return
		}
		p.initVFAll = true
		p.region = regOutsideKeep(p.region)
	case ".init_vi_all":
		if p.viListed {
			p.errToken(n, "Cannot mix .init_vi_all and .init_vi on line ")
			return
		}
		p.initVIAll = true
	case ".rem_vf", ".rem_vi", ".mpg", "--cpp", "--gasp":
	case ".vsm", ".raw":
		if p.vsmMode {
			p.die(n, "VSM mode already active")
			return
		}
		p.vsmMode = true
	case ".endvsm":
		if !p.vsmMode {
			p.errToken(n, "Not in VSM mode")
			return
		}
		p.vsmMode = false
	case ".endraw":
	case ".rawloop", ".endrawloop":
		p.emitDir(n, label, name, nil)
	case ".global":
		p.emitDir(n, label, name, args)
	case "--enter":
		if p.region == regOutside {
			p.region = regEnter
		}
	case "--endenter":
		switch p.region {
		case regEnter:
			p.region = regCode
		case regCode:
			p.region = regExit
		}
	case "--exit":
		if p.region == regCode {
			p.region = regExit
		}
		p.emitDir(n, label, name, nil)
	case "--endexit":
		if p.region == regExit {
			p.region = regOutside
		}
	case "--barrier", "--cont":
		if p.region != regCode {
			p.codeAllowed(n)
			return
		}
		p.emitDir(n, label, name, nil)
	case "--loopextra", "--loopabs":
		if len(args) == 0 {
			p.die(n, "Invalid number of arguments")
			return
		}
		if len(args) != 1 {
			p.errToken(n, "Wrong number of arguments")
			return
		}
		if p.region != regCode {
			p.codeAllowed(n)
			return
		}
		p.emitDir(n, label, name, args)
	case "--loopcs":
		if len(args) != 2 {
			p.errToken(n, "Wrong number of arguments")
			return
		}
		if p.region != regCode {
			p.codeAllowed(n)
			return
		}
		p.emitDir(n, label, name, args)
	}
	switch name {
	case ".rawloop", ".endrawloop", ".global", "--exit", "--barrier", "--cont", "--loopcs", "--loopextra", "--loopabs":
	default:
		p.toks = append(p.toks, &token{kind: tkDir, line: n, dir: "phantom"})
	}
}

func regOutsideKeep(r region) region { return r }

func (p *parser) errToken(n int, msg string) {
	p.err(n, msg)
}

func (p *parser) emitDir(n int, label, name string, args []string) {
	if label != "" {
		p.addLabel(n, label)
	}
	p.toks = append(p.toks, &token{kind: tkDir, line: n, dir: name, dargs: args})
}

func (p *parser) initList(n int, name string, args []string) {
	isF := name == ".init_vf"
	pre := "vi"
	maxReg := 15
	if isF {
		pre = "vf"
		maxReg = 31
	}
	if isF && p.initVFAll {
		p.errToken(n, "Cannot mix .init_vf_all and .init_vf")
		return
	}
	if !isF && p.initVIAll {
		p.errToken(n, "Cannot mix .init_vi_all and .init_vi")
		return
	}
	bad := func(a string) {
		p.errToken(n, "Invalid argument ("+name+" >>> "+a+" <<<)")
	}
	for _, a := range args {
		lo, hi := -1, -1
		parts := strings.Split(a, "-")
		if len(parts) > 2 {
			bad(a)
			return
		}
		parse := func(s string) int {
			s = strings.ToLower(trimBlank(s))
			if !strings.HasPrefix(s, pre) {
				return -1
			}
			v, err := strconv.Atoi(s[2:])
			if err != nil || v < 0 || v > maxReg {
				return -1
			}
			return v
		}
		lo = parse(parts[0])
		hi = lo
		if len(parts) == 2 {
			hi = parse(parts[1])
		}
		if lo < 0 || hi < 0 || hi < lo {
			bad(a)
			return
		}
		if lo == 0 {
			p.errToken(n, "Cannot insert constant register "+strings.ToUpper(pre)+"00 into register allocation")
			return
		}
		for r := lo; r <= hi; r++ {
			if isF {
				p.initVF[r] = true
			} else {
				p.initVI[r] = true
			}
		}
	}
	if isF {
		p.vfListed = p.vfListed || len(args) > 0
	} else {
		p.viListed = p.viListed || len(args) > 0
	}
}
