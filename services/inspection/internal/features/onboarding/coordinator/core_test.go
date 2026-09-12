package coordinator

import (
	"testing"
	"time"

	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/apperror"
)

func TestValidateSubmitTruthfulModesAndDigest(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	base := SubmitInput{Now: now, Steps: map[string]StepPayload{
		"AGENCY": {}, "PROPERTY": {"address": "Rua A", "purpose": "SALE", "rooms": "1", "deadlineAt": now.Add(7 * 24 * time.Hour).Format(time.RFC3339)},
		"ORIGIN": {}, "PARTICIPANT": {"self": true},
	}, TemplateMode: catalog.FixedOrigin, OriginStatus: "PENDING", DeliveryState: "READY", IdempotencyKey: "submit-1"}
	result, err := ValidateSubmit(base)
	if err != nil || result.NextAction != NextActionWaitForOrigin || result.State != StateOriginPending {
		t.Fatalf("pending origin = %+v, %v", result, err)
	}
	base.TemplateMode, base.OriginStatus = catalog.ChecklistOnly, "NONE"
	first, err := ValidateSubmit(base)
	if err != nil || first.Digest == "" {
		t.Fatalf("checklist submit = %+v, %v", first, err)
	}
	second, err := ValidateSubmit(base)
	if err != nil || first.Digest != second.Digest {
		t.Fatalf("digest not stable: %q %q %v", first.Digest, second.Digest, err)
	}
}

func TestValidateSubmitRejectsExpiredPropertyDeadline(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	in := SubmitInput{
		Now: now,
		Steps: map[string]StepPayload{
			"AGENCY":   {},
			"PROPERTY": {"address": "Rua A", "purpose": "SALE", "rooms": "1", "deadlineAt": now.Add(-time.Minute).Format(time.RFC3339)},
			"ORIGIN":   {}, "PARTICIPANT": {"self": true},
		},
		TemplateMode: catalog.ChecklistOnly, OriginStatus: "NONE", DeliveryState: "READY", IdempotencyKey: "submit-expired",
	}
	if _, err := ValidateSubmit(in); err == nil || code(err) != apperror.InvalidInput {
		t.Fatalf("expired deadline accepted during submit: %v", err)
	}
}

func TestValidatePropertyAndDelegateBoundaries(t *testing.T) {
	if err := ValidateProperty(StepPayload{"address": "x", "purpose": "SALE", "rooms": "1", "deadlineAt": "2020-01-01T00:00:00Z"}, time.Now().UTC()); code(err) != apperror.InvalidInput {
		t.Fatalf("bad deadline code=%v", err)
	}
	if err := ValidateDelegate(StepPayload{"name": "", "email": "bad"}); code(err) != apperror.InvalidInput {
		t.Fatalf("bad delegate code=%v", err)
	}
	if err := ValidateOriginMedia("text/plain", 1, "x"); code(err) != apperror.InvalidInput {
		t.Fatalf("bad media code=%v", err)
	}
}

func TestValidatePropertyRejectsInvalidPurposeAndRooms(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	base := StepPayload{"address": "Rua A", "purpose": "SALE", "rooms": "1", "deadlineAt": now.Add(time.Hour).Format(time.RFC3339)}
	for field, value := range map[string]any{"purpose": "INVALID", "rooms": "0"} {
		payload := StepPayload{}
		for key, original := range base {
			payload[key] = original
		}
		payload[field] = value
		if err := ValidateProperty(payload, now); err == nil || code(err) != apperror.InvalidInput {
			t.Fatalf("invalid %s accepted: %v", field, err)
		}
	}
	for _, value := range []any{-1, 1.5, []any{"1"}, nil} {
		payload := StepPayload{}
		for key, original := range base {
			payload[key] = original
		}
		payload["rooms"] = value
		if err := ValidateProperty(payload, now); err == nil || code(err) != apperror.InvalidInput {
			t.Fatalf("invalid rooms %v accepted: %v", value, err)
		}
	}
}

func TestValidateSubmitAcceptsPublicSelfParticipantMode(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	in := SubmitInput{Now: now, Steps: map[string]StepPayload{
		"AGENCY": {}, "PROPERTY": {"address": "Rua A", "purpose": "SALE", "rooms": "1", "deadlineAt": now.Add(time.Hour).Format(time.RFC3339)},
		"ORIGIN": {"mode": "CHECKLIST_ONLY"}, "PARTICIPANT": {"mode": "SELF"},
	}, TemplateMode: catalog.ChecklistOnly, OriginStatus: "NONE", DeliveryState: "READY", IdempotencyKey: "submit-self"}
	if _, err := ValidateSubmit(in); err != nil {
		t.Fatalf("self participant rejected: %v", err)
	}
}

func TestValidateCheckpointPayloadAcceptsCatalogPropertyDeadline(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	payload := StepPayload{"address": "Rua A", "purpose": "SALE", "rooms": "2", "deadline": "2026-09-12"}
	if err := ValidateCheckpointPayload("PROPERTY", payload, now); err != nil {
		t.Fatalf("catalog property deadline rejected: %v", err)
	}
}

func TestValidateCheckpointOrderRejectsSkippedPrerequisites(t *testing.T) {
	if err := ValidateCheckpointOrder("AGENCY", "PARTICIPANT"); err == nil || code(err) != apperror.InvalidState {
		t.Fatalf("skipped participant prerequisite accepted: %v", err)
	}
	if err := ValidateCheckpointOrder("AGENCY", "PROPERTY"); err != nil {
		t.Fatalf("next checkpoint rejected: %v", err)
	}
}

func TestValidateCheckpointPayloadRejectsInvalidStepData(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name    string
		step    string
		payload StepPayload
	}{
		{"agency", "AGENCY", StepPayload{}},
		{"property", "PROPERTY", StepPayload{}},
		{"origin", "ORIGIN", StepPayload{"mode": "UNKNOWN"}},
		{"delegate", "PARTICIPANT", StepPayload{"mode": "DELEGATE", "name": "", "email": "bad"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateCheckpointPayload(tc.step, tc.payload, now); err == nil || code(err) != apperror.InvalidInput {
				t.Fatalf("invalid %s payload accepted: %v", tc.step, err)
			}
		})
	}
}

func TestValidateCheckpointPayloadAcceptsSupportedModes(t *testing.T) {
	if err := ValidateCheckpointPayload("ORIGIN", StepPayload{"mode": "fixed_origin"}, time.Now().UTC()); err != nil {
		t.Fatalf("fixed origin rejected: %v", err)
	}
	if err := ValidateCheckpointPayload("PARTICIPANT", StepPayload{"mode": "self"}, time.Now().UTC()); err != nil {
		t.Fatalf("self participant rejected: %v", err)
	}
}

func code(err error) apperror.Code { c, _, _ := apperror.Public(err); return c }
