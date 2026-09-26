// Package request_comparisons creates one immutable comparison job per
// submitted requirement and emits a provider-neutral processing request.
package request_comparisons

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Dependencies struct {
	Now func() time.Time
}

type payload struct {
	InspectionID     identity.ID `json:"inspectionId"`
	DraftID          identity.ID `json:"draftId"`
	Kind             string      `json:"kind"`
	RecaptureRequest identity.ID `json:"recaptureRequestId"`
	JobID            identity.ID `json:"jobId"`
}

// Setup returns an inbox-compatible creation handler. A job-bearing request is
// intentionally left for the process_comparison consumer; this slice only
// creates jobs and schedules them.
func Setup(deps Dependencies) (func(context.Context, *gorm.DB, events.RawEnvelope) error, error) {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var in payload
		if err := json.Unmarshal(envelope.Payload, &in); err != nil {
			return messaging.ErrPermanent
		}
		if in.Kind != "" && in.Kind != "INSPECTION" {
			return nil
		}
		if in.InspectionID == (identity.ID{}) {
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
		var inspection database.Inspection
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, in.InspectionID).First(&inspection).Error; err != nil {
			return err
		}
		var snapshot database.AnalysisPromptSnapshot
		if err := tx.WithContext(ctx).Where("id=? AND analysis_type=?", inspection.AnalysisPromptSnapshotID, "REAL_ESTATE").First(&snapshot).Error; err != nil {
			return err
		}
		var reference database.ReferenceSnapshot
		if err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=?", envelope.TenantID, in.InspectionID).First(&reference).Error; err != nil {
			return err
		}
		now := deps.Now().UTC()
		for _, answer := range answers {
			status, err := comparisonStatus(answer)
			if err != nil {
				return err
			}
			// The reference snapshot is pinned at inspection creation. Including
			// it in the job identity makes a retry or a changed origin produce a
			// distinct job and prevents current-only input from masquerading as a
			// comparative request.
			digestInput := append(append([]byte{}, answer.MediaIDs...), answer.Flags...)
			digestInput = append(digestInput, reference.Payload...)
			digestBytes := sha256.Sum256(digestInput)
			job := database.ComparisonJob{ID: identity.NewID(), TenantID: envelope.TenantID, InspectionID: in.InspectionID, PromptSnapshotID: snapshot.ID, RequirementKey: answer.RequirementKey, ModelAlias: "inspection-vision", PromptDigest: snapshot.CanonicalDigest, InputDigest: hex.EncodeToString(digestBytes[:]), Status: status, CreatedAt: now, UpdatedAt: now}
			result := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&job)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				if err := tx.Where("tenant_id=? AND inspection_id=? AND requirement_key=? AND model_alias=? AND prompt_digest=? AND input_digest=?", job.TenantID, job.InspectionID, job.RequirementKey, job.ModelAlias, job.PromptDigest, job.InputDigest).First(&job).Error; err != nil {
					return err
				}
			}
			requestID := identity.NewID()
			eventType := "analysis.comparison_requested.v1"
			if job.Status == "INCONCLUSIVE" {
				eventType = "analysis.comparison_completed.v1"
			}
			if err := messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: requestID, Type: eventType, SchemaVersion: 1, OccurredAt: now, TenantID: envelope.TenantID, AggregateID: job.ID, CorrelationID: envelope.CorrelationID, CausationID: envelope.ID.String(), Payload: map[string]any{"inspectionId": in.InspectionID, "jobId": job.ID, "requirementKey": job.RequirementKey, "status": job.Status}}); err != nil {
				return err
			}
		}
		return nil
	}, nil
}

func comparisonStatus(answer database.RequirementAnswer) (string, error) {
	var mediaIDs []identity.ID
	if err := json.Unmarshal(answer.MediaIDs, &mediaIDs); err != nil {
		return "", messaging.ErrPermanent
	}
	if len(mediaIDs) == 0 && answer.ImpossibilityReason != "" {
		return "INCONCLUSIVE", nil
	}
	return "PENDING", nil
}
