// Package request_inventory_jobs creates idempotent per-environment inventory jobs.
package request_inventory_jobs

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	catalognotes "inspection/services/inspection/internal/features/analysis/catalog_notes"
	inventoryexecutor "inspection/services/inspection/internal/features/analysis/inventory/core"
	"inspection/services/inspection/internal/features/analysis/prompt"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Dependencies are the collaborators of the job request handler.
type Dependencies struct {
	Now func() time.Time
}

// Setup returns an idempotent handler for capture submissions, video readiness
// and terminal preparation failures. It never creates jobs for v1 inspections
// or for inspections pinned to a legacy (media-only) reference.
func Setup(deps Dependencies) (func(context.Context, *gorm.DB, events.RawEnvelope) error, error) {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var inspectionID identity.ID
		switch envelope.Type {
		case "capture.submitted.v1":
			var p struct {
				InspectionID identity.ID `json:"inspectionId"`
			}
			if json.Unmarshal(envelope.Payload, &p) != nil || p.InspectionID == (identity.ID{}) {
				return messaging.ErrPermanent
			}
			inspectionID = p.InspectionID
		case "capture.video_submitted.v2":
			var p events.CaptureVideoSubmittedV2Payload
			if json.Unmarshal(envelope.Payload, &p) != nil || p.InspectionID == (identity.ID{}) {
				return messaging.ErrPermanent
			}
			inspectionID = p.InspectionID
		case "inventory.job_requested.v1":
			var p events.InventoryJobRequestedPayload
			if json.Unmarshal(envelope.Payload, &p) != nil || p.InspectionID == (identity.ID{}) {
				return messaging.ErrPermanent
			}
			inspectionID = p.InspectionID
		case "transcription.completed.v1", "transcription.failed.v1":
			// A note reached a terminal result: the room manifest that was
			// waiting for it is re-evaluated.
			var p struct {
				NoteID     identity.ID `json:"noteId"`
				Generation int         `json:"generation"`
			}
			if json.Unmarshal(envelope.Payload, &p) != nil || p.NoteID == (identity.ID{}) || p.Generation < 1 {
				return messaging.ErrPermanent
			}
			var note struct{ InspectionID identity.ID }
			if err := tx.WithContext(ctx).Raw("SELECT inspection_id FROM transcription.audio_notes WHERE tenant_id=? AND id=?", envelope.TenantID, p.NoteID).Scan(&note).Error; err != nil {
				return err
			}
			if note.InspectionID == (identity.ID{}) {
				// Unknown note (or another tenant's): nothing to re-evaluate.
				return nil
			}
			inspectionID = note.InspectionID
		case "media.video_prepared.v1":
			var p events.VideoPreparedPayload
			if json.Unmarshal(envelope.Payload, &p) != nil || p.MediaID == (identity.ID{}) {
				return messaging.ErrPermanent
			}
			var media database.MediaObject
			if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, p.MediaID).Take(&media).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				return err
			}
			var responsibility database.Responsibility
			if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, media.ResponsibilityID).Take(&responsibility).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					// Origin and onboarding clips have no inspection responsibility.
					return nil
				}
				return err
			}
			inspectionID = responsibility.InspectionID
		default:
			return messaging.ErrPermanent
		}
		err := Request(ctx, tx, envelope.TenantID, inspectionID, deps.Now().UTC())
		if apperror.Is(err, apperror.Conflict) {
			// Another flow owns the asset's open inventory draft. The legacy
			// comparison handlers share this delivery, so this must not fail it;
			// the skip audit is best-effort when that draft already closed.
			recordSkip := func() error {
				var inspection database.Inspection
				if lookupErr := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, inspectionID).Take(&inspection).Error; lookupErr != nil {
					return lookupErr
				}
				var origin database.Origin
				if lookupErr := tx.WithContext(ctx).Where("tenant_id=? AND asset_id=?", envelope.TenantID, inspection.AssetID).Take(&origin).Error; lookupErr != nil {
					return lookupErr
				}
				var draft database.OriginVersion
				if lookupErr := tx.WithContext(ctx).Where("tenant_id=? AND origin_id=? AND status='DRAFT' AND has_inventory", envelope.TenantID, origin.ID).Take(&draft).Error; lookupErr != nil {
					return lookupErr
				}
				var existing int64
				if lookupErr := tx.WithContext(ctx).Model(&database.AuditEvent{}).Where("tenant_id=? AND action='inventory.catalog_skipped' AND target_type='INSPECTION' AND target_id=?", envelope.TenantID, inspectionID.String()).Count(&existing).Error; lookupErr != nil {
					return lookupErr
				}
				if existing == 0 {
					if lookupErr := tx.WithContext(ctx).Create(&database.AuditEvent{ID: identity.NewID(), TenantID: envelope.TenantID, ActorID: envelope.TenantID, Action: "inventory.catalog_skipped", TargetType: "INSPECTION", TargetID: inspectionID.String(), Outcome: "BLOCKED", Reason: draft.ID.String(), CorrelationID: "inventory-catalog-skipped:" + inspectionID.String(), OccurredAt: deps.Now().UTC()}).Error; lookupErr != nil {
						return lookupErr
					}
				}
				log.Printf("inventory jobs skipped for inspection %s: conflicting draft %s", inspectionID, draft.ID)
				return nil
			}
			if skipErr := recordSkip(); skipErr != nil && !errors.Is(skipErr, gorm.ErrRecordNotFound) {
				return skipErr
			}
			return nil
		}
		return err
	}, nil
}

