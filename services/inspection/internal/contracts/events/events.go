package events

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"inspection/libs/identity"
)

const MaxPayloadBytes = 256 * 1024

var (
	ErrInvalidEnvelope = errors.New("invalid event envelope")
	ErrUnknownContract = errors.New("unknown event contract")
)

type Envelope[T any] struct {
	ID            identity.ID `json:"id"`
	Type          string      `json:"type"`
	SchemaVersion int         `json:"schemaVersion"`
	OccurredAt    time.Time   `json:"occurredAt"`
	TenantID      identity.ID `json:"tenantId"`
	AggregateID   identity.ID `json:"aggregateId,omitempty"`
	CorrelationID string      `json:"correlationId"`
	CausationID   string      `json:"causationId,omitempty"`
	Payload       T           `json:"payload"`
}

type RawEnvelope = Envelope[json.RawMessage]

// VideoPreparedPayload identifies the durable frame bank committed by a worker.
type VideoPreparedPayload struct {
	MediaID        identity.ID `json:"mediaId"`
	PreparationID  identity.ID `json:"preparationId"`
	ManifestDigest string      `json:"manifestDigest"`
}

// VideoComparisonRequestedPayload binds one condition job to sealed plan membership.
type VideoComparisonRequestedPayload struct {
	ComparisonJobID identity.ID `json:"comparisonJobId"`
	VideoPlanID     identity.ID `json:"videoPlanId"`
	VideoPlanItemID identity.ID `json:"videoPlanItemId"`
}

// InspectionClassifiedPayload binds classification to the sealed video generation when applicable.
type InspectionClassifiedPayload struct {
	InspectionID        identity.ID  `json:"inspectionId"`
	ClassificationRunID identity.ID  `json:"classificationRunId"`
	VideoPlanID         *identity.ID `json:"videoPlanId,omitempty"`
	Classification      string       `json:"classification"`
	ReasonCodes         []string     `json:"reasonCodes"`
}

// CaptureVideoSubmittedV2Payload lists the environment clips in an immutable submission.
type CaptureVideoSubmittedV2Payload struct {
	InspectionID        identity.ID        `json:"inspectionId"`
	ResponsibilityID    identity.ID        `json:"responsibilityId"`
	SubmissionVersionID identity.ID        `json:"submissionVersionId"`
	Clips               []CaptureVideoClip `json:"clips"`
}

// CaptureVideoClip identifies one submitted environment clip.
type CaptureVideoClip struct {
	RequirementKey string      `json:"requirementKey"`
	MediaID        identity.ID `json:"mediaId"`
}

// InventoryJobRequestedPayload announces a due inventory analysis job.
type InventoryJobRequestedPayload struct {
	JobID        identity.ID `json:"jobId"`
	InspectionID identity.ID `json:"inspectionId"`
}

// InventoryDraftReadyPayload announces that a baseline draft can be reviewed.
type InventoryDraftReadyPayload struct {
	OriginVersionID identity.ID `json:"originVersionId"`
}

// InventoryVersionActivatedPayload announces a newly active baseline version.
type InventoryVersionActivatedPayload struct {
	OriginVersionID identity.ID `json:"originVersionId"`
}

// CounterpartyRespondedPayload announces a counterparty response. Statements,
// tokens and OTPs never travel in the event.
type CounterpartyRespondedPayload struct {
	CounterpartyInvitationID identity.ID `json:"counterpartyInvitationId"`
	OriginVersionID          identity.ID `json:"originVersionId"`
}

type Registry struct {
	mu      sync.RWMutex
	version map[string]int
}

func NewRegistry() *Registry { return &Registry{version: make(map[string]int)} }

