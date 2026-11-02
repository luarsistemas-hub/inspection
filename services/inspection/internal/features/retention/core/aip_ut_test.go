package core

import (
	"testing"
	"time"
)

// TestAIP_UT_044RetentionStartsClassClockFromClosureAndComputesDueInstant covers
// UT-044 of autonomous-inspection-platform: each class counts from the
// authoritative closure (or its own security expiry) and has one due instant.
func TestAIP_UT_044RetentionStartsClassClockFromClosureAndComputesDueInstant(t *testing.T) {
	closure := time.Date(2026, 3, 10, 12, 30, 0, 0, time.UTC)
	policy := DefaultPolicy()
	evidence, err := NewClock(EvidenceReports, closure, time.Time{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if !evidence.StartsAt.Equal(closure) || !evidence.DueAt.Equal(closure.Add(5*365*24*time.Hour)) || evidence.Class != EvidenceReports {
		t.Fatalf("evidence clock = %+v, want five years from closure", evidence)
	}
	operational, err := NewClock(Operational, closure, time.Time{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if !operational.StartsAt.Equal(closure) || !operational.DueAt.Equal(closure.Add(365*24*time.Hour)) {
		t.Fatalf("operational clock = %+v, want one year from closure", operational)
	}
	expiry := closure.Add(90 * time.Minute)
	secret, err := NewClock(Secrets, closure, expiry, policy)
	if err != nil || !secret.StartsAt.Equal(expiry) || !secret.DueAt.Equal(expiry) {
		t.Fatalf("security clock = %+v (%v), want its own expiry", secret, err)
	}
	// A configured tenant policy changes the due instant, never the start.
	custom := Policy{EvidenceReports: 10 * 24 * time.Hour, Operational: 2 * 24 * time.Hour}
	shortened, err := NewClock(EvidenceReports, closure, time.Time{}, custom)
	if err != nil || !shortened.StartsAt.Equal(closure) || !shortened.DueAt.Equal(closure.Add(10*24*time.Hour)) {
		t.Fatalf("custom evidence clock = %+v (%v)", shortened, err)
	}
	// Eligibility flips exactly at the due instant.
	if evidence.Eligible(evidence.DueAt.Add(-time.Nanosecond)) || !evidence.Eligible(evidence.DueAt) {
		t.Fatal("eligibility does not match the due instant")
	}
	for name, build := range map[string]func() (Clock, error){
		"missing closure":         func() (Clock, error) { return NewClock(EvidenceReports, time.Time{}, time.Time{}, policy) },
		"missing security expiry": func() (Clock, error) { return NewClock(Secrets, closure, time.Time{}, policy) },
		"unknown class":           func() (Clock, error) { return NewClock("UNKNOWN", closure, time.Time{}, policy) },
		"non-positive policy":     func() (Clock, error) { return NewClock(EvidenceReports, closure, time.Time{}, Policy{}) },
	} {
		if _, err := build(); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}
