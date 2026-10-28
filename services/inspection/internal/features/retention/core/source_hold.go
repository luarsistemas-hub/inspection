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

// EffectiveSourceHeld includes holds inherited from the inspection that captured
// the source or from any origin version that owns it (legacy evidence, included
// inventory evidence or a retained frame-bank preparation).
func EffectiveSourceHeld(tx *gorm.DB, tenantID, mediaID identity.ID) (bool, error) {
	if held, err := SourceHeld(tx, tenantID, mediaID); err != nil || held {
		return held, err
	}
	var active int64
	if err := tx.Raw(`SELECT count(*) FROM retention.legal_holds h WHERE h.tenant_id=@tenant AND h.active=true AND (
		h.inspection_id IN (
			SELECT r.inspection_id FROM inspections.responsibilities r
			JOIN media.media_objects m ON m.tenant_id=r.tenant_id AND m.responsibility_id=r.id
			WHERE r.tenant_id=@tenant AND m.id=@media
		) OR h.origin_version_id IN (`+referencingVersionsSQL+`)
	)`, sourceArgs(tenantID, mediaID)).Scan(&active).Error; err != nil {
		return false, err
	}
	if active > 0 {
		return true, nil
	}
	return OpenContestationSourceHeld(tx, tenantID, mediaID)
}

// OpenContestationSourceHeld reports whether an open counterparty contestation protects a source.
func OpenContestationSourceHeld(tx *gorm.DB, tenantID, mediaID identity.ID) (bool, error) {
	var openContestation int64
	if err := tx.Raw(`SELECT count(*) FROM origins.counterparty_invitations c
		WHERE c.tenant_id=@tenant AND c.response='CONTESTED' AND c.resolution='OPEN' AND c.origin_version_id IN (`+referencingVersionsSQL+`)`, sourceArgs(tenantID, mediaID)).Scan(&openContestation).Error; err != nil {
		return false, err
	}
	return openContestation > 0, nil
}

// ActiveBaselineReferences reports whether a live baseline still owns a source.
func ActiveBaselineReferences(tx *gorm.DB, tenantID, mediaID identity.ID) (bool, error) {
	var count int64
	err := tx.Raw(`SELECT count(*) FROM origins.origin_versions v WHERE v.tenant_id=@tenant AND v.status='ACTIVE' AND v.id IN (`+referencingVersionsSQL+`)`, sourceArgs(tenantID, mediaID)).Scan(&count).Error
	return count > 0, err
}
