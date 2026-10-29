package handle_classified

import (
	"context"
	"errors"
	"strings"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// promotedOriginReportEvidence carries the captured environment views of a
// promoted video inspection into a later report, even when cataloging found
// no inventory items to pair with them.
func promotedOriginReportEvidence(ctx context.Context, tx *gorm.DB, tenantID identity.ID, versionID *identity.ID, requirements []reportcore.Requirement, existing []reportcore.Evidence) ([]reportcore.Evidence, error) {
	if versionID == nil {
		return nil, nil
	}
	var promotion database.InspectionOriginPromotion
	err := tx.WithContext(ctx).Where("tenant_id=? AND origin_version_id=? AND status='COMPLETED' AND source_kind='VIDEO' AND video_plan_id IS NOT NULL", tenantID, *versionID).Take(&promotion).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var holds []database.VideoEvidenceReference
	if err := tx.WithContext(ctx).Where("tenant_id=? AND owner_type='ORIGIN_VERSION' AND owner_id=?", tenantID, *versionID).Find(&holds).Error; err != nil {
		return nil, err
	}
	held := make(map[string]bool, len(holds))
	for _, hold := range holds {
		held[hold.PreparationID.String()] = true
	}
	if len(held) == 0 {
		return nil, nil
	}
	jobs, err := loadReportVideoPlanJobs(ctx, tx, tenantID, promotion.InspectionID, *promotion.VideoPlanID)
	if err != nil {
		return nil, err
	}
	frames, err := reportAllVideoPlanEvidence(ctx, tx, tenantID, *promotion.VideoPlanID, jobs)
	if err != nil {
		return nil, err
	}
	// The promoted plan keeps capture keys (env:new:<id> for added rooms); a
	// later inspection addresses the same rooms as env:<inventory identity>.
	var environments []database.InventoryEnvironment
	if err := tx.WithContext(ctx).Where("tenant_id=? AND origin_version_id=? AND requirement_key IS NOT NULL", tenantID, *versionID).Find(&environments).Error; err != nil {
		return nil, err
	}
	laterKey := make(map[string]string, len(environments))
	for _, environment := range environments {
		laterKey[*environment.RequirementKey] = "env:" + environment.IdentityID.String()
	}
	allowed := make(map[string]bool, len(requirements))
	for _, requirement := range requirements {
		if strings.HasPrefix(requirement.Key, "env:") {
			allowed[requirement.Key] = true
		}
	}
	seen := make(map[string]bool, len(existing)+len(frames))
	for _, item := range existing {
		seen[item.ID] = true
	}
	var reference []reportcore.Evidence
	for _, frame := range frames {
		if key, ok := laterKey[frame.RequirementKey]; ok {
			frame.RequirementKey = key
		}
		if frame.Role != "CURRENT" || !allowed[frame.RequirementKey] || !held[frame.PreparationID] || seen[frame.ID] {
			continue
		}
		frame.Role = "REFERENCE"
		reference = append(reference, frame)
		seen[frame.ID] = true
	}
	return reference, nil
}
