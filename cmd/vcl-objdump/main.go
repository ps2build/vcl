package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ps2build/vcl/internal/vsm"
)

const version = "0.1.0"

const usage = `usage: vcl-objdump [options] <file.vo>

Inspect and disassemble VU1 objects.

  -d, --disassemble      disassemble the code section
  -h, --section-headers  print the section headers
  -t, --syms             print the symbol table
  -r, --reloc            print relocations
  -s, --full-contents    print the section contents as hex
  -j <section>           limit -d and -s to one section
  --start-address=<addr> first address to print
  --stop-address=<addr>  last address (exclusive) to print
  --no-show-raw-insn     do not print the instruction bytes
  --no-addresses         do not print addresses
  --version, --help

With --no-addresses and --no-show-raw-insn, -d prints plain VSM text that
assembles back to the same object.
`

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "vcl-objdump: "+format+"\n", a...)
	os.Exit(1)
}

func parseAddr(s string) uint32 {
	n, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X"), map[bool]int{true: 16, false: 10}[strings.HasPrefix(strings.ToLower(s), "0x")], 32)
	if err != nil {
		fail("bad address %q", s)
	}
	return uint32(n)
}

func main() {
	var opt vsm.DumpOptions
	var file string
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--version":
			fmt.Println("vcl-objdump", version)
			return
		case a == "--help":
			fmt.Print(usage)
			return
		case a == "-d" || a == "--disassemble":
			opt.Disassemble = true
		case a == "-h" || a == "--section-headers" || a == "--headers":
			opt.Headers = true
		case a == "-t" || a == "--syms":
			opt.Symbols = true
		case a == "-r" || a == "--reloc":
			opt.Relocs = true
		case a == "-s" || a == "--full-contents":
			opt.Contents = true
		case a == "-j":
			i++
			if i >= len(args) {
				fail("missing section after -j")
			}
			opt.Section = args[i]
		case strings.HasPrefix(a, "--section="):
			opt.Section = a[len("--section="):]
		case strings.HasPrefix(a, "--start-address="):
			opt.Start = parseAddr(a[len("--start-address="):])
		case strings.HasPrefix(a, "--stop-address="):
			opt.Stop = parseAddr(a[len("--stop-address="):])
			opt.HasStop = true
		case a == "--no-show-raw-insn":
			opt.NoRawInsn = true
		case a == "--no-addresses":
			opt.NoAddresses = true
		case strings.HasPrefix(a, "-") && a != "-":
			fail("unknown option %s", a)
		default:
			file = a
		}
	}
	if file == "" {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
	if !opt.Disassemble && !opt.Headers && !opt.Symbols && !opt.Relocs && !opt.Contents {
		fail("no action specified (use -d, -h, -t, -r or -s)")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		fail("%v", err)
	}
	obj, err := vsm.ReadObject(data)
	if err != nil {
		fail("%s: %v", file, err)
	}
	opt.FileName = file
	out, err := obj.Dump(opt)
	fmt.Print(out)
	if err != nil {
		fail("%v", err)
	}
}
