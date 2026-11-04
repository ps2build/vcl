package dvpas

import (
	"bytes"
	"debug/elf"
	"encoding/hex"
	"strings"
	"testing"
)

func TestAssembleVU1Probe(t *testing.T) {
	src := `.vu
.global vu1_probe_start
.global vu1_probe_end

vu1_probe_start:
	nop nop
	nop[e] nop
vu1_probe_end:
`
	wantVutext := "3c030080ff0200003c030080ff020040"
	assembleAndCheck(t, src, wantVutext, map[string]wantSymbol{
		"vu1_probe_start": {offset: 0, global: true},
		"vu1_probe_end":   {offset: 16, global: true},
	})
}

func TestAssembleVU1Answer(t *testing.T) {
	src := `.vu
.global VUAnswerStart
.global VUAnswerEnd

VUAnswerStart:
	nop iaddiu vi01, vi00, 42
	nop iswr.x vi01, (vi00)
	nop[e] nop
VUAnswerEnd:
`
	wantVutext := "2a000110ff020000ff030181ff0200003c030080ff020040"
	assembleAndCheck(t, src, wantVutext, map[string]wantSymbol{
		"VUAnswerStart": {offset: 0, global: true},
		"VUAnswerEnd":   {offset: 24, global: true},
	})
}

func TestAssembleSimpleScheduled(t *testing.T) {
	src := `.vu
.align 4
entry_lid:
                    nop                             lq VF01, 0(VI00)
                    add.xyz VF02, VF01, VF00        iaddiu VI01, VI00, 1
                    nop                             nop
                    nop                             waitq
                    nop[E]                          b entry_lid
`
	wantVutext := "0000e101ff02000001000110a808c0013c030080ff020000bf030080ff020000fb070040ff0200400000000000000000"
	assembleAndCheck(t, src, wantVutext, map[string]wantSymbol{"entry_lid": {offset: 0, global: false}})
}

func TestAssembleLongLatency(t *testing.T) {
	src := `.vu
.align 4
weighted_lid:
                    add.xyz VF05, VF05, VF00        div Q, VF01w, VF02w
                    mul.xyz VF06, VF06, VF00        esin P, VF03x
                    add.xyz VF07, VF07, VF00        waitp
                    nop                             waitq
`
	wantVutext := "bc0be2816829c001fc1f0080aa31c001bf070080e839c001bf030080ff020000"
	assembleAndCheck(t, src, wantVutext, map[string]wantSymbol{"weighted_lid": {offset: 0, global: false}})
}

func TestAssembleSecondWave(t *testing.T) {
	src := `.vu
.align 4
start_lid:
	sub.xyz VF05, VF06, VF07	nop
	subi.xyz VF05, VF06, i	nop
	subq.xyz VF05, VF06, q	nop
	suba.xyz ACC, VF06, VF07	nop
	subai.xyz ACC, VF06, i	nop
	subaq.xyz ACC, VF06, q	nop
	abs.xyz VF03, VF04	nop
	ftoi0.xyz VF03, VF04	nop
	ftoi4.xyz VF03, VF04	nop
	ftoi12.xyz VF03, VF04	nop
	ftoi15.xyz VF03, VF04	nop
	itof0.xyz VF03, VF04	nop
	itof4.xyz VF03, VF04	nop
	itof12.xyz VF03, VF04	nop
	itof15.xyz VF03, VF04	nop
	nop		iaddi VI03, VI04, 5
	nop		isubiu VI03, VI04, 100
	nop		ibeq VI01, VI02, start_lid
	nop		ibne VI01, VI02, start_lid
	nop		ibltz VI01, start_lid
	nop		ibgez VI01, start_lid
	nop		ibgtz VI01, start_lid
	nop		iblez VI01, start_lid
	nop		jr VI01
	nop		jalr VI02, VI01
	nop		ilw.x VI03, 0(VI04)
	nop		isw.x VI03, 0(VI04)
	nop		lqi.xyzw VF03, (VI04++)
	nop		sqi.xyzw VF03, (VI04++)
	nop		lqd.xyzw VF03, (--VI04)
	nop		sqd.xyzw VF03, (--VI04)
	nop		mtir VI04, VF03x
	nop		move.xyzw VF04, VF03
	nop		mr32.xyzw VF04, VF03
end_lid:
`
	wantVutext := "3c0300806c31c7013c0300806631c0013c0300806431c0013c030080fc32c7013c0300807e32c0013c0300807c32c0013c030080fd21c3013c0300807c21c3013c0300807d21c3013c0300807e21c3013c0300807f21c3013c0300803c21c3013c0300803d21c3013c0300803e21c3013c0300803f21c30172210380ff02000064200312ff020000ee170150ff020000ed170152ff020000ec0f0058ff020000eb0f005eff020000ea0f005aff020000e90f005cff02000000080048ff0200000008024aff02000000200309ff0200000020030bff0200007c23e381ff0200007d1be481ff0200007e23e381ff0200007f1be481ff020000fc1b0480ff0200003c1be481ff0200003d1be481ff020000"
	assembleAndCheck(t, src, wantVutext, map[string]wantSymbol{
		"start_lid": {offset: 0, global: false},
		"end_lid":   {offset: 8 * 34, global: false},
	})
}

