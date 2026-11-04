package vcl

import (
	"strings"
	"testing"
)

func TestBracketBroadcast(t *testing.T) {
	src := ".syntax new\n.vu\n.init_vf_all\n.init_vi_all\n--enter\n--endenter\n" +
		"  lq a, 0(vi00)\n  lq b, 1(vi00)\n" +
		"  mul acc, a, b[x]\n  madd c, a, b[w]\n  mula.xyz acc, a, b[z]\n  add e, c, b[w]\n" +
		"  sq c, 2(vi00)\n  sq e, 4(vi00)\n--exit\n--endexit\n"
	res := Compile(src, Options{})
	if res.Exit != 0 {
		t.Fatalf("exit %d: %s", res.Exit, res.Stderr)
	}
	for _, want := range []string{"mulax.xyzw ACC, VF01, VF02x", "maddw.xyzw VF03, VF01, VF02w", "mulaz.xyz ACC, VF01, VF02z", "addw.xyzw"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, res.Stdout)
		}
	}
}
