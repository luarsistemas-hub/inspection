package prepare_video_comparisons

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DBPlanWriter atomically seals a plan, its exact frame membership, and the
// jobs that will be evaluated by the existing condition-analysis core.
type DBPlanWriter struct {
	DB  *gorm.DB
	Now func() time.Time
}

func (w DBPlanWriter) SealPlan(ctx context.Context, plan Plan) error {
	if w.DB == nil {
		return errors.New("analysis/prepare_video_comparisons: missing database")
	}
	if plan.ID == (identity.ID{}) || plan.TenantID == (identity.ID{}) || plan.InspectionID == (identity.ID{}) || plan.SubmissionID == (identity.ID{}) || plan.Generation < 1 || !validDigest(plan.Digest) {
		return fmt.Errorf("%w: incomplete durable plan", ErrInvalidPlan)
	}
	if err := ValidatePlan(plan); err != nil {
		return err
	}
	if plan.Digest != PlanDigest(plan) {
		return fmt.Errorf("%w: plan digest does not match sealed content", ErrInvalidPlan)
	}
	if w.Now == nil {
		w.Now = time.Now
	}
	now := w.Now().UTC()
	return w.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec("SELECT set_config('app.tenant_id', ?, true)", plan.TenantID.String()).Error; err != nil {
				return err
			}
		}
		var inspection database.Inspection
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=? AND asset_id=? AND media_kind='VIDEO'", plan.TenantID, plan.InspectionID, plan.AssetID).Take(&inspection).Error; err != nil {
			return fmt.Errorf("analysis: video plan inspection scope unavailable: %w", err)
		}
		if plan.ParentPlanID != nil {
			var parent database.VideoPlan
			if err := tx.Where("tenant_id=? AND id=? AND inspection_id=? AND status='SEALED'", plan.TenantID, *plan.ParentPlanID, inspection.ID).Take(&parent).Error; err != nil {
				return fmt.Errorf("%w: parent plan is unavailable", ErrInvalidPlan)
			}
			if parent.Generation+1 != plan.Generation || inspection.ActiveVideoPlanID == nil || *inspection.ActiveVideoPlanID != parent.ID || plan.CurrentPreparationID != parent.CurrentPreparationID || plan.SupplementPreparationID == nil {
				return fmt.Errorf("%w: child plan is outside the active parent generation", ErrInvalidPlan)
			}
		}
		var existing database.VideoPlan
		err := tx.Where("tenant_id=? AND submission_id=? AND generation=?", plan.TenantID, plan.SubmissionID, plan.Generation).Take(&existing).Error
		building := false
		if err == nil {
			if existing.ID == plan.ID && existing.PlanDigest == plan.Digest && existing.Status == "SEALED" {
				if err := persistVideoPlanPreparations(tx, plan, now); err != nil {
					return err
				}
				if err := persistPlanEvidenceReferences(tx, plan, now); err != nil {
					return err
				}
				return advanceActiveVideoPlan(tx, &inspection, existing)
			}
			if existing.ID != plan.ID || existing.Status != "BUILDING" || plan.LeaseToken == nil || existing.LeaseToken == nil || *existing.LeaseToken != *plan.LeaseToken || existing.LeaseUntil == nil || !existing.LeaseUntil.After(now) {
				return fmt.Errorf("%w: submission generation already has a different plan or lease", ErrInvalidPlan)
			}
			building = true
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		sealedAt := now
		row := database.VideoPlan{ID: plan.ID, TenantID: plan.TenantID, InspectionID: plan.InspectionID, SubmissionID: plan.SubmissionID, ParentPlanID: plan.ParentPlanID, ReferenceVersionID: plan.ReferenceVersionID, OriginPreparationID: plan.OriginPreparationID, CurrentPreparationID: plan.CurrentPreparationID, Generation: plan.Generation, PlanDigest: plan.Digest, Status: "SEALED", SealedAt: &sealedAt, CreatedAt: now}
		if err := validatePersistedFrameDigests(tx, plan); err != nil {
			return err
		}
		if building {
			row.LeaseToken = plan.LeaseToken
		} else if err := tx.Create(&row).Error; err != nil {
			return err
		}
		var prompt database.AnalysisPromptSnapshot
		if err := tx.Where("id=? AND analysis_type='REAL_ESTATE'", inspection.AnalysisPromptSnapshotID).Take(&prompt).Error; err != nil {
			return err
		}
		for _, item := range plan.Items {
			originIDs, err := json.Marshal(nonNilIDs(item.OriginFrames))
			if err != nil {
				return err
			}
			currentIDs, err := json.Marshal(nonNilIDs(item.CurrentFrames))
			if err != nil {
				return err
			}
			coreGroups, err := json.Marshal(nonNilGroups(item.CoreGroups))
			if err != nil {
				return err
			}
			currentGroups, err := json.Marshal(nonNilIDGroups(item.CurrentGroups))
			if err != nil {
				return err
			}
			itemID := identity.NewDeterministicID("inspection-video-plan-item", plan.ID.String()+":"+item.ComponentID.String())
			// ComponentID is the stable VideoScopeItem ID selected by the plan
			// builder; retain it in both the row and public requirement key.
			requirementKey := "video:" + item.ComponentID.String()
			itemRow := database.VideoPlanItem{ID: itemID, TenantID: plan.TenantID, PlanID: plan.ID, ComponentID: item.ComponentID, CarriedResultJobID: item.CarriedResultJobID, RequirementKey: requirementKey, Outcome: item.Outcome, Reason: item.Reason, InputDigest: item.InputDigest, OriginFrameIDs: originIDs, CurrentFrameIDs: currentIDs, CoreGroups: coreGroups, CurrentGroups: currentGroups, CreatedAt: now}
			if err := tx.Create(&itemRow).Error; err != nil {
				return err
			}
			if err := persistFrameMembership(tx, plan.TenantID, itemID, item, now); err != nil {
				return err
			}
			if item.CarriedResultJobID != nil {
				var carried database.ComparisonJob
				if err := tx.Where("tenant_id=? AND id=? AND inspection_id=? AND video_plan_id IN (WITH RECURSIVE ancestors(id,parent_plan_id,depth) AS (SELECT id,parent_plan_id,1 FROM analysis.video_plans WHERE tenant_id=? AND id=? UNION ALL SELECT p.id,p.parent_plan_id,a.depth+1 FROM analysis.video_plans p JOIN ancestors a ON p.id=a.parent_plan_id WHERE p.tenant_id=? AND a.depth<32) SELECT id FROM ancestors WHERE depth<=32) AND status IN ('COMPLETED','INCONCLUSIVE','FAILED')", plan.TenantID, *item.CarriedResultJobID, inspection.ID, plan.TenantID, *plan.ParentPlanID, plan.TenantID).Take(&carried).Error; err != nil {
					return fmt.Errorf("analysis: carried result is not a terminal job in the plan ancestry: %w", err)
				}
				continue
			}
			switch item.Outcome {
			case Matched:
				for groupIndex, group := range item.CoreGroups {
					if err := persistComparisonJob(tx, plan, inspection.ID, prompt.ID, prompt.CanonicalDigest, itemID, requirementKey, item.InputDigest, groupIndex, group, nil, nil, now); err != nil {
						return err
					}
				}
			case CurrentOnly:
				for groupIndex, group := range item.CurrentGroups {
					if err := persistComparisonJob(tx, plan, inspection.ID, prompt.ID, prompt.CanonicalDigest, itemID, requirementKey, item.InputDigest, groupIndex, nil, group, nil, now); err != nil {
						return err
					}
				}
			case Inconclusive:
				status := "INCONCLUSIVE"
				if err := persistComparisonJob(tx, plan, inspection.ID, prompt.ID, prompt.CanonicalDigest, itemID, requirementKey, item.InputDigest, 0, nil, nil, &status, now); err != nil {
					return err
				}
			case TechnicalFailure:
			// A technical failure is recorded on the sealed plan item and counted
			// as unprocessed. It must not enter the condition-analysis queue.
			default:
				return fmt.Errorf("%w: unsupported persisted outcome", ErrInvalidPlan)
			}
		}
		if building {
			result := tx.Model(&database.VideoPlan{}).Where("tenant_id=? AND id=? AND status='BUILDING' AND lease_token=? AND lease_until>?", plan.TenantID, plan.ID, plan.LeaseToken, now).Updates(map[string]any{"status": "SEALED", "plan_digest": plan.Digest, "sealed_at": sealedAt, "lease_token": nil, "lease_until": nil, "error_code": ""})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("%w: plan lease was lost", ErrInvalidPlan)
			}
		}
		if err := persistVideoPlanPreparations(tx, plan, now); err != nil {
			return err
		}
		if err := persistPlanEvidenceReferences(tx, plan, now); err != nil {
			return err
		}
		return advanceActiveVideoPlan(tx, &inspection, row)
	})
}

