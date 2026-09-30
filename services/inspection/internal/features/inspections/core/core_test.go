package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
)

func TestUT022UT023InspectionStateMachine(t *testing.T) {
	valid := [][2]string{{"PLANNED", "INVITED"}, {"INVITED", "IN_PROGRESS"}, {"IN_PROGRESS", "SUBMITTED"}, {"SUBMITTED", "ANALYZING"}, {"ANALYZING", "RECAPTURE_PENDING"}, {"RECAPTURE_PENDING", "ANALYZING"}, {"ANALYZING", "COMPLETED"}}
	for _, transition := range valid {
		if err := ValidateInspectionTransition(transition[0], transition[1], 0, ""); err != nil {
			t.Fatalf("%v: %v", transition, err)
		}
	}
	for _, transition := range [][2]string{{"PLANNED", "COMPLETED"}, {"COMPLETED", "PLANNED"}, {"CANCELED", "CANCELED"}, {"INVALIDATED", "INVALIDATED"}} {
		if ValidateInspectionTransition(transition[0], transition[1], 0, "") == nil {
			t.Fatalf("invalid transition accepted: %v", transition)
		}
	}
	if err := ValidateInspectionTransition("IN_PROGRESS", "CANCELED", 1, ""); err == nil {
		t.Fatal("cancellation after evidence accepted")
	}
	if err := ValidateInspectionTransition("SUBMITTED", "INVALIDATED", 1, "reason"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateInspectionTransition("SUBMITTED", "INVALIDATED", 1, ""); err == nil {
		t.Fatal("blank invalidation reason accepted")
	}
}

func TestInspectionListFiltersAndSearchEscaping(t *testing.T) {
	if !validInspectionStatusGroup("") || !validInspectionStatusGroup("COMPLETED") || validInspectionStatusGroup("UNKNOWN") {
		t.Fatal("inspection status group validation returned an unexpected result")
	}
	if got := statusesForGroup("PLANNING"); strings.Join(got, ",") != "PLANNED,INVITED" {
		t.Fatalf("planning statuses = %v", got)
	}
	if got := statusesForGroup("EXECUTION"); strings.Join(got, ",") != "IN_PROGRESS,SUBMITTED,ANALYZING,RECAPTURE_PENDING" {
		t.Fatalf("execution statuses = %v", got)
	}
	if got := statusesForGroup("COMPLETED"); strings.Join(got, ",") != "COMPLETED" {
		t.Fatalf("completed statuses = %v", got)
	}
	if got := statusesForGroup("CLOSED"); strings.Join(got, ",") != "CANCELED,INVALIDATED" {
		t.Fatalf("closed statuses = %v", got)
	}
	if got := escapeInspectionSearch(`Elvio%_\\`); got != `Elvio\%\_\\\\` {
		t.Fatalf("escaped search = %q", got)
	}
}

func TestIT121ToIT140ReminderAndLifecycleBoundaries(t *testing.T) {
	due := time.Now().UTC().Truncate(time.Second)
	deadline := due.Add(time.Hour)
	values, err := NormalizeReminders([]time.Time{due.Add(10 * time.Minute), due.Add(10 * time.Minute), due.Add(20 * time.Minute)}, due, deadline)
	if err != nil || len(values) != 2 {
		t.Fatalf("reminder normalization: %v %v", values, err)
	}
	for _, values := range [][]time.Time{{due.Add(-time.Second)}, {deadline.Add(time.Second)}, {due, due.Add(time.Minute), due.Add(2 * time.Minute), due.Add(3 * time.Minute)}} {
		if _, err := NormalizeReminders(values, due, deadline); err == nil {
			t.Fatalf("invalid reminders accepted: %v", values)
		}
	}
	if _, err := (Service{}).Invalidate(context.Background(), identity.NewID(), identity.NewID(), 1, strings.Repeat("a", 2001)); err == nil {
		t.Fatal("oversized lifecycle reason accepted")
	}
}