// Request creates the inventory draft (cataloging) and the per-environment
// jobs for one submitted inspection. Repeated calls are no-ops.
func Request(ctx context.Context, tx *gorm.DB, tenantID, inspectionID identity.ID, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx = tx.WithContext(ctx)
	var inspection database.Inspection
	if err := tx.Where("tenant_id=? AND id=?", tenantID, inspectionID).Take(&inspection).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return messaging.ErrPermanent
		}
		return err
	}
	var template database.TemplateVersion
	if err := tx.Where("tenant_id=? AND id=?", tenantID, inspection.TemplateVersionID).Take(&template).Error; err != nil {
		return err
	}
	if template.SchemaVersion < 2 || inspection.InventoryPromptSnapshotID == nil {
		return nil
	}
	var capture database.CaptureDraft
	if err := tx.Where("tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?)", tenantID, tenantID, inspectionID).Order("created_at DESC").Take(&capture).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if capture.Status != "SUBMITTED" {
		return nil
	}
	var requirements []capturecore.Requirement
	if err := json.Unmarshal(capture.Requirements, &requirements); err != nil {
		return messaging.ErrPermanent
	}
	requirements = capturecore.ActiveRequirements(requirements)
	var answers []database.RequirementAnswer
	if err := tx.Where("tenant_id=? AND draft_id=?", tenantID, capture.ID).Find(&answers).Error; err != nil {
		return err
	}
	captured := make(map[string]database.RequirementAnswer, len(answers))
	for _, a := range answers {
		captured[a.RequirementKey] = a
	}
	var reference database.ReferenceSnapshot
	err := tx.Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Take(&reference).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var pinned *database.OriginVersion
	if err == nil && reference.ReferenceVersionID != nil {
		var version database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", tenantID, *reference.ReferenceVersionID).Take(&version).Error; err != nil {
			return err
		}
		if !version.HasInventory {
			// Legacy media-only reference: the existing comparison flow only.
			return nil
		}
		pinned = &version
	}
	if inspection.MediaKind == "VIDEO" {
		ready, err := preparationsTerminal(tx, tenantID, requirements, captured)
		if err != nil || !ready {
			return err
		}
	}
	if pinned != nil {
		return requestCompare(tx, tenantID, inspection, *pinned, requirements, captured, now)
	}
	return requestCatalog(ctx, tx, tenantID, inspection, capture.ResponsibilityID, requirements, captured, now)
}

