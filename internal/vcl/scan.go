package vcl

import "strings"

type lineScan struct {
	lines   []string
	inBlock bool
}

func splitLines(src string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == '\n' || c == '\r' {
			lines = append(lines, src[start:i])
			if c == '\r' && i+1 < len(src) && src[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	if start < len(src) {
		lines = append(lines, src[start:])
	}
	return lines
}

func isBlank(c byte) bool { return c == ' ' || c == '\t' }

// cursor walks one physical line, applying comment rules at positions where a token may start.
type cursor struct {
	s       string
	pos     int
	inBlock *bool
	done    bool
}

// enterBlock consumes text after an opening comment marker or while inside a block.
func (c *cursor) consumeBlock() {
	for c.pos < len(c.s) {
		if c.s[c.pos] == ';' {
			c.pos = len(c.s)
			c.done = true
			return
		}
		if c.s[c.pos] == '*' && c.pos+1 < len(c.s) && c.s[c.pos+1] == '/' {
			c.pos += 2
			*c.inBlock = false
			return
		}
		c.pos++
	}
	c.done = true
}

// skip passes blanks and comments; afterwards either done is set or pos is at a token start.
func (c *cursor) skip() {
	for !c.done {
		if *c.inBlock {
			c.consumeBlock()
			continue
		}
		for c.pos < len(c.s) && isBlank(c.s[c.pos]) {
			c.pos++
		}
		if c.pos >= len(c.s) {
			c.done = true
			return
		}
		ch := c.s[c.pos]
		if ch == ';' || (ch == '/' && c.pos+1 < len(c.s) && c.s[c.pos+1] == '/') {
			c.pos = len(c.s)
			c.done = true
			return
		}
		if ch == '/' && c.pos+1 < len(c.s) && c.s[c.pos+1] == '*' {
			c.pos += 2
			*c.inBlock = true
			continue
		}
		return
	}
}

// restText returns the unconsumed text with trailing blanks and a trailing line comment removed.
func (c *cursor) restText() string {
	return c.s[c.pos:]
}

func cutLineComment(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == ';' || (s[i] == '/' && i+1 < len(s) && s[i+1] == '/') {
			return s[:i]
		}
	}
	return s
}

func trimBlank(s string) string {
	return strings.Trim(s, " \t")
}
