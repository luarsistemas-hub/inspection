package events

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
)

func TestUT040EnvelopeRoundTrip(t *testing.T) {
	e := Envelope[map[string]string]{ID: identity.NewID(), Type: "participant.channel_verified.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: identity.NewID(), AggregateID: identity.NewID(), CorrelationID: "corr", Payload: map[string]string{"participantId": identity.NewID().String()}}
	raw, _ := json.Marshal(e)
	got, err := DefaultRegistry().Validate(raw)
	if err != nil || got.ID != e.ID {
		t.Fatalf("round trip failed: %v", err)
	}
}
func TestUT041RegistryRejectsUnsafeContracts(t *testing.T) {
	base := Envelope[map[string]string]{ID: identity.NewID(), Type: "participant.channel_verified.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: identity.NewID(), CorrelationID: "corr", Payload: map[string]string{"participantId": "p"}}
	cases := []func(*Envelope[map[string]string]){func(e *Envelope[map[string]string]) { e.SchemaVersion = 2 }, func(e *Envelope[map[string]string]) { e.CorrelationID = "" }, func(e *Envelope[map[string]string]) { e.Payload = map[string]string{"otp": "123456"} }}
	for _, mutate := range cases {
		e := base
		mutate(&e)
		raw, _ := json.Marshal(e)
		if _, err := DefaultRegistry().Validate(raw); err == nil {
			t.Fatal("expected rejection")
		}
	}
	if _, err := DefaultRegistry().Validate(make([]byte, MaxPayloadBytes+1)); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("expected size error: %v", err)
	}
}

func TestInventoryEventContracts_IT039(t *testing.T) {
	valid := map[string]map[string]any{
		"inventory.job_requested.v1":               {"jobId": identity.NewID(), "inspectionId": identity.NewID()},
		"inventory.draft_ready.v1":                 {"originVersionId": identity.NewID()},
		"origin.inspection_promotion_requested.v1": {"promotionId": identity.NewID()},
		"inventory.version_activated.v1":           {"originVersionId": identity.NewID()},
		"counterparty.responded.v1":                {"counterpartyInvitationId": identity.NewID(), "originVersionId": identity.NewID()},
		"capture.environment_removed.v1":           {"responsibilityId": identity.NewID(), "requirementKey": "env:new:example"},
	}
	for eventType, goodPayload := range valid {
		t.Run(eventType, func(t *testing.T) {
			payload := map[string]any{}
			for key, value := range goodPayload {
				payload[key] = value
			}
			raw := eventJSON(t, eventType, payload)
			if _, err := DefaultRegistry().Validate(raw); err != nil {
				t.Fatalf("registered id-only payload rejected: %v", err)
			}
			payload["otp"] = "123456"
			if _, err := DefaultRegistry().Validate(eventJSON(t, eventType, payload)); !errors.Is(err, ErrInvalidEnvelope) {
				t.Fatalf("secret field accepted: %v", err)
			}
			if _, err := DefaultRegistry().Validate(eventJSON(t, eventType, map[string]any{})); !errors.Is(err, ErrInvalidEnvelope) {
				t.Fatalf("payload missing required id accepted: %v", err)
			}
		})
	}
	t.Run("v2 capture clips payload", func(t *testing.T) {
		payload := CaptureVideoSubmittedV2Payload{InspectionID: identity.NewID(), ResponsibilityID: identity.NewID(), SubmissionVersionID: identity.NewID(), Clips: []CaptureVideoClip{{RequirementKey: "env:room", MediaID: identity.NewID()}}}
		raw := eventJSON(t, "capture.video_submitted.v2", payload)
		if _, err := DefaultRegistry().Validate(raw); err != nil {
			t.Fatal(err)
		}
		payload.Clips = append(payload.Clips, CaptureVideoClip{RequirementKey: "env:room", MediaID: identity.NewID()})
		if _, err := DefaultRegistry().Validate(eventJSON(t, "capture.video_submitted.v2", payload)); err != nil {
			t.Fatalf("multiple clips for the same environment were rejected: %v", err)
		}
		payload.Clips[1].MediaID = payload.Clips[0].MediaID
		if _, err := DefaultRegistry().Validate(eventJSON(t, "capture.video_submitted.v2", payload)); !errors.Is(err, ErrInvalidEnvelope) {
			t.Fatalf("duplicate media in clips accepted: %v", err)
		}
	})
}

func eventJSON(t *testing.T, eventType string, payload any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"id": identity.NewID(), "type": eventType, "schemaVersion": eventVersion(eventType), "occurredAt": time.Now().UTC(), "tenantId": identity.NewID(), "correlationId": "it039", "payload": payload})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func eventVersion(eventType string) int {
	if strings.HasSuffix(eventType, ".v2") {
		return 2
	}
	return 1
}