func persistVideoPlanPreparations(tx *gorm.DB, plan Plan, now time.Time) error {
	preparations := []database.VideoPlanPreparation{{TenantID: plan.TenantID, PlanID: plan.ID, PreparationID: plan.CurrentPreparationID, Role: "MAIN", CreatedAt: now}}
	if plan.SupplementPreparationID != nil {
		preparations = append(preparations, database.VideoPlanPreparation{TenantID: plan.TenantID, PlanID: plan.ID, PreparationID: *plan.SupplementPreparationID, Role: "SUPPLEMENT", CreatedAt: now})
	}
	if plan.OriginPreparationID != nil {
		preparations = append(preparations, database.VideoPlanPreparation{TenantID: plan.TenantID, PlanID: plan.ID, PreparationID: *plan.OriginPreparationID, Role: "ORIGIN", CreatedAt: now})
	}
	seen := make(map[identity.ID]string, len(preparations))
	for _, preparation := range preparations {
		if role, ok := seen[preparation.PreparationID]; ok {
			if role != preparation.Role {
				return fmt.Errorf("%w: preparation has conflicting plan roles", ErrInvalidPlan)
			}
			continue
		}
		seen[preparation.PreparationID] = preparation.Role
	}
	for _, preparation := range preparations {
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "plan_id"}, {Name: "preparation_id"}}, DoNothing: true}).Create(&preparation).Error; err != nil {
			return err
		}
		var stored database.VideoPlanPreparation
		if err := tx.Where("tenant_id=? AND plan_id=? AND preparation_id=?", preparation.TenantID, preparation.PlanID, preparation.PreparationID).Take(&stored).Error; err != nil {
			return err
		}
		if stored.Role != preparation.Role {
			return fmt.Errorf("%w: persisted preparation role differs from plan", ErrInvalidPlan)
		}
	}
	return nil
}

