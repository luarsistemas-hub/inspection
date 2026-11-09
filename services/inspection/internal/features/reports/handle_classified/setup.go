// Package handle_classified builds the immutable report and triage projection
// after a classification event has committed.
package handle_classified

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	inventoryexecutor "inspection/services/inspection/internal/features/analysis/inventory/core"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/features/notifications/core"
	requestdelivery "inspection/services/inspection/internal/features/notifications/request_delivery"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	generatesnapshot "inspection/services/inspection/internal/features/reports/generate_snapshot"
	publicationcore "inspection/services/inspection/internal/features/reports/publication/core"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/observability"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Dependencies are the external adapters of the handler. Production wires
// CreateSnapshotInTx, which finalizes the report inside the consumer's own
// transaction; CreateSnapshot opens a separate transaction and is only suitable
// for fakes and tests that do not publish automatically.
type Dependencies struct {
	Notifications      core.NotificationService
	CreateSnapshot     func(context.Context, generatesnapshot.Input) (generatesnapshot.Result, error)
	CreateSnapshotInTx func(context.Context, *gorm.DB, generatesnapshot.Input) (generatesnapshot.Result, error)
	Now                func() time.Time
}

type requirementAnalysis struct {
	mode, status     string
	noRelevantChange *bool
}

// ErrAutomaticPublicationNeedsTransaction reports a handler wired with only the
// separate-transaction snapshot adapter while the tenant policy is AUTOMATIC.
var ErrAutomaticPublicationNeedsTransaction = errors.New("automatic publication requires the transactional snapshot adapter")

// ErrClassifiedPrerequisitePending makes the inbox retry a premature event.
var ErrClassifiedPrerequisitePending = errors.New("classified report prerequisites are pending")

// UpsertTriageReviewCase advances the durable review case for a new report version.
func UpsertTriageReviewCase(ctx context.Context, tx *gorm.DB, tenantID, inspectionID identity.ID, classification database.ClassificationRun, reportVersion int, now time.Time) error {
	if tx == nil {
		return fmt.Errorf("triage projection: missing database transaction")
	}
	tx = tx.WithContext(ctx)
	var inspection database.Inspection
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Select("status").Where("tenant_id=? AND id=?", tenantID, inspectionID).First(&inspection).Error; err != nil {
		return err
	}
	if inspection.Status == "CANCELED" || inspection.Status == "INVALIDATED" {
		return nil
	}
	var triage database.TriageCase
	err := tx.Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).First(&triage).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	if err == gorm.ErrRecordNotFound && classification.Classification != "CRITICAL" && classification.Classification != "ATTENTION" {
		return nil
	}
	if err == nil && triage.Status == "ARCHIVED" {
		return nil
	}
	if err == gorm.ErrRecordNotFound {
		triage = database.TriageCase{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, Status: "NEW", Classification: classification.Classification, ReasonCodes: classification.ReasonCodes, ReportVersion: reportVersion, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&triage).Error; err != nil {
			return err
		}
		return tx.Create(&database.TriageCaseEvent{ID: identity.NewID(), TenantID: tenantID, CaseID: triage.ID, ActorID: tenantID, Kind: "RESULT_READY", Body: fmt.Sprintf("Resultado %s · laudo v%d", classification.Classification, reportVersion), CreatedAt: now}).Error
	}
	if reportVersion <= triage.ReportVersion {
		return nil
	}
	if err := tx.Model(&triage).Updates(map[string]any{"status": "IN_REVIEW", "classification": classification.Classification, "reason_codes": classification.ReasonCodes, "report_version": reportVersion, "version": triage.Version + 1, "updated_at": now}).Error; err != nil {
		return err
	}
	return tx.Create(&database.TriageCaseEvent{ID: identity.NewID(), TenantID: tenantID, CaseID: triage.ID, ActorID: tenantID, Kind: "RESULT_READY", Body: fmt.Sprintf("Novo resultado %s · laudo v%d; revisão retomada.", classification.Classification, reportVersion), CreatedAt: now}).Error
}

