package vcl

import "testing"

func TestCostWaitStall(t *testing.T) {
	r := AnalyzeCost("x", "a_lid:\n nop div Q, VF01w, VF02w\n nop waitq\n", nil)
	tot := r.Totals()
	if tot.Static != 2 || tot.Estimated != 8 || tot.Waitq != 6 {
		t.Errorf("static %d estimated %d waitq stall %d, want 2, 8, 6", tot.Static, tot.Estimated, tot.Waitq)
	}
}

func TestCostIssueStall(t *testing.T) {
	r := AnalyzeCost("x", "a_lid:\n nop div Q, VF01w, VF02w\n nop sqrt Q, VF03w\n", nil)
	tot := r.Totals()
	if tot.Fdiv != 6 || tot.Estimated != 8 {
		t.Errorf("fdiv stall %d estimated %d, want 6, 8", tot.Fdiv, tot.Estimated)
	}
}

func TestCostLoopWeights(t *testing.T) {
	text := "init_lid:\n nop nop\nxform_loop_lid:\n nop nop\n nop nop\n"
	w, err := ParseCostWeights("xform_loop_lid=10")
	if err != nil {
		t.Fatal(err)
	}
	tot := AnalyzeCost("x", text, w).Totals()
	if tot.WStatic != 21 || tot.AffStaticBase != 1 || tot.AffStaticLoop != 2 {
		t.Errorf("weighted %d base %d loop %d, want 21, 1, 2", tot.WStatic, tot.AffStaticBase, tot.AffStaticLoop)
	}
	for _, bad := range []string{"x", "x=0", "x=-3", "x=0x10", "=5", "x=99999999999"} {
		if _, err := ParseCostWeights(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestCostRegression(t *testing.T) {
	fast := AnalyzeCost("fast", "a_lid:\n nop nop\n", nil)
	slow := AnalyzeCost("slow", "a_lid:\n nop nop\n nop nop\n", nil)
	if reg, ok := CheckCost(fast, slow, "static"); !ok || reg == nil {
		t.Error("a longer candidate should regress")
	}
	if reg, ok := CheckCost(slow, fast, "static"); !ok || reg != nil {
		t.Error("a shorter candidate should pass")
	}
	if _, ok := CheckCost(fast, slow, "nonsense"); ok {
		t.Error("unknown metric should be reported")
	}
}
