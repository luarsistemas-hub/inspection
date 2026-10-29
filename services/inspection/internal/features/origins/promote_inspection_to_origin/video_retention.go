package promote_inspection_to_origin

import (
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func retainPromotionVideoPlan(tx *gorm.DB, tenantID, versionID, planID identity.ID, now time.Time) error {
	var preparationIDs []identity.ID
	if err := tx.Table("analysis.video_plan_preparations pp").
		Distinct("pp.preparation_id").
		Joins("JOIN media.video_preparation_jobs p ON p.tenant_id=pp.tenant_id AND p.id=pp.preparation_id AND p.status='READY'").
		Joins("JOIN media.video_frames f ON f.tenant_id=pp.tenant_id AND f.preparation_id=pp.preparation_id").
		Where("pp.tenant_id=? AND pp.plan_id=? AND f.sha256<>'' AND f.object_key<>''", tenantID, planID).
		Pluck("pp.preparation_id", &preparationIDs).Error; err != nil {
		return err
	}
	for _, preparationID := range preparationIDs {
		reference := database.VideoEvidenceReference{ID: identity.NewID(), TenantID: tenantID, PreparationID: preparationID, OwnerType: "ORIGIN_VERSION", OwnerID: versionID, CreatedAt: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&reference).Error; err != nil {
			return err
		}
	}
	return nil
}