func TestAssembleArithFamilies(t *testing.T) {
	src := `.vu
.align 4
start_lid:
	addi.xyz VF05, VF06, i	nop
	addq.xyz VF05, VF06, q	nop
	adda.xyz ACC, VF06, VF07	nop
	addai.xyz ACC, VF06, i	nop
	addaq.xyz ACC, VF06, q	nop
	muli.xyz VF05, VF06, i	nop
	mulq.xyz VF05, VF06, q	nop
	mula.xyz ACC, VF06, VF07	nop
	mulai.xyz ACC, VF06, i	nop
	mulaq.xyz ACC, VF06, q	nop
	madd.xyz VF05, VF06, VF07	nop
	maddi.xyz VF05, VF06, i	nop
	maddq.xyz VF05, VF06, q	nop
	madda.xyz ACC, VF06, VF07	nop
	maddai.xyz ACC, VF06, i	nop
	maddaq.xyz ACC, VF06, q	nop
	msub.xyz VF05, VF06, VF07	nop
	msubi.xyz VF05, VF06, i	nop
	msubq.xyz VF05, VF06, q	nop
	msuba.xyz ACC, VF06, VF07	nop
	msubai.xyz ACC, VF06, i	nop
	msubaq.xyz ACC, VF06, q	nop
end_lid:
`
	wantVutext := "3c0300806231c0013c0300806031c0013c030080bc32c7013c0300803e32c0013c0300803c32c0013c0300805e31c0013c0300805c31c0013c030080be32c7013c030080fe31c0013c030080fc31c0013c0300806931c7013c0300806331c0013c0300806131c0013c030080bd32c7013c0300803f32c0013c0300803d32c0013c0300806d31c7013c0300806731c0013c0300806531c0013c030080fd32c7013c0300807f32c0013c0300807d32c001"
	assembleAndCheck(t, src, wantVutext, map[string]wantSymbol{
		"start_lid": {offset: 0, global: false},
		"end_lid":   {offset: 8 * 22, global: false},
	})
}

func TestAssembleRemainingVocabulary(t *testing.T) {
	src := `.vu
.align 4
start_lid:
	max.xyz VF05, VF06, VF07	nop
	maxi.xyz VF05, VF06, i	nop
	mini.xyz VF05, VF06, VF07	nop
	minii.xyz VF05, VF06, i	nop
	nop		eatanxy P, VF04
	nop		eatanxz P, VF04
	nop		eleng P, VF04
	nop		erleng P, VF04
	nop		ersadd P, VF04
	nop		esadd P, VF04
	nop		esum P, VF04
	nop		bal VI02, start_lid
	nop		fceq VI01, 0x0ffffff
	nop		fcget VI03
	nop		fcor VI01, 0x0ffffff
	nop		fcset 0x0ffffff
	nop		fmeq VI03, VI04
	nop		fmor VI03, VI04
	nop		iadd VI03, VI04, VI05
	nop		ilwr.x VI03, (VI04)
	nop		isub VI03, VI04, VI05
	nop		mfp.xyzw VF04, P
	nop		rget.xyzw VF04, R
	nop		rinit R, VF03x
	nop		rnext.xyzw VF04, R
	nop		rxor R, VF03x
	nop		sqrt Q, VF03w
	nop		xgkick VI04
	nop		xitop VI04
end_lid:
`
	wantVutext := "3c0300806b31c7013c0300805d31c0013c0300806f31c7013c0300805f31c0017c278081ff0200007d274081ff0200003e27c081ff0200003f27c081ff0200003d27c081ff0200003c27c081ff0200007e27e081ff020000f4070242ff020000ffffff20ff02000000000338ff020000ffffff26ff020000ffffff22ff02000000200330ff02000000200336ff020000f0200580ff020000fe230381ff020000f1200580ff0200007c06e481ff0200003d04e481ff0200003e1c0080ff0200003c04e481ff0200003f1c0080ff020000bd038381ff020000fc260080ff020000bd060480ff0200000000000000000000"
	assembleAndCheck(t, src, wantVutext, map[string]wantSymbol{
		"start_lid": {offset: 0, global: false},
		"end_lid":   {offset: 8 * 29, global: false},
	})
}

func TestAssembleFlagStatus(t *testing.T) {
	src := `.vu
.align 4
start_lid:
	nop		fsand VI03, 0xabc
	nop		fseq VI03, 0xabc
	nop		fsor VI03, 0xabc
	nop		fsset 0xabc
end_lid:
`
	wantVutext := "bc02232cff020000bc022328ff020000bc02232eff0200008002202aff020000"
	assembleAndCheck(t, src, wantVutext, map[string]wantSymbol{
		"start_lid": {offset: 0, global: false},
		"end_lid":   {offset: 8 * 4, global: false},
	})
}