// Setup validates the external adapters and creates the classified-event handler.
func Setup(deps Dependencies) (func(context.Context, *gorm.DB, events.RawEnvelope) error, error) {
	if deps.Notifications == nil || (deps.CreateSnapshot == nil && deps.CreateSnapshotInTx == nil) {
		return nil, fmt.Errorf("reports/handle_classified: missing notification or snapshot adapter")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	now := deps.Now
	return func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var payload events.InspectionClassifiedPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.InspectionID == (identity.ID{}) || payload.ClassificationRunID == (identity.ID{}) || payload.VideoPlanID != nil && *payload.VideoPlanID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		// rejectPermanently ends a v3 photo event (pinned descriptive scope)
		// whose inputs can never produce a valid report. Retrying would not help.
		// When a reissue request is still QUEUED for the inspection, the event is
		// acknowledged in this transaction and the request is closed as FAILED
		// instead of blocking new requests forever. Otherwise (for example, the
		// first classification) it returns messaging.ErrPermanent, so the event
		// is dead-lettered and stays replayable, as for video and v1/v2 events.
		// Nothing else has been written by then: the CRITICAL alert is requested
		// only after a snapshot exists.
		rejectPermanently := func(reason string, cause error) error {
			closed := tx.WithContext(ctx).Model(&database.AuditEvent{}).
				Where("tenant_id=? AND action='report.reissue_requested' AND target_id=? AND outcome='QUEUED'", envelope.TenantID, payload.InspectionID.String()).
				Update("outcome", "FAILED")
			if closed.Error != nil {
				return closed.Error
			}
			if closed.RowsAffected == 0 {
				// No reissue waits for this event: dead-letter it, as before, so
				// it can be replayed once its inputs are corrected.
				return fmt.Errorf("%w: %s: %v", messaging.ErrPermanent, reason, cause)
			}
			slog.WarnContext(ctx, "report_classified_permanent_failure", "tenantId", envelope.TenantID.String(), "inspectionId", payload.InspectionID.String(), "reason", reason, "error", cause)
			if recorder := observability.OutcomeRecorderFromContext(ctx); recorder != nil {
				recorder.Set("failed")
			}
			return nil
		}
		var inspection database.Inspection
		// FOR UPDATE, not SHARE: the handler later writes under this lock (and the
		// snapshot publishes through the same transaction), so a shared lock would
		// let two concurrent deliveries of one event deadlock on the upgrade. It is
		// the same lock handle_state_changed takes, keeping one lock order.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", envelope.TenantID, payload.InspectionID).First(&inspection).Error; err != nil {
			return err
		}
		var class database.ClassificationRun
		if err := tx.Where("tenant_id=? AND inspection_id=? AND id=?", envelope.TenantID, payload.InspectionID, payload.ClassificationRunID).First(&class).Error; err != nil {
			return err
		}
		if inspection.MediaKind == "VIDEO" {
			// An event that contradicts its own classification run is a contract
			// violation: it is dead-lettered without a receipt, as before.
			if payload.VideoPlanID == nil || class.VideoPlanID == nil || *payload.VideoPlanID != *class.VideoPlanID {
				return messaging.ErrPermanent
			}
		} else if payload.VideoPlanID != nil || class.VideoPlanID != nil {
			return messaging.ErrPermanent
		}
		var previous database.ClassificationRun
		previousClassification := ""
		if err := tx.Where("tenant_id=? AND inspection_id=? AND (created_at < ? OR (created_at = ? AND id < ?))", envelope.TenantID, payload.InspectionID, class.CreatedAt, class.CreatedAt, class.ID).Order("created_at DESC, id DESC").First(&previous).Error; err == nil {
			previousClassification = previous.Classification
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		// The CRITICAL alert is requested only after the report snapshot exists
		// (below): an event acknowledged without a report must not commit an
		// alert that points to no report.
		requestCriticalAlert := func() error {
			if previousClassification == "CRITICAL" || class.Classification != "CRITICAL" || inspection.Status == "CANCELED" || inspection.Status == "INVALIDATED" {
				return nil
			}
			configuredRecipients, err := loadCriticalRecipients(ctx, tx, envelope.TenantID, inspection)
			if err != nil {
				return err
			}
			recipients := make([]requestdelivery.Recipient, 0, len(configuredRecipients))
			for _, recipient := range configuredRecipients {
				recipients = append(recipients, requestdelivery.Recipient{ID: recipient.RecipientMembershipID.String(), ChannelID: recipient.ID.String(), Channel: recipient.Channel, Destination: recipient.Destination, Internal: true, Verified: recipient.VerifiedAt != nil, Selected: recipient.Selected})
			}
			return requestdelivery.Request(ctx, tx, deps.Notifications, requestdelivery.Input{TenantID: envelope.TenantID, InspectionID: payload.InspectionID, Previous: previousClassification, Current: class.Classification, Recipients: recipients})
		}
		var reference database.ReferenceSnapshot
		if err := tx.Where("tenant_id=? AND inspection_id=?", envelope.TenantID, payload.InspectionID).First(&reference).Error; err != nil {
			return err
		}
		referenceVersion := reference.ReferenceVersionID
		referenceVersionID := generatesnapshot.ReferenceFree
		if referenceVersion != nil {
			referenceVersionID = referenceVersion.String()
		}
		// Reports are always scoped to the inspection that produced them. Project
		// membership only adds stage context to the snapshot.
		mode := "HISTORICAL"
		var timeline []reportcore.TimelineEntry
		if inspection.ProjectID != nil {
			var stages []database.ProjectStage
			if err := tx.Where("tenant_id=? AND project_id=?", envelope.TenantID, *inspection.ProjectID).Order("position ASC").Find(&stages).Error; err == nil {
				for _, stage := range stages {
					timeline = append(timeline, reportcore.TimelineEntry{StageID: stage.ID.String(), Status: stage.Status, Position: stage.Position})
				}
			}
		}
		var videoPlanID *identity.ID
		if inspection.MediaKind == "VIDEO" {
			plan, err := resolveClassifiedVideoPlan(tx, envelope.TenantID, payload.InspectionID, payload.VideoPlanID)
			if err != nil {
				return err
			}
			videoPlanID = &plan.ID
		}
		jobs, err := loadClassifiedJobs(ctx, tx, envelope.TenantID, payload.InspectionID, videoPlanID)
		if err != nil {
			return err
		}
		for _, job := range jobs {
			if job.Status != "COMPLETED" && job.Status != "INCONCLUSIVE" && job.Status != "FAILED" {
				return ErrClassifiedPrerequisitePending
			}
		}
		findings := make([]reportcore.Finding, 0)
		analysisStatuses := make(map[string]requirementAnalysis)
		evidence := make([]reportcore.Evidence, 0)
		for _, job := range jobs {
			var run database.AnalysisRun
			if err := tx.Where("tenant_id=? AND job_id=?", envelope.TenantID, job.ID).Order("created_at DESC").First(&run).Error; err == nil {
				var output struct {
					NoRelevantChange *bool `json:"noRelevantChange"`
				}
				_ = json.Unmarshal(run.Output, &output)
				analysisMode := "CURRENT_ONLY"
				if inspection.MediaKind == "VIDEO" && videoPlanID != nil {
					var planItem database.VideoPlanItem
					var lookup *gorm.DB
					if job.VideoPlanID != nil && *job.VideoPlanID == *videoPlanID && job.VideoPlanItemID != nil {
						lookup = tx.Where("tenant_id=? AND id=? AND plan_id=?", envelope.TenantID, *job.VideoPlanItemID, *videoPlanID)
					} else {
						lookup = tx.Where("tenant_id=? AND plan_id=? AND carried_result_job_id=?", envelope.TenantID, *videoPlanID, job.ID)
					}
					if err := lookup.Take(&planItem).Error; err != nil {
						return messaging.ErrPermanent
					}
					if planItem.Outcome == "MATCHED" {
						analysisMode = "COMPARE_ORIGIN_CURRENT"
					}
				} else if reference.ReferenceVersionID != nil && reference.ComparisonMode == "FIXED_ORIGIN" && strings.HasPrefix(job.RequirementKey, "origin:") {
					analysisMode = "COMPARE_ORIGIN_CURRENT"
				}
				analysisStatuses[job.RequirementKey] = mergeRequirementAnalysis(analysisStatuses[job.RequirementKey], requirementAnalysis{mode: analysisMode, status: job.Status, noRelevantChange: output.NoRelevantChange})
				var rows []database.FindingRecord
				if err := tx.Where("tenant_id=? AND analysis_run_id=?", envelope.TenantID, run.ID).Order("created_at ASC").Find(&rows).Error; err != nil {
					return err
				}
				for _, row := range rows {
					var evidenceIDs []string
					_ = json.Unmarshal(row.Evidence, &evidenceIDs)
					findings = append(findings, reportcore.Finding{ID: row.ID.String(), Category: row.Category, Title: row.Title, Description: row.Description, Severity: row.Severity, Confidence: row.Confidence, EvidenceIDs: evidenceIDs, Quality: row.Quality, RecommendedAction: row.RecommendedAction})
				}
			} else if err != gorm.ErrRecordNotFound {
				return err
			}
			if inspection.MediaKind == "VIDEO" {
				continue
			}
			var answer database.RequirementAnswer
			if err := latestSubmittedAnswer(ctx, tx, envelope.TenantID, payload.InspectionID, job.RequirementKey, &answer); err != nil {
				if err == gorm.ErrRecordNotFound {
					continue
				}
				return err
			}
			var mediaIDs []identity.ID
			_ = json.Unmarshal(answer.MediaIDs, &mediaIDs)
			var flags []string
			_ = json.Unmarshal(answer.Flags, &flags)
			flags = reportFlags(job.RequirementKey, flags)
			for _, mediaID := range mediaIDs {
				item := reportcore.Evidence{ID: mediaID.String(), RequirementKey: job.RequirementKey, Role: "CURRENT", Flags: append([]string(nil), flags...)}
				var media database.MediaObject
				if err := tx.Where("tenant_id=? AND id=?", envelope.TenantID, mediaID).First(&media).Error; err == nil {
					item.Description = media.Description
					item.CaptureSource = media.CaptureSource
					if media.CapturedAt != nil {
						item.CapturedAt = media.CapturedAt.UTC().Format(time.RFC3339Nano)
					}
					item.Availability = "MISSING"
					var derivative database.MediaDerivative
					if err := tx.Where("tenant_id=? AND media_id=? AND kind=?", envelope.TenantID, mediaID, "DISPLAY").First(&derivative).Error; err == nil {
						item.DisplayDigest, item.Availability = derivative.SHA256, "AVAILABLE"
					} else if err != gorm.ErrRecordNotFound {
						return err
					}
					var mediaFlags []string
					if json.Unmarshal(media.Flags, &mediaFlags) == nil && len(mediaFlags) > 0 {
						item.Flags = append(item.Flags, reportFlags(job.RequirementKey, mediaFlags)...)
					}
				} else if err != gorm.ErrRecordNotFound {
					return err
				}
				evidence = append(evidence, item)
			}
		}
		if videoPlanID != nil {
			evidence, err = reportAllVideoPlanEvidence(ctx, tx, envelope.TenantID, *videoPlanID, jobs)
			if err != nil {
				return err
			}
		}
		context, requirements, referenceEvidence, err := reportSnapshotContext(ctx, tx, envelope.TenantID, inspection, reference, envelope.OccurredAt)
		if err != nil {
			return err
		}
		var videoGaps map[string]struct{}
		if videoPlanID != nil {
			var videoPlan database.VideoPlan
			if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, *videoPlanID).Take(&videoPlan).Error; err != nil {
				return err
			}
			videoRequirements, gaps, err := generatesnapshot.VideoPlanRequirements(ctx, tx, envelope.TenantID, videoPlan, reference.ReferenceVersionID != nil)
			if err != nil {
				return err
			}
			requirements = append(requirements, videoRequirements...)
			videoGaps = gaps
		}
		for i := range requirements {
			_, isGap := videoGaps[requirements[i].Key]
			if result, ok := analysisStatuses[requirements[i].Key]; ok && !isGap {
				requirements[i].AnalysisMode = result.mode
				requirements[i].AnalysisStatus = result.status
				requirements[i].NoRelevantChange = result.noRelevantChange
			} else if !isGap {
				requirements[i].AnalysisMode = "CURRENT_ONLY"
				requirements[i].AnalysisStatus = "PENDING"
			}
		}
		promptSnapshot, err := resolvePromptSnapshot(ctx, tx, class.PromptSnapshotID, inspection.AnalysisPromptSnapshotID)
		if err != nil {
			return err
		}
		evidence = append(referenceEvidence, evidence...)
		beforeInventory := make(map[string]bool, len(evidence))
		for _, item := range evidence {
			beforeInventory[item.ID] = true
		}
		inventorySection, err := buildInventoryReport(ctx, tx, envelope.TenantID, inspection, reference, class.CreatedAt, &evidence)
		if errors.Is(err, errClassificationSuperseded) {
			// Acknowledged without a version: the newer classification publishes.
			slog.InfoContext(ctx, "report_classified_superseded", "tenantId", envelope.TenantID.String(), "inspectionId", payload.InspectionID.String(), "classificationRunId", class.ID.String())
			return nil
		}
		if err != nil {
			return err
		}
		// Report v3 carries the complete comparison of every baseline item
		// beside the historical exception-first inventory section.
		comparison, err := buildComparisonReport(ctx, tx, envelope.TenantID, inspection, reference, &evidence)
		if err != nil {
			return err
		}
		promotedEvidence, err := promotedOriginReportEvidence(ctx, tx, envelope.TenantID, reference.ReferenceVersionID, requirements, evidence)
		if err != nil {
			return err
		}
		evidence = append(promotedEvidence, evidence...)
		evidence, err = includeSupplementalMedia(ctx, tx, envelope.TenantID, payload.InspectionID, evidence)
		if err != nil {
			return err
		}
		var catalogJobs []database.InventoryJob
		if err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=? AND kind='CATALOG'", envelope.TenantID, payload.InspectionID).Order("generation DESC, created_at DESC").Find(&catalogJobs).Error; err != nil {
			return err
		}
		catalogJobs = inventoryexecutor.LatestGeneration(catalogJobs)
		var checklist *reportcore.ChecklistSection
		var processing *reportcore.ProcessingState
		var scopeSnapshot *reportcore.ScopeSnapshot
		var handover *reportcore.HandoverSection
		var provenance *reportcore.Provenance
		if shouldBuildDescriptiveReport(inspection.MediaKind) {
			checklist, processing, scopeSnapshot, handover, provenance, err = buildDescriptiveReport(ctx, tx, envelope.TenantID, payload.InspectionID, evidence, findings, promptSnapshot.CanonicalDigest, aggregateReportProcessing(jobs, catalogJobs))
			if errors.Is(err, messaging.ErrPermanent) {
				return rejectPermanently("descriptive_input_invalid", err)
			}
			if err != nil {
				return err
			}
		}
		catalogFailed := false
		catalogIncomplete := false
		for _, job := range catalogJobs {
			if job.Status == "FAILED" {
				catalogFailed = true
			} else if job.Status != "SUCCEEDED" && job.Status != "COMPLETED" {
				catalogIncomplete = true
			}
		}
		applyCatalogAvailability(checklist, processing, catalogFailed, catalogIncomplete)
		if checklist == nil {
			comparison = nil
		}
		// Report v3: the exception-first inventory states the same per-item
		// results and counts as the complete comparison.
		alignInventoryWithComparison(inventorySection, comparison)
		// The alignment drops pairs the comparison does not state (a gap
		// never shows a pair): their images, added for the inventory
		// section only, would be orphans in the gallery and the index.
		if comparison != nil {
			evidence = dropOrphanInventoryEvidence(evidence, beforeInventory, inventorySection, comparison, checklist, handover, findings)
			keepProvenanceEvidence(provenance, evidence)
		}
		policy, err := effectivePublicationPolicy(ctx, tx, envelope.TenantID)
		if err != nil {
			return err
		}
		// A canceled or invalidated inspection never releases a report to
		// customers on its own, whatever the tenant policy says.
		publicationMode := policy.Mode
		if checklist != nil {
			publicationMode = publicationcore.Automatic
		}
		if inspection.Status == "CANCELED" || inspection.Status == "INVALIDATED" {
			publicationMode = ""
		}
		// Releasing in a separate transaction would take the inspection locks in the
		// opposite order of this handler, so AUTOMATIC needs the transactional adapter.
		if publicationMode == publicationcore.Automatic && deps.CreateSnapshotInTx == nil {
			return ErrAutomaticPublicationNeedsTransaction
		}
		snapshotInput := generatesnapshot.Input{PublicationMode: publicationMode, PublicationPolicyVersion: policy.Version, TenantID: envelope.TenantID, InspectionID: payload.InspectionID, VideoPlanID: videoPlanID, ProjectID: inspection.ProjectID, Mode: mode, Classification: class.Classification, TemplateVersionID: inspection.TemplateVersionID.String(), ReferenceVersionID: referenceVersionID, PromptDigest: promptSnapshot.CanonicalDigest, ReasonCodes: payload.ReasonCodes, Timeline: timeline, Coverage: map[string]string{}, Context: context, Requirements: requirements, Findings: findings, Evidence: evidence, Inventory: inventorySection, Checklist: checklist, Processing: processing, Scope: scopeSnapshot, Handover: handover, Provenance: provenance, Comparison: comparison}
		var result generatesnapshot.Result
		// A v3 snapshot is created behind a savepoint so a permanent rejection
		// acknowledges the event without committing any partial snapshot write.
		savepoint := checklist != nil && deps.CreateSnapshotInTx != nil
		if savepoint {
			if err := tx.SavePoint(reportSnapshotSavepoint).Error; err != nil {
				return err
			}
		}
		if deps.CreateSnapshotInTx != nil {
			result, err = deps.CreateSnapshotInTx(ctx, tx, snapshotInput)
		} else {
			result, err = deps.CreateSnapshot(ctx, snapshotInput)
		}
		if checklist == nil {
			// Video and legacy v1/v2 reports keep their dead-letter behavior.
			if errors.Is(err, generatesnapshot.ErrFrameIntegrity) {
				return messaging.ErrPermanent
			}
		} else if reason := permanentSnapshotFailure(err); reason != "" {
			if savepoint {
				if rollbackErr := tx.RollbackTo(reportSnapshotSavepoint).Error; rollbackErr != nil {
					return rollbackErr
				}
			}
			return rejectPermanently(reason, err)
		}
		if err != nil {
			return err
		}
		if err := requestCriticalAlert(); err != nil {
			return err
		}
		now := now().UTC()
		if err := UpsertTriageReviewCase(ctx, tx, envelope.TenantID, payload.InspectionID, class, result.Version, now); err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Model(&database.AuditEvent{}).
			Where("tenant_id=? AND action='report.reissue_requested' AND target_id=? AND outcome='QUEUED'", envelope.TenantID, payload.InspectionID.String()).
			Update("outcome", "COMPLETED").Error; err != nil {
			return err
		}
		eventID := identity.NewID()
		return messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "report.snapshot_created.v1", SchemaVersion: 1, OccurredAt: now, TenantID: envelope.TenantID, AggregateID: payload.InspectionID, CorrelationID: envelope.CorrelationID, CausationID: envelope.ID.String(), Payload: map[string]any{"snapshotId": result.SnapshotID, "inspectionId": payload.InspectionID, "version": result.Version}})

	}, nil
}

