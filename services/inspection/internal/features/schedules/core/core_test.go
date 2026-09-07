package core

import (
	"testing"
	"time"
)

func TestUT028UT029DailyOrSlowerRecurrenceAndDST(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2024, 3, 9, 2, 30, 0, 0, location)
	for _, rule := range []string{"FREQ=DAILY", "FREQ=WEEKLY", "FREQ=MONTHLY", "FREQ=YEARLY"} {
		if _, err := ParseRecurrence(rule, location.String(), start); err != nil {
			t.Fatalf("%s rejected: %v", rule, err)
		}
	}
	for _, test := range []struct{ rule, zone string }{{"FREQ=HOURLY", location.String()}, {"FREQ=MINUTELY", location.String()}, {"FREQ=DAILY", "Invalid/Timezone"}, {"not-an-rrule", location.String()}} {
		if _, err := ParseRecurrence(test.rule, test.zone, start); err == nil {
			t.Fatalf("invalid recurrence accepted: %+v", test)
		}
	}
	recurrence, err := ParseRecurrence("FREQ=DAILY", location.String(), start)
	if err != nil {
		t.Fatal(err)
	}
	next, err := recurrence.Next(start)
	if err != nil {
		t.Fatal(err)
	}
	if next.In(location).Day() != 10 || next.In(location).Hour() < 3 {
		t.Fatalf("DST gap was not shifted forward: %s", next)
	}
	overlapStart := time.Date(2024, 11, 2, 1, 30, 0, 0, location)
	overlap, err := ParseRecurrence("FREQ=DAILY", location.String(), overlapStart)
	if err != nil {
		t.Fatal(err)
	}
	first, err := overlap.Next(overlapStart)
	if err != nil {
		t.Fatal(err)
	}
	_, offset := first.Zone()
	if offset != -4*60*60 {
		t.Fatalf("DST overlap did not select earliest instant: %s", first)
	}
}

func TestUT050UT051ReminderSnapshotAndFutureRules(t *testing.T) {
	due := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	deadline := due.Add(24 * time.Hour)
	if err := validateOffsets(24*60, []int{60, 120, 180}); err != nil {
		t.Fatal(err)
	}
	for _, offsets := range [][]int{{1, 2, 3, 4}, {-1}, {24*60 + 1}, {1, 1}} {
		if validateOffsets(24*60, offsets) == nil {
			t.Fatalf("invalid reminder offsets accepted: %v", offsets)
		}
	}
	oldRule, _ := ParseRecurrence("FREQ=DAILY", "UTC", due)
	newRule, _ := ParseRecurrence("FREQ=WEEKLY", "UTC", due)
	oldNext, _ := oldRule.Next(due)
	newNext, _ := newRule.Next(due)
	if oldNext != due.Add(24*time.Hour) || newNext != due.Add(7*24*time.Hour) {
		t.Fatalf("future rules not independently resolved: %s %s", oldNext, newNext)
	}
	if deadline.Before(due) {
		t.Fatal("invalid fixture")
	}
}
