// Package generate_snapshot creates immutable internal advisory report records.
package generate_snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	report "inspection/services/inspection/internal/features/reports/core"
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

// Create increments versions only by append. A repeated terminal event with
// identical report input returns the already-stored immutable snapshot.
func Create(ctx context.Context, db *gorm.DB, input Input, now time.Time) (Result, error) {
	if db == nil || input.TenantID == (identity.ID{}) || input.InspectionID == (identity.ID{}) {
		return Result{}, fmt.Errorf("report snapshot: missing identity")
	}
	var latest database.ReportSnapshot
	err := db.WithContext(ctx).Where("tenant_id=? AND inspection_id=?", input.TenantID, input.InspectionID).Order("version_number DESC").First(&latest).Error
	version := 1
	if err == nil {
		version = latest.VersionNumber + 1
	} else if err != gorm.ErrRecordNotFound {
		return Result{}, err
	}
	id := identity.NewID()
	reportID := id.String()
	if latest.ID != (identity.ID{}) {
		reportID = latest.ID.String()
	}
	findings := input.Findings
	if hasVideoEvidence(input.Evidence) {
		findings = consolidateFindings(findings, componentLabels(input.Evidence, input.Requirements))
	}
	snapshot := report.Snapshot{SchemaVersion: 1, ReportID: reportID, InspectionID: input.InspectionID.String(), Mode: input.Mode, Classification: input.Classification, TemplateVersionID: input.TemplateVersionID, ReferenceVersionID: input.ReferenceVersionID, PromptDigest: input.PromptDigest, ReasonCodes: input.ReasonCodes, Timeline: input.Timeline, Coverage: input.Coverage, Context: input.Context, Requirements: input.Requirements, Findings: findings, Evidence: input.Evidence, Advisory: "Esta triagem interna descreve alterações observadas e não atribui culpa, custo, responsabilidade ou consequência automática."}
	if input.VideoPlanID != nil {
		snapshot.VideoPlanID = input.VideoPlanID.String()
		snapshot.ScopeStatements = VideoScopeStatements(hasReference(input.ReferenceVersionID), input.Requirements)
	}
	if input.ProjectID != nil {
		snapshot.ProjectID = input.ProjectID.String()
	}
	canonical, jsonDigest, err := report.CanonicalJSON(snapshot)
	if err != nil {
		return Result{}, err
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
	if latest.ID != (identity.ID{}) && latest.JSONDigest == jsonDigest {
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := setSnapshotTenant(tx, input.TenantID); err != nil {
				return err
			}
			return persistSnapshotEvidenceReferences(ctx, tx, input.TenantID, latest.ID, snapshot.Evidence, now.UTC())
		}); err != nil {
			return Result{}, err
		}
		return Result{SnapshotID: latest.ID, Version: latest.VersionNumber, JSONDigest: latest.JSONDigest, HTMLDigest: latest.HTMLDigest}, nil
	}
	row := database.ReportSnapshot{ID: id, TenantID: input.TenantID, InspectionID: input.InspectionID, ProjectID: input.ProjectID, VideoPlanID: input.VideoPlanID, Mode: input.Mode, Classification: input.Classification, JSONDigest: jsonDigest, HTMLDigest: htmlDigest, VersionNumber: version, PDFRenderVersion: 1, PublicationPolicyVersion: input.PublicationPolicyVersion, CanonicalJSON: canonical, HTML: htmlJSON, CreatedAt: now.UTC()}
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := setSnapshotTenant(tx, input.TenantID); err != nil {
			return err
		}
		if input.VideoPlanID != nil {
			if err := verifyPlanFrameEvidence(ctx, tx, input.TenantID, *input.VideoPlanID, snapshot.Evidence); err != nil {
				return err
			}
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return persistSnapshotEvidenceReferences(ctx, tx, input.TenantID, id, snapshot.Evidence, now.UTC())
	}); err != nil {
		return Result{}, err
	}
	result := Result{SnapshotID: id, Version: version, JSONDigest: jsonDigest, HTMLDigest: htmlDigest}
	if input.PublicationMode == "AUTOMATIC" && db.Migrator().HasTable(&database.ReportPublication{}) {
		actor := input.ActorID
		if actor == (identity.ID{}) {
			actor = input.TenantID
		}
		published := now.UTC()
		publication := database.ReportPublication{ID: identity.NewID(), TenantID: input.TenantID, SnapshotID: id, InspectionID: input.InspectionID, PolicyVersion: input.PublicationPolicyVersion, Status: "PUBLISHED", ActorID: actor, Version: 1, PublishedAt: &published, CreatedAt: published, UpdatedAt: published}
		if err := db.WithContext(ctx).Create(&publication).Error; err != nil {
			return Result{}, err
		}
		result.PublicationID = publication.ID
	}
	return result, nil
}

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
func verifyPlanFrameEvidence(ctx context.Context, tx *gorm.DB, tenantID, planID identity.ID, evidence []report.Evidence) error {
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
			return ErrFrameIntegrity
		}
	}
	return nil
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
