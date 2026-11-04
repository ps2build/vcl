package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ps2build/vcl/internal/dvpas"
	"github.com/ps2build/vcl/internal/preproc"
	"github.com/ps2build/vcl/internal/vcl"
	"github.com/ps2build/vcl/internal/vsm"
)

const version = "0.1.0"

const usage = `usage: vcl [options] <file.vcl|file.vsm>

Compile VCL source or assemble VSM text into a VU1 object.

Phases:
  -E                     preprocess only and print the result
  -fsyntax-only          parse and allocate registers, emit nothing
  -S                     emit scheduled VSM text
  -c                     emit the .vo object (default)
  -o <file>              output file ("-" for standard output)
  -x vcl|vsm             input language (default: from the extension)

Preprocessing:
  -I <dir>, -D <name[=value]>, -U <name>, -include <file>
  -fpreprocessed         the input is already preprocessed
  -MD, -MMD              write a dependency file next to the output
  -MF <file>, -MT <target>

Code generation:
  -O0 | -O1 | -O2        no software pipelining or search | no software pipelining | full (default)
  -n                     the source uses the new field syntax
  -fno-software-pipeline, -fno-loops
                         do not software pipeline loops
  -finterlock            rely on hardware interlocks instead of padding VF and VI hazards
  -fno-search            keep the first register assignment
  -fverbose-vsm          copy source lines into the VSM text as comments
  -fshow-encoding        with -S, add the instruction bytes as comments
  -fno-auto-end-bit, -fno-align, -fgenerate-mpg, -funique-prefix=<s>
                         accepted and ignored

Reports:
  -Rregalloc             print the register allocation
  -Rschedule             print the software pipelining decisions
  -fcost-report[=<file>] print a static cost report of the generated code
  -fcost-report-format=text|json|markdown
  -fcost-baseline=<file.vsm>
                         compare the generated code with a baseline
  -fcost-loop-weights=ps2gl|<label>=<n>,...
  -Wcost-regression      warn when slower than the baseline (with -Werror, fail)
  -fcost-compare-list=<manifest>
                         compare "baseline candidate" file pairs (no input file)
  -fcost-compare-list-check=<metric>
                         fail when a candidate is slower for the metric
  --print-instructions[=json]
  --print-schedule-slots[=json]
                         print internal tables and exit

Other:
  -W, -w, -Werror        warning control
  -save-temps            keep the preprocessed source and VSM text
  -v, -###, --version, --help, @file
`

type driver struct {
	opt          vcl.Options
	pre          preproc.Options
	in, out      string
	lang         string
	phase        string
	preprocessed bool
	syntaxOnly   bool
	depFile      string
	depTarget    string
	writeDeps    bool
	costReport   bool
	costOut      string
	costFormat   string
	costBase     string
	weights      vcl.CostWeights
	costRegress  bool
	werror       bool
	nowarn       bool
	listManifest string
	listCheck    string
	printInstr   string
	printSlots   string
	saveTemps    bool
	verbose      bool
	dryRun       bool
	remarkSched  bool
	showEncoding bool
	includes     []string
	undefs       []string
	level        int
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "vcl: "+format+"\n", a...)
	os.Exit(1)
}

func expandArgs(args []string) []string {
	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a, "@") || len(a) == 1 {
			out = append(out, a)
			continue
		}
		data, err := os.ReadFile(a[1:])
		if err != nil {
			fail("%v", err)
		}
		out = append(out, splitResponse(string(data))...)
	}
	return out
}

