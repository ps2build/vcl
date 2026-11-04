package vcl

import (
	"fmt"
	"strings"
)

func vfName(n int) string { return fmt.Sprintf("VF%02d", n) }
func viName(n int) string { return fmt.Sprintf("VI%02d", n) }

// asMax reports whether a move is spelled as an upper max.
func (ins *instr) text(asMax bool) string {
	f := ins.f
	var b strings.Builder
	name := ins.f.name
	if ins.bcLane != 0 {
		name += string("xyzw"[laneIndex(ins.bcLane)])
	} else if f.broadcast {
		for _, op := range ins.args {
			if op.spec.bc && op.hasFld {
				name += string("xyzw"[laneIndex(op.fields)])
				break
			}
		}
	}
	if asMax {
		name = "max"
	}
	b.WriteString(name)
	if ins.flags != "" {
		b.WriteString("[" + ins.flags + "]")
	}
	if f.hasDest || asMax {
		if f.upper || asMax || ins.mask != laneAll {
			b.WriteString("." + laneString(ins.mask))
		}
	} else if ins.fieldText != "" {
		b.WriteString("." + ins.fieldText)
	}
	if len(ins.args) == 0 {
		if f.waitQ || f.waitP {
			return b.String() + " "
		}
		return b.String()
	}
	b.WriteByte(' ')
	for i, op := range ins.args {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(op.out(ins))
	}
	if asMax {
		b.WriteString(", " + ins.args[1].out(ins))
	}
	return b.String()
}

func laneIndex(m uint8) int {
	for i := 0; i < 4; i++ {
		if m&(1<<uint(i)) != 0 {
			return i
		}
	}
	return 0
}

func (op *operand) out(ins *instr) string {
	switch op.kind {
	case kVF:
		s := vfName(op.phys)
		if op.spec.bc && ins.bcLane == 0 {
			s += string("xyzw"[laneIndex(op.fields)])
		} else if op.spec.flag || op.spec.wcomp {
			if op.hasFld {
				s += string("xyzw"[laneIndex(op.fields)])
			} else if op.spec.wcomp {
				s += "w"
			}
		} else if op.spec.bc && ins.bcLane != 0 {
			s += string("xyzw"[laneIndex(ins.bcLane)])
		}
		return s
	case kVI:
		return viName(op.phys)
	case kACC:
		return "ACC"
	case kQ, kP, kI, kR:
		return op.special
	case kImm:
		return op.imm
	case kMem:
		if op.postinc {
			return "(" + viName(op.base.phys) + "++)"
		}
		if op.predec {
			return "(--" + viName(op.base.phys) + ")"
		}
		return op.offset + "(" + viName(op.base.phys) + ")"
	}
	return op.text
}
