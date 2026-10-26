package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	inventoryexecutor "inspection/services/inspection/internal/features/analysis/inventory/core"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// importComparison pre-fills a comparative draft with what the inspection
// observed. Only the current generation of each COMPARE requirement counts: a
// re-analysis keeps earlier generations for history, never for the next
// inventory. Condition changes, worsened or improved, become pending
// suggestions backed by the confirmed current evidence; objects outside the
// inventory become pending items, in a new environment when the capture added
// one.
func (s Service) importComparison(tx *gorm.DB, tenantID, inspectionID, draftID identity.ID) error {
	var all []database.InventoryJob
	if err := tx.Where("tenant_id=? AND inspection_id=? AND kind='COMPARE'", tenantID, inspectionID).Find(&all).Error; err != nil {
		return err
	}
	jobByID := map[identity.ID]database.InventoryJob{}
	jobIDs := []identity.ID{}
	for _, job := range inventoryexecutor.LatestGeneration(all) {
		if job.Status == "SUCCEEDED" {
			jobByID[job.ID] = job
			jobIDs = append(jobIDs, job.ID)
		}
	}
	if len(jobIDs) == 0 {
		return nil
	}
	var results []database.InventoryItemResult
	if err := tx.Where("tenant_id=? AND job_id IN ?", tenantID, jobIDs).Order("job_id, id").Find(&results).Error; err != nil {
		return err
	}
	environments := comparisonEnvironments{tenantID: tenantID, inspectionID: inspectionID, draftID: draftID, now: s.now()}
	for _, itemResult := range results {
		switch itemResult.Status {
		case "CONDITION_CHANGED", "IMPROVED":
			if itemResult.ItemIdentityID == nil || itemResult.ConditionAfter == nil {
				continue
			}
			if err := s.suggestCondition(tx, tenantID, draftID, itemResult); err != nil {
				return err
			}
		case "OUTSIDE_INVENTORY":
			job := jobByID[itemResult.JobID]
			if job.EnvironmentIdentityID == nil || itemResult.OutsideTypeCode == nil || itemResult.OutsideName == nil {
				continue
			}
			env, ok, err := environments.resolve(tx, job)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			created := database.InventoryItem{ID: identity.NewID(), TenantID: tenantID, OriginVersionID: draftID, EnvironmentID: env.ID, IdentityID: identity.NewID(), TypeCode: *itemResult.OutsideTypeCode, Name: *itemResult.OutsideName, Condition: "NOT_ASSESSABLE", Observation: "OBSERVED", Origin: "AI", Decision: "PENDING", Version: 1, Suggested: marshal(map[string]any{"status": "OUTSIDE_INVENTORY"}), CreatedAt: s.now(), UpdatedAt: s.now()}
			if err := tx.Create(&created).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// suggestCondition sends a copied item back to review with the observed
// condition and puts the confirmed current evidence ahead of the historical
// evidence, so the reviewer sees what supports the suggestion and the next
// comparison starts from it.
func (s Service) suggestCondition(tx *gorm.DB, tenantID, draftID identity.ID, itemResult database.InventoryItemResult) error {
	var copied database.InventoryItem
	if err := tx.Where("tenant_id=? AND origin_version_id=? AND identity_id=?", tenantID, draftID, *itemResult.ItemIdentityID).Take(&copied).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	copied.Decision = "PENDING"
	copied.Suggested = marshal(map[string]any{"condition": *itemResult.ConditionAfter})
	copied.Version++
	if err := tx.Save(&copied).Error; err != nil {
		return err
	}
	var pairs []database.InventoryEvidencePair
	if err := tx.Where("tenant_id=? AND job_id=? AND item_identity_id=? AND confirmed", tenantID, itemResult.JobID, *itemResult.ItemIdentityID).Order("created_at, id").Find(&pairs).Error; err != nil {
		return err
	}
	var existing []database.InventoryItemEvidence
	if err := tx.Where("tenant_id=? AND item_id=?", tenantID, copied.ID).Find(&existing).Error; err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, row := range existing {
		seen[evidenceKey(row.Kind, row.MediaID, row.FrameID)] = true
	}
	current := make([]database.InventoryItemEvidence, 0, len(pairs))
	for _, pair := range pairs {
		key := evidenceKey(pair.CurrentKind, pair.CurrentMediaID, pair.CurrentFrameID)
		if seen[key] {
			continue
		}
		seen[key] = true
		current = append(current, database.InventoryItemEvidence{ID: identity.NewID(), TenantID: tenantID, ItemID: copied.ID, Kind: pair.CurrentKind, MediaID: pair.CurrentMediaID, FrameID: pair.CurrentFrameID, Position: len(current), CreatedAt: s.now()})
	}
	if len(current) == 0 {
		return nil
	}
	if err := tx.Model(&database.InventoryItemEvidence{}).Where("tenant_id=? AND item_id=?", tenantID, copied.ID).Update("position", gorm.Expr("position + ?", len(current))).Error; err != nil {
		return err
	}
	return tx.Create(&current).Error
}

func evidenceKey(kind string, media identity.ID, frame *identity.ID) string {
	if frame == nil {
		return kind + ":" + media.String()
	}
	return kind + ":" + media.String() + ":" + frame.String()
}

// comparisonEnvironments resolves the draft environment of a COMPARE job. An
// environment added during the comparative capture has no row in the previous
// baseline; it is created once from its capture requirement, pending review.
type comparisonEnvironments struct {
	tenantID, inspectionID, draftID identity.ID
	now                             time.Time
	requirements                    map[string]capturecore.Requirement
	loaded                          bool
}

func (c *comparisonEnvironments) resolve(tx *gorm.DB, job database.InventoryJob) (database.InventoryEnvironment, bool, error) {
	var env database.InventoryEnvironment
	err := tx.Where("tenant_id=? AND origin_version_id=? AND identity_id=?", c.tenantID, c.draftID, *job.EnvironmentIdentityID).Take(&env).Error
	if err == nil {
		return env, env.Decision != "REJECTED", nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return env, false, err
	}
	if err := c.loadRequirements(tx); err != nil {
		return env, false, err
	}
	requirement, ok := c.requirements[job.RequirementKey]
	if !ok || !requirement.AddedDuringCapture {
		// The environment was removed from the baseline the draft copies.
		return env, false, nil
	}
	name := requirement.EnvironmentName
	if name == "" {
		name = requirement.Label
	}
	typeCode := requirement.EnvironmentTypeCode
	if typeCode == "" {
		typeCode = "OTHER"
	}
	var position int64
	if err := tx.Model(&database.InventoryEnvironment{}).Select("COALESCE(MAX(position), -1) + 1").Where("tenant_id=? AND origin_version_id=?", c.tenantID, c.draftID).Scan(&position).Error; err != nil {
		return env, false, err
	}
	key := requirement.Key
	env = database.InventoryEnvironment{ID: identity.NewID(), TenantID: c.tenantID, OriginVersionID: c.draftID, IdentityID: *job.EnvironmentIdentityID, TypeCode: typeCode, Name: name, Expected: requirement.Expected, AddedDuringCapture: true, RequirementKey: &key, Decision: "PENDING", Position: int(position), Version: 1, CreatedAt: c.now, UpdatedAt: c.now}
	if err := tx.Create(&env).Error; err != nil {
		return env, false, err
	}
	return env, true, nil
}

// loadRequirements reads the requirements of the inspection's latest capture
// draft, the same source the COMPARE job analyzed.
func (c *comparisonEnvironments) loadRequirements(tx *gorm.DB) error {
	if c.loaded {
		return nil
	}
	c.loaded = true
	c.requirements = map[string]capturecore.Requirement{}
	var draft database.CaptureDraft
	if err := tx.Where("tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?)", c.tenantID, c.tenantID, c.inspectionID).Order("created_at DESC").Take(&draft).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	var requirements []capturecore.Requirement
	if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
		return fmt.Errorf("capture requirements: %w", err)
	}
	for _, requirement := range requirements {
		c.requirements[requirement.Key] = requirement
	}
	return nil
}
