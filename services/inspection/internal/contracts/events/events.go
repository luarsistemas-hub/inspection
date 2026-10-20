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
	return envelope, nil
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
		"media.video_prepared.v1", "report.snapshot_created.v1", "report.ready.v1", "notification.delivery_terminal.v1", "project.stage_changed.v1", "retention.purge_due.v1", "retention.purged.v1",
	} {
		_ = r.Register(name, 1)
	}
	_ = r.Register("notification.delivery_requested.v2", 2)
	return r
}
