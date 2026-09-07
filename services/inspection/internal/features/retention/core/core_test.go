package core

import (
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
