// Package video_admission centralizes transactional checks for new video work.
package video_admission

import (
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RequireEnabled locks the singleton admission row until the caller's transaction ends.
func RequireEnabled(tx *gorm.DB) error {
	var admission database.VideoAdmission
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=1").First(&admission).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return apperror.New(apperror.IntegrationDisabled, "videoAdmission", "new video admission is disabled")
		}
		return err
	}
	if !admission.Enabled {
		return apperror.New(apperror.IntegrationDisabled, "videoAdmission", "new video admission is disabled")
	}
	return nil
}