const reportSnapshotSavepoint = "report_snapshot_v3"

// permanentSnapshotFailure names a v3 snapshot error that no retry can fix.
func permanentSnapshotFailure(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, generatesnapshot.ErrFrameIntegrity):
		return "frame_integrity"
	case errors.Is(err, reportcore.ErrInvalidSnapshot):
		return "snapshot_validation_failed"
	case errors.Is(err, messaging.ErrPermanent):
		return "snapshot_rejected"
	default:
		return ""
	}
}

func shouldBuildDescriptiveReport(mediaKind string) bool { return mediaKind == "PHOTO" }

func aggregateReportProcessing(jobs []database.ComparisonJob, catalogJobs []database.InventoryJob) string {
	if len(jobs) == 0 && len(catalogJobs) == 0 {
		return "INCONCLUSIVE"
	}
	status := "COMPLETED"
	for _, job := range jobs {
		if job.Status == "FAILED" {
			return "FAILED"
		}
		if job.Status == "INCONCLUSIVE" {
			status = "INCONCLUSIVE"
		}
		if job.Status != "COMPLETED" && job.Status != "FAILED" {
			status = "INCONCLUSIVE"
		}
	}
	for _, job := range catalogJobs {
		if job.Status == "FAILED" {
			return "FAILED"
		}
		if job.Status != "SUCCEEDED" && job.Status != "COMPLETED" {
			status = "INCONCLUSIVE"
		}
	}
	return status
}

