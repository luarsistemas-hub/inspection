package session

import (
	"strings"
	"testing"

	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/security"
)

func TestOTPCodeAcceptanceHonorsStageAndFormat(t *testing.T) {
	pepper := []byte("01234567890123456789012345678901")
	hash, err := security.HashOTP("123456", pepper)
	if err != nil {
		t.Fatal(err)
	}

	production := Service{Stage: "PRODUCTION", Pepper: pepper}
	if production.acceptsOTPCode("654321", hash[:]) {
		t.Fatal("non-matching production code accepted")
	}
	if production.acceptsOTPCode("123456", hash[:]) == false {
		t.Fatal("matching production code rejected")
	}

	for _, code := range []string{"12345", "1234567", "12345a", " 123456", "123456 "} {
		if production.acceptsOTPCode(code, hash[:]) {
			t.Fatalf("invalid production code accepted: %q", code)
		}
	}

	for _, stage := range []string{"dev", "staging", "test"} {
		nonProduction := Service{Stage: stage, Pepper: pepper}
		if nonProduction.sendsOTPEmail() {
			t.Fatalf("non-production stage would send OTP email: %q", stage)
		}
		if !nonProduction.acceptsOTPCode("654321", hash[:]) {
			t.Fatalf("six-digit non-production code rejected for stage %q", stage)
		}
		if nonProduction.acceptsOTPCode("12345a", hash[:]) {
			t.Fatalf("invalid non-production code accepted for stage %q", stage)
		}
	}

	if !(Service{}).sendsOTPEmail() {
		t.Fatal("missing stage did not default to production behavior")
	}
}

func TestRateLimitEnforcementHonorsStage(t *testing.T) {
	for _, stage := range []string{"production", "PRODUCTION", " production "} {
		if !(Service{Stage: stage}).enforcesRateLimit() {
			t.Fatalf("production stage %q bypassed rate limit", stage)
		}
	}

	for _, stage := range []string{"dev", "staging", "test"} {
		if (Service{Stage: stage}).enforcesRateLimit() {
			t.Fatalf("non-production stage %q enforced rate limit", stage)
		}
	}

	if !(Service{}).enforcesRateLimit() {
		t.Fatal("missing stage did not default to production behavior")
	}
}

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

func TestNormalizeEmailPreservesDotsAndPlusSuffix(t *testing.T) {
	got, err := NormalizeEmail(" User.Name+trial@Example.COM ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "user.name+trial@example.com" {
		t.Fatalf("normalized email = %q", got)
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
