package core

import (
	"errors"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RetryFailedPreparation requeues the current failed job and resumes blocked
// inspection plans in the caller's transaction.
func RetryFailedPreparation(tx *gorm.DB, tenantID, mediaID identity.ID, expectedVersion int, now time.Time) (database.VideoPreparationJob, error) {
	var job database.VideoPreparationJob
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND media_id=?", tenantID, mediaID).Order("created_at DESC, id DESC").Take(&job).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return job, apperror.New(apperror.NotFound, "mediaId", "video preparation not found")
	} else if err != nil {
		return job, err
	}
	if job.PreparationVersion != expectedVersion {
		return job, apperror.New(apperror.Conflict, "expectedPreparationVersion", "video preparation has changed")
	}
	if job.Status != "FAILED" {
		return job, apperror.New(apperror.InvalidState, "mediaId", "video preparation cannot be retried")
	}
	var source database.VideoSource
	if err := tx.Where("tenant_id=? AND media_id=?", tenantID, mediaID).Take(&source).Error; err == nil {
		if source.Status == "DELETING" || (source.Status == "EXPIRED" && source.FirstFrameReadyAt == nil) {
			return job, apperror.New(apperror.SourceUnavailable, "mediaId", "video source is unavailable")
		}
		if source.ScreeningStatus == "BLOCKED" {
			return job, apperror.New(apperror.InvalidState, "mediaId", "video source is blocked pending review")
		}
		if source.Status == "EXPIRED" {
			var frameCount int64
			if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", tenantID, job.ID).Count(&frameCount).Error; err != nil {
				return job, err
			}
			if source.FirstFrameReadyAt == nil || frameCount == 0 || (source.ScreeningStatus != "APPROVED" && source.ScreeningStatus != "DISABLED") {
				return job, apperror.New(apperror.SourceUnavailable, "mediaId", "video source is unavailable")
			}
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return job, err
	} else {
		var media database.MediaObject
		if err := tx.Where("tenant_id=? AND id=?", tenantID, mediaID).Take(&media).Error; err != nil {
			return job, err
		}
		if media.ObjectKey == "" || media.Status == "EXPIRED" || media.Status == "DELETED" || media.Status == "REJECTED" {
			return job, apperror.New(apperror.SourceUnavailable, "mediaId", "video source is unavailable")
		}
	}
	now = now.UTC()
	result := tx.Model(&job).Where("tenant_id=? AND id=? AND preparation_version=? AND status='FAILED'", tenantID, job.ID, expectedVersion).Updates(map[string]any{"status": "QUEUED", "preparation_version": expectedVersion + 1, "attempts": 0, "next_attempt_at": now, "lease_token": nil, "lease_expires_at": nil, "heartbeat_at": nil, "error_code": ""})
	if result.Error != nil {
		return job, result.Error
	}
	if result.RowsAffected != 1 {
		return job, apperror.New(apperror.Conflict, "expectedPreparationVersion", "video preparation has changed")
	}
	if err := ResumeBlockedPlans(tx, tenantID, job.ID, job.ID, now); err != nil {
		return job, err
	}
	job.Status, job.PreparationVersion, job.Attempts, job.ErrorCode, job.NextAttemptAt = "QUEUED", expectedVersion+1, 0, "", now
	return job, nil
}

// ResumeBlockedPlans changes the blocked plan and inspection in the same transaction.
func ResumeBlockedPlans(tx *gorm.DB, tenantID, previousPreparationID, nextPreparationID identity.ID, now time.Time) error {
	var plans []database.VideoPlan
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND status='BLOCKED' AND (current_preparation_id=? OR origin_preparation_id=?)", tenantID, previousPreparationID, previousPreparationID).Find(&plans).Error; err != nil {
		return err
	}
	for _, plan := range plans {
		var pending int64
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=? AND status='REPLACEMENT_PENDING'", tenantID, plan.InspectionID).Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return apperror.New(apperror.InvalidState, "mediaId", "a replacement recording is pending for this inspection")
		}
		updates := map[string]any{"status": "BUILDING", "next_attempt_at": now.UTC(), "attempts": 0, "error_code": "", "lease_token": nil, "lease_until": nil}
		if plan.CurrentPreparationID == previousPreparationID {
			updates["current_preparation_id"] = nextPreparationID
		}
		if plan.OriginPreparationID != nil && *plan.OriginPreparationID == previousPreparationID {
			updates["origin_preparation_id"] = nextPreparationID
		}
		if err := tx.Model(&database.VideoPlan{}).Where("tenant_id=? AND id=? AND status='BLOCKED'", tenantID, plan.ID).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=? AND status IN ('REVIEW_REQUIRED','PROCESSING_FAILED')", tenantID, plan.InspectionID).Updates(map[string]any{"status": "ANALYZING", "updated_at": now.UTC(), "version": gorm.Expr("version + 1")}).Error; err != nil {
			return err
		}
	}
	return nil
}
