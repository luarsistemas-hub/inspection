// Package classify_inspection coordinates terminal comparison jobs into one
// deterministic immutable inspection classification.
package classify_inspection

import (
	"context"
	"encoding/json"
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

func Setup(deps Dependencies) (func(context.Context, *gorm.DB, events.RawEnvelope) error, error) {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var payload struct {
			InspectionID identity.ID `json:"inspectionId"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.InspectionID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		var inspection database.Inspection
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, payload.InspectionID).First(&inspection).Error; err != nil {
			return err
		}
		var jobs []database.ComparisonJob
		if err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=?", envelope.TenantID, payload.InspectionID).Order("requirement_key ASC").Find(&jobs).Error; err != nil {
			return err
		}
		if len(jobs) == 0 {
			return messaging.ErrPermanent
		}
		for _, job := range jobs {
			if job.Status != "COMPLETED" && job.Status != "INCONCLUSIVE" {
				return nil
			}
		}
		var existing database.ClassificationRun
		if err := tx.Where("tenant_id=? AND inspection_id=?", envelope.TenantID, payload.InspectionID).Order("created_at DESC").First(&existing).Error; err == nil {
			fresh := false
			for _, job := range jobs {
				if job.UpdatedAt.After(existing.CreatedAt) {
					fresh = true
					break
				}
			}
			if !fresh {
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
			var runs []database.AnalysisRun
			if err := tx.Where("tenant_id=? AND job_id=?", envelope.TenantID, job.ID).Order("created_at DESC").Find(&runs).Error; err != nil {
				return err
			}
			if len(runs) == 0 {
				fact.Inconclusive = true
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
		}
		if inspection.StageID != nil {
			var stage database.ProjectStage
			if err := tx.Where("tenant_id=? AND id=?", envelope.TenantID, *inspection.StageID).First(&stage).Error; err == nil && stage.Status == "SKIPPED" {
				for i := range facts {
					facts[i].Skipped = true
				}
			}
		}
		var pendingRecapture int64
		if err := tx.Model(&database.RecaptureRequirement{}).Where("tenant_id=? AND request_id IN (SELECT id FROM recapture.requests WHERE tenant_id=? AND inspection_id=?) AND status IN ('OPEN','EXPIRED')", envelope.TenantID, envelope.TenantID, payload.InspectionID).Count(&pendingRecapture).Error; err != nil {
			return err
		}
		if pendingRecapture > 0 {
			for i := range facts {
				facts[i].Uncorrected = true
			}
		}
		decision := analysis.Classify(facts)
		reasons, _ := json.Marshal(decision.ReasonCodes)
		now := deps.Now().UTC()
		row := database.ClassificationRun{ID: identity.NewID(), TenantID: envelope.TenantID, InspectionID: payload.InspectionID, ProfileVersionID: inspection.AnalysisProfileVersionID, Classification: decision.Classification, ReasonCodes: reasons, CreatedAt: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", envelope.TenantID, payload.InspectionID).Updates(map[string]any{"status": "COMPLETED", "updated_at": now, "version": gorm.Expr("version + 1")}).Error; err != nil {
			return err
		}
		eventID := identity.NewID()
		return messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "inspection.classified.v1", SchemaVersion: 1, OccurredAt: now, TenantID: envelope.TenantID, AggregateID: payload.InspectionID, CorrelationID: envelope.CorrelationID, CausationID: envelope.ID.String(), Payload: map[string]any{"inspectionId": payload.InspectionID, "classification": decision.Classification, "reasonCodes": decision.ReasonCodes, "classificationRunId": row.ID}})
	}, nil
}
