package core

import (
	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// SourceHeld reports whether an active legal hold protects a video source.
func SourceHeld(tx *gorm.DB, tenantID, mediaID identity.ID) (bool, error) {
	var active int64
	if err := tx.Model(&database.LegalHold{}).
		Where("tenant_id=? AND source_media_id=? AND active=true", tenantID, mediaID).
		Count(&active).Error; err != nil {
		return false, err
	}
	return active > 0, nil
}

// EffectiveSourceHeld includes holds inherited from the inspection or origin
// version whose retained preparation contains the source.
func EffectiveSourceHeld(tx *gorm.DB, tenantID, mediaID identity.ID) (bool, error) {
	if held, err := SourceHeld(tx, tenantID, mediaID); err != nil || held {
		return held, err
	}
	var active int64
	query := `tenant_id = ? AND active = true AND (
		inspection_id IN (
			SELECT r.inspection_id FROM inspections.responsibilities r
			JOIN media.media_objects m ON m.tenant_id = r.tenant_id AND m.responsibility_id = r.id
			WHERE r.tenant_id = ? AND m.id = ?
		) OR origin_version_id IN (
			SELECT ov.id FROM origins.origin_versions ov
			JOIN media.video_preparation_jobs p ON p.tenant_id = ov.tenant_id AND p.id = ov.video_preparation_id
			WHERE ov.tenant_id = ? AND p.media_id = ?
		)
	)`
	if err := tx.Model(&database.LegalHold{}).Where(query, tenantID, tenantID, mediaID, tenantID, mediaID).Count(&active).Error; err != nil {
		return false, err
	}
	return active > 0, nil
}