func persistPlanEvidenceReferences(tx *gorm.DB, plan Plan, now time.Time) error {
	preparations := []identity.ID{plan.CurrentPreparationID}
	if plan.SupplementPreparationID != nil {
		preparations = append(preparations, *plan.SupplementPreparationID)
	}
	if plan.OriginPreparationID != nil {
		preparations = append(preparations, *plan.OriginPreparationID)
	}
	for _, preparationID := range preparations {
		reference := database.VideoEvidenceReference{
			ID:            identity.NewID(),
			TenantID:      plan.TenantID,
			PreparationID: preparationID,
			OwnerType:     "VIDEO_PLAN",
			OwnerID:       plan.ID,
			CreatedAt:     now,
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "preparation_id"}, {Name: "owner_type"}, {Name: "owner_id"}}, DoNothing: true}).Create(&reference).Error; err != nil {
			return err
		}
	}
	return nil
}

func advanceActiveVideoPlan(tx *gorm.DB, inspection *database.Inspection, candidate database.VideoPlan) error {
	if inspection.ActiveVideoPlanID != nil {
		var active database.VideoPlan
		if err := tx.Where("tenant_id=? AND id=? AND inspection_id=? AND status='SEALED'", inspection.TenantID, *inspection.ActiveVideoPlanID, inspection.ID).Take(&active).Error; err != nil {
			return err
		}
		if active.Generation >= candidate.Generation {
			return nil
		}
	}
	result := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", inspection.TenantID, inspection.ID).Update("active_video_plan_id", candidate.ID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("analysis: inspection generation pointer was not updated")
	}
	inspection.ActiveVideoPlanID = &candidate.ID
	return nil
}

