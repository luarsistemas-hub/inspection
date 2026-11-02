package core

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultPolicyAndLegalHold(t *testing.T) {
	now := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	clock, err := NewClock(EvidenceReports, now, time.Time{}, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if clock.Eligible(now.Add(4 * 365 * 24 * time.Hour)) {
		t.Fatal("premature deletion allowed")
	}
	clock.LegalHold = true
	if clock.Eligible(now.Add(6 * 365 * 24 * time.Hour)) {
		t.Fatal("legal hold ignored")
	}
}

func TestSecuritySecretsUseTheirOwnExpiry(t *testing.T) {
	expiry := time.Date(2026, 9, 6, 1, 0, 0, 0, time.UTC)
	clock, err := NewClock(Secrets, time.Time{}, expiry, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !clock.DueAt.Equal(expiry) {
		t.Fatalf("due %s want %s", clock.DueAt, expiry)
	}
}

func TestPrematureRequestIsRefusedAndManifestIsCompleteOnlyAfterAllArtifacts(t *testing.T) {
	now := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	clock, err := NewClock(EvidenceReports, now, time.Time{}, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if request := RequestDeletion(clock, now); !request.Refused || request.Restriction == "" {
		t.Fatalf("unexpected request: %#v", request)
	}
	if (Manifest{Originals: true, Parts: true, Derivatives: true, Reports: true}).Complete() {
		t.Fatal("manifest completed without association purge")
	}
}

// TestDefaultClocksForEveryDataClass pins the platform defaults of US-031 AC-2:
// five years for evidence and reports, one year for operational records and
// location, and the security deadline itself for secrets.
func TestDefaultClocksForEveryDataClass(t *testing.T) {
	closure := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	expiry := closure.Add(15 * time.Minute)
	for _, tc := range []struct {
		class string
		want  time.Time
	}{
		{EvidenceReports, closure.Add(5 * 365 * 24 * time.Hour)},
		{Operational, closure.Add(365 * 24 * time.Hour)},
		{Secrets, expiry},
	} {
		clock, err := NewClock(tc.class, closure, expiry, DefaultPolicy())
		if err != nil {
			t.Fatalf("%s: %v", tc.class, err)
		}
		if !clock.DueAt.Equal(tc.want) {
			t.Fatalf("%s due %s, want %s", tc.class, clock.DueAt, tc.want)
		}
		if clock.Eligible(tc.want.Add(-time.Nanosecond)) || !clock.Eligible(tc.want) {
			t.Fatalf("%s eligibility must start exactly at its due instant", tc.class)
		}
	}
}

func TestClockRejectsInvalidInputsAndUnknownClasses(t *testing.T) {
	closure := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := NewClock(EvidenceReports, time.Time{}, time.Time{}, DefaultPolicy()); err == nil {
		t.Fatal("clock without an authoritative closure accepted")
	}
	if _, err := NewClock(Secrets, closure, time.Time{}, DefaultPolicy()); err == nil {
		t.Fatal("secret clock without its security expiry accepted")
	}
	if _, err := NewClock("UNKNOWN", closure, time.Time{}, DefaultPolicy()); err == nil {
		t.Fatal("unknown retention class accepted")
	}
	if _, err := NewClock(EvidenceReports, closure, time.Time{}, Policy{EvidenceReports: -time.Hour, Operational: time.Hour}); err == nil {
		t.Fatal("negative policy period accepted")
	}
}

func TestClosureSQLCountsProjectInspectionsFromProjectClosure(t *testing.T) {
	sql := ClosureSQL("i")
	for _, want := range []string{"i.status NOT IN ('COMPLETED','CANCELED','INVALIDATED') THEN NULL", "i.project_id IS NULL THEN i.updated_at", "projects.projects p", "p.status='CLOSED'", "GREATEST(COALESCE(p.closed_at, p.updated_at), i.updated_at)"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("ClosureSQL = %q, missing %q", sql, want)
		}
	}
	if !strings.Contains(ClosureSQL("x"), "x.project_id") || !strings.Contains(ClosureSQL("x"), "x.status") || strings.Contains(ClosureSQL("x"), "i.") {
		t.Fatalf("ClosureSQL ignores its alias: %q", ClosureSQL("x"))
	}
}

func TestSecurityGraceAndCutoffs(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	policy := Policy{EvidenceReports: time.Hour, Operational: time.Hour, Security: 30 * 24 * time.Hour}
	if got := SecretsCutoff(now, policy); !got.Equal(now.AddDate(0, 0, -30)) {
		t.Fatalf("SecretsCutoff = %s, want 30 days before now", got)
	}
	if got := SecretsCutoff(now, DefaultPolicy()); !got.Equal(now) {
		t.Fatalf("a zero grace keeps the platform security deadline, got cutoff %s", got)
	}
	if err := (Policy{EvidenceReports: time.Hour, Operational: time.Hour, Security: -time.Hour}).Validate(); err == nil {
		t.Fatal("a negative security grace was accepted")
	}
}
