// Package classify_inspection coordinates terminal comparison jobs into one
// deterministic immutable inspection classification.
package classify_inspection

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	analysis "inspection/services/inspection/internal/features/analysis/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Dependencies struct{ Now func() time.Time }

// ErrClassificationPending asks the message consumer to retry after all jobs
// in the authoritative generation reach a terminal state.
var ErrClassificationPending = errors.New("classification prerequisites are pending")

func Setup(deps Dependencies) (func(context.Context, *gorm.DB, events.RawEnvelope) error, error) {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var payload struct {
			InspectionID identity.ID `json:"inspectionId"`
			Force        bool        `json:"force"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.InspectionID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		var inspection database.Inspection
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", envelope.TenantID, payload.InspectionID).First(&inspection).Error; err != nil {
			return err
		}
		var jobs []database.ComparisonJob
		var err error
		jobQuery := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=?", envelope.TenantID, payload.InspectionID)
		var activeVideoPlan identity.ID
		if inspection.MediaKind == "VIDEO" {
			if inspection.ActiveVideoPlanID == nil {
				return messaging.ErrPermanent
			}
			var plan database.VideoPlan
			if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND inspection_id=? AND status='SEALED'", envelope.TenantID, *inspection.ActiveVideoPlanID, payload.InspectionID).Take(&plan).Error; err != nil {
				return err
			}
			activeVideoPlan = plan.ID
			jobQuery = jobQuery.Where("video_plan_id=?", plan.ID)
		} else {
			jobQuery = jobQuery.Where("id IN (SELECT DISTINCT ON (requirement_key) id FROM analysis.comparison_jobs WHERE tenant_id=? AND inspection_id=? ORDER BY requirement_key, created_at DESC, id DESC)", envelope.TenantID, payload.InspectionID)
		}
		if err := jobQuery.Order("requirement_key ASC,video_core_group ASC").Find(&jobs).Error; err != nil {
			return err
		}
		if inspection.MediaKind == "VIDEO" {
			var itemCount int
			jobs, itemCount, err = loadVideoPlanJobs(ctx, tx, envelope.TenantID, payload.InspectionID, activeVideoPlan)
			if err != nil {
				return err
			}
			if len(jobs) == 0 && itemCount == 0 {
				return messaging.ErrPermanent
			}
		}
		if len(jobs) == 0 {
			return messaging.ErrPermanent
		}
		for _, job := range jobs {
			if job.Status != "COMPLETED" && job.Status != "INCONCLUSIVE" && job.Status != "FAILED" {
				return ErrClassificationPending
			}
		}
		var existing database.ClassificationRun
		existingQuery := tx.Where("tenant_id=? AND inspection_id=?", envelope.TenantID, payload.InspectionID)
		if inspection.MediaKind == "VIDEO" {
			existingQuery = existingQuery.Where("video_plan_id=?", activeVideoPlan)
		} else {
			existingQuery = existingQuery.Where("video_plan_id IS NULL")
		}
		if err := existingQuery.Order("created_at DESC").First(&existing).Error; err == nil {
			fresh := false
			for _, job := range jobs {
				if job.UpdatedAt.After(existing.CreatedAt) {
					fresh = true
					break
				}
			}
			if !fresh && !payload.Force {
				return nil
			}
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		facts := make([]analysis.ComparisonFacts, 0, len(jobs))
		for _, job := range jobs {
			fact := analysis.ComparisonFacts{Terminal: true}
			if job.Status == "INCONCLUSIVE" {
				fact.Inconclusive = true
			}
			if job.Status == "FAILED" {
				fact.TechnicalFailure = true
			}
			if inspection.MediaKind == "VIDEO" {
				var item database.VideoPlanItem
				var itemErr error
				if job.VideoPlanID != nil && *job.VideoPlanID == activeVideoPlan && job.VideoPlanItemID != nil {
					itemErr = tx.WithContext(ctx).Where("tenant_id=? AND id=? AND plan_id=?", envelope.TenantID, *job.VideoPlanItemID, activeVideoPlan).Take(&item).Error
				} else {
					itemErr = tx.WithContext(ctx).Where("tenant_id=? AND plan_id=? AND carried_result_job_id=?", envelope.TenantID, activeVideoPlan, job.ID).Take(&item).Error
				}
				if itemErr != nil {
					return messaging.ErrPermanent
				}
				if item.Outcome == "INCONCLUSIVE" {
					fact.Inconclusive = true
				}
				if item.Outcome == "TECHNICAL_FAILURE" || job.Status == "FAILED" {
					fact.TechnicalFailure = true
				}
				var openRequirement int64
				if err := tx.Model(&database.RecaptureRequirement{}).Where("tenant_id=? AND requirement_key=? AND request_id IN (SELECT id FROM recapture.requests WHERE tenant_id=? AND inspection_id=?) AND status IN ('OPEN','EXPIRED')", envelope.TenantID, item.RequirementKey, envelope.TenantID, payload.InspectionID).Count(&openRequirement).Error; err != nil {
					return err
				}
				fact.Uncorrected = openRequirement > 0
				var unresolvedRequirement int64
				if err := tx.Model(&database.RecaptureRequirement{}).Where("tenant_id=? AND requirement_key=? AND request_id IN (SELECT id FROM recapture.requests WHERE tenant_id=? AND inspection_id=?) AND status='UNRESOLVED'", envelope.TenantID, item.RequirementKey, envelope.TenantID, payload.InspectionID).Count(&unresolvedRequirement).Error; err != nil {
					return err
				}
				fact.Inconclusive = fact.Inconclusive || unresolvedRequirement > 0
			} else {
				var answer database.RequirementAnswer
				answerErr := tx.Where("tenant_id=? AND requirement_key=? AND draft_id IN (SELECT id FROM capture.capture_drafts WHERE tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?))", envelope.TenantID, job.RequirementKey, envelope.TenantID, envelope.TenantID, payload.InspectionID).Order("updated_at DESC").First(&answer).Error
				if answerErr == gorm.ErrRecordNotFound {
					fact.Missing = true
				} else if answerErr != nil {
					return answerErr
				} else {
					var flags []string
					if err := json.Unmarshal(answer.Flags, &flags); err == nil && len(flags) > 0 {
						fact.Flagged = true
					}
					if answer.ImpossibilityReason != "" {
						fact.Missing = true
					}
				}
			}
			var runs []database.AnalysisRun
			if err := tx.Where("tenant_id=? AND job_id=?", envelope.TenantID, job.ID).Order("created_at DESC").Find(&runs).Error; err != nil {
				return err
			}
			if len(runs) == 0 {
				if job.Status != "FAILED" {
					fact.Inconclusive = true
				}
				facts = append(facts, fact)
				continue
			}
			var findings []database.FindingRecord
			if err := tx.Where("tenant_id=? AND analysis_run_id=?", envelope.TenantID, runs[0].ID).Order("created_at ASC").Find(&findings).Error; err != nil {
				return err
			}
			for _, finding := range findings {
				var evidence []string
				_ = json.Unmarshal(finding.Evidence, &evidence)
				fact.Findings = append(fact.Findings, analysis.Finding{Category: finding.Category, Title: finding.Title, Description: finding.Description, Severity: finding.Severity, Quality: finding.Quality, RecommendedAction: finding.RecommendedAction, Confidence: finding.Confidence, EvidenceIDs: evidence})
			}
			facts = append(facts, fact)
		}
		if inspection.StageID != nil {
			var stage database.ProjectStage
			if err := tx.Where("tenant_id=? AND id=?", envelope.TenantID, *inspection.StageID).First(&stage).Error; err == nil && stage.Status == "SKIPPED" {
				for i := range facts {
					facts[i].Skipped = true
				}
			}
		}
		pendingRecapture, err := hasPendingVideoRecapture(ctx, tx, envelope.TenantID, payload.InspectionID)
		if err != nil {
			return err
		}
		if pendingRecapture {
			for i := range facts {
				facts[i].Uncorrected = true
			}
		}
		decision := analysis.Classify(facts)
		reasons, _ := json.Marshal(decision.ReasonCodes)
		now := deps.Now().UTC()
		row := database.ClassificationRun{ID: identity.NewID(), TenantID: envelope.TenantID, InspectionID: payload.InspectionID, PromptSnapshotID: inspection.AnalysisPromptSnapshotID, VideoPlanID: nil, Classification: decision.Classification, ReasonCodes: reasons, CreatedAt: now}
		if inspection.MediaKind == "VIDEO" {
			row.VideoPlanID = &activeVideoPlan
		}
		created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 0 {
			return nil
		}
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", envelope.TenantID, payload.InspectionID).Updates(map[string]any{"status": "COMPLETED", "updated_at": now, "version": gorm.Expr("version + 1")}).Error; err != nil {
			return err
		}
		eventID := identity.NewID()
		eventPayload := map[string]any{"inspectionId": payload.InspectionID, "classification": decision.Classification, "reasonCodes": decision.ReasonCodes, "classificationRunId": row.ID}
		if row.VideoPlanID != nil {
			eventPayload["videoPlanId"] = *row.VideoPlanID
		}
		return messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "inspection.classified.v1", SchemaVersion: 1, OccurredAt: now, TenantID: envelope.TenantID, AggregateID: payload.InspectionID, CorrelationID: envelope.CorrelationID, CausationID: envelope.ID.String(), Payload: eventPayload})
	}, nil
}

func hasPendingVideoRecapture(ctx context.Context, tx *gorm.DB, tenantID, inspectionID identity.ID) (bool, error) {
	var count int64
	err := tx.WithContext(ctx).Model(&database.RecaptureRequest{}).Where("tenant_id=? AND inspection_id=? AND status IN ('REQUESTED','ACCESSED','RECEIVED')", tenantID, inspectionID).Count(&count).Error
	return count > 0, err
}

func loadVideoPlanJobs(ctx context.Context, tx *gorm.DB, tenantID, inspectionID, planID identity.ID) ([]database.ComparisonJob, int, error) {
	var jobs []database.ComparisonJob
	if err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=? AND video_plan_id=?", tenantID, inspectionID, planID).Order("requirement_key ASC,video_core_group ASC").Find(&jobs).Error; err != nil {
		return nil, 0, err
	}
	var items []database.VideoPlanItem
	if err := tx.WithContext(ctx).Where("tenant_id=? AND plan_id=?", tenantID, planID).Order("requirement_key ASC").Find(&items).Error; err != nil {
		return nil, 0, err
	}
	var plan database.VideoPlan
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, planID).Take(&plan).Error; err != nil {
		return nil, 0, err
	}
	ancestors := make(map[identity.ID]struct{})
	current := plan
	for depth := 0; current.ParentPlanID != nil; depth++ {
		if depth >= 32 {
			return nil, len(items), messaging.ErrPermanent
		}
		parentID := *current.ParentPlanID
		if _, exists := ancestors[parentID]; exists {
			return nil, len(items), messaging.ErrPermanent
		}
		ancestors[parentID] = struct{}{}
		var ancestor database.VideoPlan
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, parentID).Take(&ancestor).Error; err != nil {
			return nil, len(items), err
		}
		current = ancestor
		if current.InspectionID != inspectionID {
			return nil, len(items), messaging.ErrPermanent
		}
	}
	for _, item := range items {
		if item.CarriedResultJobID == nil {
			continue
		}
		if plan.ParentPlanID == nil {
			return nil, len(items), messaging.ErrPermanent
		}
		var carried database.ComparisonJob
		if len(ancestors) == 0 {
			return nil, len(items), messaging.ErrPermanent
		}
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND inspection_id=? AND video_plan_id IN ? AND status IN ('COMPLETED','INCONCLUSIVE','FAILED')", tenantID, *item.CarriedResultJobID, inspectionID, identityIDs(ancestors)).Take(&carried).Error; err != nil {
			return nil, len(items), err
		}
		jobs = append(jobs, carried)
	}
	return jobs, len(items), nil
}

func identityIDs(ids map[identity.ID]struct{}) []identity.ID {
	result := make([]identity.ID, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	return result
}
