package vcl

import "strings"

type isaEntry struct {
	mnemonic   string
	pipe       string
	unit       string
	latency    int
	throughput int
	delay      int
	parser     []string
	pattern    string
	reads      []string
	writes     []string
	mem        string
	memFlags   []string
	flags      []string
	bypass     []string
}

type argKind int

const (
	kVF argKind = iota
	kVI
	kACC
	kQ
	kP
	kI
	kR
	kImm
	kMem
)

type argSpec struct {
	kind    argKind
	write   bool
	dest    bool
	bc      bool
	flag    bool
	wcomp   bool
	rotate  bool
	zero    bool
	postinc bool
	predec  bool
	eval    bool
	raw     bool
	branch  bool
	address bool
	alts    []argSpec
}

type form struct {
	name       string
	upper      bool
	lower      bool
	nop        bool
	unit       string
	latency    int
	throughput int
	delay      int
	hasDest    bool
	broadcast  bool
	xyzOnly    bool
	dynamic    bool
	iwrite     bool
	args       []argSpec
	readsRes   []string
	writesRes  []string
	mem        string
	postinc    bool
	predec     bool
	isBranch   bool
	uncond     bool
	link       bool
	regBranch  bool
	writesQ    bool
	writesP    bool
	writesI    bool
	waitQ      bool
	waitP      bool
	xgkick     bool
	bypassFtoi bool
	bypassMini bool
	bypassMtir bool
	reads      map[string]bool
	writes     map[string]bool
}

func (f *form) readsRes1(r string) bool  { return f.reads[r] }
func (f *form) writesRes1(r string) bool { return f.writes[r] }

var forms = map[string][]*form{}

func parseArgSpec(text string) argSpec {
	var s argSpec
	if i := strings.IndexByte(text, '|'); i >= 0 {
		first := parseArgSpec(text[:i])
		first.alts = append(first.alts, parseArgSpec(text[i+1:]))
		return first
	}
	parts := strings.Split(text, ":")
	head := parts[0]
	if strings.HasPrefix(head, "imm(") {
		s.kind = kMem
		head = "(vi)"
	}
	switch head {
	case "vf":
		s.kind = kVF
	case "vi":
		s.kind = kVI
	case "acc":
		s.kind = kACC
	case "q":
		s.kind = kQ
	case "p":
		s.kind = kP
	case "i":
		s.kind = kI
	case "r":
		s.kind = kR
	case "imm":
		s.kind = kImm
	case "(vi)":
		s.kind = kMem
	}
	for _, m := range parts[1:] {
		switch m {
		case "write":
			s.write = true
		case "dest":
			s.dest = true
		case "bc":
			s.bc = true
		case "flag":
			s.flag = true
		case "wcomp":
			s.wcomp = true
		case "rotate":
			s.rotate = true
		case "zero":
			s.zero = true
		case "postinc":
			s.postinc = true
		case "predec":
			s.predec = true
		case "evaluate":
			s.eval = true
		case "raw":
			s.raw = true
		case "branch":
			s.branch = true
		case "address":
			s.address = true
		}
	}
	return s
}

func has(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func init() {
	for _, e := range isaTable {
		f := &form{name: e.mnemonic, unit: e.unit, latency: e.latency, throughput: e.throughput, delay: e.delay}
		f.nop = e.pipe == "nop"
		f.upper = e.pipe == "upper"
		f.lower = e.pipe == "lower"
		f.hasDest = has(e.parser, "dest")
		f.broadcast = has(e.parser, "broadcast")
		f.xyzOnly = has(e.parser, "xyz")
		f.dynamic = has(e.parser, "dynamic")
		f.iwrite = has(e.parser, "iwrite")
		f.mem = e.mem
		f.postinc = has(e.memFlags, "postinc")
		f.predec = has(e.memFlags, "predec")
		f.isBranch = has(e.flags, "branch")
		f.uncond = has(e.flags, "unconditional_branch")
		f.link = has(e.flags, "link_branch")
		f.regBranch = has(e.flags, "register_branch")
		f.writesQ = has(e.flags, "writes_q")
		f.writesP = has(e.flags, "writes_p")
		f.writesI = has(e.flags, "writes_i")
		f.waitQ = has(e.flags, "wait_q")
		f.waitP = has(e.flags, "wait_p")
		f.xgkick = has(e.flags, "xgkick")
		f.bypassFtoi = has(e.bypass, "load_to_ftoi")
		f.bypassMini = has(e.bypass, "load_to_minii")
		f.bypassMtir = has(e.bypass, "ftoi_to_mtir")
		f.reads = map[string]bool{}
		f.writes = map[string]bool{}
		for _, r := range e.reads {
			f.reads[r] = true
			f.readsRes = append(f.readsRes, r)
		}
		for _, r := range e.writes {
			f.writes[r] = true
			f.writesRes = append(f.writesRes, r)
		}
		if e.pattern != "" {
			for _, a := range strings.Split(e.pattern, ",") {
				f.args = append(f.args, parseArgSpec(a))
			}
		}
		forms[e.mnemonic] = append(forms[e.mnemonic], f)
	}
}
