package vcl

import "testing"

func lanes(reg int, lanes ...int) resSet {
	s := resSet{}
	for _, l := range lanes {
		s[reg*4+l] = true
	}
	return s
}

func TestWriteClash(t *testing.T) {
	upper := &node{writes: lanes(5, 0, 1)}
	lowerSameRegister := &node{writes: lanes(5, 2)}
	lowerOtherRegister := &node{writes: lanes(6, 2)}
	if !writeClash(upper, lowerSameRegister) {
		t.Error("writes to different lanes of one register must not share a word")
	}
	if writeClash(upper, lowerOtherRegister) {
		t.Error("writes to different registers may share a word")
	}
	flagsOnly := &node{writes: resSet{resMAC: true, resACC: true}}
	if writeClash(flagsOnly, &node{writes: resSet{resMAC: true, resACC: true}}) {
		t.Error("flag and accumulator writes are not register writes")
	}
}
