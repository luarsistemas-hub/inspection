// Package generate_snapshot creates immutable internal advisory report records.
package generate_snapshot

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	report "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/publication"
	publicationcore "inspection/services/inspection/internal/features/reports/publication/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Input struct {
	TenantID, InspectionID                              identity.ID
	VideoPlanID                                         *identity.ID
	ProjectID                                           *identity.ID
	Mode, Classification                                string
	TemplateVersionID, ReferenceVersionID, PromptDigest string
	ReasonCodes                                         []string
	Timeline                                            []report.TimelineEntry
	Coverage                                            map[string]string
	Context                                             report.Context
	Requirements                                        []report.Requirement
	Findings                                            []report.Finding
	Evidence                                            []report.Evidence
	Inventory                                           *report.InventorySection
	Checklist                                           *report.ChecklistSection
	Processing                                          *report.ProcessingState
	Scope                                               *report.ScopeSnapshot
	Handover                                            *report.HandoverSection
	Provenance                                          *report.Provenance
	Comparison                                          *report.ComparisonSection
	VerificationCode                                    string
	PublicationPolicyVersion                            int64
	PublicationMode                                     string
	ActorID                                             identity.ID
}
type Result struct {
	SnapshotID             identity.ID
	Version                int
	JSONDigest, HTMLDigest string
	PublicationID          identity.ID
}

type Dependencies struct {
	DB  *gorm.DB
	Now func() time.Time
}

