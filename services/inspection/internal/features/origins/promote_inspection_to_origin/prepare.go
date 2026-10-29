package promote_inspection_to_origin

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"inspection/libs/identity"
	requestjobs "inspection/services/inspection/internal/features/analysis/request_inventory_jobs"
	resolveprompt "inspection/services/inspection/internal/features/analysis/resolve_prompt"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func prepareCatalog(ctx context.Context, tx *gorm.DB, d Dependencies, tenant identity.ID, inspection database.Inspection, capture database.CaptureDraft, draftID identity.ID, now time.Time) (int, *identity.ID, int, error) {
	var requirements []capturecore.Requirement
	if err := json.Unmarshal(capture.Requirements, &requirements); err != nil {
		return 0, nil, 0, err
	}
	requirements = capturecore.ActiveRequirements(requirements)
	var answers []database.RequirementAnswer
	if err := tx.Where("tenant_id=? AND draft_id=?", tenant, capture.ID).Find(&answers).Error; err != nil {
		return 0, nil, 0, err
	}
	captured := make(map[string]database.RequirementAnswer, len(answers))
	for _, a := range answers {
		captured[a.RequirementKey] = a
	}
	var analysis database.AnalysisPromptSnapshot
	if err := tx.Where("id=?", inspection.AnalysisPromptSnapshotID).Take(&analysis).Error; err != nil {
		return 0, nil, 0, err
	}
	resolved, err := resolveprompt.ResolveTask(ctx, tx, analysis.AnalysisType, "INVENTORY_CATALOG", now)
	if err != nil {
		return 0, nil, 0, err
	}
	promptID := resolved.Snapshot.ID
	var maxGeneration int
	if err := tx.Model(&database.InventoryJob{}).Select("COALESCE(MAX(generation),0)").Where("tenant_id=? AND inspection_id=? AND kind='CATALOG'", tenant, inspection.ID).Scan(&maxGeneration).Error; err != nil {
		return 0, nil, 0, err
	}
	generation := maxGeneration + 1
	count, err := requestjobs.CreateCatalogJobs(ctx, tx, tenant, inspection, draftID, requirements, captured, generation, &promptID, now)
	return count, &promptID, generation, err
}

