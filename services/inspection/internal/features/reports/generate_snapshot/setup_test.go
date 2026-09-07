package generate_snapshot

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
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
	input := Input{TenantID: tenant, InspectionID: inspection, Mode: "HISTORICAL", Classification: "ATTENTION", TemplateVersionID: "template-v1", ReferenceVersionID: "reference-v1", ProfileVersionID: "profile-v1"}
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
