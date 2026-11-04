package vusim

import (
	"strings"
	"testing"
)

func TestSameRegisterDualWriteKeepsUpperOnly(t *testing.T) {
	src := `.vu
	nop lq vf02, 1(vi00)
	nop lq vf03, 2(vi00)
	nop lq vf05, 0(vi00)
	nop lq vf07, 4(vi00)
	nop nop
	nop nop
	nop nop
	nop nop
	add.xy vf05, vf02, vf03 mr32.z vf05, vf07
	nop nop
	nop nop
	nop nop
	nop nop
	nop sq vf05, 16(vi00)
	nop[e] nop
	nop nop
`
	vm, err := New(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	for l := 0; l < 4; l++ {
		vm.Mem[0][l] = 0x3f800000
		vm.Mem[1][l] = 0x40000000
		vm.Mem[2][l] = 0x40400000
		vm.Mem[4][l] = 0x41000000
	}
	if !vm.Run(1000, 1) {
		t.Fatal("program did not finish")
	}
	if got := vm.Mem[16]; got[0] != 0x40a00000 || got[2] != 0x3f800000 {
		t.Errorf("x=%08x z=%08x, want upper result 40a00000 in x and the old value 3f800000 in z", got[0], got[2])
	}
}
