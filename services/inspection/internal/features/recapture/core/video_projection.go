package core

import (
	"context"
	"encoding/json"
	"strings"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

type videoRecaptureSelection struct {
	ComponentID     identity.ID   `json:"componentId"`
	RequirementKey  string        `json:"requirementKey"`
	OriginalMediaID identity.ID   `json:"originalMediaId"`
	Reason          string        `json:"reason"`
	OriginFrameIDs  []identity.ID `json:"originFrameIds,omitempty"`
}

type videoRecaptureProjection struct {
	PlanID     identity.ID               `json:"planId"`
	Generation int                       `json:"generation"`
	Selected   []videoRecaptureSelection `json:"selected"`
}

func projectVideoRecapture(ctx context.Context, tx *gorm.DB, in RequestInput, originalDraft database.CaptureDraft, originalRequirements []capturecore.Requirement) ([]capturecore.Requirement, json.RawMessage, error) {
	var inspection database.Inspection
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", in.TenantID, in.InspectionID).Take(&inspection).Error; err != nil {
		return nil, nil, err
	}
	if inspection.ActiveVideoPlanID == nil {
		return nil, nil, apperror.New(apperror.InvalidState, "inspectionId", "active sealed video scope is required")
	}
	var plan database.VideoPlan
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND inspection_id=? AND status='SEALED'", in.TenantID, *inspection.ActiveVideoPlanID, in.InspectionID).Take(&plan).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil, apperror.New(apperror.InvalidState, "inspectionId", "active sealed video scope is required")
		}
		return nil, nil, err
	}
	var planPreparations []database.VideoPlanPreparation
	if err := tx.WithContext(ctx).Where("tenant_id=? AND plan_id=?", in.TenantID, plan.ID).Find(&planPreparations).Error; err != nil {
		return nil, nil, err
	}
	preparationIDs := make(map[identity.ID]bool, len(planPreparations))
	for _, linked := range planPreparations {
		preparationIDs[linked.PreparationID] = true
	}
	if len(preparationIDs) == 0 {
		return nil, nil, apperror.New(apperror.InvalidState, "inspectionId", "prepared video scope is required")
	}
	comparisonTarget := ""
	videoRequirementFound := false
	for _, requirement := range originalRequirements {
		if (requirement.Key == "video:main" && requirement.EvidenceKind == "VIDEO") || requirement.EvidenceKind == "VIDEO_FRAME" {
			comparisonTarget = requirement.ComparisonTarget
			videoRequirementFound = true
			break
		}
	}
	if !videoRequirementFound {
		return nil, nil, apperror.New(apperror.InvalidState, "inspectionId", "video capture requirement is unavailable")
	}
	projection := videoRecaptureProjection{PlanID: plan.ID, Generation: plan.Generation, Selected: make([]videoRecaptureSelection, 0, len(in.Items))}
	lines := make([]string, 0, len(in.Items)+1)
	lines = append(lines, "Grave um único vídeo complementar apenas dos itens selecionados:")
	for _, selected := range in.Items {
		if !strings.HasPrefix(selected.RequirementKey, "video:") || selected.RequirementKey == "video:main" || selected.RequirementKey == "video:supplement" || selected.OriginalMediaID == nil {
			return nil, nil, apperror.New(apperror.InvalidInput, "items", "selected video component or original is invalid")
		}
		componentID, err := identity.ParseID(strings.TrimPrefix(selected.RequirementKey, "video:"))
		if err != nil || componentID == (identity.ID{}) {
			return nil, nil, apperror.New(apperror.InvalidInput, "items", "selected video component is invalid")
		}
		// The original video is a tenant resource: an unknown or foreign ID is
		// not found rather than a malformed selection.
		var originalCount int64
		if err := tx.WithContext(ctx).Model(&database.MediaObject{}).Where("tenant_id=? AND id=?", in.TenantID, *selected.OriginalMediaID).Count(&originalCount).Error; err != nil {
			return nil, nil, err
		}
		if originalCount == 0 {
			return nil, nil, apperror.New(apperror.NotFound, "originalMediaId", "original video not found")
		}
		var item database.VideoPlanItem
		if err := tx.WithContext(ctx).Where("tenant_id=? AND plan_id=? AND component_id=? AND requirement_key=?", in.TenantID, plan.ID, componentID, selected.RequirementKey).Take(&item).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil, nil, apperror.New(apperror.InvalidInput, "items", "selected video component is not in the sealed plan")
			}
			return nil, nil, err
		}
		var currentFrameIDs []identity.ID
		if len(item.CurrentFrameIDs) == 0 || json.Unmarshal(item.CurrentFrameIDs, &currentFrameIDs) != nil || len(currentFrameIDs) == 0 {
			return nil, nil, apperror.New(apperror.InvalidState, "inspectionId", "sealed video scope is invalid")
		}
		var frames []database.VideoFrame
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id IN ?", in.TenantID, currentFrameIDs).Find(&frames).Error; err != nil {
			return nil, nil, err
		}
		if len(frames) != len(currentFrameIDs) {
			return nil, nil, apperror.New(apperror.InvalidState, "inspectionId", "current component evidence is unavailable")
		}
		mediaFound := false
		for _, frame := range frames {
			if frame.SourceMediaID == *selected.OriginalMediaID {
				mediaFound = true
			}
			if !preparationIDs[frame.PreparationID] {
				if item.CarriedResultJobID == nil {
					return nil, nil, apperror.New(apperror.InvalidState, "inspectionId", "component evidence is outside the active plan")
				}
				var carried database.ComparisonJob
				if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND status IN ('COMPLETED','INCONCLUSIVE','FAILED')", in.TenantID, *item.CarriedResultJobID).Take(&carried).Error; err != nil {
					if err == gorm.ErrRecordNotFound {
						return nil, nil, apperror.New(apperror.InvalidState, "inspectionId", "carried component result is invalid")
					}
					return nil, nil, err
				}
				if carried.VideoPlanItemID == nil {
					return nil, nil, apperror.New(apperror.InvalidState, "inspectionId", "carried component result is invalid")
				}
				var sourceItem database.VideoPlanItem
				if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND component_id=?", in.TenantID, *carried.VideoPlanItemID, item.ComponentID).Take(&sourceItem).Error; err != nil {
					return nil, nil, apperror.New(apperror.InvalidState, "inspectionId", "carried component result is invalid")
				}
				var sourcePlan database.VideoPlan
				if carried.VideoPlanID == nil {
					return nil, nil, apperror.New(apperror.InvalidState, "inspectionId", "carried component plan is unavailable")
				}
				if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND status='SEALED'", in.TenantID, *carried.VideoPlanID).Take(&sourcePlan).Error; err != nil {
					if err == gorm.ErrRecordNotFound {
						return nil, nil, apperror.New(apperror.InvalidState, "inspectionId", "carried component plan is unavailable")
					}
					return nil, nil, err
				}
				if sourceItem.PlanID != sourcePlan.ID {
					return nil, nil, apperror.New(apperror.InvalidState, "inspectionId", "carried component plan is unavailable")
				}
				var sourcePreparation database.VideoPlanPreparation
				if err := tx.WithContext(ctx).Where("tenant_id=? AND plan_id=? AND preparation_id=?", in.TenantID, sourcePlan.ID, frame.PreparationID).Take(&sourcePreparation).Error; err != nil {
					if err == gorm.ErrRecordNotFound {
						return nil, nil, apperror.New(apperror.InvalidState, "inspectionId", "component evidence is outside its carried plan")
					}
					return nil, nil, err
				}
			}
			var preparation database.VideoPreparationJob
			if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND status='READY'", in.TenantID, frame.PreparationID).Take(&preparation).Error; err != nil {
				if err == gorm.ErrRecordNotFound {
					return nil, nil, apperror.New(apperror.InvalidState, "inspectionId", "component evidence preparation is unavailable")
				}
				return nil, nil, err
			}
		}
		if !mediaFound {
			return nil, nil, apperror.New(apperror.InvalidInput, "items", "selected original video does not contain the current component evidence")
		}
		var original database.MediaObject
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND media_kind='VIDEO' AND status='READY'", in.TenantID, *selected.OriginalMediaID).Take(&original).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil, nil, apperror.New(apperror.InvalidInput, "items", "selected original video is unavailable")
			}
			return nil, nil, err
		}
		projection.Selected = append(projection.Selected, videoRecaptureSelection{ComponentID: componentID, RequirementKey: selected.RequirementKey, OriginalMediaID: original.ID, Reason: selected.Reason, OriginFrameIDs: currentFrameIDs})
		lines = append(lines, selected.RequirementKey+": "+selected.Reason)
	}
	supplement := capturecore.VideoSupplementRequirement(comparisonTarget, strings.Join(lines, "\n"))
	var reference map[string]json.RawMessage
	if err := json.Unmarshal(originalDraft.ReferencePayload, &reference); err != nil {
		return nil, nil, apperror.New(apperror.InvalidState, "inspectionId", "video reference is invalid")
	}
	if reference == nil {
		reference = make(map[string]json.RawMessage)
	}
	projectionJSON, _ := json.Marshal(projection)
	reference["videoRecapture"] = projectionJSON
	encoded, _ := json.Marshal(reference)
	return []capturecore.Requirement{supplement}, encoded, nil
}
