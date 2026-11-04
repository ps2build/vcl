package vcl

import "sort"

// resource ids
const (
	resACC = 128 + iota
	resQ
	resP
	resI
	resR
	resMAC
	resCLIP
	resCount
)

// vf lanes: id = reg*4+lane (0..127); vi: 160+reg (160..175)
const viBase = 176

type resSet map[int]bool

type node struct {
	t       *token
	ins     *instr
	reads   resSet
	writes  resSet
	upper   bool
	selUp   bool
	memKind string // "", "load", "store"
	memBase int
	memOff  string
	memOK   bool // offset comparable
	writesQ bool
	writesP bool
	isLoad  bool
	fixed   bool
}

func laneRes(reg int, lanes uint8) []int {
	var out []int
	for l := 0; l < 4; l++ {
		if lanes&(1<<uint(l)) != 0 {
			out = append(out, reg*4+l)
		}
	}
	return out
}

func newNode(t *token, moveMax bool) *node {
	ins := t.ins
	f := ins.f
	n := &node{t: t, ins: ins, reads: resSet{}, writes: resSet{}}
	n.upper = f.upper || moveMax
	n.selUp = f.upper
	for _, a := range ins.accesses() {
		if a.special != "" {
			switch a.special {
			case "acc":
				// handled via form resources
			case "q":
				if a.write {
					n.writes[resQ] = true
				} else {
					n.reads[resQ] = true
				}
			case "p":
				if a.write {
					n.writes[resP] = true
				} else {
					n.reads[resP] = true
				}
			case "i":
				if a.write {
					n.writes[resI] = true
				} else {
					n.reads[resI] = true
				}
			case "r":
				if a.write {
					n.writes[resR] = true
				} else {
					n.reads[resR] = true
				}
			}
			continue
		}
		if a.op == nil {
			continue
		}
		var ids []int
		if a.isInt {
			if a.op.phys == 0 {
				continue
			}
			ids = []int{viBase + a.op.phys}
		} else {
			if a.op.phys == 0 && !a.write {
				continue
			}
			ids = laneRes(a.op.phys, a.lanes)
		}
		for _, id := range ids {
			if a.write {
				n.writes[id] = true
			} else {
				n.reads[id] = true
			}
		}
	}
	for _, r := range f.readsRes {
		n.reads[resID(r)] = true
	}
	for _, r := range f.writesRes {
		n.writes[resID(r)] = true
	}
	n.writesQ = f.writesQ
	n.writesP = f.writesP
	if f.mem == "load" || f.mem == "store" {
		n.memKind = f.mem
		n.isLoad = f.mem == "load"
		for _, op := range ins.args {
			if op.kind == kMem {
				n.memBase = op.base.phys
				n.memOff = op.offset
				n.memOK = !(op.postinc || op.predec) && isNumber(op.offset)
			}
		}
	}
	return n
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	if s[0] == '-' || s[0] == '+' {
		i = 1
	}
	if i >= len(s) {
		return false
	}
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func resID(r string) int {
	switch r {
	case "acc":
		return resACC
	case "q":
		return resQ
	case "p":
		return resP
	case "i":
		return resI
	case "r":
		return resR
	case "mac":
		return resMAC
	case "clip":
		return resCLIP
	}
	return resCount
}

func intersects(a, b resSet) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
}

// dependent reports whether b must stay after a (a earlier).
func dependent(a, b *node, flagWAW func(res int) bool) bool {
	for k := range a.writes {
		if b.reads[k] {
			return true
		}
	}
	for k := range a.reads {
		if b.writes[k] {
			return true
		}
	}
	for k := range a.writes {
		if b.writes[k] {
			if k == resMAC || k == resCLIP {
				if !flagWAW(k) {
					continue
				}
			}
			return true
		}
	}
	return memDependent(a, b)
}

func memDependent(a, b *node) bool {
	if a.memKind == "" || b.memKind == "" {
		return false
	}
	if a.isLoad && b.isLoad {
		return false
	}
	if a.memBase != b.memBase {
		return false
	}
	if a.memOK && b.memOK && a.memOff != b.memOff {
		return false
	}
	if a.isLoad && !b.isLoad {
		return false
	}
	return true
}

type word struct {
	upper *node
	lower *node
	text  [2]string
}

type prioInfo struct{ prio []int }

func (n *node) lat() int {
	l := n.ins.f.latency
	if l < 1 {
		l = 1
	}
	return l
}

func computePriorities(nodes []*node, deps [][]int) []int {
	prio := make([]int, len(nodes))
	for i := len(nodes) - 1; i >= 0; i-- {
		best := 0
		for _, s := range deps[i] {
			if prio[s] > best {
				best = prio[s]
			}
		}
		prio[i] = best + nodes[i].lat()
	}
	return prio
}

func sortInts(a []int) { sort.Ints(a) }

// writeClash reports whether both nodes write the same float register; the lower pipe's write is lost when they share a word.
func writeClash(a, b *node) bool {
	for k := range a.writes {
		if k < 128 && k >= 4 {
			for l := 0; l < 4; l++ {
				if b.writes[k/4*4+l] {
					return true
				}
			}
		}
	}
	return false
}
