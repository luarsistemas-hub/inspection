// Package generate_snapshot creates immutable internal advisory report records.
package generate_snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"inspection/libs/identity"
	report "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

type Input struct {
	TenantID, InspectionID                                  identity.ID
	ProjectID                                               *identity.ID
	Mode, Classification                                    string
	TemplateVersionID, ReferenceVersionID, ProfileVersionID string
	ReasonCodes                                             []string
	Timeline                                                []report.TimelineEntry
	Coverage                                                map[string]string
	Findings                                                []report.Finding
	Evidence                                                []report.Evidence
	PublicationPolicyVersion                                int64
	PublicationMode                                         string
	ActorID                                                 identity.ID
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
	snapshot := report.Snapshot{SchemaVersion: 1, ReportID: reportID, InspectionID: input.InspectionID.String(), Mode: input.Mode, Classification: input.Classification, TemplateVersionID: input.TemplateVersionID, ReferenceVersionID: input.ReferenceVersionID, ProfileVersionID: input.ProfileVersionID, ReasonCodes: input.ReasonCodes, Timeline: input.Timeline, Coverage: input.Coverage, Findings: input.Findings, Evidence: input.Evidence, Advisory: "Internal advisory triage; findings describe observed changes and do not assign fault, cost, liability, or automatic consequence."}
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
		return Result{SnapshotID: latest.ID, Version: latest.VersionNumber, JSONDigest: latest.JSONDigest, HTMLDigest: latest.HTMLDigest}, nil
	}
	row := database.ReportSnapshot{ID: id, TenantID: input.TenantID, InspectionID: input.InspectionID, ProjectID: input.ProjectID, Mode: input.Mode, Classification: input.Classification, JSONDigest: jsonDigest, HTMLDigest: htmlDigest, VersionNumber: version, PublicationPolicyVersion: input.PublicationPolicyVersion, CanonicalJSON: canonical, HTML: htmlJSON, CreatedAt: now.UTC()}
	if err := db.WithContext(ctx).Create(&row).Error; err != nil {
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