// preparationsTerminal reports whether the latest preparation of every clip of
// every captured environment is terminal. A FAILED clip contributes no view but
// must not hold the other environments back.
func preparationsTerminal(tx *gorm.DB, tenantID identity.ID, requirements []capturecore.Requirement, captured map[string]database.RequirementAnswer) (bool, error) {
	for _, requirement := range requirements {
		mediaIDs, ok, err := CapturedMedia(requirement, captured)
		if err != nil {
			return false, err
		}
		if !ok {
			continue
		}
		var preparations []database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND media_id IN ?", tenantID, mediaIDs).Order("generation DESC").Find(&preparations).Error; err != nil {
			return false, err
		}
		latest := make(map[identity.ID]string, len(mediaIDs))
		for _, preparation := range preparations {
			if _, seen := latest[preparation.MediaID]; !seen {
				latest[preparation.MediaID] = preparation.Status
			}
		}
		for _, id := range mediaIDs {
			if status := latest[id]; status != "READY" && status != "FAILED" {
				return false, nil
			}
		}
	}
	return true, nil
}

// CapturedMedia returns the media of an answered, possible environment.
func CapturedMedia(requirement capturecore.Requirement, captured map[string]database.RequirementAnswer) ([]identity.ID, bool, error) {
	if !strings.HasPrefix(requirement.Key, "env:") {
		return nil, false, nil
	}
	answer, exists := captured[requirement.Key]
	if !exists || answer.ImpossibilityReason != "" {
		return nil, false, nil
	}
	var mediaIDs []identity.ID
	if err := json.Unmarshal(answer.MediaIDs, &mediaIDs); err != nil {
		return nil, false, messaging.ErrPermanent
	}
	return mediaIDs, len(mediaIDs) > 0, nil
}

// requestCompare schedules one COMPARE job per environment of the pinned
// version, captured or not, and one per environment added during capture. The
// pinned version is never modified.
func requestCompare(tx *gorm.DB, tenantID identity.ID, inspection database.Inspection, version database.OriginVersion, requirements []capturecore.Requirement, captured map[string]database.RequirementAnswer, now time.Time) error {
	var environments []database.InventoryEnvironment
	if err := tx.Where("tenant_id=? AND origin_version_id=? AND decision<>'REJECTED'", tenantID, version.ID).Order("position ASC").Find(&environments).Error; err != nil {
		return err
	}
	for _, env := range environments {
		identityID := env.IdentityID
		// Comparative captures key each environment by its stable identity. A
		// descriptive (v3) capture keys a baseline room by its scope room key
		// and records the baseline identity it was matched to: the job then
		// reads that requirement's answer.
		if err := createJob(tx, tenantID, inspection, version.ID, "COMPARE", baselineRequirementKey(env, requirements), &identityID, now); err != nil {
			return err
		}
	}
	for _, requirement := range requirements {
		if !requirement.AddedDuringCapture {
			continue
		}
		if _, ok, err := CapturedMedia(requirement, captured); err != nil || !ok {
			if err != nil {
				return err
			}
			continue
		}
		identityID := addedEnvironmentIdentity(inspection.ID, requirement)
		if err := createJob(tx, tenantID, inspection, version.ID, "COMPARE", requirement.Key, &identityID, now); err != nil {
			return err
		}
	}
	return nil
}

// baselineRequirementKey is the capture requirement key of a baseline
// environment: the expected requirement matched to its identity, else the
// historical "env:<identity>" key.
func baselineRequirementKey(env database.InventoryEnvironment, requirements []capturecore.Requirement) string {
	identityKey := "env:" + env.IdentityID.String()
	for _, requirement := range requirements {
		if requirement.Key == identityKey {
			return identityKey
		}
	}
	for _, requirement := range requirements {
		if !requirement.AddedDuringCapture && strings.HasPrefix(requirement.Key, "env:") && requirement.EnvironmentIdentityID == env.IdentityID.String() {
			return requirement.Key
		}
	}
	return identityKey
}

