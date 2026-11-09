//go:build integration

package resolvers

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/reports/core"
	customerreport "inspection/services/inspection/internal/features/reports/customer/core"
	"inspection/services/inspection/internal/platform/database"
)

func TestTask07CustomerResolverUsesV3ProcessingAndTriagedLocalizedSnapshot(t *testing.T) {
	ctx, h, fixture, resolver, created := newTask04ResolverFixture(t)
	var row database.ReportSnapshot
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.TenantID, created.SnapshotID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot core.Snapshot
	if err := json.Unmarshal(row.CanonicalJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Processing = &core.ProcessingState{Status: "FAILED", Message: "provider timeout internal"}
	snapshot.Requirements = []core.Requirement{{Key: "video:fireplace", Section: "Vídeo", Label: "living_room — fireplace", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "COMPLETED"}}
	snapshot.ScopeStatements = []string{"Escopo: living_room — fireplace"}
	snapshot.VideoPlanID = "video-plan-presentation-only"
	snapshot.Evidence = []core.Evidence{{ID: fixture.MediaID.String(), RequirementKey: "video:fireplace", Role: "CURRENT", EvidenceKind: "PHOTO", Availability: "AVAILABLE"}}
	snapshot.Checklist.Rooms[0].Components[0].EvidenceIDs = []string{fixture.MediaID.String()}
	canonical, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	// Seed a second immutable stored snapshot. Its canonical presentation has
	// video metadata, while the relational VideoPlanID is intentionally absent:
	// the customer resolver only reads the issued snapshot and does not load a
	// plan as part of report projection.
	row.ID, row.VersionNumber, row.CanonicalJSON = identity.NewID(), row.VersionNumber+1, canonical
	row.JSONDigest, row.HTMLDigest = "task07-customer-resolver-json", "task07-customer-resolver-html"
	row.CreatedAt = time.Now().UTC()
	if err := h.AdminDB.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	previousSnapshotID := created.SnapshotID
	created.SnapshotID, created.Version = row.ID, row.VersionNumber
	if err := h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND snapshot_id=?", fixture.TenantID, previousSnapshotID).Updates(map[string]any{"status": "SUPERSEDED", "superseded_by": row.ID}).Error; err != nil {
		t.Fatal(err)
	}
	publishedAt := time.Now().UTC()
	if err := h.AdminDB.Create(&database.ReportPublication{ID: identity.NewID(), TenantID: fixture.TenantID, SnapshotID: row.ID, InspectionID: fixture.InspectionID, PolicyVersion: 1, ClientMutationID: "task07-resolver-v3", Status: "PUBLISHED", ActorID: fixture.IdentityID, Version: 1, PublishedAt: &publishedAt, CreatedAt: publishedAt, UpdatedAt: publishedAt}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Create(&database.ScreeningRun{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: fixture.MediaID, Status: "BLOCKED", ModelDigest: "task07-screening", Regions: json.RawMessage(`[]`), CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}

	// Call the resolver entry point (the same method used by GraphQL), which
	// triages the snapshot, computes the customer notice, and localizes video.
	result, err := resolver.CustomerReport(ctx, fixture.InspectionID.String(), &created.Version, nil)
	if err != nil || result == nil {
		t.Fatalf("customer report = %+v, err=%v", result, err)
	}
	if result.Processing == nil || result.Processing.Message == nil || *result.Processing.Message != core.CustomerProcessingMessage("FAILED") {
		t.Fatalf("v3 processing message=%+v, want customer-safe Portuguese message", result.Processing)
	}
	if result.EvidenceNotice != core.EvidenceNotice(func() core.Snapshot {
		triaged := snapshot
		triaged.Evidence = []core.Evidence{{ID: fixture.MediaID.String(), RequirementKey: "video:fireplace", Role: "CURRENT", EvidenceKind: "PHOTO", Availability: core.EvidenceWithheld}}
		return triaged
	}()) {
		t.Fatalf("customer evidence notice=%q, does not match the triaged PDF snapshot", result.EvidenceNotice)
	}
	if result.EvidenceNotice == "" || result.EvidenceNotice == core.EvidenceNotice(snapshot) {
		t.Fatalf("evidence notice was not computed after screening triage: %q", result.EvidenceNotice)
	}
	if len(result.ScopeStatements) != 1 || result.ScopeStatements[0] != "Escopo: Sala de estar — Lareira" {
		t.Fatalf("video scope statements=%v, want localized customer PDF wording", result.ScopeStatements)
	}
	evidence, err := resolver.CustomerEvidence(ctx, fixture.InspectionID.String(), nil, nil, nil)
	if err != nil || evidence == nil || len(evidence.Nodes) != 1 {
		t.Fatalf("customer evidence=%+v err=%v", evidence, err)
	}
	if evidence.Nodes[0].Reference != "E001" || evidence.Nodes[0].URL != nil || evidence.Nodes[0].MediaAvailability != customerreport.BlockedAvailability {
		t.Fatalf("triaged customer evidence reference/media=%+v, want E001 with no URL", evidence.Nodes[0])
	}
	for _, legacyVersion := range []int{1, 2} {
		legacySnapshot := snapshot
		legacySnapshot.SchemaVersion = legacyVersion
		legacySnapshot.VideoPlanID = ""
		legacySnapshot.Checklist = nil
		legacySnapshot.Scope = nil
		legacySnapshot.Provenance = nil
		legacySnapshot.Processing = &core.ProcessingState{Status: "FAILED", Message: "A análise ainda não foi concluída. Tente novamente mais tarde."}
		legacySnapshot.Evidence = nil
		legacyJSON, err := json.Marshal(legacySnapshot)
		if err != nil {
			t.Fatal(err)
		}
		legacyRow := row
		legacyRow.ID, legacyRow.VersionNumber, legacyRow.SchemaVersion, legacyRow.CanonicalJSON = identity.NewID(), row.VersionNumber+legacyVersion, legacyVersion, legacyJSON
		legacyRow.JSONDigest, legacyRow.HTMLDigest = fmt.Sprintf("task07-legacy-json-%d", legacyVersion), fmt.Sprintf("task07-legacy-html-%d", legacyVersion)
		legacyRow.VideoPlanID = nil
		legacyRow.CreatedAt = time.Now().UTC()
		if err := h.AdminDB.Create(&legacyRow).Error; err != nil {
			t.Fatal(err)
		}
		if err := h.AdminDB.Create(&database.ReportPublication{ID: identity.NewID(), TenantID: fixture.TenantID, SnapshotID: legacyRow.ID, InspectionID: fixture.InspectionID, PolicyVersion: 1, ClientMutationID: fmt.Sprintf("task07-legacy-v%d", legacyVersion), Status: "SUPERSEDED", ActorID: fixture.IdentityID, Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}).Error; err != nil {
			t.Fatal(err)
		}
		version := legacyRow.VersionNumber
		legacy, err := resolver.CustomerReport(ctx, fixture.InspectionID.String(), &version, nil)
		if err != nil || legacy == nil || legacy.Processing == nil || legacy.Processing.Message == nil || *legacy.Processing.Message != "A análise ainda não foi concluída. Tente novamente mais tarde." {
			t.Fatalf("v%d legacy processing projection=%+v err=%v", legacyVersion, legacy, err)
		}
	}
}