func applyCatalogAvailability(checklist *reportcore.ChecklistSection, processing *reportcore.ProcessingState, failed, incomplete bool) {
	if !failed && !incomplete {
		return
	}
	if checklist != nil {
		for i := range checklist.Rooms {
			for j := range checklist.Rooms[i].Components {
				component := &checklist.Rooms[i].Components[j]
				component.Description = ""
				component.Condition = ""
				component.AssessedScope = ""
				if failed {
					component.Limitations = append(component.Limitations, "Falha na catalogação; a condição não foi determinada.")
				} else {
					component.Limitations = append(component.Limitations, "Catalogação pendente ou inconclusiva; a condição não foi determinada.")
				}
			}
		}
	}
	if processing == nil {
		return
	}
	if failed {
		processing.Status = "FAILED"
		processing.Message = "A catalogação dos componentes falhou; suas condições não foram determinadas."
	} else if processing.Status != "FAILED" {
		processing.Status = "INCONCLUSIVE"
		processing.Message = "A catalogação dos componentes está pendente ou inconclusiva."
	}
}

func resolvePromptSnapshot(ctx context.Context, tx *gorm.DB, classificationSnapshotID, legacySnapshotID identity.ID) (database.AnalysisPromptSnapshot, error) {
	for _, snapshotID := range []identity.ID{classificationSnapshotID, legacySnapshotID} {
		if snapshotID == (identity.ID{}) {
			continue
		}
		var snapshot database.AnalysisPromptSnapshot
		err := tx.WithContext(ctx).Where("id=?", snapshotID).Take(&snapshot).Error
		if err == nil {
			return snapshot, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return database.AnalysisPromptSnapshot{}, err
		}
	}
	return database.AnalysisPromptSnapshot{}, nil
}

