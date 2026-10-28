package core

import (
	"testing"

	"inspection/libs/identity"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestUnit_Issue008_RejectedInventoryEnvironmentDoesNotOwnSource(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"origins", "media"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, ddl := range []string{
		`CREATE TABLE origins.origin_evidence(id text, tenant_id text, origin_version_id text, media_id text)`,
		`CREATE TABLE origins.inventory_items(id text, tenant_id text, origin_version_id text, environment_id text, decision text, merged_into_id text)`,
		`CREATE TABLE origins.inventory_item_evidence(id text, tenant_id text, item_id text, media_id text)`,
		`CREATE TABLE origins.inventory_environments(id text, tenant_id text, decision text)`,
		`CREATE TABLE origins.origin_versions(id text, tenant_id text, video_preparation_id text)`,
		`CREATE TABLE media.video_preparation_jobs(id text, tenant_id text, media_id text)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, versionID, acceptedEnv, rejectedEnv, acceptedMedia, rejectedMedia := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	for _, env := range []struct {
		id       identity.ID
		decision string
	}{{acceptedEnv, "ACCEPTED"}, {rejectedEnv, "REJECTED"}} {
		if err := db.Exec("INSERT INTO origins.inventory_environments VALUES (?,?,?)", env.id, tenantID, env.decision).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct{ envID, mediaID identity.ID }{{acceptedEnv, acceptedMedia}, {rejectedEnv, rejectedMedia}} {
		id := identity.NewID()
		if err := db.Exec("INSERT INTO origins.inventory_items VALUES (?,?,?,?,?,NULL)", id, tenantID, versionID, row.envID, "ACCEPTED").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO origins.inventory_item_evidence VALUES (?,?,?,?)", identity.NewID(), tenantID, id, row.mediaID).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, mediaID := range []identity.ID{acceptedMedia, rejectedMedia} {
		if err := db.Exec("INSERT INTO media.video_preparation_jobs VALUES (?,?,?)", identity.NewID(), tenantID, mediaID).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		mediaID identity.ID
		want    int64
	}{{acceptedMedia, 1}, {rejectedMedia, 0}} {
		var count int64
		query := "SELECT count(*) FROM (" + ReferencingVersionsFor("?", "?") + ") refs"
		if err := db.Raw(query, tenantID, tc.mediaID, tenantID, tc.mediaID, tenantID, tc.mediaID).Scan(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != tc.want {
			t.Fatalf("Issue 008 media %s refs=%d; want %d", tc.mediaID, count, tc.want)
		}
	}
}
