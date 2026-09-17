package admin_activation

import (
	"context"
	"testing"

	"inspection/services/inspection/internal/features/onboarding/coordinator"
	"inspection/services/inspection/internal/features/onboarding/session"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestOTPCodeAcceptanceHonorsStageAndFormat(t *testing.T) {
	pepper := []byte("01234567890123456789012345678901")
	hash, err := security.HashOTP("123456", pepper)
	if err != nil {
		t.Fatal(err)
	}

	production := Service{Stage: "production", Pepper: pepper}
	if production.acceptsOTPCode("654321", hash[:]) {
		t.Fatal("non-matching production code accepted")
	}
	if !production.acceptsOTPCode("123456", hash[:]) {
		t.Fatal("matching production code rejected")
	}

	for _, code := range []string{"12345", "1234567", "12345a", " 123456", "123456 "} {
		if production.acceptsOTPCode(code, hash[:]) {
			t.Fatalf("invalid production code accepted: %q", code)
		}
	}

	for _, stage := range []string{"dev", "staging", "test"} {
		nonProduction := Service{Stage: stage, Pepper: pepper}
		if !nonProduction.acceptsOTPCode("654321", hash[:]) {
			t.Fatalf("six-digit non-production code rejected for stage %q", stage)
		}
		if nonProduction.acceptsOTPCode("12345a", hash[:]) {
			t.Fatalf("invalid non-production code accepted for stage %q", stage)
		}
	}

	if !((Service{Pepper: pepper}).acceptsOTPCode("123456", hash[:])) {
		t.Fatal("missing stage did not default to production behavior")
	}
}

func TestActivationSessionStateAcceptsActiveTenantBoundOnboardingStates(t *testing.T) {
	for _, state := range []string{
		session.StateVerified,
		coordinator.StateAgencySaved,
		coordinator.StatePropertySaved,
		coordinator.StateParticipantSaved,
		coordinator.StateReadyToSubmit,
		coordinator.StateSubmitted,
	} {
		if !activationSessionState(state) {
			t.Errorf("activation state %q rejected", state)
		}
	}
	for _, state := range []string{session.StatePending, "EXPIRED", "CANCELED", ""} {
		if activationSessionState(state) {
			t.Errorf("inactive state %q accepted", state)
		}
	}
}

func TestValidateCSRFRejectsMissingOrWrongProof(t *testing.T) {
	pepper := []byte("01234567890123456789012345678901")
	locator := "session-locator"
	nonce := "csrf-nonce"
	proof, err := security.CSRFProof(security.HashToken(locator), nonce, pepper)
	if err != nil {
		t.Fatal(err)
	}

	if err := validateCSRF(locator, nonce, proof[:], pepper); err != nil {
		t.Fatalf("valid proof rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		csrf   string
		digest []byte
	}{
		{name: "missing token", csrf: "", digest: proof[:]},
		{name: "wrong token", csrf: "other", digest: proof[:]},
		{name: "wrong digest length", csrf: nonce, digest: []byte("short")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateCSRF(locator, tc.csrf, tc.digest, pepper); err == nil {
				t.Fatal("invalid proof accepted")
			}
		})
	}
}

func TestClaimInvitationRejectsMalformedTokenBeforeDatabaseAccess(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	service := Service{DB: db, Pepper: []byte("01234567890123456789012345678901")}
	_, err = service.ClaimInvitation(context.Background(), "short")
	if err == nil {
		t.Fatal("malformed activation token accepted")
	}
}