// effectivePublicationPolicy returns the tenant policy in force when the
// report becomes final; a tenant without a stored policy is MANUAL, version 0.
func effectivePublicationPolicy(ctx context.Context, tx *gorm.DB, tenantID identity.ID) (publicationcore.Policy, error) {
	var row database.PublicationPolicy
	err := tx.WithContext(ctx).Where("tenant_id=?", tenantID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return publicationcore.EffectivePolicy(nil), nil
	}
	if err != nil {
		return publicationcore.Policy{}, err
	}
	return publicationcore.EffectivePolicy(&publicationcore.Policy{Mode: row.Mode, Version: row.Version}), nil
}

// criticalRecipient is a verified dashboard membership destination eligible
// for internal alerts about one inspection.
type criticalRecipient struct {
	database.RecipientChannel
}

// loadCriticalRecipients uses RecipientChannel, the current membership-owned
// preference source. notifications.recipients is a legacy table and is not
// used for alert delivery.
func loadCriticalRecipients(ctx context.Context, tx *gorm.DB, tenantID identity.ID, inspection database.Inspection) ([]criticalRecipient, error) {
	var recipients []criticalRecipient
	err := tx.WithContext(ctx).Table("notifications.recipient_channels AS rc").
		Select("rc.*").
		Joins("JOIN access.memberships AS m ON m.tenant_id=rc.tenant_id AND m.id=rc.recipient_membership_id").
		Where(`EXISTS (SELECT 1 FROM access.product_entitlements AS pe
			WHERE pe.tenant_id=m.tenant_id AND pe.membership_id=m.id AND pe.product='DASHBOARD')`).
		Where("rc.tenant_id=? AND rc.selected=true AND rc.verified_at IS NOT NULL", tenantID).
		Where("m.status='ACTIVE' AND m.role <> 'CUSTOMER_VIEWER'").
		Where(`NOT EXISTS (SELECT 1 FROM tenancy.business_units bu WHERE bu.tenant_id=? AND bu.id=? AND bu.status='ARCHIVED')`, tenantID, inspection.BusinessUnitID).
		Where(`NOT EXISTS (SELECT 1 FROM assets.assets a WHERE a.tenant_id=? AND a.id=? AND a.status='ARCHIVED')`, tenantID, inspection.AssetID).
		Where(`NOT EXISTS (SELECT 1 FROM inspections.inspections i WHERE i.tenant_id=? AND i.id=? AND i.status='ARCHIVED')`, tenantID, inspection.ID).
		Where(`NOT EXISTS (SELECT 1 FROM projects.projects p WHERE p.tenant_id=? AND p.id=? AND p.status='ARCHIVED')`, tenantID, inspection.ProjectID).
		Where(`(m.role='TENANT_ADMIN' OR EXISTS (
			SELECT 1 FROM access.resource_scopes AS s
			WHERE s.tenant_id=m.tenant_id AND s.membership_id=m.id AND (
				(s.kind='BUSINESS_UNIT' AND s.resource_id=?) OR
				(s.kind='ASSET' AND s.resource_id=?) OR
				(s.kind='INSPECTION' AND s.resource_id=?) OR
				(s.kind='PROJECT' AND s.resource_id=?)
			)
		))`, inspection.BusinessUnitID, inspection.AssetID, inspection.ID, inspection.ProjectID).
		Order("rc.channel ASC, rc.destination ASC, rc.recipient_membership_id ASC").
		Find(&recipients).Error
	return recipients, err
}