func TestNotificationDeliveryV2IsReferenceOnly(t *testing.T) {
	envelope := Envelope[map[string]string]{ID: identity.NewID(), Type: "notification.delivery_requested.v2", SchemaVersion: 2, OccurredAt: time.Now().UTC(), TenantID: identity.NewID(), AggregateID: identity.NewID(), CorrelationID: "corr", Payload: map[string]string{"notificationId": identity.NewID().String()}}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DefaultRegistry().Validate(raw); err != nil {
		t.Fatalf("v2 reference event rejected: %v", err)
	}
	envelope.SchemaVersion = 1
	raw, _ = json.Marshal(envelope)
	if _, err := DefaultRegistry().Validate(raw); !errors.Is(err, ErrUnknownContract) {
		t.Fatalf("wrong v2 version accepted: %v", err)
	}
	for _, eventType := range []string{"notification.delivery_requested.v1", "notification.channel_status.v1"} {
		envelope.Type = eventType
		envelope.SchemaVersion = 1
		raw, _ = json.Marshal(envelope)
		if _, err := DefaultRegistry().Validate(raw); !errors.Is(err, ErrUnknownContract) {
			t.Fatalf("removed contract %s accepted: %v", eventType, err)
		}
	}
}

func TestVideoPreparedRequiresBoundedReferencePayloadUT072UT073(t *testing.T) {
	envelope := Envelope[VideoPreparedPayload]{ID: identity.NewID(), Type: "media.video_prepared.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: identity.NewID(), AggregateID: identity.NewID(), CorrelationID: "video-preparation", Payload: VideoPreparedPayload{MediaID: identity.NewID(), PreparationID: identity.NewID(), ManifestDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DefaultRegistry().Validate(raw); err != nil {
		t.Fatalf("valid preparation event rejected: %v", err)
	}
	envelope.Payload.PreparationID = identity.ID{}
	raw, _ = json.Marshal(envelope)
	if _, err := DefaultRegistry().Validate(raw); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("missing preparation id accepted: %v", err)
	}
	envelope.Payload.PreparationID = identity.NewID()
	envelope.Payload.ManifestDigest = "short"
	raw, _ = json.Marshal(envelope)
	if _, err := DefaultRegistry().Validate(raw); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("invalid manifest digest accepted: %v", err)
	}
	if _, err := DefaultRegistry().Validate(make([]byte, MaxPayloadBytes+1)); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("oversized event accepted: %v", err)
	}
}

func TestVideoComparisonRequestRequiresExactPlanMembership(t *testing.T) {
	envelope := Envelope[VideoComparisonRequestedPayload]{ID: identity.NewID(), Type: "analysis.video_comparison_requested.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: identity.NewID(), AggregateID: identity.NewID(), CorrelationID: "video-plan", Payload: VideoComparisonRequestedPayload{ComparisonJobID: identity.NewID(), VideoPlanID: identity.NewID(), VideoPlanItemID: identity.NewID()}}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DefaultRegistry().Validate(raw); err != nil {
		t.Fatalf("valid pinned video comparison event rejected: %v", err)
	}
	envelope.Payload.VideoPlanItemID = identity.ID{}
	raw, _ = json.Marshal(envelope)
	if _, err := DefaultRegistry().Validate(raw); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("event without plan-item membership accepted: %v", err)
	}
}

func TestInspectionClassifiedAllowsPhotoAndPinsVideoGeneration(t *testing.T) {
	base := Envelope[InspectionClassifiedPayload]{ID: identity.NewID(), Type: "inspection.classified.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: identity.NewID(), CorrelationID: "classified", Payload: InspectionClassifiedPayload{InspectionID: identity.NewID(), ClassificationRunID: identity.NewID(), Classification: "NORMAL", ReasonCodes: []string{}}}
	validate := func(e Envelope[InspectionClassifiedPayload]) error {
		raw, err := json.Marshal(e)
		if err != nil {
			return err
		}
		_, err = DefaultRegistry().Validate(raw)
		return err
	}
	if err := validate(base); err != nil {
		t.Fatalf("PHOTO classification without videoPlanId rejected: %v", err)
	}
	base.Payload.VideoPlanID = ptrID(identity.NewID())
	if err := validate(base); err != nil {
		t.Fatalf("pinned video classification rejected: %v", err)
	}
	base.Payload.VideoPlanID = ptrID(identity.ID{})
	if err := validate(base); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("zero videoPlanId accepted: %v", err)
	}
}

func ptrID(id identity.ID) *identity.ID { return &id }

func TestLifecycleEventsIT551ToIT554IT581ToIT582(t *testing.T) {
	registry := DefaultRegistry()
	for _, eventType := range []string{"inspection.created.v1", "inspection.state_changed.v1", "project.stage_changed.v1"} {
		envelope := Envelope[map[string]any]{ID: identity.NewID(), Type: eventType, SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: identity.NewID(), AggregateID: identity.NewID(), CorrelationID: "lifecycle-contract", Payload: map[string]any{"state": "ACTIVE"}}
		raw, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		validated, err := registry.Validate(raw)
		if err != nil || validated.Type != eventType {
			t.Fatalf("%s rejected: %v", eventType, err)
		}
		envelope.SchemaVersion = 2
		invalid, _ := json.Marshal(envelope)
		if _, err := registry.Validate(invalid); !errors.Is(err, ErrUnknownContract) {
			t.Fatalf("%s invalid major accepted: %v", eventType, err)
		}
	}
}
