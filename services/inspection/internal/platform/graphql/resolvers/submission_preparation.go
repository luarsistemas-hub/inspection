package resolvers

import (
	"context"
	"encoding/json"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// submissionPreparationPending reports whether a video submission was accepted
// before its preparation reached READY. Photo submissions are never pending.
func submissionPreparationPending(ctx context.Context, db *gorm.DB, tenantID identity.ID, submission database.SubmissionVersion) (bool, error) {
	var payload struct {
		MediaKind string      `json:"mediaKind"`
		MediaID   identity.ID `json:"mediaId"`
	}
	if json.Unmarshal(submission.Payload, &payload) != nil || payload.MediaKind != "VIDEO" || payload.MediaID == (identity.ID{}) {
		return false, nil
	}
	var ready int64
	err := withTask06Tenant(ctx, db, tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND media_id=? AND status=?", tenantID, payload.MediaID, "READY").Count(&ready).Error
	})
	return ready == 0, err
}
