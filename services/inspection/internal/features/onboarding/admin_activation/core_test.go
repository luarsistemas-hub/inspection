package admin_activation

import (
	"testing"

	"inspection/services/inspection/internal/features/onboarding/coordinator"
	"inspection/services/inspection/internal/features/onboarding/session"
	"inspection/services/inspection/internal/platform/security"
)

func TestActivationSessionStateAcceptsActiveTenantBoundOnboardingStates(t *testing.T) {
	for _, state := range []string{
		session.StateVerified,
		coordinator.StateAgencySaved,
		coordinator.StatePropertySaved,
		coordinator.StateParticipantSaved,
		coordinator.StateReadyToSubmit,
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
