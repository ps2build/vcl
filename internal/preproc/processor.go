package preproc

import (
	"bytes"
	"os"
	"strings"
)

const (
	maxExpansions = 1001
	maxStreams    = 4096
)

type Options struct {
	CommentChar  byte
	SetComment   bool
	Prefix       byte
	SetPrefix    bool
	IncludeDirs  []string
	LineNumbers  bool
	CopySource   bool
	Unreasonable bool
	Defines      []string
	SharedStdout bool
	ReadFile     func(name string) ([]byte, error)
}

type Result struct {
	Output []byte
	Stdout []byte
	Stderr []byte
	Status int
}

type Processor struct {
	commentChar byte
	prefix      byte
	basePrefix  byte
	copySource  bool
	lineNumbers bool
	noLimit     bool
	shared      bool
	includeDirs []string
	readFile    func(string) ([]byte, error)

	out    bytes.Buffer
	stdout bytes.Buffer
	stderr bytes.Buffer
	status int

	streams []*stream
	base    *stream

	symbols map[string]string
	vars    map[string]string
	macros  map[string]*macro
	levels  []level
	radix   int
	gasp    bool

	sourceLine string
	expansions int
	macroCount int
	ended      bool
	stopped    bool
}

type fatalError struct{}

func New(opts Options) *Processor {
	p := &Processor{
		commentChar: opts.CommentChar,
		prefix:      opts.Prefix,
		copySource:  opts.CopySource,
		lineNumbers: opts.LineNumbers,
		noLimit:     opts.Unreasonable,
		shared:      opts.SharedStdout,
		includeDirs: opts.IncludeDirs,
		readFile:    opts.ReadFile,
		symbols:     map[string]string{},
		vars:        map[string]string{},
		macros:      map[string]*macro{},
		radix:       10,
	}
	if !opts.SetComment {
		p.commentChar = '!'
	}
	if !opts.SetPrefix {
		p.prefix = '.'
	}
	p.basePrefix = p.prefix
	if p.readFile == nil {
		p.readFile = os.ReadFile
	}
	for _, d := range opts.Defines {
		p.define(d)
	}
	return p
}

func (p *Processor) Result() Result {
	return Result{
		Output: p.out.Bytes(),
		Stdout: p.stdout.Bytes(),
		Stderr: p.stderr.Bytes(),
		Status: p.status,
	}
}

func Process(name string, src []byte, opts Options) Result {
	p := New(opts)
	p.File(name, src)
	return p.Result()
}

func (p *Processor) File(name string, data []byte) {
	if p.stopped {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(fatalError); !ok {
				panic(r)
			}
			p.stopped = true
			p.streams = nil
		}
	}()
	p.ended = false
	p.base = nil
	p.streams = []*stream{{name: name, kind: kindFile, data: data}}
	for !p.ended {
		line, ok := p.readLine()
		if !ok {
			break
		}
		p.processLine(line)
	}
	if !p.ended {
		p.report(severityWarning, "END missing from end of file.", true)
	}
	p.streams = nil
}

func (p *Processor) define(spec string) {
	name, expr, hasValue := strings.Cut(spec, "=")
	n := int32(1)
	if hasValue {
		v, _ := p.evalText(expr)
		if !v.absolute() {
			p.fail("can't find primary in expression.")
			v = value{}
		}
		n = v.c
	}
	p.vars[name] = itoa32(n)
}

func (p *Processor) fatal(msg string) {
	p.report(severityFatal, msg, true)
	panic(fatalError{})
}

func (p *Processor) on() bool {
	return len(p.levels) == 0 || p.levels[len(p.levels)-1].on
}

func (p *Processor) strayWrite(text string) {
	if p.shared {
		p.out.WriteString(text)
		return
	}
	p.stdout.WriteString(text)
}