func (r *Registry) Register(eventType string, version int) error {
	if !validType(eventType, version) {
		return fmt.Errorf("%w: type or version", ErrInvalidEnvelope)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.version[eventType]; exists {
		return fmt.Errorf("%w: duplicate %s", ErrInvalidEnvelope, eventType)
	}
	r.version[eventType] = version
	return nil
}

func (r *Registry) Validate(data []byte) (RawEnvelope, error) {
	if len(data) == 0 || len(data) > MaxPayloadBytes {
		return RawEnvelope{}, fmt.Errorf("%w: payload size", ErrInvalidEnvelope)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var envelope RawEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return RawEnvelope{}, fmt.Errorf("%w: malformed", ErrInvalidEnvelope)
	}
	if envelope.ID == (identity.ID{}) || envelope.TenantID == (identity.ID{}) || envelope.OccurredAt.IsZero() || envelope.CorrelationID == "" || envelope.Type == "" || envelope.SchemaVersion < 1 || len(envelope.Payload) == 0 {
		return RawEnvelope{}, fmt.Errorf("%w: missing context", ErrInvalidEnvelope)
	}
	r.mu.RLock()
	version, ok := r.version[envelope.Type]
	r.mu.RUnlock()
	if !ok || version != envelope.SchemaVersion {
		return RawEnvelope{}, fmt.Errorf("%w: %s v%d", ErrUnknownContract, envelope.Type, envelope.SchemaVersion)
	}
	if containsForbidden(envelope.Payload) {
		return RawEnvelope{}, fmt.Errorf("%w: forbidden payload field", ErrInvalidEnvelope)
	}
	if envelope.Type == "media.video_prepared.v1" {
		var payload VideoPreparedPayload
		payloadDecoder := json.NewDecoder(bytes.NewReader(envelope.Payload))
		payloadDecoder.DisallowUnknownFields()
		if err := payloadDecoder.Decode(&payload); err != nil || payload.MediaID == (identity.ID{}) || payload.PreparationID == (identity.ID{}) || !validDigest(payload.ManifestDigest) {
			return RawEnvelope{}, fmt.Errorf("%w: video preparation payload", ErrInvalidEnvelope)
		}
	}
	if envelope.Type == "analysis.video_comparison_requested.v1" {
		var payload VideoComparisonRequestedPayload
		payloadDecoder := json.NewDecoder(bytes.NewReader(envelope.Payload))
		payloadDecoder.DisallowUnknownFields()
		if err := payloadDecoder.Decode(&payload); err != nil || payload.ComparisonJobID == (identity.ID{}) || payload.VideoPlanID == (identity.ID{}) || payload.VideoPlanItemID == (identity.ID{}) {
			return RawEnvelope{}, fmt.Errorf("%w: video comparison payload", ErrInvalidEnvelope)
		}
	}
	if envelope.Type == "inspection.classified.v1" {
		var payload InspectionClassifiedPayload
		payloadDecoder := json.NewDecoder(bytes.NewReader(envelope.Payload))
		payloadDecoder.DisallowUnknownFields()
		if err := payloadDecoder.Decode(&payload); err != nil || payload.InspectionID == (identity.ID{}) || payload.ClassificationRunID == (identity.ID{}) || payload.Classification == "" || payload.ReasonCodes == nil || payload.VideoPlanID != nil && *payload.VideoPlanID == (identity.ID{}) {
			return RawEnvelope{}, fmt.Errorf("%w: inspection classified payload", ErrInvalidEnvelope)
		}
	}
	if envelope.Type == "capture.video_submitted.v2" {
		var payload CaptureVideoSubmittedV2Payload
		if err := decodePayload(envelope.Payload, &payload); err != nil || payload.InspectionID == (identity.ID{}) || payload.ResponsibilityID == (identity.ID{}) || payload.SubmissionVersionID == (identity.ID{}) || len(payload.Clips) == 0 {
			return RawEnvelope{}, fmt.Errorf("%w: capture video submitted payload", ErrInvalidEnvelope)
		}
		for _, clip := range payload.Clips {
			if clip.RequirementKey == "" || clip.MediaID == (identity.ID{}) {
				return RawEnvelope{}, fmt.Errorf("%w: capture video clip", ErrInvalidEnvelope)
			}
		}
		keys, media := map[string]bool{}, map[identity.ID]bool{}
		for _, clip := range payload.Clips {
			if keys[clip.RequirementKey] || media[clip.MediaID] {
				return RawEnvelope{}, fmt.Errorf("%w: duplicate capture video clip", ErrInvalidEnvelope)
			}
			keys[clip.RequirementKey], media[clip.MediaID] = true, true
		}
	}
	if valid, ok := inventoryPayloadValidators[envelope.Type]; ok && !valid(envelope.Payload) {
		return RawEnvelope{}, fmt.Errorf("%w: %s payload", ErrInvalidEnvelope, envelope.Type)
	}
	return envelope, nil
}

// inventoryPayloadValidators accept only the documented id fields; any other
// key (an OTP, a token, a statement) makes the payload invalid.
var inventoryPayloadValidators = map[string]func(json.RawMessage) bool{
	"inventory.job_requested.v1": func(raw json.RawMessage) bool {
		var payload InventoryJobRequestedPayload
		return decodePayload(raw, &payload) == nil && nonZeroIDs(payload.JobID, payload.InspectionID)
	},
	"inventory.draft_ready.v1": func(raw json.RawMessage) bool {
		var payload InventoryDraftReadyPayload
		return decodePayload(raw, &payload) == nil && nonZeroIDs(payload.OriginVersionID)
	},
	"inventory.version_activated.v1": func(raw json.RawMessage) bool {
		var payload InventoryVersionActivatedPayload
		return decodePayload(raw, &payload) == nil && nonZeroIDs(payload.OriginVersionID)
	},
	"counterparty.responded.v1": func(raw json.RawMessage) bool {
		var payload CounterpartyRespondedPayload
		return decodePayload(raw, &payload) == nil && nonZeroIDs(payload.CounterpartyInvitationID, payload.OriginVersionID)
	},
}

func nonZeroIDs(ids ...identity.ID) bool {
	for _, id := range ids {
		if id == (identity.ID{}) {
			return false
		}
	}
	return true
}

func decodePayload(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if d.More() {
		return fmt.Errorf("trailing payload data")
	}
	return nil
}

func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func validType(eventType string, version int) bool {
	return version > 0 && strings.HasSuffix(eventType, fmt.Sprintf(".v%d", version)) && len(eventType) <= 200
}

func containsForbidden(payload []byte) bool {
	var value any
	if json.Unmarshal(payload, &value) != nil {
		return true
	}
	forbidden := []string{"otp", "password", "secret", "token", "cookie", "presigned", "imagebytes", "base64", "personalprofile", "broadprofile"}
	var walk func(any) bool
	walk = func(v any) bool {
		switch typed := v.(type) {
		case map[string]any:
			for key, child := range typed {
				normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
				for _, denied := range forbidden {
					if strings.Contains(normalized, denied) {
						return true
					}
				}
				if walk(child) {
					return true
				}
			}
		case []any:
			for _, child := range typed {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	return walk(value)
}

func DefaultRegistry() *Registry {
	r := NewRegistry()
	for _, name := range []string{
		"participant.channel_verified.v1", "origin.invitation_requested.v1", "inspection.created.v1",
		"origin.promotion_requested.v1",
		"inspection.state_changed.v1", "media.upload_completed.v1", "media.verified.v1", "media.screened.v1", "media.received.v1",
		"capture.submitted.v1", "capture.video_submitted.v1", "recapture.requested.v1", "recapture.completed.v1", "recapture.deadline_reached.v1", "video_replacement.deadline_reached.v1",
		"analysis.comparison_requested.v1", "analysis.video_comparison_requested.v1", "analysis.comparison_completed.v1", "inspection.classified.v1",
		"inventory.job_requested.v1", "inventory.draft_ready.v1", "inventory.version_activated.v1", "counterparty.responded.v1",
		"media.video_prepared.v1", "report.snapshot_created.v1", "report.ready.v1", "notification.delivery_terminal.v1", "project.stage_changed.v1", "retention.purge_due.v1", "retention.purged.v1",
	} {
		_ = r.Register(name, 1)
	}
	_ = r.Register("notification.delivery_requested.v2", 2)
	_ = r.Register("capture.video_submitted.v2", 2)
	return r
}