func mergeRequirementAnalysis(current, next requirementAnalysis) requirementAnalysis {
	if current.status == "" {
		return next
	}
	rank := func(status string) int {
		switch status {
		case "FAILED":
			return 3
		case "INCONCLUSIVE":
			return 2
		case "COMPLETED":
			return 1
		default:
			return 0
		}
	}
	if rank(next.status) > rank(current.status) {
		current.status = next.status
	}
	currentIsComparison := current.mode == "COMPARE_ORIGIN_CURRENT"
	nextIsComparison := next.mode == "COMPARE_ORIGIN_CURRENT"
	if nextIsComparison {
		if !currentIsComparison {
			current.noRelevantChange = next.noRelevantChange
		} else if current.noRelevantChange == nil || next.noRelevantChange == nil {
			current.noRelevantChange = nil
		} else {
			unchanged := *current.noRelevantChange && *next.noRelevantChange
			current.noRelevantChange = &unchanged
		}
		current.mode = "COMPARE_ORIGIN_CURRENT"
	} else if !currentIsComparison {
		current.noRelevantChange = nil
	}
	return current
}

func resolveClassifiedVideoPlan(tx *gorm.DB, tenantID, inspectionID identity.ID, videoPlanID *identity.ID) (database.VideoPlan, error) {
	if videoPlanID == nil || *videoPlanID == (identity.ID{}) {
		return database.VideoPlan{}, messaging.ErrPermanent
	}
	var plan database.VideoPlan
	err := tx.Where("tenant_id=? AND id=? AND inspection_id=? AND status='SEALED'", tenantID, *videoPlanID, inspectionID).Take(&plan).Error
	return plan, err
}

