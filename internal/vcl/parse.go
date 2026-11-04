package vcl

import (
	"regexp"
	"strings"
)

// Options controls compilation.
type Options struct {
	NewSyntax       bool
	SourceComments  bool
	BranchThreshold int
	NoPipeline      bool
	Spread          bool
	SpreadWidth     int
	CSSlack         int
	Respread        int
	RespreadLoops   []int
	NoSearch        bool
	ShowAlloc       bool
	Name            string
	Interlock       bool
	SchedMode       int
	Remark          func(string)
	WantSlots       bool
}

type parser struct {
	opt    Options
	diags  []diag
	toks   []*token
	fatal  bool
	syntax bool // true = new syntax
	scan   lineScan
	// directives
	progName   string
	hasName    bool
	hasVU      bool
	initVF     map[int]bool
	initVI     map[int]bool
	initVFAll  bool
	initVIAll  bool
	vfListed   bool
	viListed   bool
	vsmMode    bool
	needEnter  bool
	inEnter    bool
	inExit     bool
	sawExit    bool
	codeSeen   bool
	seenLabels map[string]bool
	region     region
	inPins     map[string]int
	outPins    map[string]int
	hw         map[string]bool
}

func (p *parser) err(line int, msg string) {
	p.diags = append(p.diags, diag{line: line, msg: msg})
}

func (p *parser) die(line int, msg string) {
	p.err(line, msg)
	p.fatal = true
}

var aliasRe = regexp.MustCompile(`^[A-Za-z_.\[\]][A-Za-z0-9_.\[\]]*$`)
var physVF = regexp.MustCompile(`(?i)^vf([0-9]+)$`)
var physVI = regexp.MustCompile(`(?i)^vi([0-9]+)$`)
var prefixVReg = regexp.MustCompile(`(?i)^v[fi][0-9]`)

func validAlias(s string) bool {
	if s == "" || !aliasRe.MatchString(s) {
		return false
	}
	if prefixVReg.MatchString(s) {
		return false
	}
	switch strings.ToLower(s) {
	case "q", "p", "acc", "i":
		return false
	}
	return true
}

func parseFields(s string) (uint8, bool) {
	var m uint8
	last := -1
	for i := 0; i < len(s); i++ {
		var idx int
		switch s[i] {
		case 'x':
			idx = 0
		case 'y':
			idx = 1
		case 'z':
			idx = 2
		case 'w':
			idx = 3
		default:
			return 0, false
		}
		if idx <= last {
			return 0, false
		}
		last = idx
		m |= 1 << idx
	}
	return m, true
}

func (p *parser) parseSource(src string) {
	p.initVF = map[int]bool{}
	p.initVI = map[int]bool{}
	p.seenLabels = map[string]bool{}
	p.syntax = p.opt.NewSyntax
	lines := splitLines(src)
	inBlock := false
	for i, raw := range lines {
		if p.fatal {
			break
		}
		p.line(i+1, raw, &inBlock)
	}
}

func isWordEnd(c byte) bool { return isBlank(c) }

func (p *parser) line(n int, raw string, inBlock *bool) {
	c := &cursor{s: raw, inBlock: inBlock}
	c.skip()
	if c.done {
		return
	}
	label := ""
	// label detection
	start := c.pos
	j := start
	for j < len(raw) && !isBlank(raw[j]) && raw[j] != ':' && raw[j] != ';' {
		j++
	}
	if j < len(raw) && raw[j] == ':' {
		name := raw[start:j]
		if name == "" || !labelOK(name) {
			p.die(n, "Invalid label")
			return
		}
		label = name
		c.pos = j + 1
		c.skip()
		if c.done {
			p.addLabel(n, label)
			return
		}
		// a second label?
		k := c.pos
		for k < len(raw) && !isBlank(raw[k]) && raw[k] != ':' && raw[k] != ';' {
			k++
		}
		if k < len(raw) && raw[k] == ':' {
			p.die(n, "Label already present")
			return
		}
	}
	// word
	ws := c.pos
	we := ws
	for we < len(raw) && !isWordEnd(raw[we]) && raw[we] != ';' {
		we++
	}
	word := raw[ws:we]
	c.pos = we
	if strings.HasPrefix(word, ".") || strings.HasPrefix(word, "--") {
		p.directive(n, label, word, c, raw)
		return
	}
	p.instruction(n, label, word, c, raw)
}

func labelOK(s string) bool {
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '.' || ch == '[' || ch == ']') {
			return false
		}
	}
	return true
}

func (p *parser) addLabel(n int, name string) {
	if p.seenLabels[name] {
		p.die(n, "Duplicate label '"+name+"'")
		return
	}
	p.seenLabels[name] = true
	p.toks = append(p.toks, &token{kind: tkLabel, line: n, label: name})
}

// splitArgs reads the argument list that follows the mnemonic.
func (p *parser) splitArgs(n int, c *cursor, raw string, lenient bool) (args []string, ok bool, depthOpen bool) {
	depth := 0
	first := true
	for {
		c.skip()
		if c.done {
			if !first {
				p.die(n, "Missing argument")
				return nil, false, false
			}
			break
		}
		start := c.pos
		i := start
		consumed := false
		for i < len(raw) {
			ch := raw[i]
			if consumed && depth == 0 && ch == ',' {
				break
			}
			if ch == ';' || (ch == '/' && i+1 < len(raw) && raw[i+1] == '/') {
				break
			}
			if ch == '(' {
				depth++
			} else if ch == ')' {
				depth--
				if depth < 0 {
					p.die(n, "Unbalanced parentheses")
					return nil, false, false
				}
			}
			i++
			consumed = true
		}
		text := trimBlank(raw[start:i])
		args = append(args, text)
		first = false
		c.pos = i
		if i < len(raw) && raw[i] == ',' {
			c.pos = i + 1
			rest := raw[c.pos:]
			if trimBlank(cutLineComment(rest)) == "" {
				if lenient {
					break
				}
				p.die(n, "Missing argument")
				return nil, false, false
			}
			continue
		}
		break
	}
	return args, true, depth > 0
}