func Setup(deps Dependencies) (func(context.Context, Input) (Result, error), error) {
	if deps.DB == nil {
		return nil, fmt.Errorf("slice reports/generate_snapshot: missing database")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return func(ctx context.Context, input Input) (Result, error) { return Create(ctx, deps.DB, input, deps.Now()) }, nil
}

// ErrFrameIntegrity reports video frame evidence that is not a member of the
// sealed plan, or whose provenance differs from the stored frame. The snapshot
// fails instead of substituting another image.
var ErrFrameIntegrity = errors.New("FRAME_INTEGRITY_FAILED")

const verificationPlaceholder = "verification-pending-code-placeholder-32chars"

// Create increments versions only by append. A repeated terminal event with
// identical report input returns the already-stored immutable snapshot.
//
// Reading the latest version and inserting the next one happen in one
// transaction guarded by a per-inspection advisory lock, so two concurrent
// generation attempts (a duplicated or replayed terminal event) retain exactly
// one logical report version instead of two rows with the same version number.
func Create(ctx context.Context, db *gorm.DB, input Input, now time.Time) (Result, error) {
	if db == nil || input.TenantID == (identity.ID{}) || input.InspectionID == (identity.ID{}) {
		return Result{}, fmt.Errorf("report snapshot: missing identity")
	}
	var result Result
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := setSnapshotTenant(tx, input.TenantID); err != nil {
			return err
		}
		if err := lockInspectionSnapshots(tx, input.TenantID, input.InspectionID); err != nil {
			return err
		}
		var err error
		result, err = createLocked(ctx, tx, input, now)
		return err
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

// CreateInTx is Create inside the caller's transaction. Report finalization
// runs in the consumer's transaction, which already holds a lock on the
// inspection; opening a second transaction here would make the snapshot's
// publication wait for a lock held by the very caller that waits for it (a
// cycle PostgreSQL cannot detect). The caller owns commit and rollback.
func CreateInTx(ctx context.Context, tx *gorm.DB, input Input, now time.Time) (Result, error) {
	if tx == nil || input.TenantID == (identity.ID{}) || input.InspectionID == (identity.ID{}) {
		return Result{}, fmt.Errorf("report snapshot: missing identity")
	}
	tx = tx.WithContext(ctx)
	if err := setSnapshotTenant(tx, input.TenantID); err != nil {
		return Result{}, err
	}
	if err := lockInspectionSnapshots(tx, input.TenantID, input.InspectionID); err != nil {
		return Result{}, err
	}
	return createLocked(ctx, tx, input, now)
}

// lockInspectionSnapshots serializes snapshot versioning per inspection until
// the surrounding transaction ends. SQLite (unit tests) has a single writer.
func lockInspectionSnapshots(tx *gorm.DB, tenantID, inspectionID identity.ID) error {
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	return tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "report-snapshot:"+tenantID.String()+":"+inspectionID.String()).Error
}

// snapshotValidationError marks a v3 validation failure as permanent. v1/v2
// keep returning the validation error unchanged, as they always did.
func snapshotValidationError(schemaVersion int, err error) error {
	if schemaVersion >= 3 {
		return fmt.Errorf("%w: %v", report.ErrInvalidSnapshot, err)
	}
	return err
}

// createLocked runs inside the transaction opened by Create, after the tenant is
// set and the inspection's snapshot lock is held.
func createLocked(ctx context.Context, tx *gorm.DB, input Input, now time.Time) (Result, error) {
	var latest database.ReportSnapshot
	err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=?", input.TenantID, input.InspectionID).Order("version_number DESC").First(&latest).Error
	version := 1
	if err == nil {
		version = latest.VersionNumber + 1
	} else if err != gorm.ErrRecordNotFound {
		return Result{}, err
	}
	schemaVersion := 1
	if input.Inventory != nil {
		schemaVersion = 2
	}
	if input.Checklist != nil {
		schemaVersion = 3
	}
	// Only v3 reads the previous canonical document: its report number is
	// stable across versions and its dedupe ignores the per-version
	// verification code. v1/v2 keep the historical rules byte for byte.
	var latestSnapshot report.Snapshot
	if schemaVersion >= 3 && latest.ID != (identity.ID{}) {
		if err := json.Unmarshal(latest.CanonicalJSON, &latestSnapshot); err != nil {
			return Result{}, err
		}
	}
	id := identity.NewID()
	reportID := id.String()
	if latest.ID != (identity.ID{}) {
		// Historical v1/v2 rule: version N carries the snapshot ID of N-1.
		reportID = latest.ID.String()
		if schemaVersion >= 3 && latestSnapshot.SchemaVersion >= 3 && latestSnapshot.ReportID != "" {
			reportID = latestSnapshot.ReportID
		}
	}
	findings := input.Findings
	if hasVideoEvidence(input.Evidence) {
		findings = consolidateFindings(findings, componentLabels(input.Evidence, input.Requirements))
	}
	snapshot := report.Snapshot{SchemaVersion: schemaVersion, ReportID: reportID, InspectionID: input.InspectionID.String(), Mode: input.Mode, Classification: input.Classification, TemplateVersionID: input.TemplateVersionID, ReferenceVersionID: input.ReferenceVersionID, PromptDigest: input.PromptDigest, ReasonCodes: input.ReasonCodes, Timeline: input.Timeline, Coverage: input.Coverage, Context: input.Context, Requirements: input.Requirements, Findings: findings, Evidence: input.Evidence, Inventory: input.Inventory, Checklist: input.Checklist, Processing: input.Processing, Scope: input.Scope, Handover: input.Handover, Provenance: input.Provenance, Comparison: input.Comparison, VerificationCode: input.VerificationCode, Advisory: "Esta triagem interna descreve alterações observadas e não atribui culpa, custo, responsabilidade ou consequência automática."}
	if schemaVersion >= 3 && snapshot.VerificationCode == "" {
		snapshot.VerificationCode = verificationPlaceholder
	}
	if input.VideoPlanID != nil {
		snapshot.VideoPlanID = input.VideoPlanID.String()
		snapshot.ScopeStatements = VideoScopeStatements(hasReference(input.ReferenceVersionID), input.Requirements)
	}
	if input.ProjectID != nil {
		snapshot.ProjectID = input.ProjectID.String()
	}
	if schemaVersion >= 3 {
		snapshot = report.SanitizeURLs(snapshot)
	}
	// Compare only report facts for deduplication. A fresh verification code is
	// generated after this check so replayed events retain the original code.
	comparable := snapshot
	if schemaVersion >= 3 {
		comparable.VerificationCode = ""
	}
	if schemaVersion >= 3 {
		comparable.Context.Inspection.GeneratedAt = "report-generation"
	}
	canonical, jsonDigest, err := report.CanonicalJSON(comparable)
	if err != nil {
		return Result{}, snapshotValidationError(schemaVersion, err)
	}
	html, err := report.HTML(snapshot)
	if err != nil {
		return Result{}, err
	}
	htmlSum := sha256.Sum256(html)
	htmlDigest := hex.EncodeToString(htmlSum[:])
	htmlJSON, err := json.Marshal(string(html))
	if err != nil {
		return Result{}, err
	}
	// v1/v2 compare the stored digest exactly as before; a v3 candidate is
	// compared with the previous v3 facts without the verification code and
	// the generation time, so a replayed event never mints a new version.
	latestComparableDigest := latest.JSONDigest
	if schemaVersion >= 3 && latestSnapshot.SchemaVersion >= 3 {
		latestSnapshot.VerificationCode = ""
		latestSnapshot.Context.Inspection.GeneratedAt = "report-generation"
		_, latestComparableDigest, err = report.CanonicalJSON(latestSnapshot)
		if err != nil {
			return Result{}, err
		}
	}
	if latest.ID != (identity.ID{}) && latestComparableDigest == jsonDigest {
		if err := persistSnapshotEvidenceReferences(ctx, tx, input.TenantID, latest.ID, snapshot.Evidence, now.UTC()); err != nil {
			return Result{}, err
		}
		return Result{SnapshotID: latest.ID, Version: latest.VersionNumber, JSONDigest: latest.JSONDigest, HTMLDigest: latest.HTMLDigest}, nil
	}
	if schemaVersion >= 3 {
		code := make([]byte, 24)
		if _, err := rand.Read(code); err != nil {
			return Result{}, err
		}
		snapshot.VerificationCode = base64.RawURLEncoding.EncodeToString(code)
	}
	storedSnapshot := snapshot
	if schemaVersion >= 3 {
		storedSnapshot.VerificationCode = ""
	}
	canonical, jsonDigest, err = report.CanonicalJSON(storedSnapshot)
	if err != nil {
		return Result{}, snapshotValidationError(schemaVersion, err)
	}
	// Keep the renderer artifact version stable. Stored PDFs are served as
	// issued; changing the v3 template does not invalidate existing artifacts.
	pdfRenderVersion := 1
	row := database.ReportSnapshot{ID: id, TenantID: input.TenantID, InspectionID: input.InspectionID, ProjectID: input.ProjectID, VideoPlanID: input.VideoPlanID, Mode: input.Mode, Classification: input.Classification, JSONDigest: jsonDigest, HTMLDigest: htmlDigest, SchemaVersion: schemaVersion, VersionNumber: version, PDFRenderVersion: pdfRenderVersion, PublicationPolicyVersion: input.PublicationPolicyVersion, CanonicalJSON: canonical, HTML: htmlJSON, CreatedAt: now.UTC()}
	if input.VideoPlanID != nil {
		if err := verifyPlanFrameEvidence(ctx, tx, input.TenantID, *input.VideoPlanID, input.ReferenceVersionID, snapshot.Evidence); err != nil {
			return Result{}, err
		}
	}
	if err := tx.Create(&row).Error; err != nil {
		return Result{}, err
	}
	if schemaVersion >= 3 && tx.Dialector.Name() == "postgres" {
		codeHash := sha256.Sum256([]byte(snapshot.VerificationCode))
		if err := tx.Exec(
			"INSERT INTO reports.report_verification_codes(tenant_id,snapshot_id,inspection_id,code_hash,verification_code) VALUES (?,?,?,?,?)",
			input.TenantID, id, input.InspectionID, hex.EncodeToString(codeHash[:]), snapshot.VerificationCode,
		).Error; err != nil {
			return Result{}, err
		}
	}
	if err := persistSnapshotEvidenceReferences(ctx, tx, input.TenantID, id, snapshot.Evidence, now.UTC()); err != nil {
		return Result{}, err
	}
	result := Result{SnapshotID: id, Version: version, JSONDigest: jsonDigest, HTMLDigest: htmlDigest}
	// The AUTOMATIC policy version effective at finalization releases the report
	// in the snapshot's own transaction.
	if input.PublicationMode == publicationcore.Automatic {
		key := AutomaticMutationID(id)
		released, err := publication.PublishTx(ctx, tx, publication.PublishInput{TenantID: input.TenantID, InspectionID: input.InspectionID, SnapshotID: id, ActorID: input.ActorID, ClientMutationID: key, CorrelationID: key, Final: true}, now.UTC())
		if err != nil {
			return Result{}, err
		}
		result.PublicationID = released.ID
	}
	return result, nil
}

// AutomaticMutationID is the deterministic publication key of a snapshot
// released by the AUTOMATIC policy, so a replay cannot release it twice.
func AutomaticMutationID(snapshotID identity.ID) string { return "auto:" + snapshotID.String() }

func hasVideoEvidence(evidence []report.Evidence) bool {
	for _, item := range evidence {
		if item.EvidenceKind == "VIDEO_FRAME" {
			return true
		}
	}
	return false
}

// componentLabels maps each video evidence ID to the label of the requirement
// (component) it supports.
func componentLabels(evidence []report.Evidence, requirements []report.Requirement) map[string]string {
	byKey := make(map[string]string, len(requirements))
	for _, requirement := range requirements {
		byKey[requirement.Key] = requirement.Label
	}
	labels := make(map[string]string, len(evidence))
	for _, item := range evidence {
		if label := byKey[item.RequirementKey]; item.EvidenceKind == "VIDEO_FRAME" && label != "" {
			labels[item.ID] = label
		}
	}
	return labels
}

// findingComponent returns the single component label that all evidence of a
// finding supports, or "" when the evidence spans components or is unknown.
func findingComponent(finding report.Finding, labels map[string]string) string {
	component := ""
	for _, id := range finding.EvidenceIDs {
		label := labels[id]
		if label == "" || component != "" && label != component {
			return ""
		}
		component = label
	}
	return component
}

// consolidateFindings merges an identical observation emitted by overlapping
// video comparison groups when they share evidence, or by repeated views of
// the same labelled component, preserving occurrences with distinct text or
// on other components.
func consolidateFindings(findings []report.Finding, labels map[string]string) []report.Finding {
	result := make([]report.Finding, 0, len(findings))
	indexes := make(map[string][]int, len(findings))
	for _, finding := range findings {
		key := finding.Category + "\x00" + finding.Title + "\x00" + finding.Description + "\x00" + finding.Severity + "\x00" + finding.Quality + "\x00" + finding.RecommendedAction
		index := -1
		component := findingComponent(finding, labels)
		for _, candidate := range indexes[key] {
			if findingsShareEvidence(result[candidate].EvidenceIDs, finding.EvidenceIDs) || component != "" && findingComponent(result[candidate], labels) == component {
				index = candidate
				break
			}
		}
		if index < 0 {
			finding.EvidenceIDs = append([]string(nil), finding.EvidenceIDs...)
			indexes[key] = append(indexes[key], len(result))
			result = append(result, finding)
			continue
		}
		merged := &result[index]
		seen := make(map[string]struct{}, len(merged.EvidenceIDs))
		for _, id := range merged.EvidenceIDs {
			seen[id] = struct{}{}
		}
		for _, id := range finding.EvidenceIDs {
			if _, exists := seen[id]; !exists {
				merged.EvidenceIDs = append(merged.EvidenceIDs, id)
				seen[id] = struct{}{}
			}
		}
		if finding.Confidence > merged.Confidence {
			merged.Confidence = finding.Confidence
		}
	}
	return result
}

func findingsShareEvidence(left, right []string) bool {
	seen := make(map[string]struct{}, len(left))
	for _, id := range left {
		seen[id] = struct{}{}
	}
	for _, id := range right {
		if _, exists := seen[id]; exists {
			return true
		}
	}
	return false
}

func setSnapshotTenant(tx *gorm.DB, tenantID identity.ID) error {
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	return tx.Exec("SELECT set_config('app.tenant_id', ?, true)", tenantID.String()).Error
}

// verifyPlanFrameEvidence accepts only frames that belong to an item of the
// plan, directly or through a result carried from an earlier generation, with
// the same preparation and digest as the stored frame.
func verifyPlanFrameEvidence(ctx context.Context, tx *gorm.DB, tenantID, planID identity.ID, referenceVersionID string, evidence []report.Evidence) error {
	for _, item := range evidence {
		if item.EvidenceKind != "VIDEO_FRAME" {
			continue
		}
		frameID, frameErr := identity.ParseID(item.FrameID)
		preparationID, preparationErr := identity.ParseID(item.PreparationID)
		if frameErr != nil || preparationErr != nil {
			return ErrFrameIntegrity
		}
		var members int64
		if err := tx.WithContext(ctx).Table("analysis.video_plan_item_frames AS membership").
			Joins("JOIN analysis.video_plan_items AS plan_item ON plan_item.tenant_id=membership.tenant_id AND plan_item.id=membership.plan_item_id").
			Joins("JOIN media.video_frames AS frame ON frame.tenant_id=membership.tenant_id AND frame.id=membership.frame_id").
			Where("membership.tenant_id=? AND membership.frame_id=? AND frame.preparation_id=? AND frame.sha256=?", tenantID, frameID, preparationID, item.FrameDigest).
			Where("plan_item.plan_id=? OR plan_item.id IN (SELECT job.video_plan_item_id FROM analysis.comparison_jobs AS job JOIN analysis.video_plan_items AS carried ON carried.tenant_id=job.tenant_id AND carried.carried_result_job_id=job.id WHERE carried.tenant_id=? AND carried.plan_id=?)", planID, tenantID, planID).
			Count(&members).Error; err != nil {
			return err
		}
		if members == 0 {
			if item.Role != "REFERENCE" {
				return ErrFrameIntegrity
			}
			promoted, err := promotedOriginFrameMember(ctx, tx, tenantID, referenceVersionID, frameID, preparationID, item)
			if err != nil {
				return err
			}
			if !promoted {
				return ErrFrameIntegrity
			}
		}
	}
	return nil
}

// promotedOriginFrameMember accepts a frame from the inspection promoted to
// the pinned origin only while that origin still retains its preparation.
func promotedOriginFrameMember(ctx context.Context, tx *gorm.DB, tenantID identity.ID, referenceVersionID string, frameID, preparationID identity.ID, item report.Evidence) (bool, error) {
	versionID, err := identity.ParseID(referenceVersionID)
	if err != nil {
		return false, nil
	}
	var members int64
	err = tx.WithContext(ctx).Table("analysis.video_plan_item_frames AS membership").
		Joins("JOIN analysis.video_plan_items AS plan_item ON plan_item.tenant_id=membership.tenant_id AND plan_item.id=membership.plan_item_id").
		Joins("JOIN origins.inspection_origin_promotions AS promotion ON promotion.tenant_id=plan_item.tenant_id AND promotion.video_plan_id=plan_item.plan_id").
		Joins("JOIN media.video_frames AS frame ON frame.tenant_id=membership.tenant_id AND frame.id=membership.frame_id").
		Joins("JOIN media.video_evidence_references AS hold ON hold.tenant_id=frame.tenant_id AND hold.preparation_id=frame.preparation_id").
		Where("membership.tenant_id=? AND membership.frame_id=? AND membership.role='CURRENT'", tenantID, frameID).
		Where("(plan_item.requirement_key=? OR plan_item.requirement_key IN (SELECT environment.requirement_key FROM origins.inventory_environments AS environment WHERE environment.tenant_id=? AND environment.origin_version_id=? AND 'env:' || CAST(environment.identity_id AS text)=?)) AND frame.preparation_id=? AND frame.sha256=?", item.RequirementKey, tenantID, versionID, item.RequirementKey, preparationID, item.FrameDigest).
		Where("promotion.origin_version_id=? AND promotion.status='COMPLETED' AND promotion.source_kind='VIDEO'", versionID).
		Where("hold.owner_type='ORIGIN_VERSION' AND hold.owner_id=?", versionID).
		Count(&members).Error
	return members > 0, err
}

func persistSnapshotEvidenceReferences(ctx context.Context, db *gorm.DB, tenantID, snapshotID identity.ID, evidence []report.Evidence, now time.Time) error {
	seen := make(map[identity.ID]struct{})
	for _, item := range evidence {
		if item.EvidenceKind != "VIDEO_FRAME" {
			continue
		}
		preparationID, err := identity.ParseID(item.PreparationID)
		if err != nil || preparationID == (identity.ID{}) {
			return fmt.Errorf("report snapshot: invalid video preparation reference")
		}
		if _, ok := seen[preparationID]; ok {
			continue
		}
		seen[preparationID] = struct{}{}
		reference := database.VideoEvidenceReference{ID: identity.NewID(), TenantID: tenantID, PreparationID: preparationID, OwnerType: "REPORT_SNAPSHOT", OwnerID: snapshotID, CreatedAt: now}
		if err := db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "preparation_id"}, {Name: "owner_type"}, {Name: "owner_id"}}, DoNothing: true}).Create(&reference).Error; err != nil {
			return err
		}
	}
	return nil
}
