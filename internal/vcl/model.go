package vcl

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	laneX = 1 << iota
	laneY
	laneZ
	laneW
	laneAll = laneX | laneY | laneZ | laneW
)

type operand struct {
	kind    argKind
	text    string
	phys    int
	name    string
	fields  uint8
	hasFld  bool
	special string
	imm     string
	offset  string
	base    *operand
	spec    argSpec
	raw     bool
	zero    bool
	postinc bool
	predec  bool
	lit     bool
}

type instr struct {
	f         *form
	word      string
	mask      uint8
	hasMask   bool
	flags     string
	args      []*operand
	bcLane    uint8
	fieldText string
	// emitted spelling pieces
	outName string
}

type tokKind int

const (
	tkInstr tokKind = iota
	tkLabel
	tkDir
	tkErr
	tkNop
)

type token struct {
	kind  tokKind
	line  int
	label string
	dir   string
	dargs []string
	ins   *instr
	raw   bool
	src   string
	sink  bool
	fill  *token
	noPad bool
}

type diag struct {
	line  int
	msg   string
	plain bool
}

func (d diag) format(name string) string {
	if d.plain {
		return "vcl: " + d.msg
	}
	return fmt.Sprintf("%s(%d) : %s", name, d.line, d.msg)
}

func (d diag) String() string { return d.format("stdin") }

func laneString(m uint8) string {
	var b strings.Builder
	if m&laneX != 0 {
		b.WriteByte('x')
	}
	if m&laneY != 0 {
		b.WriteByte('y')
	}
	if m&laneZ != 0 {
		b.WriteByte('z')
	}
	if m&laneW != 0 {
		b.WriteByte('w')
	}
	return b.String()
}

func laneBox(m uint8) string {
	s := []byte("    ")
	if m&laneX != 0 {
		s[0] = 'x'
	}
	if m&laneY != 0 {
		s[1] = 'y'
	}
	if m&laneZ != 0 {
		s[2] = 'z'
	}
	if m&laneW != 0 {
		s[3] = 'w'
	}
	return "[" + string(s) + "]"
}

func parseFloat(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return v
		}
		return 0
	}
	return v
}