func splitResponse(s string) []string {
	var out []string
	var cur strings.Builder
	quote := byte(0)
	have := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && i+1 < len(s) && s[i+1] == quote {
				cur.WriteByte(quote)
				i++
			} else {
				cur.WriteByte(c)
			}
		case c == '"' || c == '\'':
			quote, have = c, true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if have || cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteByte(c)
		}
	}
	if have || cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func parseArgs(args []string) *driver {
	d := &driver{phase: "c", costFormat: "text", level: 2}
	d.pre.CommentChar, d.pre.SetComment = ';', true
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() string {
			i++
			if i >= len(args) {
				fail("missing argument after %s", a)
			}
			return args[i]
		}
		switch {
		case a == "--version":
			fmt.Println("vcl", version)
			os.Exit(0)
		case a == "-h" || a == "--help":
			fmt.Print(usage)
			os.Exit(0)
		case a == "-E" || a == "-S" || a == "-c":
			d.phase = a[1:]
		case a == "-fsyntax-only":
			d.syntaxOnly = true
		case a == "-x":
			d.lang = next()
		case strings.HasPrefix(a, "-x") && len(a) > 2:
			d.lang = a[2:]
		case a == "-o":
			d.out = next()
		case a == "-I":
			d.pre.IncludeDirs = append(d.pre.IncludeDirs, next())
		case strings.HasPrefix(a, "-I"):
			d.pre.IncludeDirs = append(d.pre.IncludeDirs, a[2:])
		case a == "-D":
			d.pre.Defines = append(d.pre.Defines, next())
		case strings.HasPrefix(a, "-D"):
			d.pre.Defines = append(d.pre.Defines, a[2:])
		case a == "-U":
			d.undefs = append(d.undefs, next())
		case strings.HasPrefix(a, "-U"):
			d.undefs = append(d.undefs, a[2:])
		case a == "-include":
			d.includes = append(d.includes, next())
		case a == "-fpreprocessed":
			d.preprocessed = true
		case a == "-MD" || a == "-MMD":
			d.writeDeps = true
		case a == "-MF":
			d.depFile = next()
			d.writeDeps = true
		case a == "-MT" || a == "-MQ":
			d.depTarget = next()
		case a == "-O0" || a == "-O1" || a == "-O2" || a == "-O":
			d.level = 2
			if a == "-O0" {
				d.level = 0
			} else if a == "-O1" {
				d.level = 1
			}
		case a == "-n":
			d.opt.NewSyntax = true
		case a == "-fno-software-pipeline" || a == "-fno-loops":
			d.opt.NoPipeline = true
		case a == "-finterlock":
			d.opt.Interlock = true
		case a == "-fno-search":
			d.opt.NoSearch = true
		case a == "-fspread-registers":
			d.opt.Spread = true
		case strings.HasPrefix(a, "-fspread-width="):
			fmt.Sscanf(a[len("-fspread-width="):], "%d", &d.opt.SpreadWidth)
			d.opt.Spread = true
			d.opt.NoSearch = true
		case strings.HasPrefix(a, "-frespread="):
			fmt.Sscanf(a[len("-frespread="):], "%d", &d.opt.Respread)
			d.opt.NoSearch = true
		case strings.HasPrefix(a, "-fsched="):
			fmt.Sscanf(a[len("-fsched="):], "%d", &d.opt.SchedMode)
		case a == "-fverbose-vsm":
			d.opt.SourceComments = true
		case a == "-fshow-encoding":
			d.showEncoding = true
		case a == "-fno-auto-end-bit" || a == "-fno-align" || a == "-fgenerate-mpg" || strings.HasPrefix(a, "-funique-prefix="):
		case a == "-Rregalloc":
			d.opt.ShowAlloc = true
		case a == "-Rschedule":
			d.remarkSched = true
		case a == "-fcost-report":
			d.costReport = true
		case strings.HasPrefix(a, "-fcost-report="):
			d.costReport = true
			d.costOut = a[len("-fcost-report="):]
		case strings.HasPrefix(a, "-fcost-report-format="):
			d.costFormat = a[len("-fcost-report-format="):]
			if d.costFormat != "text" && d.costFormat != "json" && d.costFormat != "markdown" {
				fail("unknown cost report format %q", d.costFormat)
			}
		case strings.HasPrefix(a, "-fcost-baseline="):
			d.costBase = a[len("-fcost-baseline="):]
		case strings.HasPrefix(a, "-fcost-loop-weights="):
			w, err := vcl.ParseCostWeights(a[len("-fcost-loop-weights="):])
			if err != nil {
				fail("%v", err)
			}
			d.weights = w
		case a == "-Wcost-regression":
			d.costRegress = true
		case strings.HasPrefix(a, "-fcost-compare-list="):
			d.listManifest = a[len("-fcost-compare-list="):]
		case strings.HasPrefix(a, "-fcost-compare-list-check="):
			d.listCheck = a[len("-fcost-compare-list-check="):]
		case a == "--print-instructions" || a == "--print-instructions=text":
			d.printInstr = "text"
		case a == "--print-instructions=json":
			d.printInstr = "json"
		case a == "--print-schedule-slots" || a == "--print-schedule-slots=text":
			d.printSlots = "text"
		case a == "--print-schedule-slots=json":
			d.printSlots = "json"
		case a == "-Werror":
			d.werror = true
		case a == "-w":
			d.nowarn = true
		case a == "-W":
		case a == "-save-temps":
			d.saveTemps = true
		case a == "-v":
			d.verbose = true
		case a == "-###":
			d.dryRun = true
		case strings.HasPrefix(a, "-") && a != "-":
			fail("unknown option %s", a)
		default:
			d.in = a
		}
	}
	if d.level <= 1 {
		d.opt.NoPipeline = true
	}
	if d.level == 0 {
		d.opt.NoSearch = true
	}
	for _, name := range d.undefs {
		var keep []string
		for _, def := range d.pre.Defines {
			if def != name && !strings.HasPrefix(def, name+"=") {
				keep = append(keep, def)
			}
		}
		d.pre.Defines = keep
	}
	return d
}

