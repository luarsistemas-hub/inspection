package generate_snapshot

import (
	"context"
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
	if err = db.AutoMigrate(&database.ReportSnapshot{}); err != nil {
		t.Fatal(err)
	}
	tenant, inspection := identity.NewID(), identity.NewID()
	input := Input{TenantID: tenant, InspectionID: inspection, Mode: "HISTORICAL", Classification: "ATTENTION", TemplateVersionID: "template-v1", ReferenceVersionID: "reference-v1", PromptDigest: "prompt-v1", Context: report.Context{Asset: report.AssetContext{ID: "asset", Name: "Imóvel", ExternalKey: "A-1", Address: "Rua 1"}, Participant: report.ParticipantContext{ID: "participant", Name: "Responsável"}, Template: report.TemplateContext{ID: "template", Name: "Modelo", Version: 1}, Inspection: report.InspectionContext{GeneratedAt: "2026-09-16T00:00:00Z"}}}
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
}