// requestCatalog opens (or reuses) the CAPTURE draft of this inspection and one
// CATALOG job per captured environment. Impossible environments get no job.
func requestCatalog(ctx context.Context, tx *gorm.DB, tenantID identity.ID, inspection database.Inspection, responsibilityID identity.ID, requirements []capturecore.Requirement, captured map[string]database.RequirementAnswer, now time.Time) error {
	version, err := captureDraft(tx, tenantID, inspection, now)
	if err != nil {
		return err
	}
	// The room's visual evidence is ready here. Rooms with submitted voice
	// notes get a manifest: their job becomes due once every note is resolved
	// or, at the latest, when the two-minute wait ends.
	schedule := func(requirement capturecore.Requirement, mediaIDs []identity.ID) (time.Time, error) {
		outcome, err := catalognotes.Evaluate(ctx, tx, catalognotes.Room{TenantID: tenantID, InspectionID: inspection.ID, ResponsibilityID: responsibilityID, RequirementKey: requirement.Key, MediaIDs: mediaIDs, Generation: 1}, now, false)
		if err != nil {
			return time.Time{}, err
		}
		if outcome.Ready || outcome.Manifest == nil {
			return now, nil
		}
		return outcome.Manifest.DeadlineAt, nil
	}
	count, err := createCatalogJobs(ctx, tx, tenantID, inspection, version.ID, requirements, captured, 1, inspection.InventoryPromptSnapshotID, now, schedule)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	updated := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=? AND status='DRAFT' AND draft_state='PREPARING'", tenantID, version.ID).Update("draft_state", "READY")
	if updated.Error != nil || updated.RowsAffected != 1 {
		return updated.Error
	}
	return inventorycore.EmitDraftReady(tx, tenantID, version.ID, inspection.ID, now)
}

// CreateCatalogJobs creates capture environments and CATALOG jobs for a chosen draft.
func CreateCatalogJobs(ctx context.Context, tx *gorm.DB, tenantID identity.ID, inspection database.Inspection, draftID identity.ID, requirements []capturecore.Requirement, captured map[string]database.RequirementAnswer, generation int, promptSnapshotID *identity.ID, now time.Time) (int, error) {
	return createCatalogJobs(ctx, tx, tenantID, inspection, draftID, requirements, captured, generation, promptSnapshotID, now, nil)
}

// catalogSchedule returns when the CATALOG job of one captured environment is due.
type catalogSchedule func(requirement capturecore.Requirement, mediaIDs []identity.ID) (time.Time, error)