func (d *driver) language() string {
	if d.lang != "" {
		return d.lang
	}
	switch strings.ToLower(filepath.Ext(d.in)) {
	case ".vsm", ".s":
		return "vsm"
	}
	return "vcl"
}

func readFileOrStdin(name string) ([]byte, error) {
	if name == "" || name == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(name)
}

func main() {
	d := parseArgs(expandArgs(os.Args[1:]))
	if d.dryRun || d.verbose {
		fmt.Fprintf(os.Stderr, "vcl %s\n", version)
	}
	if d.printInstr != "" {
		fmt.Print(vcl.InstructionTable(d.printInstr == "json"))
		return
	}
	if d.listManifest != "" {
		os.Exit(runCompareList(d))
	}
	if d.dryRun {
		fmt.Fprintf(os.Stderr, "vcl: would compile %s (%s) to phase %s\n", d.in, d.language(), d.phase)
		return
	}
	os.Exit(run(d))
}

func run(d *driver) int {
	name := "stdin"
	if d.in != "" && d.in != "-" {
		name = d.in
	}
	src, err := readFileOrStdin(d.in)
	if err != nil {
		fail("%v", err)
	}
	deps := []string{}
	if name != "stdin" {
		deps = append(deps, name)
	}
	lang := d.language()
	if lang == "vcl" && !d.preprocessed {
		d.pre.ReadFile = func(n string) ([]byte, error) {
			data, err := os.ReadFile(n)
			if err == nil {
				deps = append(deps, n)
			}
			return data, err
		}
		for _, inc := range d.includes {
			src = append([]byte(".include \""+inc+"\"\n"), src...)
		}
		r := preproc.Process(name, src, d.pre)
		os.Stderr.Write(r.Stderr)
		if r.Status != 0 {
			return 1
		}
		src = r.Output
		if d.saveTemps {
			os.WriteFile(replaceExt(baseName(name), ".i"), src, 0o644)
		}
	}
	if d.phase == "E" {
		emit(d.out, "", src)
		return d.finish(deps, "")
	}
	var text string
	if lang == "vsm" {
		text = string(src)
	} else {
		d.opt.Name = name
		d.opt.WantSlots = d.printSlots != ""
		res := vcl.Compile(string(src), d.opt)
		fmt.Fprint(os.Stderr, res.Stderr)
		if d.remarkSched {
			for _, r := range res.Remarks {
				fmt.Fprintln(os.Stderr, "remark: "+r)
			}
		}
		if res.Exit != 0 {
			return res.Exit
		}
		if d.printSlots == "text" {
			fmt.Print(vcl.SlotsText(res.Slots))
			return 0
		}
		if d.printSlots == "json" {
			fmt.Print(vcl.SlotsJSON(res.Slots))
			return 0
		}
		text = res.Stdout
	}
	if d.saveTemps {
		os.WriteFile(replaceExt(baseName(name), ".vsm"), []byte(text), 0o644)
	}
	if code := d.costWork(name, text); code != 0 {
		return code
	}
	if d.syntaxOnly {
		return d.finish(deps, "")
	}
	target := ""
	switch d.phase {
	case "S":
		if d.showEncoding {
			annotated, err := annotateEncoding(text)
			if err != nil {
				fail("assemble: %v", err)
			}
			text = annotated
		}
		target = outputName(d.out, replaceExt(d.in, ".vsm"))
		emit(d.out, replaceExt(d.in, ".vsm"), []byte(text))
	default:
		var obj bytes.Buffer
		if err := dvpas.Assemble(strings.NewReader(withVU(text)), &obj); err != nil {
			fail("assemble: %v", err)
		}
		target = outputName(d.out, replaceExt(d.in, ".vo"))
		emit(d.out, replaceExt(d.in, ".vo"), obj.Bytes())
	}
	return d.finish(deps, target)
}

