package finalize_submission

import (
	"context"
	"fmt"

	capturecore "inspection/services/inspection/internal/features/capture/core"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

type Dependencies struct {
	Recapture recapturecore.Service
}

type finalizer struct {
	recapture recapturecore.Service
}

func Setup(d Dependencies) (capturecore.SubmissionFinalizer, error) {
	if d.Recapture.DB == nil {
		return nil, fmt.Errorf("slice capture/finalize_submission: missing dependency")
	}
	return finalizer{recapture: d.Recapture}, nil
}

func (f finalizer) Finalize(ctx context.Context, tx *gorm.DB, draft database.CaptureDraft, submission database.SubmissionVersion) error {
	switch draft.Kind {
	case "RECAPTURE":
		var request database.RecaptureRequest
		if err := tx.Where("tenant_id=? AND responsibility_id=?", draft.TenantID, draft.ResponsibilityID).First(&request).Error; err != nil {
			return apperror.New(apperror.NotFound, "responsibility", "recapture request not found")
		}
		if draft.MediaKind == "VIDEO" {
			_, err := f.recapture.ReceiveVideoTx(tx, draft.TenantID, request.ID, draft.ResponsibilityID, submission.ID)
			return err
		}
		_, err := f.recapture.FinalizeTx(tx, draft.TenantID, request.ID, true)
		return err
	case "INSPECTION":
		return finalizeInspection(tx, draft, submission)
	case "VIDEO_REPLACEMENT":
		return f.recapture.FinalizeVideoReplacementTx(tx, draft, submission)
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
	evidence := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=?", draft.TenantID, draft.ResponsibilityID)
	if draft.MediaKind == "VIDEO" {
		evidence = evidence.Where("media_kind='VIDEO' AND status IN ?", []string{"RECEIVED", "READY"})
	} else {
		evidence = evidence.Where("status='READY'")
	}
	if err := evidence.Count(&evidenceCount).Error; err != nil {
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
	return nil
}
