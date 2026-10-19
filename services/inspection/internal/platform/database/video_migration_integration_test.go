//go:build integration

package database

import (
	"context"
	"os"
	"strings"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database/migrations"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestVideoMigrationsPreserveExistingPhotoRowsIT588(t *testing.T) {
	emptyURL := os.Getenv("INSPECTION_TEST_VIDEO_EMPTY_DATABASE_URL")
	legacyURL := os.Getenv("INSPECTION_TEST_VIDEO_LEGACY_DATABASE_URL")
	if emptyURL == "" || legacyURL == "" {
		t.Skip("two fresh disposable PostgreSQL database URLs are required")
	}
	if emptyURL == legacyURL {
		t.Fatal("empty and legacy migration fixtures must use separate databases")
	}
	ctx := context.Background()
	for _, fixture := range []struct {
		name, dsn string
		legacy    bool
	}{
		{name: "empty", dsn: emptyURL},
		{name: "existing_photo", dsn: legacyURL, legacy: true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			db, err := gorm.Open(postgres.Open(fixture.dsn), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			var existing *string
			if err := db.Raw("SELECT to_regclass('platform.schema_migrations')").Scan(&existing).Error; err != nil {
				t.Fatal(err)
			}
			if existing != nil {
				t.Fatal("migration fixture database is not fresh")
			}
			photoID := identity.NewID()
			if fixture.legacy {
				if err := db.Exec(`CREATE SCHEMA media;
CREATE TABLE media.media_objects (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, responsibility_id uuid NOT NULL,
 object_key varchar(1000) NOT NULL, content_type varchar(100) NOT NULL,
 sha256 varchar(64) NOT NULL, size_bytes bigint NOT NULL, status varchar(20) NOT NULL,
 idempotency_key varchar(200) NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
)`).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Exec(`INSERT INTO media.media_objects
 (id,tenant_id,responsibility_id,object_key,content_type,sha256,size_bytes,status,idempotency_key)
 VALUES (?,?,?,?,?,?,?,?,?)`, photoID, identity.NewID(), identity.NewID(), "qa/legacy-photo/"+photoID.String(), "image/jpeg", strings.Repeat("a", 64), 100, "READY", "qa-legacy-photo-"+photoID.String()).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := (Migrator{DB: db}).Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			var latest int
			if err := db.Raw("SELECT MAX(version) FROM platform.schema_migrations").Scan(&latest).Error; err != nil {
				t.Fatal(err)
			}
			if latest != migrations.LatestVersion() {
				t.Fatalf("applied version=%d; catalog latest=%d", latest, migrations.LatestVersion())
			}
			if err := Compatible(ctx, db, 1, migrations.LatestVersion()); err != nil {
				t.Fatal(err)
			}
			if fixture.legacy {
				var photo MediaObject
				if err := db.Where("id=?", photoID).Take(&photo).Error; err != nil {
					t.Fatal(err)
				}
				if photo.MediaKind != "PHOTO" || photo.Status != "READY" || photo.ContentType != "image/jpeg" || photo.SizeBytes != 100 || photo.SHA256 != strings.Repeat("a", 64) {
					t.Fatalf("legacy PHOTO row changed during video migrations: %+v", photo)
				}
			}
		})
	}
}
