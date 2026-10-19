package generate_snapshot

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	report "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestCreateKeepsSnapshotsImmutableAndIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("ATTACH DATABASE ':memory:' AS reports").Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("ATTACH DATABASE ':memory:' AS media").Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("CREATE TABLE media.video_evidence_references (id text, tenant_id text, preparation_id text, owner_type text, owner_id text, created_at datetime, UNIQUE(tenant_id, preparation_id, owner_type, owner_id))").Error; err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&database.ReportSnapshot{}); err != nil {
		t.Fatal(err)
	}
	tenant, inspection := identity.NewID(), identity.NewID()
	prep, frame, source := identity.NewID(), identity.NewID(), identity.NewID()
	timestamp := int64(250)
	input := Input{TenantID: tenant, InspectionID: inspection, Mode: "HISTORICAL", Classification: "ATTENTION", TemplateVersionID: "template-v1", ReferenceVersionID: "reference-v1", PromptDigest: "prompt-v1", Context: report.Context{Asset: report.AssetContext{ID: "asset", Name: "Imóvel", ExternalKey: "A-1", Address: "Rua 1"}, Participant: report.ParticipantContext{ID: "participant", Name: "Responsável"}, Template: report.TemplateContext{ID: "template", Name: "Modelo", Version: 1}, Inspection: report.InspectionContext{GeneratedAt: "2026-09-16T00:00:00Z"}}, Evidence: []report.Evidence{{ID: frame.String(), RequirementKey: "video:room", Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: frame.String(), SourceMediaID: source.String(), PreparationID: prep.String(), TimestampMS: &timestamp, FrameDigest: strings.Repeat("a", 64)}}}
	first, err := Create(context.Background(), db, input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := Create(context.Background(), db, input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if first.SnapshotID != repeated.SnapshotID || first.Version != 1 {
		t.Fatalf("unexpected versions: %#v %#v", first, repeated)
	}
	var persisted database.ReportSnapshot
	if err := db.First(&persisted, "id=?", first.SnapshotID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.PDFRenderVersion != 1 {
		t.Fatalf("new snapshot renderer version=%d, want 1", persisted.PDFRenderVersion)
	}
	var references int64
	if err := db.Model(&database.VideoEvidenceReference{}).Where("tenant_id=? AND preparation_id=? AND owner_type='REPORT_SNAPSHOT' AND owner_id=?", tenant, prep, first.SnapshotID).Count(&references).Error; err != nil || references != 1 {
		t.Fatalf("snapshot evidence references=%d, err=%v; want one", references, err)
	}
}

func TestCreateRejectsInvalidVideoPreparationReference(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("ATTACH DATABASE ':memory:' AS reports").Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("ATTACH DATABASE ':memory:' AS media").Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("CREATE TABLE media.video_evidence_references (id text, tenant_id text, preparation_id text, owner_type text, owner_id text, created_at datetime, UNIQUE(tenant_id, preparation_id, owner_type, owner_id))").Error; err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&database.ReportSnapshot{}); err != nil {
		t.Fatal(err)
	}
	timestamp := int64(250)
	frameID := identity.NewID().String()
	input := Input{TenantID: identity.NewID(), InspectionID: identity.NewID(), Mode: "HISTORICAL", Classification: "ATTENTION", TemplateVersionID: "template-v1", ReferenceVersionID: "reference-v1", PromptDigest: "prompt-v1", Context: report.Context{Asset: report.AssetContext{ID: "asset", Name: "Asset", ExternalKey: "A-1", Address: "Street"}, Participant: report.ParticipantContext{ID: "participant", Name: "Person"}, Template: report.TemplateContext{ID: "template", Name: "Template", Version: 1}, Inspection: report.InspectionContext{GeneratedAt: "2026-09-16T00:00:00Z"}}, Evidence: []report.Evidence{{ID: frameID, RequirementKey: "video:room", Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: frameID, SourceMediaID: identity.NewID().String(), PreparationID: "invalid", TimestampMS: &timestamp, FrameDigest: strings.Repeat("a", 64)}}}
	if _, err := Create(context.Background(), db, input, time.Now()); err == nil {
		t.Fatal("invalid video preparation reference was accepted")
	}
}

func TestVV_IT_181SnapshotConsolidatesOverlappingScratchFinding(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("ATTACH DATABASE ':memory:' AS reports").Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("ATTACH DATABASE ':memory:' AS media").Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("CREATE TABLE media.video_evidence_references (id text, tenant_id text, preparation_id text, owner_type text, owner_id text, created_at datetime, UNIQUE(tenant_id, preparation_id, owner_type, owner_id))").Error; err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&database.ReportSnapshot{}); err != nil {
		t.Fatal(err)
	}
	tenantID, inspectionID := identity.NewID(), identity.NewID()
	frameID := identity.NewID().String()
	timestamp := int64(250)
	input := Input{TenantID: tenantID, InspectionID: inspectionID, Mode: "HISTORICAL", Classification: "ATTENTION", TemplateVersionID: "template-v1", ReferenceVersionID: "origin-v1", PromptDigest: "prompt-v1", Context: report.Context{Asset: report.AssetContext{ID: "asset", Name: "QA", Address: "Local"}, Participant: report.ParticipantContext{ID: "participant", Name: "QA"}, Template: report.TemplateContext{ID: "template", Name: "QA", Version: 1}, Inspection: report.InspectionContext{GeneratedAt: time.Now().UTC().Format(time.RFC3339)}}, Evidence: []report.Evidence{{ID: frameID, RequirementKey: "video:room", Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: frameID, SourceMediaID: identity.NewID().String(), PreparationID: identity.NewID().String(), TimestampMS: &timestamp, FrameDigest: strings.Repeat("a", 64)}}, Findings: []report.Finding{
		{Category: "CONSERVATION", Title: "Arranhão D1", Description: "Um arranhão fino visível na pia.", Severity: "LOW", Confidence: .87, EvidenceIDs: []string{"origin-frame", "current-frame-1"}, Quality: "ADEQUATE", RecommendedAction: "Acompanhar na próxima vistoria"},
		{Category: "CONSERVATION", Title: "Arranhão D1", Description: "Um arranhão fino visível na pia.", Severity: "LOW", Confidence: .93, EvidenceIDs: []string{"origin-frame", "current-frame-2"}, Quality: "ADEQUATE", RecommendedAction: "Acompanhar na próxima vistoria"},
		{Category: "CONSERVATION", Title: "Arranhão D1", Description: "Um arranhão fino visível na pia.", Severity: "LOW", Confidence: .89, EvidenceIDs: []string{"other-room-frame"}, Quality: "ADEQUATE", RecommendedAction: "Acompanhar na próxima vistoria"},
	}}
	created, err := Create(context.Background(), db, input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var stored database.ReportSnapshot
	if err := db.Where("id=?", created.SnapshotID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	var got report.Snapshot
	if err := json.Unmarshal(stored.CanonicalJSON, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Findings) != 2 || got.Findings[0].Title != "Arranhão D1" || got.Findings[0].Confidence != .93 || len(got.Findings[0].EvidenceIDs) != 3 || len(got.Findings[1].EvidenceIDs) != 1 {
		t.Fatalf("overlapping groups produced duplicate D1 or lost evidence: %+v", got.Findings)
	}
}

func TestCreatePreservesDuplicatePhotoFindings(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("ATTACH DATABASE ':memory:' AS reports").Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("ATTACH DATABASE ':memory:' AS media").Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("CREATE TABLE media.video_evidence_references (id text, tenant_id text, preparation_id text, owner_type text, owner_id text, created_at datetime, UNIQUE(tenant_id, preparation_id, owner_type, owner_id))").Error; err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&database.ReportSnapshot{}); err != nil {
		t.Fatal(err)
	}
	input := Input{
		TenantID: identity.NewID(), InspectionID: identity.NewID(), Mode: "HISTORICAL", Classification: "ATTENTION",
		TemplateVersionID: "template-v1", ReferenceVersionID: "origin-v1", PromptDigest: "prompt-v1",
		Context:  report.Context{Asset: report.AssetContext{ID: "asset", Name: "QA", Address: "Local"}, Participant: report.ParticipantContext{ID: "participant", Name: "QA"}, Template: report.TemplateContext{ID: "template", Name: "QA", Version: 1}, Inspection: report.InspectionContext{GeneratedAt: time.Now().UTC().Format(time.RFC3339)}},
		Evidence: []report.Evidence{{ID: "photo-1", RequirementKey: "bathroom:sink", Role: "CURRENT", EvidenceKind: "PHOTO"}},
		Findings: []report.Finding{
			{Category: "CONSERVATION", Title: "Arranhão D1", Description: "Um arranhão fino visível na pia.", Severity: "LOW", Confidence: .87, EvidenceIDs: []string{"photo-1"}, Quality: "ADEQUATE", RecommendedAction: "Acompanhar na próxima vistoria"},
			{Category: "CONSERVATION", Title: "Arranhão D1", Description: "Um arranhão fino visível na pia.", Severity: "LOW", Confidence: .93, EvidenceIDs: []string{"photo-1"}, Quality: "ADEQUATE", RecommendedAction: "Acompanhar na próxima vistoria"},
		},
	}
	created, err := Create(context.Background(), db, input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var stored database.ReportSnapshot
	if err := db.Where("id=?", created.SnapshotID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	var got report.Snapshot
	if err := json.Unmarshal(stored.CanonicalJSON, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Findings) != 2 {
		t.Fatalf("PHOTO snapshot consolidated findings: got %d, want 2: %+v", len(got.Findings), got.Findings)
	}
}

func TestConsolidateFindingsMergesRepeatedViewsOfSameComponent(t *testing.T) {
	d1 := report.Finding{Category: "CONSERVATION", Title: "Risco D1", Description: "Risco visível na pia.", Severity: "LOW", Confidence: .8, Quality: "ADEQUATE", RecommendedAction: "Acompanhar"}
	d2 := d1
	d2.Title, d2.Description = "Mancha D2", "Mancha no armário."
	at := func(f report.Finding, ids ...string) report.Finding { f.EvidenceIDs = ids; return f }
	labels := map[string]string{"k1": "kitchen — sink", "k2": "kitchen — sink", "k3": "kitchen — sink", "b1": "bathroom — sink"}
	result := consolidateFindings([]report.Finding{at(d1, "k1"), at(d1, "k2"), at(d2, "k3"), at(d1, "b1"), at(d1, "k3")}, labels)
	if len(result) != 3 {
		t.Fatalf("consolidated findings=%+v; want D1 kitchen, D2 kitchen and D1 bathroom", result)
	}
	if result[0].Title != "Risco D1" || strings.Join(result[0].EvidenceIDs, ",") != "k1,k2,k3" {
		t.Fatalf("kitchen D1=%+v; want one finding citing k1,k2,k3", result[0])
	}
	if result[1].Title != "Mancha D2" || result[2].EvidenceIDs[0] != "b1" {
		t.Fatalf("distinct D2 or other-component D1 were merged: %+v", result)
	}
}
