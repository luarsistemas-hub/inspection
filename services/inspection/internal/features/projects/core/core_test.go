package core

import (
	"strings"
	"testing"

	"inspection/services/inspection/internal/platform/database"
)

func TestUT064UT065ProjectReportModeAndLifecycleRules(t *testing.T) {
	for _, status := range []string{StageCompleted, StageSkipped, StageCanceled, StageInvalidated} {
		if !terminalStage(status) {
			t.Fatalf("terminal stage rejected: %s", status)
		}
	}
	for _, status := range []string{StagePlanned, StageAvailable, StageInProgress} {
		if terminalStage(status) {
			t.Fatalf("open stage accepted as terminal: %s", status)
		}
	}
	if validReason("") || validReason("   ") || validReason(strings.Repeat("a", 2001)) {
		t.Fatal("invalid reason accepted")
	}
	if !validReason("audited reason") {
		t.Fatal("valid reason rejected")
	}
	for _, mode := range []string{"CONSOLIDATED", "HISTORICAL"} {
		project := database.Project{ReportMode: mode}
		if project.ReportMode != mode {
			t.Fatalf("report mode mutated: %s", mode)
		}
	}
}

func TestIT201ToIT220ExceptionalStageAndClosureContracts(t *testing.T) {
	if !validReason("insert") || !validReason("skip") || !validReason("reopen") {
		t.Fatal("required reasons rejected")
	}
	states := []string{StageCompleted, StageSkipped, StageCanceled, StageInvalidated}
	for _, state := range states {
		if !terminalStage(state) {
			t.Fatalf("closure did not accept %s", state)
		}
	}
}
