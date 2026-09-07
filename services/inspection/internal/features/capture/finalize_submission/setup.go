package finalize_submission

import (
	"context"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	origincore "inspection/services/inspection/internal/features/origins/core"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

type Dependencies struct {
	Origins   origincore.Service
	Recapture recapturecore.Service
}

type finalizer struct {
	origins   origincore.Service
	recapture recapturecore.Service
}

func Setup(d Dependencies) (capturecore.SubmissionFinalizer, error) {
	if d.Origins.DB == nil || d.Recapture.DB == nil {
		return nil, fmt.Errorf("slice capture/finalize_submission: missing dependency")
	}
	return finalizer{origins: d.Origins, recapture: d.Recapture}, nil
}

func (f finalizer) Finalize(ctx context.Context, tx *gorm.DB, draft database.CaptureDraft, submission database.SubmissionVersion) error {
	switch draft.Kind {
	case "ORIGIN":
		_, err := f.origins.ActivateCompletedTx(tx, draft.TenantID, draft.ResponsibilityID)
		return err
	case "RECAPTURE":
		var request database.RecaptureRequest
		if err := tx.Where("tenant_id=? AND responsibility_id=?", draft.TenantID, draft.ResponsibilityID).First(&request).Error; err != nil {
			return apperror.New(apperror.NotFound, "responsibility", "recapture request not found")
		}
		_, err := f.recapture.FinalizeTx(tx, draft.TenantID, request.ID, true)
		return err
	case "INSPECTION":
		return finalizeInspection(tx, draft, submission)
	default:
		return apperror.New(apperror.InvalidState, "capture", "unsupported capture responsibility")
	}
}

func finalizeInspection(tx *gorm.DB, draft database.CaptureDraft, submission database.SubmissionVersion) error {
	var responsibility database.Responsibility
	if err := tx.Where("tenant_id=? AND id=?", draft.TenantID, draft.ResponsibilityID).First(&responsibility).Error; err != nil {
		return apperror.New(apperror.NotFound, "responsibility", "inspection responsibility not found")
	}
	var evidenceCount int64
	if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=? AND status='READY'", draft.TenantID, draft.ResponsibilityID).Count(&evidenceCount).Error; err != nil {
		return err
	}
	now := submission.SubmittedAt.UTC()
	if err := tx.Model(&responsibility).Updates(map[string]any{"status": "SUBMITTED", "version": gorm.Expr("version + 1"), "updated_at": now}).Error; err != nil {
		return err
	}
	transition := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=? AND status IN ?", draft.TenantID, responsibility.InspectionID, []string{"INVITED", "IN_PROGRESS"}).Updates(map[string]any{"status": "ANALYZING", "evidence_count": evidenceCount, "version": gorm.Expr("version + 1"), "updated_at": now})
	if transition.Error != nil {
		return transition.Error
	}
	if transition.RowsAffected != 1 {
		return apperror.New(apperror.InvalidState, "inspection", "inspection no longer accepts capture")
	}
	eventID := identity.NewID()
	return messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "analysis.comparison_requested.v1", SchemaVersion: 1, OccurredAt: normalizeTime(now), TenantID: draft.TenantID, AggregateID: responsibility.InspectionID, CorrelationID: "analysis-request-" + submission.ID.String(), Payload: map[string]any{"inspectionId": responsibility.InspectionID, "submissionId": submission.ID, "complete": submission.Complete, "requiresAttention": submission.RequiresAttention}})
}

func normalizeTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value
}
