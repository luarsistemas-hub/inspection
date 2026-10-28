package expire_video_sources

import (
	"context"
	"io"
	"testing"
	"time"

	"inspection/libs/identity"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/platform/objectstore"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSetupRejectsEachMissingDependencyUT070(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	base := Dependencies{DB: db, Store: objectstore.Store{Bucket: "test", Client: expiryStoreStub{}}}
	for name, change := range map[string]func(*Dependencies){
		"database": func(d *Dependencies) { d.DB = nil },
		"store":    func(d *Dependencies) { d.Store.Client = nil },
		"bucket":   func(d *Dependencies) { d.Store.Bucket = "" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			change(&candidate)
			if executor, err := Setup(candidate); err == nil || executor != nil {
				t.Fatalf("Setup with missing %s = executor=%v err=%v", name, executor, err)
			}
		})
	}
	if _, err := Setup(base); err != nil {
		t.Fatalf("Setup with complete dependencies: %v", err)
	}
}

type expiryStoreStub struct{}

func (expiryStoreStub) CreateMultipart(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (expiryStoreStub) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "", nil
}
func (expiryStoreStub) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (expiryStoreStub) AbortMultipart(context.Context, string, string, string) error { return nil }
func (expiryStoreStub) Head(context.Context, string, string) (objectstore.Head, error) {
	return objectstore.Head{}, nil
}
func (expiryStoreStub) Get(context.Context, string, string) (io.ReadCloser, error) { return nil, nil }
func (expiryStoreStub) Put(context.Context, string, string, io.Reader, int64, string) error {
	return nil
}

func TestExpiryAtUsesRoleSpecificElapsedDays(t *testing.T) {
	first := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	got, ok := ExpiryAt(first, "CURRENT", 5, 30)
	want := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	if !ok || !got.Equal(want) {
		t.Fatalf("ExpiryAt() = %v, %v; want %v, true", got, ok, want)
	}
	got, ok = ExpiryAt(first, "ORIGIN", 5, 30)
	want = first.Add(30 * 24 * time.Hour)
	if !ok || !got.Equal(want) {
		t.Fatalf("origin ExpiryAt() = %v, %v; want %v, true", got, ok, want)
	}
}

func TestSourceHeldReadsVideoScopedLegalHold(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS retention").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE retention.legal_holds (
		id text PRIMARY KEY, tenant_id text NOT NULL, inspection_id text, source_media_id text,
		origin_version_id text, reason text NOT NULL, active numeric NOT NULL DEFAULT 1
	)`).Error; err != nil {
		t.Fatal(err)
	}
	tenantID, mediaID := identity.ID(uuid.New()), identity.ID(uuid.New())
	if held, err := retentioncore.SourceHeld(db, tenantID, mediaID); err != nil || held {
		t.Fatalf("unheld source = %v, %v", held, err)
	}
	if err := db.Exec("INSERT INTO retention.legal_holds (id,tenant_id,source_media_id,reason,active) VALUES (?,?,?,?,?)", identity.ID(uuid.New()), tenantID, mediaID, "preserve source", true).Error; err != nil {
		t.Fatal(err)
	}
	if held, err := retentioncore.SourceHeld(db, tenantID, mediaID); err != nil || !held {
		t.Fatalf("held source = %v, %v", held, err)
	}
}

func TestEffectiveSourceHeldIncludesInspectionAndOriginHolds(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"retention", "inspections", "media", "origins"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	statements := []string{
		`CREATE TABLE retention.legal_holds (id text, tenant_id text, inspection_id text, source_media_id text, origin_version_id text, reason text, active numeric)`,
		`CREATE TABLE inspections.responsibilities (id text, tenant_id text, inspection_id text)`,
		`CREATE TABLE media.media_objects (id text, tenant_id text, responsibility_id text)`,
		`CREATE TABLE media.video_preparation_jobs (id text, tenant_id text, media_id text)`,
		`CREATE TABLE media.video_evidence_references (id text, tenant_id text, preparation_id text, owner_type text, owner_id text)`,
		`CREATE TABLE origins.origin_versions (id text, tenant_id text, video_preparation_id text)`,
		// Origin holds resolve ownership through the shared retention reference query.
		`CREATE TABLE origins.origin_evidence (id text, tenant_id text, origin_version_id text, media_id text)`,
		`CREATE TABLE origins.inventory_items (id text, tenant_id text, origin_version_id text, environment_id text, decision text, merged_into_id text)`,
		`CREATE TABLE origins.inventory_environments (id text, tenant_id text, decision text)`,
		`CREATE TABLE origins.inventory_item_evidence (id text, tenant_id text, item_id text, media_id text)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, mediaID := identity.NewID(), identity.NewID()
	inspectionID, responsibilityID := identity.NewID(), identity.NewID()
	if err := db.Exec("INSERT INTO inspections.responsibilities VALUES (?,?,?)", responsibilityID, tenantID, inspectionID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO media.media_objects VALUES (?,?,?)", mediaID, tenantID, responsibilityID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO retention.legal_holds VALUES (?,?,?,?,?,?,?)", identity.NewID(), tenantID, inspectionID, nil, nil, "inspection", true).Error; err != nil {
		t.Fatal(err)
	}
	if held, err := retentioncore.EffectiveSourceHeld(db, tenantID, mediaID); err != nil || !held {
		t.Fatalf("inspection hold = %v, %v", held, err)
	}
	if err := db.Exec("DELETE FROM retention.legal_holds").Error; err != nil {
		t.Fatal(err)
	}
	preparationID, versionID := identity.NewID(), identity.NewID()
	if err := db.Exec("INSERT INTO media.video_preparation_jobs VALUES (?,?,?)", preparationID, tenantID, mediaID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO origins.origin_versions VALUES (?,?,?)", versionID, tenantID, preparationID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO media.video_evidence_references VALUES (?,?,?,?,?)", identity.NewID(), tenantID, preparationID, "ORIGIN_VERSION", versionID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO retention.legal_holds VALUES (?,?,?,?,?,?,?)", identity.NewID(), tenantID, nil, nil, versionID, "origin", true).Error; err != nil {
		t.Fatal(err)
	}
	if held, err := retentioncore.EffectiveSourceHeld(db, tenantID, mediaID); err != nil || !held {
		t.Fatalf("origin hold = %v, %v", held, err)
	}
}

func TestExpiryAtDoesNotInventClockWithoutReadiness(t *testing.T) {
	if got, ok := ExpiryAt(time.Time{}, "CURRENT", 5, 30); ok || !got.IsZero() {
		t.Fatalf("ExpiryAt() = %v, %v; want zero, false", got, ok)
	}
}
