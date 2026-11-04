package vcl

// access describes one register access made by an instruction.
type access struct {
	arg     int
	op      *operand
	isInt   bool
	write   bool
	lanes   uint8
	check   bool
	special string
}

func rotateLanes(m uint8) uint8 {
	var r uint8
	for i := uint(0); i < 4; i++ {
		if m&(1<<i) != 0 {
			r |= 1 << ((i + 1) % 4)
		}
	}
	return r
}

// accesses lists the register-file reads and writes of an instruction (no resources).
func (ins *instr) accesses() []access {
	var out []access
	f := ins.f
	for i, op := range ins.args {
		spec := op.spec
		switch op.kind {
		case kVF:
			a := access{arg: i, op: op, write: spec.write, check: !spec.write}
			switch {
			case spec.bc:
				a.lanes = ins.bcLane
				if a.lanes == 0 {
					a.lanes = op.fields
				}
				a.check = ins.bcLane == 0
			case spec.flag:
				a.lanes = op.fields
			case spec.wcomp:
				a.lanes = laneW
				if op.hasFld {
					a.lanes = op.fields
				}
			case spec.dest:
				a.lanes = ins.mask
				if spec.rotate {
					a.lanes = rotateLanes(ins.mask)
				}
			default:
				a.lanes = laneAll
				a.check = false
			}
			out = append(out, a)
		case kVI:
			out = append(out, access{arg: i, op: op, isInt: true, write: spec.write, lanes: laneAll, check: !spec.write})
		case kMem:
			if op.base != nil {
				out = append(out, access{arg: i, op: op.base, isInt: true, lanes: laneAll, check: true})
				if op.postinc || op.predec {
					out = append(out, access{arg: i, op: op.base, isInt: true, write: true, lanes: laneAll})
				}
			}
		case kACC, kQ, kP, kI, kR:
			out = append(out, access{arg: i, op: op, special: op.special, write: spec.write, lanes: laneAll})
		}
	}
	_ = f
	return out
}