func TestAssembleLoi(t *testing.T) {
	src := `.vu
.align 4
start_lid:
	nop[i]	loi 1.5
	nop		loi 1.5
	nop		loi -2.25
end_lid:
`
	wantVutext := "0000c03fff0200800000c03fff020080000010c0ff0200800000000000000000"
	assembleAndCheck(t, src, wantVutext, map[string]wantSymbol{
		"start_lid": {offset: 0, global: false},
		"end_lid":   {offset: 8 * 3, global: false},
	})
}

func TestAssembleIaddiNegativeImmediate(t *testing.T) {
	src := `.vu
start_lid:
	nop	iaddi VI04, VI04, -1
end_lid:
`
	wantVutext := "f2270480ff020000"
	assembleAndCheck(t, src, wantVutext, map[string]wantSymbol{
		"start_lid": {offset: 0, global: false},
		"end_lid":   {offset: 8, global: false},
	})
}

func TestAssembleBroadcastFamily(t *testing.T) {
	src := `.vu
start_lid:
	mulx.xyz VF06, VF06, VF00x	nop
	muly.xyz VF06, VF06, VF00y	nop
	mulz.xyz VF06, VF06, VF00z	nop
	mulw.xyz VF06, VF06, VF00w	nop
	addx.xyz VF06, VF01, VF00x	nop
	subx.xyz VF06, VF01, VF00x	nop
	maddx.xyz VF06, VF01, VF00x	nop
	msubx.xyz VF06, VF01, VF00x	nop
	maxx.xyz VF06, VF01, VF00x	nop
	minix.xyz VF06, VF01, VF00x	nop
	addax.xyz ACC, VF01, VF00x	nop
	subax.xyz ACC, VF01, VF00x	nop
	maddax.xyz ACC, VF01, VF00x	nop
	msubax.xyz ACC, VF01, VF00x	nop
	mulax.xyz ACC, VF01, VF00x	nop
	clipw.xyz VF01, VF05w	nop
end_lid:
`
	wantVutext := "3c0300809831c0013c0300809931c0013c0300809a31c0013c0300809b31c0013c0300808009c0013c0300808409c0013c0300808809c0013c0300808c09c0013c0300809009c0013c0300809409c0013c0300803c08c0013c0300807c08c0013c030080bc08c0013c030080fc08c0013c030080bc09c0013c030080ff09c501"
	assembleAndCheck(t, src, wantVutext, map[string]wantSymbol{
		"start_lid": {offset: 0, global: false},
		"end_lid":   {offset: 8 * 16, global: false},
	})
}

func TestAssembleBroadcastFamilyRejectsMismatchedLane(t *testing.T) {
	src := `.vu
	mulx.xyz VF06, VF06, VF00y	nop
`
	if err := Assemble(strings.NewReader(src), &bytes.Buffer{}); err == nil {
		t.Fatal("expected an error for a lane mismatch between mnemonic and operand, got nil")
	}
}

type wantSymbol struct {
	offset uint32
	global bool
}

func assembleAndCheck(t *testing.T, src, wantVutextHex string, wantSymbols map[string]wantSymbol) {
	t.Helper()

	var out bytes.Buffer
	if err := Assemble(strings.NewReader(src), &out); err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	got := out.Bytes()
	wantVutext, err := hex.DecodeString(wantVutextHex)
	if err != nil {
		t.Fatalf("bad want hex: %v", err)
	}

	f, err := elf.NewFile(bytes.NewReader(got))
	if err != nil {
		t.Fatalf("debug/elf could not parse the emitted object: %v", err)
	}
	vutext := f.Section(".vutext")
	if vutext == nil {
		t.Fatal("missing .vutext section")
	}
	gotVutext, err := vutext.Data()
	if err != nil {
		t.Fatalf("reading .vutext: %v", err)
	}
	if !bytes.Equal(gotVutext, wantVutext) {
		t.Errorf(".vutext mismatch:\n got  %x\n want %x", gotVutext, wantVutext)
	}

	syms, err := f.Symbols()
	if err != nil {
		t.Fatalf("reading symbols: %v", err)
	}
	byName := make(map[string]elf.Symbol, len(syms))
	for _, s := range syms {
		byName[s.Name] = s
	}
	for name, want := range wantSymbols {
		s, ok := byName[name]
		if !ok {
			t.Errorf("missing symbol %q", name)
			continue
		}
		if s.Value != uint64(want.offset) {
			t.Errorf("symbol %q: got offset %d, want %d", name, s.Value, want.offset)
		}
		wantBind := elf.STB_LOCAL
		if want.global {
			wantBind = elf.STB_GLOBAL
		}
		if gotBind := elf.ST_BIND(s.Info); gotBind != wantBind {
			t.Errorf("symbol %q: got bind %v, want %v", name, gotBind, wantBind)
		}
	}
}

func TestAssembleLoiHexAndNegativeOffsets(t *testing.T) {
	src := `.vu
start_hex:
	nop loi 0x43000000
	nop sq VF06, -1(VI03)
	nop lq VF06, -1024(VI03)
end_hex:
`
	wantVutext := "00000043ff020080ff37e303ff020000001ce601ff020000"
	assembleAndCheck(t, src, wantVutext, map[string]wantSymbol{
		"start_hex": {offset: 0, global: false},
		"end_hex":   {offset: 8 * 3, global: false},
	})
}
