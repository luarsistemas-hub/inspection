package core

import (
	"testing"
	"time"
)

func TestCostCompletenessIgnoresErrorsWithoutCost(t *testing.T) {
	if !CostComplete(0, 0, 0) {
		t.Fatal("error calls without a reported cost must not block cost completeness")
	}
	for _, counts := range [][3]int64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}} {
		if CostComplete(counts[0], counts[1], counts[2]) {
			t.Fatalf("incomplete counts %v were reported complete", counts)
		}
	}
}

func TestCoverageCompletenessUsesLedgerCutoff(t *testing.T) {
	cutoff := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if !WindowCoverageComplete(&cutoff, cutoff, 0) {
		t.Fatal("window beginning at cutoff should be covered")
	}
	if WindowCoverageComplete(&cutoff, cutoff.Add(-time.Nanosecond), 0) {
		t.Fatal("window beginning before cutoff must not be covered")
	}
	if WindowCoverageComplete(&cutoff, cutoff, 1) {
		t.Fatal("unverified calls must block coverage completeness")
	}
	if !InspectionCoverageComplete(&cutoff, 0, 0) {
		t.Fatal("inspection with only post-cutoff calls should be covered")
	}
	if InspectionCoverageComplete(&cutoff, 1, 0) || InspectionCoverageComplete(&cutoff, 0, 1) {
		t.Fatal("pre-cutoff or unverified calls must block inspection coverage")
	}
	if WindowCoverageComplete(nil, cutoff, 0) || InspectionCoverageComplete(nil, 0, 0) {
		t.Fatal("missing cutoff must not be treated as complete coverage")
	}
}