// reportSnapshotContext freezes the human-readable context and reference
// evidence needed to understand a report after operational records evolve.
func reportSnapshotContext(ctx context.Context, tx *gorm.DB, tenantID identity.ID, inspection database.Inspection, reference database.ReferenceSnapshot, generatedAt time.Time) (reportcore.Context, []reportcore.Requirement, []reportcore.Evidence, error) {
	var asset database.Asset
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, inspection.AssetID).First(&asset).Error; err != nil {
		return reportcore.Context{}, nil, nil, err
	}
	var participant database.Participant
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, inspection.ParticipantID).First(&participant).Error; err != nil {
		return reportcore.Context{}, nil, nil, err
	}
	var templateRow database.Template
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, inspection.TemplateID).First(&templateRow).Error; err != nil {
		return reportcore.Context{}, nil, nil, err
	}
	var version database.TemplateVersion
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, inspection.TemplateVersionID).First(&version).Error; err != nil {
		return reportcore.Context{}, nil, nil, err
	}

	context := reportcore.Context{Asset: reportcore.AssetContext{ID: asset.ID.String(), Name: asset.Name, ExternalKey: asset.ExternalKey, Address: asset.Address}, Participant: reportcore.ParticipantContext{ID: participant.ID.String(), Name: participant.Name}, Template: reportcore.TemplateContext{ID: templateRow.ID.String(), Name: templateRow.Name, Version: version.VersionNumber}, Inspection: reportcore.InspectionContext{DueAt: inspection.DueAt.UTC().Format(time.RFC3339Nano), GeneratedAt: generatedAt.UTC().Format(time.RFC3339Nano)}}
	if inspection.ProjectID != nil {
		context.Inspection.ProjectID = inspection.ProjectID.String()
	}
	if inspection.StageID != nil {
		context.Inspection.StageID = inspection.StageID.String()
		var stage database.ProjectStage
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, *inspection.StageID).First(&stage).Error; err == nil {
			context.Inspection.StageLabel = stage.Label
		} else if err != gorm.ErrRecordNotFound {
			return reportcore.Context{}, nil, nil, err
		}
	}
	var submission database.SubmissionVersion
	if err := tx.WithContext(ctx).Where("tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?)", tenantID, tenantID, inspection.ID).Order("submitted_at DESC").First(&submission).Error; err == nil {
		context.Inspection.SubmittedAt = submission.SubmittedAt.UTC().Format(time.RFC3339Nano)
	} else if err != gorm.ErrRecordNotFound {
		return reportcore.Context{}, nil, nil, err
	}

	requirements, err := reportSnapshotRequirements(ctx, tx, tenantID, inspection.ID, version.DefinitionJSON)
	if err != nil {
		return reportcore.Context{}, nil, nil, err
	}

	var payload struct {
		Items []struct {
			MediaID     identity.ID `json:"mediaId"`
			Category    string      `json:"category"`
			Description string      `json:"description"`
		} `json:"items"`
	}
	if err := json.Unmarshal(reference.Payload, &payload); err != nil {
		return reportcore.Context{}, nil, nil, err
	}
	evidence := make([]reportcore.Evidence, 0, len(payload.Items))
	for _, item := range payload.Items {
		if item.MediaID == (identity.ID{}) {
			continue
		}
		requirementKey := item.Category
		if reference.ComparisonMode == "FIXED_ORIGIN" {
			requirementKey = "origin:" + item.MediaID.String()
		}
		var media database.MediaObject
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, item.MediaID).First(&media).Error; err == gorm.ErrRecordNotFound {
			evidence = append(evidence, reportcore.Evidence{ID: item.MediaID.String(), RequirementKey: requirementKey, Role: "REFERENCE", Description: item.Description, Availability: "MISSING"})
			continue
		} else if err != nil {
			return reportcore.Context{}, nil, nil, err
		}
		value := reportcore.Evidence{ID: media.ID.String(), RequirementKey: requirementKey, Role: "REFERENCE", Description: item.Description, CaptureSource: media.CaptureSource, Availability: "MISSING"}
		if value.Description == "" {
			value.Description = media.Description
		}
		if media.CapturedAt != nil {
			value.CapturedAt = media.CapturedAt.UTC().Format(time.RFC3339Nano)
		}
		var derivative database.MediaDerivative
		if err := tx.WithContext(ctx).Where("tenant_id=? AND media_id=? AND kind=?", tenantID, media.ID, "DISPLAY").First(&derivative).Error; err == nil {
			value.DisplayDigest, value.Availability = derivative.SHA256, "AVAILABLE"
		} else if err != gorm.ErrRecordNotFound {
			return reportcore.Context{}, nil, nil, err
		}
		var flags []string
		_ = json.Unmarshal(media.Flags, &flags)
		value.Flags = reportFlags(requirementKey, flags)
		evidence = append(evidence, value)
	}
	return context, requirements, evidence, nil
}

// latestSubmittedAnswer loads the newest answer to one requirement among the
// inspection's SUBMITTED capture drafts. An OPEN (in progress or abandoned)
// recapture never contributes evidence or flags to an immutable report.
func latestSubmittedAnswer(ctx context.Context, tx *gorm.DB, tenantID, inspectionID identity.ID, requirementKey string, answer *database.RequirementAnswer) error {
	return capturecore.LatestSubmittedAnswer(ctx, tx, tenantID, inspectionID, requirementKey, answer)
}

func reportFlags(requirementKey string, flags []string) []string {
	if strings.HasPrefix(requirementKey, "env:") {
		return flags
	}
	filtered := make([]string, 0, len(flags))
	for _, flag := range flags {
		if strings.HasPrefix(flag, "IMPOSSIBILITY:") || strings.HasPrefix(flag, "QUALITY_WARNING") {
			continue
		}
		filtered = append(filtered, flag)
	}
	return filtered
}

func reportVideoPlanEvidence(ctx context.Context, tx *gorm.DB, tenantID, planItemID identity.ID) ([]reportcore.Evidence, error) {
	var item database.VideoPlanItem
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, planItemID).Take(&item).Error; err != nil {
		return nil, err
	}
	var memberships []database.VideoPlanItemFrame
	if err := tx.WithContext(ctx).Where("tenant_id=? AND plan_item_id=?", tenantID, item.ID).Order("core_group ASC,role ASC,pair_order ASC").Find(&memberships).Error; err != nil {
		return nil, err
	}
	evidence := make([]reportcore.Evidence, 0, len(memberships))
	for _, membership := range memberships {
		var frame database.VideoFrame
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, membership.FrameID).Take(&frame).Error; err != nil {
			return nil, err
		}
		var source database.VideoSource
		if err := tx.WithContext(ctx).Where("tenant_id=? AND media_id=?", tenantID, frame.SourceMediaID).Take(&source).Error; err != nil {
			return nil, err
		}
		evidence = append(evidence, reportVideoFrameEvidence(item, membership, frame, source))
	}
	return evidence, nil
}