func annotateEncoding(text string) (string, error) {
	var obj bytes.Buffer
	if err := dvpas.Assemble(strings.NewReader(withVU(text)), &obj); err != nil {
		return "", err
	}
	o, err := vsm.ReadObject(obj.Bytes())
	if err != nil {
		return "", err
	}
	code := o.Section(".vutext")
	var out strings.Builder
	word := 0
	for _, line := range strings.Split(text, "\n") {
		out.WriteString(line)
		if vsm.IsInstructionLine(line) && (word+1)*8 <= len(code) {
			b := code[word*8 : word*8+8]
			fmt.Fprintf(&out, " ; %02x%02x%02x%02x %02x%02x%02x%02x", b[3], b[2], b[1], b[0], b[7], b[6], b[5], b[4])
			word++
		}
		out.WriteString("\n")
	}
	return strings.TrimRight(out.String(), "\n") + "\n", nil
}

func baseName(p string) string {
	if p == "" || p == "-" || p == "stdin" {
		return "out"
	}
	return filepath.Base(p)
}

func outputName(out, def string) string {
	if out != "" {
		return out
	}
	return def
}

func replaceExt(in, ext string) string {
	if in == "" || in == "-" {
		return ""
	}
	return strings.TrimSuffix(in, filepath.Ext(in)) + ext
}

func emit(out, def string, data []byte) {
	if out == "" {
		out = def
	}
	if out == "" || out == "-" {
		os.Stdout.Write(data)
		return
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		fail("%v", err)
	}
}

func (d *driver) finish(deps []string, target string) int {
	if !d.writeDeps {
		return 0
	}
	file := d.depFile
	if file == "" {
		base := target
		if base == "" || base == "-" {
			base = replaceExt(baseName(d.in), ".o")
		}
		file = replaceExt(base, ".d")
	}
	t := d.depTarget
	if t == "" {
		t = target
		if t == "" || t == "-" {
			t = replaceExt(baseName(d.in), ".o")
		}
	}
	var sb strings.Builder
	sb.WriteString(makeEscape(t) + ":")
	seen := map[string]bool{}
	for _, dep := range deps {
		if seen[dep] {
			continue
		}
		seen[dep] = true
		sb.WriteString(" \\\n  " + makeEscape(filepath.ToSlash(dep)))
	}
	sb.WriteString("\n")
	if err := os.WriteFile(file, []byte(sb.String()), 0o644); err != nil {
		fail("%v", err)
	}
	return 0
}

func makeEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "/")
	s = strings.ReplaceAll(s, " ", "\\ ")
	return strings.ReplaceAll(s, "#", "\\#")
}

func (d *driver) costWork(name, text string) int {
	if !d.costReport && d.costBase == "" && !d.costRegress {
		return 0
	}
	w := d.weights
	if w == nil {
		w = vcl.CostWeights{}
	}
	cand := vcl.AnalyzeCost(name, text, w)
	var base *vcl.CostReport
	if d.costBase != "" {
		data, err := os.ReadFile(d.costBase)
		if err != nil {
			fail("%v", err)
		}
		base = vcl.AnalyzeCost(d.costBase, string(data), w)
	}
	if d.costReport {
		var rendered string
		switch {
		case base != nil:
			c := &vcl.CostComparison{Base: base, Cand: cand}
			switch d.costFormat {
			case "json":
				rendered = c.JSON()
			case "markdown":
				rendered = c.Markdown()
			default:
				rendered = c.Text()
			}
		default:
			switch d.costFormat {
			case "json":
				rendered = cand.JSON()
			case "markdown":
				fail("the markdown format needs -fcost-baseline")
			default:
				rendered = cand.Text()
			}
		}
		if d.costOut == "" || d.costOut == "-" {
			fmt.Print(rendered)
		} else if err := os.WriteFile(d.costOut, []byte(rendered), 0o644); err != nil {
			fail("%v", err)
		}
	}
	if d.costRegress {
		if base == nil {
			fail("-Wcost-regression needs -fcost-baseline")
		}
		if reg, ok := vcl.CheckCost(base, cand, "weighted-estimated"); ok && reg != nil && !d.nowarn {
			label := "warning"
			if d.werror {
				label = "error"
			}
			fmt.Fprintf(os.Stderr, "vcl: %s: cost regression: %s is slower than %s (weighted estimated cycles %d against %d)\n", label, reg.CandIn, reg.BaseName, reg.Cand, reg.Baseline)
			if d.werror {
				return 1
			}
		}
	}
	return 0
}

func runCompareList(d *driver) int {
	data, err := readFileOrStdin(d.listManifest)
	if err != nil {
		fail("%v", err)
	}
	w := d.weights
	if w == nil {
		w = vcl.CostWeights{}
	}
	var out strings.Builder
	if d.costFormat == "markdown" || d.listCheck == "" {
		out.WriteString(vcl.MarkdownHeader())
	}
	failures := 0
	for n, line := range strings.Split(string(data), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 {
			fmt.Print(out.String())
			fail("%s:%d: expected a baseline and a candidate path", d.listManifest, n+1)
		}
		var reports [2]*vcl.CostReport
		for k := range f {
			b, err := os.ReadFile(f[k])
			if err != nil {
				fmt.Print(out.String())
				fail("%v", err)
			}
			reports[k] = vcl.AnalyzeCost(f[k], string(b), w)
		}
		c := &vcl.CostComparison{Base: reports[0], Cand: reports[1]}
		if d.costFormat == "markdown" || d.listCheck == "" {
			out.WriteString(c.MarkdownRow())
		}
		if d.listCheck != "" {
			reg, ok := vcl.CheckCost(reports[0], reports[1], d.listCheck)
			if !ok {
				fail("unknown cost metric %q", d.listCheck)
			}
			if reg != nil {
				failures++
				fmt.Fprintf(os.Stderr, "vcl: cost check failed for %s against %s: %s baseline=%d candidate=%d\n", reg.CandIn, reg.BaseName, reg.Metric, reg.Baseline, reg.Cand)
			}
		}
	}
	fmt.Print(out.String())
	if failures > 0 {
		fmt.Fprintf(os.Stderr, "vcl: cost check failed for %d file pair(s)\n", failures)
		return 1
	}
	return 0
}

// withVU selects VU mode for assembler input that does not choose one itself.
func withVU(text string) string {
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) == ".vu" {
			return text
		}
	}
	return ".vu\n" + text
}