func createCatalogJobs(ctx context.Context, tx *gorm.DB, tenantID identity.ID, inspection database.Inspection, draftID identity.ID, requirements []capturecore.Requirement, captured map[string]database.RequirementAnswer, generation int, promptSnapshotID *identity.ID, now time.Time, schedule catalogSchedule) (int, error) {
	if generation < 1 || draftID == (identity.ID{}) {
		return 0, apperror.New(apperror.InvalidInput, "draftId", "rascunho e geração são obrigatórios")
	}
	tx = tx.WithContext(ctx)
	schemaVersion, schemaDigest := 1, ""
	if promptSnapshotID != nil {
		var snapshot database.AnalysisPromptSnapshot
		if err := tx.Where("id=?", *promptSnapshotID).Take(&snapshot).Error; err != nil {
			return 0, err
		}
		schemaVersion, schemaDigest = snapshot.SchemaVersion, snapshot.CanonicalDigest
		if schemaVersion == 0 {
			schemaVersion = 1
		}
	}
	jobs := 0
	for position, requirement := range requirements {
		mediaIDs, ok, err := CapturedMedia(requirement, captured)
		if err != nil || !ok {
			if err != nil {
				return 0, err
			}
			continue
		}
		dueAt := now
		if schedule != nil {
			if dueAt, err = schedule(requirement, mediaIDs); err != nil {
				return 0, err
			}
		}
		name := requirement.EnvironmentName
		if name == "" {
			name = requirement.Label
		}
		typeCode := requirement.EnvironmentTypeCode
		if typeCode == "" {
			typeCode = "OTHER"
		}
		key := requirement.Key
		env := database.InventoryEnvironment{ID: identity.NewID(), TenantID: tenantID, OriginVersionID: draftID, IdentityID: addedEnvironmentIdentity(inspection.ID, requirement), TypeCode: typeCode, Name: name, Expected: requirement.Expected, AddedDuringCapture: requirement.AddedDuringCapture, RequirementKey: &key, Decision: "PENDING", Position: position, Version: 1, CreatedAt: now, UpdatedAt: now}
		var existing database.InventoryEnvironment
		if err := tx.Where("tenant_id=? AND origin_version_id=? AND requirement_key=?", tenantID, draftID, key).Take(&existing).Error; err == nil {
			env = existing
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, err
		} else if err := tx.Create(&env).Error; err != nil {
			return 0, err
		}
		identityID := env.IdentityID
		job := database.InventoryJob{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspection.ID, OriginVersionID: draftID, Kind: "CATALOG", RequirementKey: key, Generation: generation, Status: "PENDING", NextAttemptAt: dueAt, EnvironmentIdentityID: &identityID, PromptSnapshotID: promptSnapshotID, SchemaVersion: schemaVersion, SchemaDigest: &schemaDigest, CreatedAt: now, UpdatedAt: now}
		created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&job)
		if created.Error != nil {
			return 0, created.Error
		}
		if created.RowsAffected == 0 && !dueAt.After(now) {
			// The job already waits for voice notes that are now resolved.
			if err := tx.Model(&database.InventoryJob{}).Where("tenant_id=? AND inspection_id=? AND kind='CATALOG' AND requirement_key=? AND generation=? AND status='PENDING' AND attempts=0 AND next_attempt_at>?", tenantID, inspection.ID, key, generation, dueAt).Updates(map[string]any{"next_attempt_at": dueAt, "updated_at": now}).Error; err != nil {
				return 0, err
			}
		}
		jobs++
	}
	return jobs, nil
}

// captureDraft returns the inspection's own draft, creating it on first use.
// A draft of another flow is reported as a Conflict.
func captureDraft(tx *gorm.DB, tenantID identity.ID, inspection database.Inspection, now time.Time) (database.OriginVersion, error) {
	var version database.OriginVersion
	err := tx.Where("tenant_id=? AND source_inspection_id=? AND source='CAPTURE' AND has_inventory", tenantID, inspection.ID).Take(&version).Error
	if err == nil {
		return version, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return version, err
	}
	service := inventorycore.Service{Now: func() time.Time { return now }}
	return service.CreateDraft(tx, inventorycore.DraftInput{TenantID: tenantID, AssetID: inspection.AssetID, Source: "CAPTURE", SourceInspectionID: inspection.ID})
}

func createJob(tx *gorm.DB, tenantID identity.ID, inspection database.Inspection, versionID identity.ID, kind, key string, environment *identity.ID, now time.Time) error {
	schemaVersion, schemaDigest := 1, ""
	if inspection.InventoryPromptSnapshotID != nil {
		var snapshot database.AnalysisPromptSnapshot
		if err := tx.Where("id=?", *inspection.InventoryPromptSnapshotID).Take(&snapshot).Error; err != nil {
			return err
		}
		schemaVersion, schemaDigest = snapshot.SchemaVersion, snapshot.CanonicalDigest
		if schemaVersion == 0 {
			schemaVersion = 1
		}
	}
	job := database.InventoryJob{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspection.ID, OriginVersionID: versionID, Kind: kind, RequirementKey: key, EnvironmentIdentityID: environment, Generation: 1, Status: "PENDING", NextAttemptAt: now, PromptSnapshotID: inspection.InventoryPromptSnapshotID, SchemaVersion: schemaVersion, SchemaDigest: &schemaDigest, CreatedAt: now, UpdatedAt: now}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&job).Error
}

// addedEnvironmentIdentity is the environment identity of a capture
// requirement: the one recorded by the capture, else a deterministic id so a
// redelivery resolves to the same identity.
func addedEnvironmentIdentity(inspectionID identity.ID, requirement capturecore.Requirement) identity.ID {
	if id, err := identity.ParseID(requirement.EnvironmentIdentityID); err == nil {
		return id
	}
	return identity.NewDeterministicID("inventory-environment", inspectionID.String()+":"+requirement.Key)
}

