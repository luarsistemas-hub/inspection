package resolvers

import (
	"context"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestManualVideoReportRequiresPublicationUT057(t *testing.T) {
	db := task06ResolverDB(t)
	if err := db.Exec(`CREATE TABLE reports.report_publications (id blob PRIMARY KEY, tenant_id blob NOT NULL, snapshot_id blob NOT NULL, inspection_id blob NOT NULL, policy_version integer, client_mutation_id text, status text NOT NULL, actor_id blob NOT NULL, reason text, version integer NOT NULL, published_at datetime, invalidated_at datetime, superseded_by blob, created_at datetime, updated_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	tenantID, inspectionID := identity.NewID(), identity.NewID()
	planID, frameID := identity.NewID(), identity.NewID()
	ptsMicros, timestampMS := int64(70123456), int64(70123)
	preparationID := identity.NewID()
	if err := db.Exec(`CREATE TABLE media.video_frames (id blob primary key, tenant_id blob, preparation_id blob, sha256 text)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO media.video_frames (id,tenant_id,preparation_id,sha256) VALUES (?,?,?,?)`, frameID, tenantID, preparationID, strings.Repeat("a", 64)).Error; err != nil {
		t.Fatal(err)
	}
	seedTask06PlanFrames(t, db, tenantID, planID, frameID)
	created, err := generate_snapshot.Create(context.Background(), db, generate_snapshot.Input{
		TenantID: tenantID, InspectionID: inspectionID, Mode: "CONSOLIDATED",
		Classification: "ATTENTION", PublicationMode: "MANUAL", PublicationPolicyVersion: 1,
		TemplateVersionID: "template-v1", ReferenceVersionID: "reference-v1", PromptDigest: "prompt-v1",
		VideoPlanID: &planID,
		Evidence:    []reportcore.Evidence{{ID: frameID.String(), RequirementKey: "video:sink", Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: frameID.String(), SourceMediaID: identity.NewID().String(), PreparationID: preparationID.String(), PTSMicros: &ptsMicros, TimestampMS: &timestampMS, FrameDigest: strings.Repeat("a", 64)}},
		Context:     reportcore.Context{Asset: reportcore.AssetContext{ID: identity.NewID().String(), Name: "Imóvel QA", Address: "Rua QA"}, Participant: reportcore.ParticipantContext{ID: identity.NewID().String(), Name: "Responsável QA"}, Template: reportcore.TemplateContext{ID: identity.NewID().String(), Name: "Modelo QA", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano)}},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if created.PublicationID != (identity.ID{}) {
		t.Fatalf("manual video report was automatically released: %+v", created)
	}
	businessUnitID := identity.NewID()
	if err := db.Exec(`INSERT INTO inspections.inspections (id,tenant_id,business_unit_id,asset_id) VALUES (?,?,?,?)`, inspectionID, tenantID, businessUnitID, identity.NewID()).Error; err != nil {
		t.Fatal(err)
	}
	server := task06GraphQLServer(db)
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.CustomerViewer}, Scopes: []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: businessUnitID}}}})
	query := `query { customerReport(inspectionId:"` + inspectionID.String() + `") { snapshotId status } }`
	if response := executeTask06GraphQL(t, server, query, ctx); strings.Contains(response, `"errors"`) || !strings.Contains(response, `"customerReport":null`) {
		t.Fatalf("unpublished manual report was accessible or query failed: %s", response)
	}
	now := time.Now().UTC()
	if err := db.Create(&database.ReportPublication{ID: identity.NewID(), TenantID: tenantID, SnapshotID: created.SnapshotID, InspectionID: inspectionID, PolicyVersion: 1, Status: "PUBLISHED", ActorID: identity.NewID(), Version: 1, PublishedAt: &now, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if response := executeTask06GraphQL(t, server, query, ctx); strings.Contains(response, `"errors"`) || !strings.Contains(response, `"snapshotId":"`+created.SnapshotID.String()+`"`) {
		t.Fatalf("released manual report was unavailable: %s", response)
	}
}
