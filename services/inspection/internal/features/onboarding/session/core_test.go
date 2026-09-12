package session

import (
	"strings"
	"testing"

	"inspection/services/inspection/internal/platform/apperror"
)

func TestValidateOwnerNormalizesEmailAndRejectsMalformedInput(t *testing.T) {
	owner, err := ValidateOwner(" Ana ", " ANA@example.com ")
	if err != nil || owner.Email != "ana@example.com" || owner.Name != "Ana" {
		t.Fatalf("owner = %#v, err = %v", owner, err)
	}
	if _, err := ValidateOwner("", "ana@example.com"); err == nil {
		t.Fatal("blank name accepted")
	}
	if _, err := ValidateOwner("Ana", "bad"); err == nil {
		t.Fatal("malformed email accepted")
	}
}

func TestOnboardingPurposeAndCodeBoundaries(t *testing.T) {
	if PurposeOnboarding == PurposeActivation {
		t.Fatal("onboarding and activation purposes must be distinct")
	}
	for i := 0; i < 20; i++ {
		code, err := newCode()
		if err != nil || len(code) != 6 || strings.Trim(code, "0123456789") != "" {
			t.Fatalf("invalid OTP %q: %v", code, err)
		}
	}
}

func TestStepRankIsMonotonic(t *testing.T) {
	steps := []string{StepIdentity, StepAgency, StepProperty, StepOrigin, StepParticipant, StepReady}
	for i := 1; i < len(steps); i++ {
		if stepRank(steps[i]) <= stepRank(steps[i-1]) {
			t.Fatalf("step %s did not advance", steps[i])
		}
	}
}

func TestMarshalCheckpointPayloadRejectsOversizedPayload(t *testing.T) {
	_, err := marshalCheckpointPayload(map[string]any{"agencyName": strings.Repeat("x", maxCheckpointPayloadBytes)})
	if err == nil {
		t.Fatal("oversized checkpoint payload accepted")
	}
	if code, field, _ := apperror.Public(err); code != apperror.InvalidInput || field != "payload" {
		t.Fatalf("unexpected oversized payload error: code=%v field=%q", code, field)
	}
}

func TestRequestOTPLimitKeysCoverEmailAndIPDimensions(t *testing.T) {
	keys := requestOTPLimitKeys("ana@example.com", "192.0.2.10")
	if len(keys) != 2 || keys[0] == keys[1] || keys[0] != "email:ana@example.com" || keys[1] != "ip:192.0.2.10" {
		t.Fatalf("unexpected limit keys: %#v", keys)
	}
}
