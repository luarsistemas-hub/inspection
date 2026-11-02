package core

import "testing"
import "time"

func TestPolicyRequiresFourPositiveLimits(t *testing.T) {
	p := Policy{TenantNoteLimit: 100, TenantAudioLimitMS: 300_000, InspectionNoteLimit: 20, InspectionAudioLimitMS: 30_000}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.Enabled = true
	if err := p.CanReserve(0, 0, 0, 0, 30_000); err != nil {
		t.Fatal(err)
	}
	p.InspectionAudioLimitMS = 0
	if err := p.Validate(); err != ErrInvalidInput {
		t.Fatalf("Validate()=%v", err)
	}
}

func TestNoteRevisionIsAppendOnlyAndOptimistic(t *testing.T) {
	n := Note{Status: "COMPLETED", Version: 1, Revisions: []Revision{{Number: 1, Text: "cinco pratos", Kind: "MODEL"}}}
	if err := n.Revise(1, " cinco pratos ", time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if n.Version != 2 || len(n.Revisions) != 2 || n.Revisions[0].Text != "cinco pratos" || n.Revisions[1].Text != "cinco pratos" || n.Revisions[1].Kind != "HUMAN_EDIT" {
		t.Fatalf("unexpected revision history: %+v", n)
	}
	if err := n.Revise(1, "stale", time.Now()); err != ErrConflict {
		t.Fatalf("stale revision error=%v", err)
	}
	if err := n.ConfirmText(2); err != nil {
		t.Fatal(err)
	}
	if !n.TextConfirmed || n.Version != 3 {
		t.Fatalf("confirmation changed incorrect state: %+v", n)
	}
}

func TestNoteRetryRequiresAvailableAudioAndDiscardIsTerminal(t *testing.T) {
	n := Note{Status: "FAILED", Version: 4, AudioExpired: true}
	if err := n.Retry(4); err != ErrInvalidState {
		t.Fatalf("expired audio retry=%v", err)
	}
	n.AudioExpired, n.Status = false, "FAILED"
	if err := n.Retry(4); err != nil || n.Status != "QUEUED" {
		t.Fatalf("retry status=%s error=%v", n.Status, err)
	}
	if err := n.Discard(5); err != nil || n.Status != "CANCELED" {
		t.Fatalf("discard status=%s error=%v", n.Status, err)
	}
	if err := n.SetHighlighted(6, true); err != ErrInvalidState {
		t.Fatalf("highlight discarded note=%v", err)
	}
}

func TestCanReserveEnforcesTenantAndInspectionCeilings(t *testing.T) {
	p := Policy{Enabled: true, TenantNoteLimit: 20, TenantAudioLimitMS: 300_000, InspectionNoteLimit: 20, InspectionAudioLimitMS: 30_000}
	if err := p.CanReserve(19, 290_000, 19, 20_000, 10_000); err != nil {
		t.Fatal(err)
	}
	if err := p.CanReserve(20, 290_000, 19, 20_000, 10_000); err != ErrLimitReached {
		t.Fatalf("tenant limit error=%v", err)
	}
	if err := p.CanReserve(0, 0, 20, 20_000, 10_000); err != ErrLimitReached {
		t.Fatalf("inspection limit error=%v", err)
	}
}

func TestValidateAnchorSeparatesPhotoAndVideo(t *testing.T) {
	start, end := int64(1_000), int64(4_000)
	if err := ValidateAnchor("PHOTO", "photo", "", nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAnchor("VIDEO_INTERVAL", "", "attempt", &start, &end, "CAPTURE_CLOCK"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAnchor("PHOTO", "photo", "", &start, &end, "CAPTURE_CLOCK"); err != ErrInvalidInput {
		t.Fatalf("mixed anchor error=%v", err)
	}
}
