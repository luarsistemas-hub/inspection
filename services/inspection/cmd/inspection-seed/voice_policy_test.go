package main

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/transcription/configure_policy"
	"inspection/services/inspection/internal/features/transcription/core"
)

type voicePolicyStub struct {
	policy core.Policy
	exists bool
	calls  []configure_policy.ConfigureInput
}

func (s *voicePolicyStub) CurrentPolicy(context.Context, identity.ID) (core.Policy, bool, error) {
	return s.policy, s.exists, nil
}

func (s *voicePolicyStub) Configure(_ context.Context, in configure_policy.ConfigureInput) (core.Policy, error) {
	s.calls = append(s.calls, in)
	s.policy = in.Policy
	s.policy.Version = in.ExpectedVersion + 1
	s.exists = true
	return s.policy, nil
}

func TestApplyQAVoicePolicyIsIdempotentAndUsesExpectedCeilings(t *testing.T) {
	tenantID := identity.NewID()
	stub := &voicePolicyStub{}
	for range 2 {
		if err := applyQAVoicePolicy(context.Background(), tenantID, stub, stub); err != nil {
			t.Fatal(err)
		}
	}
	if len(stub.calls) != 1 {
		t.Fatalf("configure calls=%d, want 1", len(stub.calls))
	}
	call := stub.calls[0]
	if call.TenantID != tenantID || call.ExpectedVersion != 0 || call.ClientMutationID != "qa-seed-voice-v1-v0" {
		t.Fatalf("unexpected configure input: %+v", call)
	}
	want := core.Policy{
		Enabled:                true,
		TenantNoteLimit:        500,
		TenantAudioLimitMS:     300 * 60 * 1000,
		InspectionNoteLimit:    20,
		InspectionAudioLimitMS: 30 * 60 * 1000,
	}
	if !samePolicyLimits(call.Policy, want) {
		t.Fatalf("policy=%+v, want %+v", call.Policy, want)
	}
}