func copyPreviousCatalog(tx *gorm.DB, tenant, inspectionID identity.ID, capture database.CaptureDraft, target identity.ID, now time.Time) (bool, error) {
	var submission database.SubmissionVersion
	if err := tx.Where("tenant_id=? AND draft_id=?", tenant, capture.ID).Order("submitted_at DESC").First(&submission).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	var requirements []capturecore.Requirement
	if err := json.Unmarshal(capture.Requirements, &requirements); err != nil {
		return false, err
	}
	requirements = capturecore.ActiveRequirements(requirements)
	var answers []database.RequirementAnswer
	if err := tx.Where("tenant_id=? AND draft_id=?", tenant, capture.ID).Find(&answers).Error; err != nil {
		return false, err
	}
	captured := make(map[string]database.RequirementAnswer, len(answers))
	for _, answer := range answers {
		captured[answer.RequirementKey] = answer
	}
	expectedKeys := make(map[string]bool)
	for _, requirement := range requirements {
		_, possible, err := requestjobs.CapturedMedia(requirement, captured)
		if err != nil {
			return false, err
		}
		if possible {
			expectedKeys[requirement.Key] = true
		}
	}
	if len(expectedKeys) == 0 {
		return false, nil
	}
	var prior []database.OriginVersion
	if err := tx.Where("tenant_id=? AND source_inspection_id=? AND source IN ('CAPTURE','INSPECTION_PROMOTION') AND id<>?", tenant, inspectionID, target).Order("created_at DESC").Find(&prior).Error; err != nil {
		return false, err
	}
	for _, source := range prior {
		if source.DraftState != nil && *source.DraftState == "FAILED" {
			continue
		}
		var jobs []database.InventoryJob
		if err := tx.Where("tenant_id=? AND inspection_id=? AND origin_version_id=? AND kind='CATALOG' AND created_at>=?", tenant, inspectionID, source.ID, submission.SubmittedAt).Order("generation DESC").Find(&jobs).Error; err != nil {
			return false, err
		}
		if len(jobs) == 0 {
			continue
		}
		latestGeneration := jobs[0].Generation
		keys := map[string]bool{}
		for _, j := range jobs {
			if j.Generation != latestGeneration {
				break
			}
			if j.Status != "SUCCEEDED" {
				keys = nil
				break
			}
			keys[j.RequirementKey] = true
		}
		if keys == nil {
			continue
		}
		covered := true
		for key := range expectedKeys {
			if !keys[key] {
				covered = false
				break
			}
		}
		if !covered {
			continue
		}
		var usable int64
		if err := tx.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=? AND decision<>'REJECTED' AND merged_into_id IS NULL", tenant, source.ID).Count(&usable).Error; err != nil {
			return false, err
		}
		if usable == 0 {
			// An empty catalog is not a result worth reusing: catalogue again.
			continue
		}
		var envs []database.InventoryEnvironment
		if err := tx.Where("tenant_id=? AND origin_version_id=? AND decision<>'REJECTED'", tenant, source.ID).Order("position").Find(&envs).Error; err != nil {
			return false, err
		}
		clonedEnvs := make(map[identity.ID]identity.ID)
		for _, env := range envs {
			if env.RequirementKey == nil || !keys[*env.RequirementKey] {
				continue
			}
			oldID := env.ID
			env.ID = identity.NewID()
			env.OriginVersionID = target
			env.CreatedAt = now
			env.UpdatedAt = now
			if err := tx.Create(&env).Error; err != nil {
				return false, err
			}
			clonedEnvs[oldID] = env.ID
			var items []database.InventoryItem
			if err := tx.Where("tenant_id=? AND origin_version_id=? AND environment_id=? AND decision<>'REJECTED' AND merged_into_id IS NULL", tenant, source.ID, oldID).Order("created_at,id").Find(&items).Error; err != nil {
				return false, err
			}
			for _, item := range items {
				oldItem := item.ID
				item.ID = identity.NewID()
				item.OriginVersionID = target
				item.EnvironmentID = env.ID
				item.MergedIntoID = nil
				item.AutoAcceptedByPromotionID = nil
				item.CreatedAt = now
				item.UpdatedAt = now
				if err := tx.Create(&item).Error; err != nil {
					return false, err
				}
				var evidence []database.InventoryItemEvidence
				if err := tx.Where("tenant_id=? AND item_id=?", tenant, oldItem).Order("position").Find(&evidence).Error; err != nil {
					return false, err
				}
				for _, ev := range evidence {
					ev.ID = identity.NewID()
					ev.ItemID = item.ID
					ev.CreatedAt = now
					if err := tx.Create(&ev).Error; err != nil {
						return false, err
					}
				}
			}
		}
		if len(clonedEnvs) == 0 {
			continue
		}
		ready := "READY"
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", tenant, target).Update("draft_state", ready).Error; err != nil {
			return false, err
		}
		if err := inventorycore.EmitDraftReady(tx, tenant, target, inspectionID, now); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func submittedCapture(tx *gorm.DB, tenant, inspection identity.ID) (database.CaptureDraft, error) {
	var row database.CaptureDraft
	err := tx.Where("tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?) AND status='SUBMITTED'", tenant, tenant, inspection).Order("created_at DESC").First(&row).Error
	return row, err
}

func requirementsHaveEnvironment(capture database.CaptureDraft) bool {
	var reqs []capturecore.Requirement
	if json.Unmarshal(capture.Requirements, &reqs) != nil {
		return false
	}
	for _, r := range capturecore.ActiveRequirements(reqs) {
		if len(r.Key) >= 4 && r.Key[:4] == "env:" {
			return true
		}
	}
	return false
}
