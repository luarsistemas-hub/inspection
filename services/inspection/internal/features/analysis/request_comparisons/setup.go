// Package request_comparisons creates one immutable comparison job per
// submitted requirement and emits a provider-neutral processing request.
package request_comparisons

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Dependencies struct {
	ModelAlias    string
	PromptVersion string
	Now           func() time.Time
}

type payload struct {
	InspectionID     identity.ID `json:"inspectionId"`
	DraftID          identity.ID `json:"draftId"`
	RecaptureRequest identity.ID `json:"recaptureRequestId"`
	JobID            identity.ID `json:"jobId"`
}

// Setup returns an inbox-compatible creation handler. A job-bearing request is
// intentionally left for the process_comparison consumer; this slice only
// creates jobs and schedules them.
func Setup(deps Dependencies) (func(context.Context, *gorm.DB, events.RawEnvelope) error, error) {
	if deps.ModelAlias == "" || deps.PromptVersion == "" {
		return nil, fmt.Errorf("analysis/request_comparisons: missing provider version")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var in payload
		if err := json.Unmarshal(envelope.Payload, &in); err != nil || in.InspectionID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		if in.JobID != (identity.ID{}) {
			return nil
		}
		var draft database.CaptureDraft
		query := tx.WithContext(ctx).Where("tenant_id=?", envelope.TenantID)
		if in.DraftID != (identity.ID{}) {
			query = query.Where("id=?", in.DraftID)
		} else {
			query = query.Where("responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?)", envelope.TenantID, in.InspectionID)
		}
		if err := query.Order("created_at DESC").First(&draft).Error; err != nil {
			return err
		}
		var answers []database.RequirementAnswer
		if err := tx.WithContext(ctx).Where("tenant_id=? AND draft_id=?", envelope.TenantID, draft.ID).Order("requirement_key ASC").Find(&answers).Error; err != nil {
			return err
		}
		if len(answers) == 0 {
			return messaging.ErrPermanent
		}
		now := deps.Now().UTC()
		for _, answer := range answers {
			digestBytes := sha256.Sum256(append(append([]byte{}, answer.MediaIDs...), answer.Flags...))
			job := database.ComparisonJob{ID: identity.NewID(), TenantID: envelope.TenantID, InspectionID: in.InspectionID, RequirementKey: answer.RequirementKey, ModelAlias: deps.ModelAlias, PromptVersion: deps.PromptVersion, InputDigest: hex.EncodeToString(digestBytes[:]), Status: "PENDING", CreatedAt: now, UpdatedAt: now}
			result := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&job)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				if err := tx.Where("tenant_id=? AND inspection_id=? AND requirement_key=? AND model_alias=? AND prompt_version=? AND input_digest=?", job.TenantID, job.InspectionID, job.RequirementKey, job.ModelAlias, job.PromptVersion, job.InputDigest).First(&job).Error; err != nil {
					return err
				}
			}
			requestID := identity.NewID()
			if err := messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: requestID, Type: "analysis.comparison_requested.v1", SchemaVersion: 1, OccurredAt: now, TenantID: envelope.TenantID, AggregateID: job.ID, CorrelationID: envelope.CorrelationID, CausationID: envelope.ID.String(), Payload: map[string]any{"inspectionId": in.InspectionID, "jobId": job.ID, "requirementKey": job.RequirementKey}}); err != nil {
				return err
			}
		}
		return nil
	}, nil
}
