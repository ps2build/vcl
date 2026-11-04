package preproc

import (
	"fmt"
	"strconv"
	"strings"
)

const positionWidth = 24

const (
	kindFile streamKind = iota
	kindMacro
	kindLoop
)

type streamKind int

type stream struct {
	name string
	kind streamKind
	data []byte
	pos  int
	nl   int
}

func (s *stream) exhausted() bool {
	return s.pos >= len(s.data)
}

func (p *Processor) push(kind streamKind, name string, text string) {
	if len(p.streams) >= maxStreams {
		p.fatal("unreasonable nesting.")
	}
	p.streams = append(p.streams, &stream{name: name, kind: kind, data: []byte(text)})
}

func (p *Processor) top() *stream {
	if len(p.streams) == 0 {
		return nil
	}
	return p.streams[len(p.streams)-1]
}

func (p *Processor) popStream() {
	p.base = p.top()
	p.streams = p.streams[:len(p.streams)-1]
}

func (p *Processor) nextByte() (byte, bool) {
	for len(p.streams) > 0 {
		s := p.top()
		if s.exhausted() {
			p.popStream()
			continue
		}
		c := s.data[s.pos]
		s.pos++
		if c == '\n' {
			s.nl++
		}
		return c, true
	}
	return 0, false
}

func (p *Processor) popExhausted() {
	for len(p.streams) > 0 && p.top().exhausted() {
		p.popStream()
	}
}

func (p *Processor) positionField() string {
	var b strings.Builder
	streams := p.streams
	if len(streams) == 0 && p.base != nil {
		streams = []*stream{p.base}
	}
	for i, s := range streams {
		if i == 0 {
			fmt.Fprintf(&b, "%4d", s.nl+1)
		} else {
			b.WriteString("." + strconv.Itoa(s.nl+1))
		}
	}
	for b.Len() < positionWidth {
		b.WriteByte(' ')
	}
	return b.String()
}

func (p *Processor) echoStart() {
	if !p.copySource {
		return
	}
	p.out.WriteByte(p.commentChar)
	if p.lineNumbers {
		p.out.WriteString(p.positionField())
	}
}

func (p *Processor) echo(c byte) {
	if p.copySource {
		p.out.WriteByte(c)
	}
}

func (p *Processor) readLine() (string, bool) {
	p.echoStart()
	var buf []byte
	started := false
	for {
		c, ok := p.nextByte()
		if !ok {
			if !started {
				return "", false
			}
			p.report(severityWarning, "End of file not at start of line.", true)
			p.echo('\n')
			return string(buf), true
		}
		if c == '\r' {
			continue
		}
		p.echo(c)
		if c != '\n' {
			buf = append(buf, c)
			started = true
			continue
		}
		p.popExhausted()
		if t := p.top(); t != nil && !t.exhausted() && t.data[t.pos] == '+' {
			t.pos++
			if p.copySource {
				p.out.WriteByte(p.commentChar)
				p.out.WriteByte('+')
			}
			if !t.exhausted() {
				if t.data[t.pos] == '\n' {
					t.nl++
				}
				t.pos++
			}
			started = true
			continue
		}
		return string(buf), true
	}
}