// Reanalyze appends the next generation of COMPARE jobs of an inspection, so a
// re-analysis recomputes pairs and item results without touching the previous
// generation. It returns the number of jobs scheduled; inspections without
// inventory comparison schedule nothing.
func Reanalyze(ctx context.Context, tx *gorm.DB, tenantID, inspectionID identity.ID, now time.Time) (int, error) {
	tx = tx.WithContext(ctx)
	var jobs []database.InventoryJob
	if err := tx.Where("tenant_id=? AND inspection_id=? AND kind='COMPARE'", tenantID, inspectionID).Find(&jobs).Error; err != nil {
		return 0, err
	}
	latest := inventoryexecutor.LatestGeneration(jobs)
	for _, job := range latest {
		if job.Status != "SUCCEEDED" && job.Status != "FAILED" && job.Status != "CANCELED" {
			return 0, apperror.New(apperror.InvalidState, "inspectionId", "A análise do inventário já está em andamento.")
		}
	}
	for _, job := range latest {
		if _, err := nextGeneration(tx, job, now); err != nil {
			return 0, err
		}
	}
	return len(latest), nil
}

// ReanalyzeRequirement appends the next generation of the COMPARE job of one
// room (requirementKey) only, leaving every other room on its current
// generation. It is used when a correction concerns a single room, such as a
// staff pairing revision; Reanalyze keeps re-analysing the whole inspection.
func ReanalyzeRequirement(ctx context.Context, tx *gorm.DB, tenantID, inspectionID identity.ID, requirementKey string, now time.Time) (database.InventoryJob, error) {
	tx = tx.WithContext(ctx)
	var jobs []database.InventoryJob
	if err := tx.Where("tenant_id=? AND inspection_id=? AND kind='COMPARE' AND requirement_key=?", tenantID, inspectionID, requirementKey).Find(&jobs).Error; err != nil {
		return database.InventoryJob{}, err
	}
	latest := inventoryexecutor.LatestGeneration(jobs)
	if len(latest) != 1 {
		return database.InventoryJob{}, apperror.New(apperror.InvalidState, "itemIdentityId", "O ambiente deste item não tem uma comparação que possa ser refeita.")
	}
	if job := latest[0]; job.Status != "SUCCEEDED" && job.Status != "FAILED" && job.Status != "CANCELED" {
		return database.InventoryJob{}, apperror.New(apperror.InvalidState, "inspectionId", "A análise do inventário já está em andamento.")
	}
	return nextGeneration(tx, latest[0], now)
}

// nextGeneration creates the PENDING next generation of one COMPARE job with
// the job's pinned prompt and schema.
func nextGeneration(tx *gorm.DB, job database.InventoryJob, now time.Time) (database.InventoryJob, error) {
	next := database.InventoryJob{ID: identity.NewID(), TenantID: job.TenantID, InspectionID: job.InspectionID, OriginVersionID: job.OriginVersionID, Kind: job.Kind, RequirementKey: job.RequirementKey, EnvironmentIdentityID: job.EnvironmentIdentityID, Generation: job.Generation + 1, Status: "PENDING", NextAttemptAt: now, PromptSnapshotID: job.PromptSnapshotID, SchemaVersion: job.SchemaVersion, SchemaDigest: job.SchemaDigest, CreatedAt: now, UpdatedAt: now}
	if job.PromptSnapshotID != nil {
		var snapshot database.AnalysisPromptSnapshot
		if err := tx.Where("id=?", *job.PromptSnapshotID).Take(&snapshot).Error; err != nil {
			return database.InventoryJob{}, err
		}
		next.SchemaVersion, next.SchemaDigest = prompt.SchemaPin(snapshot.SchemaVersion, snapshot.CanonicalDigest)
	}
	if err := tx.Create(&next).Error; err != nil {
		return database.InventoryJob{}, err
	}
	return next, nil
}