func validatePersistedFrameDigests(tx *gorm.DB, plan Plan) error {
	var currentPreparation database.VideoPreparationJob
	if err := tx.Where("tenant_id=? AND id=? AND status='READY'", plan.TenantID, plan.CurrentPreparationID).Take(&currentPreparation).Error; err != nil {
		return fmt.Errorf("%w: current preparation is not ready", ErrInvalidPlan)
	}
	if plan.ParentPlanID == nil && (currentPreparation.SubmissionID == nil || *currentPreparation.SubmissionID != plan.SubmissionID) {
		return fmt.Errorf("%w: current preparation is outside the submitted generation", ErrInvalidPlan)
	}
	currentFramesPreparation := plan.CurrentPreparationID
	if plan.ParentPlanID != nil {
		currentFramesPreparation = *plan.SupplementPreparationID
		var supplement database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=? AND status='READY'", plan.TenantID, currentFramesPreparation).Take(&supplement).Error; err != nil {
			return fmt.Errorf("%w: supplement preparation is not ready", ErrInvalidPlan)
		}
		if supplement.SubmissionID == nil || *supplement.SubmissionID != plan.SubmissionID {
			return fmt.Errorf("%w: supplement is outside submitted generation", ErrInvalidPlan)
		}
	}
	if plan.OriginPreparationID != nil {
		var originPreparation database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=? AND status='READY'", plan.TenantID, *plan.OriginPreparationID).Take(&originPreparation).Error; err != nil {
			return fmt.Errorf("%w: pinned origin preparation is not ready", ErrInvalidPlan)
		}
		var reference database.OriginVersion
		if plan.ReferenceVersionID == nil {
			return fmt.Errorf("%w: missing pinned reference version", ErrInvalidPlan)
		}
		err := tx.Where("tenant_id=? AND id=? AND media_kind='VIDEO'", plan.TenantID, *plan.ReferenceVersionID).Take(&reference).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: pinned video reference was not found", ErrInvalidPlan)
		}
		if err != nil {
			return err
		}
		if reference.VideoPreparationID == nil || *reference.VideoPreparationID != *plan.OriginPreparationID {
			return fmt.Errorf("%w: origin preparation does not match pinned reference", ErrInvalidPlan)
		}
	}
	ids := make([]identity.ID, 0)
	seen := make(map[identity.ID]struct{})
	for _, item := range plan.Items {
		for _, id := range append(append([]identity.ID(nil), item.OriginFrames...), item.CurrentFrames...) {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
	}
	framesByID := make(map[identity.ID]Frame, len(ids))
	if len(ids) > 0 {
		var rows []database.VideoFrame
		if err := tx.Where("tenant_id=? AND id IN ?", plan.TenantID, ids).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			framesByID[row.ID] = Frame{ID: row.ID, TenantID: row.TenantID, AssetID: plan.AssetID, PreparationID: row.PreparationID, Digest: row.SHA256}
		}
		if len(rows) != len(ids) {
			return fmt.Errorf("%w: unknown persisted frame", ErrInvalidPlan)
		}
	}
	for _, item := range plan.Items {
		if item.CarriedResultJobID != nil {
			continue
		}
		origin := make([]Frame, 0, len(item.OriginFrames))
		current := make([]Frame, 0, len(item.CurrentFrames))
		for _, id := range item.OriginFrames {
			frame, ok := framesByID[id]
			expectedOrigin := plan.OriginPreparationID
			if plan.ParentPlanID != nil {
				expectedOrigin = &plan.CurrentPreparationID
			}
			if !ok || expectedOrigin == nil || frame.PreparationID != *expectedOrigin {
				return fmt.Errorf("%w: frame is outside pinned origin", ErrInvalidPlan)
			}
			origin = append(origin, frame)
		}
		for _, id := range item.CurrentFrames {
			frame, ok := framesByID[id]
			if !ok || frame.PreparationID != currentFramesPreparation {
				return fmt.Errorf("%w: frame is outside current preparation", ErrInvalidPlan)
			}
			current = append(current, frame)
		}
		if componentDigest(origin, current) != item.InputDigest {
			return fmt.Errorf("%w: item digest does not match persisted frames", ErrInvalidPlan)
		}
	}
	return nil
}