// reportAllVideoPlanEvidence pins the frames of every plan item, including
// gaps without a comparison job. Carried items resolve their frames through
// jobs already validated against the plan lineage.
func reportAllVideoPlanEvidence(ctx context.Context, tx *gorm.DB, tenantID, planID identity.ID, jobs []database.ComparisonJob) ([]reportcore.Evidence, error) {
	var items []database.VideoPlanItem
	if err := tx.WithContext(ctx).Where("tenant_id=? AND plan_id=?", tenantID, planID).Order("requirement_key ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	carriedItems := make(map[identity.ID]*identity.ID, len(jobs))
	for _, job := range jobs {
		carriedItems[job.ID] = job.VideoPlanItemID
	}
	var evidence []reportcore.Evidence
	for _, item := range items {
		sourceItemID := item.ID
		if item.CarriedResultJobID != nil {
			carriedItemID := carriedItems[*item.CarriedResultJobID]
			if carriedItemID == nil {
				return nil, messaging.ErrPermanent
			}
			sourceItemID = *carriedItemID
		}
		frames, err := reportVideoPlanEvidence(ctx, tx, tenantID, sourceItemID)
		if err != nil {
			return nil, err
		}
		for i := range frames {
			frames[i].RequirementKey = item.RequirementKey
		}
		evidence = append(evidence, frames...)
	}
	return evidence, nil
}

func loadReportVideoPlanJobs(ctx context.Context, tx *gorm.DB, tenantID, inspectionID, planID identity.ID) ([]database.ComparisonJob, error) {
	var jobs []database.ComparisonJob
	if err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=? AND video_plan_id=?", tenantID, inspectionID, planID).Order("requirement_key ASC,video_core_group ASC").Find(&jobs).Error; err != nil {
		return nil, err
	}
	var items []database.VideoPlanItem
	if err := tx.WithContext(ctx).Where("tenant_id=? AND plan_id=? AND carried_result_job_id IS NOT NULL", tenantID, planID).Find(&items).Error; err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return jobs, nil
	}
	var plan database.VideoPlan
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, planID).Take(&plan).Error; err != nil {
		return nil, err
	}
	if plan.ParentPlanID == nil {
		return nil, messaging.ErrPermanent
	}
	ancestorIDs := make([]identity.ID, 0, 4)
	seen := map[identity.ID]struct{}{plan.ID: {}}
	current := plan
	for depth := 0; current.ParentPlanID != nil; depth++ {
		if depth >= 32 {
			return nil, messaging.ErrPermanent
		}
		parentID := *current.ParentPlanID
		if _, exists := seen[parentID]; exists {
			return nil, messaging.ErrPermanent
		}
		seen[parentID] = struct{}{}
		ancestorIDs = append(ancestorIDs, parentID)
		var ancestor database.VideoPlan
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND inspection_id=?", tenantID, parentID, inspectionID).Take(&ancestor).Error; err != nil {
			return nil, err
		}
		current = ancestor
	}
	for _, item := range items {
		var carried database.ComparisonJob
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND inspection_id=? AND video_plan_id IN ? AND status IN ('COMPLETED','INCONCLUSIVE','FAILED')", tenantID, *item.CarriedResultJobID, inspectionID, ancestorIDs).Take(&carried).Error; err != nil {
			return nil, err
		}
		jobs = append(jobs, carried)
	}
	return jobs, nil
}

func loadClassifiedJobs(ctx context.Context, tx *gorm.DB, tenantID, inspectionID identity.ID, videoPlanID *identity.ID) ([]database.ComparisonJob, error) {
	if videoPlanID != nil {
		return loadReportVideoPlanJobs(ctx, tx, tenantID, inspectionID, *videoPlanID)
	}
	var jobs []database.ComparisonJob
	err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).
		Where("id IN (SELECT DISTINCT ON (requirement_key) id FROM analysis.comparison_jobs WHERE tenant_id=? AND inspection_id=? ORDER BY requirement_key, created_at DESC, id DESC)", tenantID, inspectionID).
		Order("requirement_key ASC,video_core_group ASC").Find(&jobs).Error
	return jobs, err
}

func reportVideoFrameEvidence(item database.VideoPlanItem, membership database.VideoPlanItemFrame, frame database.VideoFrame, source database.VideoSource) reportcore.Evidence {
	role := "CURRENT"
	if membership.Role == "ORIGIN" {
		role = "REFERENCE"
	}
	ptsMicros := frame.PTSMicros
	timestampMS := ptsMicros / 1000
	return reportcore.Evidence{
		ID: frame.ID.String(), RequirementKey: item.RequirementKey, Role: role,
		EvidenceKind: "VIDEO_FRAME", FrameID: frame.ID.String(), SourceMediaID: frame.SourceMediaID.String(),
		PreparationID: frame.PreparationID.String(), PTSMicros: &ptsMicros, TimestampMS: &timestampMS, FrameDigest: frame.SHA256,
		CaptureSource: "VIDEO", DisplayDigest: frame.SHA256, Availability: "AVAILABLE", SourceAvailability: source.Status,
		Description: fmt.Sprintf("Quadro do vídeo em %d ms", timestampMS),
	}
}

func reportSnapshotRequirements(ctx context.Context, tx *gorm.DB, tenantID, inspectionID identity.ID, templateDefinition []byte) ([]reportcore.Requirement, error) {
	var draft database.CaptureDraft
	// A SUBMITTED draft wins over an OPEN recapture, whose focused requirement
	// list must not replace the report's requirements before it is submitted.
	err := tx.WithContext(ctx).
		Where("tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?)", tenantID, tenantID, inspectionID).
		Order("(status='SUBMITTED') DESC, updated_at DESC").First(&draft).Error
	if err == nil {
		return reportRequirementsFromCapture(draft.Requirements)
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	var document catalog.TemplateDocument
	if err := json.Unmarshal(templateDefinition, &document); err != nil {
		return nil, err
	}
	requirements := make([]reportcore.Requirement, 0, len(document.Requirements))
	for _, value := range document.Requirements {
		requirements = append(requirements, reportcore.Requirement{Key: value.Key, Section: value.Section, Label: value.Label, Instructions: value.Instructions})
	}
	return requirements, nil
}

func reportRequirementsFromCapture(raw []byte) ([]reportcore.Requirement, error) {
	var captured []capturecore.Requirement
	if err := json.Unmarshal(raw, &captured); err != nil {
		return nil, err
	}
	captured = capturecore.ActiveRequirements(captured)
	requirements := make([]reportcore.Requirement, 0, len(captured))
	for _, value := range captured {
		requirements = append(requirements, reportcore.Requirement{Key: value.Key, Section: value.Section, Label: value.Label, Instructions: value.Instructions})
	}
	return requirements, nil
}
