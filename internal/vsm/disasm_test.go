package vsm

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ps2build/vcl/internal/dvpas"
)

func TestFormsDerived(t *testing.T) {
	u, l := ProbeForms()
	t.Logf("upper forms %d lower forms %d", u, l)
	if u < 60 || l < 40 {
		t.Errorf("only %d upper and %d lower forms were derived", u, l)
	}
}

func assemble(t *testing.T, src string) []byte {
	var obj bytes.Buffer
	if err := dvpas.Assemble(strings.NewReader(src), &obj); err != nil {
		t.Fatalf("assemble: %v\n%s", err, src)
	}
	o, err := ReadObject(obj.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return o.Section(".vutext")
}

func TestRoundTripInstructions(t *testing.T) {
	lines := []string{
		"nop lq VF01, 3(VI02)",
		"add.xyz VF03, VF04, VF05 nop",
		"mulx.xyzw VF03, VF04, VF05x nop",
		"maddaw.xyz ACC, VF06, VF09w nop",
		"nop div Q, VF00w, VF01y",
		"nop iaddiu VI01, VI02, 100",
		"nop sq.xyz VF01, -2(VI03)",
		"nop[E] nop",
		"nop ilw.w VI01, 5(VI02)",
		"clipw.xyz VF17, VF09w nop",
		"nop fcand VI01, 262143",
	}
	for _, l := range lines {
		src := ".vu\n" + l + "\n"
		code := assemble(t, src)
		words := Disassemble(code, func(int) string { return "L0" })
		if len(words) == 0 || words[0].Unknown {
			t.Errorf("%q: not decoded", l)
			continue
		}
		back := ".vu\n" + words[0].Render() + "\n"
		code2 := assemble(t, back)
		if !bytes.Equal(code, code2) {
			t.Errorf("%q decoded as %q, which assembles differently", l, strings.TrimSpace(words[0].Render()))
		}
	}
}

func TestRoundTripRandomOperands(t *testing.T) {
	seed := uint32(12345)
	rnd := func(n int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>8) % n
	}
	dests := []string{"xyzw", "x", "y", "z", "w", "xy", "xz", "yz", "zw", "xyz", "yzw"}
	checked, skipped := 0, 0
	for _, info := range vclInstructions() {
		if info.Pipe == "nop" {
			continue
		}
		laneSet := []string{""}
		if info.Broadcast {
			laneSet = []string{"x", "y", "z", "w"}
		}
		hasDest := false
		for _, f := range info.Flags {
			if f == "dest" {
				hasDest = true
			}
		}
		for _, lane := range laneSet {
			slots := parseSlots(info.Pattern)
			for n := 0; n < 40; n++ {
				vals := make([]probeVal, len(slots))
				for i := range vals {
					vals[i] = probeVal{reg: rnd(32), lane: rnd(4), imm: rnd(8) - 4}
					if slots[i].kind == "imm" {
						vals[i].imm = rnd(8)
					}
				}
				text := info.Name + lane
				if hasDest {
					text += "." + dests[rnd(len(dests))]
				}
				if ops := renderOperands(slots, vals, lane); len(ops) > 0 {
					text += " " + strings.Join(ops, ", ")
				}
				var src string
				if info.Pipe == "upper" {
					src = ".vu\nL0:\n" + text + " nop\n"
				} else {
					src = ".vu\nL0:\nnop " + text + "\n"
				}
				var obj bytes.Buffer
				if err := dvpas.Assemble(strings.NewReader(src), &obj); err != nil {
					skipped++
					continue
				}
				o, _ := ReadObject(obj.Bytes())
				code := o.Section(".vutext")
				words := Disassemble(code, func(int) string { return "L0" })
				if len(words) == 0 || words[0].Unknown {
					t.Errorf("%q not decoded", text)
					continue
				}
				back := ".vu\nL0:\n" + words[0].Render() + "\n"
				var obj2 bytes.Buffer
				if err := dvpas.Assemble(strings.NewReader(back), &obj2); err != nil {
					t.Errorf("%q decoded as %q, which does not assemble: %v", text, strings.TrimSpace(words[0].Render()), err)
					continue
				}
				o2, _ := ReadObject(obj2.Bytes())
				if !bytes.Equal(code, o2.Section(".vutext")) {
					t.Errorf("%q decoded as %q, which assembles differently", text, strings.TrimSpace(words[0].Render()))
				}
				checked++
			}
		}
	}
	t.Logf("checked %d, skipped %d", checked, skipped)
	if checked < 2000 {
		t.Errorf("only %d samples round-tripped", checked)
	}
}