func persistComparisonJob(tx *gorm.DB, plan Plan, inspectionID, promptID identity.ID, promptDigest string, itemID identity.ID, requirementKey, itemDigest string, groupIndex int, pairs []Pair, current []identity.ID, terminalStatus *string, now time.Time) error {
	groupPayload := struct {
		Pairs   []Pair        `json:"pairs"`
		Current []identity.ID `json:"current"`
	}{Pairs: pairs, Current: current}
	encoded, err := json.Marshal(groupPayload)
	if err != nil {
		return err
	}
	groupDigest := sha256.Sum256(append([]byte(itemDigest+":"), encoded...))
	inputDigest := hex.EncodeToString(groupDigest[:])
	group := groupIndex
	jobID := identity.NewDeterministicID("inspection-video-comparison-job", plan.ID.String()+":"+itemID.String()+":"+fmt.Sprint(groupIndex))
	status := "PENDING"
	if terminalStatus != nil {
		status = *terminalStatus
	}
	job := database.ComparisonJob{ID: jobID, TenantID: plan.TenantID, InspectionID: inspectionID, VideoPlanID: idPointer(plan.ID), VideoPlanItemID: idPointer(itemID), VideoCoreGroup: &group, PromptSnapshotID: promptID, RequirementKey: requirementKey, ModelAlias: "inspection-vision", InputDigest: inputDigest, PromptDigest: promptDigest, Status: status, CreatedAt: now, UpdatedAt: now}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&job).Error; err != nil {
		return err
	}
	eventType := "analysis.video_comparison_requested.v1"
	payload := map[string]any{"comparisonJobId": jobID, "videoPlanId": plan.ID, "videoPlanItemId": itemID}
	if terminalStatus != nil {
		eventType = "analysis.comparison_completed.v1"
		payload = map[string]any{"inspectionId": inspectionID, "jobId": jobID, "status": status}
	}
	if terminalStatus != nil {
		envelope := events.Envelope[map[string]any]{ID: identity.NewDeterministicID("inspection-video-comparison-event", jobID.String()), Type: eventType, SchemaVersion: 1, OccurredAt: now, TenantID: plan.TenantID, AggregateID: jobID, CorrelationID: "video-plan:" + plan.ID.String(), Payload: payload}
		return messaging.AddOutbox(tx, envelope)
	}
	videoPayload := events.VideoComparisonRequestedPayload{ComparisonJobID: jobID, VideoPlanID: plan.ID, VideoPlanItemID: itemID}
	envelope := events.Envelope[events.VideoComparisonRequestedPayload]{ID: identity.NewDeterministicID("inspection-video-comparison-event", jobID.String()), Type: eventType, SchemaVersion: 1, OccurredAt: now, TenantID: plan.TenantID, AggregateID: jobID, CorrelationID: "video-plan:" + plan.ID.String(), Payload: videoPayload}
	return messaging.AddOutbox(tx, envelope)
}

func persistFrameMembership(tx *gorm.DB, tenantID, itemID identity.ID, item PlanItem, now time.Time) error {
	rows := make([]database.VideoPlanItemFrame, 0)
	seen := map[string]struct{}{}
	add := func(frameID identity.ID, role string, groupIndex, pairOrder int) {
		key := fmt.Sprintf("%s:%d", frameID.String(), groupIndex)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		rows = append(rows, database.VideoPlanItemFrame{ID: identity.NewDeterministicID("inspection-video-plan-frame", itemID.String()+":"+key+":"+role), TenantID: tenantID, PlanItemID: itemID, FrameID: frameID, Role: role, CoreGroup: groupIndex, PairOrder: pairOrder, CreatedAt: now})
	}
	for groupIndex, group := range item.CoreGroups {
		for pairIndex, pair := range group {
			add(pair.OriginFrameID, "ORIGIN", groupIndex, pairIndex)
			add(pair.CurrentFrameID, "CURRENT", groupIndex, pairIndex)
		}
	}
	for groupIndex, group := range item.CurrentGroups {
		for frameIndex, frameID := range group {
			add(frameID, "CURRENT", groupIndex, frameIndex)
		}
	}
	if len(rows) == 0 {
		for index, frameID := range item.OriginFrames {
			add(frameID, "ORIGIN", 0, index)
		}
		for index, frameID := range item.CurrentFrames {
			add(frameID, "CURRENT", 0, index)
		}
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.Create(&rows).Error
}

func nonNilIDs(ids []identity.ID) []identity.ID {
	if ids == nil {
		return []identity.ID{}
	}
	return ids
}

func nonNilGroups(groups [][]Pair) [][]Pair {
	if groups == nil {
		return [][]Pair{}
	}
	return groups
}

func nonNilIDGroups(groups [][]identity.ID) [][]identity.ID {
	if groups == nil {
		return [][]identity.ID{}
	}
	return groups
}

func idPointer(id identity.ID) *identity.ID { return &id }
