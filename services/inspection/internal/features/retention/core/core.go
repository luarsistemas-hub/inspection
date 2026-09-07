// Package core owns retention clock calculation and purge eligibility rules.
package core

import (
	"fmt"
	"time"
)

const (
	EvidenceReports = "EVIDENCE_REPORTS"
	Operational     = "OPERATIONAL_LOCATION"
	Secrets         = "SECURITY_SECRETS"
)

type Policy struct{ EvidenceReports, Operational time.Duration }
type Clock struct {
	Class           string
	StartsAt, DueAt time.Time
	LegalHold       bool
}

// DeletionRequest explains a refusal without making data disappear early.
type DeletionRequest struct {
	RequestedAt time.Time
	Refused     bool
	Restriction string
}

func RequestDeletion(clock Clock, now time.Time) DeletionRequest {
	if clock.Eligible(now) {
		return DeletionRequest{RequestedAt: now}
	}
	restriction := "retention period has not elapsed"
	if clock.LegalHold {
		restriction = "legal hold prevents deletion"
	}
	return DeletionRequest{RequestedAt: now, Refused: true, Restriction: restriction}
}

// Manifest lists every artifact class a purge must reconcile. Completion is
// idempotent only after all required classes have been attempted.
type Manifest struct{ Originals, Parts, Derivatives, Reports, Associations bool }

func (m Manifest) Complete() bool {
	return m.Originals && m.Parts && m.Derivatives && m.Reports && m.Associations
}

func DefaultPolicy() Policy {
	return Policy{EvidenceReports: 5 * 365 * 24 * time.Hour, Operational: 365 * 24 * time.Hour}
}

func (p Policy) Validate() error {
	if p.EvidenceReports <= 0 || p.Operational <= 0 {
		return fmt.Errorf("retention periods must be positive")
	}
	return nil
}

// NewClock uses the authoritative closure instant. Security secrets use their
// own expiry instant and may never be extended through a general policy.
func NewClock(class string, closure, secretExpiry time.Time, policy Policy) (Clock, error) {
	if err := policy.Validate(); err != nil {
		return Clock{}, err
	}
	if class == Secrets {
		if secretExpiry.IsZero() {
			return Clock{}, fmt.Errorf("security expiry is required")
		}
		return Clock{Class: class, StartsAt: secretExpiry, DueAt: secretExpiry}, nil
	}
	if closure.IsZero() {
		return Clock{}, fmt.Errorf("authoritative closure is required")
	}
	duration := policy.EvidenceReports
	if class == Operational {
		duration = policy.Operational
	} else if class != EvidenceReports {
		return Clock{}, fmt.Errorf("unknown retention class")
	}
	return Clock{Class: class, StartsAt: closure, DueAt: closure.Add(duration)}, nil
}

// Eligible refuses premature deletion and legal-hold deletion. A repeated
// purge may safely call this again after prior derivatives are already absent.
func (clock Clock) Eligible(now time.Time) bool { return !clock.LegalHold && !now.Before(clock.DueAt) }
