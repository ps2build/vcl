package preproc

import (
	"strconv"
	"strings"
)

const (
	severityWarning = iota
	severityError
	severityFatal
)

func itoa32(v int32) string {
	return strconv.FormatInt(int64(v), 10)
}

func (p *Processor) prefixText() string {
	var b strings.Builder
	for _, s := range p.streams {
		b.WriteString(s.name)
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(s.nl))
		b.WriteByte(' ')
	}
	return b.String()
}

func (p *Processor) report(severity int, msg string, newline bool) {
	p.stderr.WriteString(p.prefixText() + msg)
	if newline {
		p.stderr.WriteByte('\n')
	}
	if severity != severityWarning {
		p.status = 1
	}
}

func (p *Processor) warn(msg string) {
	p.report(severityWarning, msg, true)
}

func (p *Processor) fail(msg string) {
	p.report(severityError, msg, true)
}
