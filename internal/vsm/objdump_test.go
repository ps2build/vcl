package vsm

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ps2build/vcl/internal/dvpas"
	"github.com/ps2build/vcl/internal/vcl"
)

const loopSource = `.init_vf_all
.init_vi_all
--enter
--endenter
iaddiu next_input, vi00, 100
iaddiu last_input, vi00, 112
lq scale, 3(vi00)
loop_lid: --LoopCS 1,3
 lq.xyz vert, 0(next_input)
 mulax acc, scale, vert
 maddw out, scale, vf00
 div q, vf00w, outw
 mulq.xyz out, out, q
 sq.xyz out, 1(next_input)
 iaddiu next_input, next_input, 3
 ibne next_input, last_input, loop_lid
--exit
--endexit
`

func TestObjdumpRoundTrip(t *testing.T) {
	res := vcl.Compile(loopSource, vcl.Options{NoSearch: true})
	if res.Exit != 0 {
		t.Fatalf("compile failed: %s", res.Stderr)
	}
	var first bytes.Buffer
	if err := dvpas.Assemble(strings.NewReader(res.Stdout), &first); err != nil {
		t.Fatal(err)
	}
	obj, err := ReadObject(first.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	text, err := obj.Dump(DumpOptions{Disassemble: true, NoAddresses: true, NoRawInsn: true, FileName: "x.vo"})
	if err != nil {
		t.Fatal(err)
	}
	var second bytes.Buffer
	if err := dvpas.Assemble(strings.NewReader(text), &second); err != nil {
		t.Fatalf("the disassembly does not assemble: %v\n%s", err, text)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Errorf("reassembled object differs\n%s", text)
	}
}

func TestObjdumpViews(t *testing.T) {
	var buf bytes.Buffer
	if err := dvpas.Assemble(strings.NewReader(".vu\n.global start\nstart:\n nop lq VF01, 3(VI02)\n nop[E] nop\n nop nop\n"), &buf); err != nil {
		t.Fatal(err)
	}
	obj, err := ReadObject(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	full, _ := obj.Dump(DumpOptions{Disassemble: true, Headers: true, Symbols: true, Contents: true, FileName: "x.vo"})
	for _, want := range []string{"file format", "Sections:", "SYMBOL TABLE:", "start", "Contents of section .vutext", "Disassembly of section .vutext", "<start>:", "lq VF01, 3(VI02)"} {
		if !strings.Contains(full, want) {
			t.Errorf("output lacks %q:\n%s", want, full)
		}
	}
	ranged, _ := obj.Dump(DumpOptions{Disassemble: true, Start: 8, FileName: "x.vo"})
	if strings.Contains(ranged, "lq VF01") {
		t.Errorf("--start-address did not skip the first word:\n%s", ranged)
	}
}
