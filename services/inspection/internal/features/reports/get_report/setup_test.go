package get_report

import (
	"context"
	"errors"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func snapshotDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS reports").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.ReportSnapshot{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSetupRequiresDatabase(t *testing.T) {
	if _, err := Setup(Dependencies{}); !errors.Is(err, gorm.ErrInvalidDB) {
		t.Fatalf("missing database accepted: %v", err)
	}
}

func TestGetReportReturnsNewestOrRequestedVersionWithinTheTenantOnly(t *testing.T) {
	db := snapshotDB(t)
	tenant, other, inspection := identity.NewID(), identity.NewID(), identity.NewID()
	rows := map[int]identity.ID{1: identity.NewID(), 2: identity.NewID()}
	for version, id := range rows {
		if err := db.Create(&database.ReportSnapshot{ID: id, TenantID: tenant, InspectionID: inspection, Mode: "HISTORICAL", Classification: "NORMAL", JSONDigest: "j", HTMLDigest: "h", VersionNumber: version, CanonicalJSON: []byte(`{}`), HTML: []byte(`""`), CreatedAt: time.Now().UTC()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	query, err := Setup(Dependencies{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	newest, err := query(context.Background(), Input{TenantID: tenant, InspectionID: inspection})
	if err != nil || newest.ID != rows[2] {
		t.Fatalf("newest = %v (%v), want version 2", newest.ID, err)
	}
	one := 1
	first, err := query(context.Background(), Input{TenantID: tenant, InspectionID: inspection, Version: &one})
	if err != nil || first.ID != rows[1] {
		t.Fatalf("explicit version = %v (%v), want version 1", first.ID, err)
	}
	for name, in := range map[string]Input{"other tenant": {TenantID: other, InspectionID: inspection}, "unknown inspection": {TenantID: tenant, InspectionID: identity.NewID()}} {
		if row, err := query(context.Background(), in); !errors.Is(err, gorm.ErrRecordNotFound) || row.ID != (identity.ID{}) {
			t.Fatalf("%s returned %v (%v)", name, row.ID, err)
		}
	}
}

// UT-051: malformed and unknown version references resolve to the same empty
// not-found result and cannot select another inspection's report.
func TestUT051UnknownOrAlteredVersionDoesNotExposeAnotherReport(t *testing.T) {
	db := snapshotDB(t)
	tenant, inspection := identity.NewID(), identity.NewID()
	if err := db.Create(&database.ReportSnapshot{ID: identity.NewID(), TenantID: tenant, InspectionID: inspection, Mode: "HISTORICAL", Classification: "NORMAL", JSONDigest: "private-digest", HTMLDigest: "private-html", VersionNumber: 1, CanonicalJSON: []byte(`{"verificationCode":"secret"}`), HTML: []byte(`"private"`), CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	query, err := Setup(Dependencies{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{0, 2, 999} {
		row, err := query(context.Background(), Input{TenantID: tenant, InspectionID: inspection, Version: &version})
		if !errors.Is(err, gorm.ErrRecordNotFound) || row.ID != (identity.ID{}) || row.JSONDigest != "" {
			t.Fatalf("version %d returned %+v (%v), want an empty not-found result", version, row, err)
		}
	}
}
