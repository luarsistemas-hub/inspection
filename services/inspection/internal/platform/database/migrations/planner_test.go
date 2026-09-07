package migrations

import "testing"

func TestPlannerContractsUT056UT057(t *testing.T) {
	steps := []Step{{Version: 2, Name: "b", SQL: "SELECT 2", Compatible: true}, {Version: 1, Name: "a", SQL: "SELECT 1", Compatible: true}}
	plan, err := Plan(steps, nil)
	if err != nil || len(plan) != 2 || plan[0].Version != 1 {
		t.Fatalf("plan=%v err=%v", plan, err)
	}
	if _, err := Plan([]Step{steps[0], steps[0]}, nil); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := Plan([]Step{{Version: 3, Name: "drop", SQL: "DROP TABLE x", Destructive: true}}, nil); err == nil {
		t.Fatal("unsafe destructive accepted")
	}
	if _, err := Plan([]Step{steps[0]}, map[int]string{2: "changed"}); err == nil {
		t.Fatal("checksum drift accepted")
	}
}
