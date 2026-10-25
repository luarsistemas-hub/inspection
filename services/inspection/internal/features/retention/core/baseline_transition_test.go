package core

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestUnit_UT310_311_316_319_SharedSourceRetentionTransition(t *testing.T) {
	db := retentionTransitionDB(t)
	tenantID, mediaID := identity.NewID(), identity.NewID()
	versionA, versionB := identity.NewID(), identity.NewID()
	supersededA := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	supersededB := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	expiredAt := supersededB.AddDate(0, 0, 365)
	if err := db.Exec(`INSERT INTO media.video_sources(id,tenant_id,media_id,status,retention_anchor,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, identity.NewID(), tenantID, mediaID, "RETAINED", "FIXED", time.Now(), time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO media.video_policies(tenant_id,current_retention_days,superseded_baseline_retention_days,version,updated_at) VALUES(?,?,?,?,?)`, tenantID, 30, 365, 1, time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id     identity.ID
		status string
		at     time.Time
	}{{versionA, "SUPERSEDED", supersededA}, {versionB, "ACTIVE", time.Time{}}} {
		if err := db.Exec(`INSERT INTO origins.origin_versions(id,tenant_id,status,superseded_at,created_at,activated_at) VALUES(?,?,?,?,?,?)`, row.id, tenantID, row.status, nullableTime(row.at), time.Now(), row.at).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`INSERT INTO origins.origin_evidence(id,tenant_id,origin_version_id,media_id) VALUES(?,?,?,?)`, identity.NewID(), tenantID, row.id, mediaID).Error; err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	transition := BaselineTransition{Now: func() time.Time { return now }}
	if err := transition.Transition(context.Background(), db, tenantID, []identity.ID{mediaID}); err != nil {
		t.Fatal(err)
	}
	var source database.VideoSource
	if err := db.Where("tenant_id=? AND media_id=?", tenantID, mediaID).First(&source).Error; err != nil {
		t.Fatal(err)
	}
	if source.RetentionAnchor != "ACTIVE_BASELINE" || source.SourceExpiryAt != nil {
		t.Fatalf("UT-310 active shared source = %s/%v, want ACTIVE_BASELINE/nil", source.RetentionAnchor, source.SourceExpiryAt)
	}
	if err := db.Exec("UPDATE origins.origin_versions SET status='SUPERSEDED' WHERE id=?", versionB).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE origins.origin_versions SET superseded_at=? WHERE id=?", supersededB, versionB).Error; err != nil {
		t.Fatal(err)
	}
	if err := transition.Transition(context.Background(), db, tenantID, []identity.ID{mediaID}); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("tenant_id=? AND media_id=?", tenantID, mediaID).First(&source).Error; err != nil {
		t.Fatal(err)
	}
	if source.RetentionAnchor != "FIXED" || source.SourceExpiryAt == nil || !source.SourceExpiryAt.Equal(expiredAt) {
		t.Fatalf("UT-311/319 transition expiry=%v, want latest superseded version %s", source.SourceExpiryAt, expiredAt)
	}
	olderSourceID := identity.NewID()
	assignedExpiry := expiredAt.AddDate(0, 0, 20)
	if err := db.Exec(`INSERT INTO media.video_sources(id,tenant_id,media_id,status,retention_anchor,source_expiry_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, identity.NewID(), tenantID, olderSourceID, "RETAINED", "FIXED", assignedExpiry, time.Now(), time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO origins.origin_evidence(id,tenant_id,origin_version_id,media_id) VALUES(?,?,?,?)`, identity.NewID(), tenantID, versionA, olderSourceID).Error; err != nil {
		t.Fatal(err)
	}
	if err := transition.Transition(context.Background(), db, tenantID, []identity.ID{olderSourceID}); err != nil {
		t.Fatal(err)
	}
	source = database.VideoSource{}
	if err := db.Where("tenant_id=? AND media_id=?", tenantID, olderSourceID).First(&source).Error; err != nil {
		t.Fatal(err)
	}
	if source.SourceExpiryAt == nil || !source.SourceExpiryAt.Equal(assignedExpiry) {
		t.Fatalf("UT-316 transition shortened assigned expiry: got %v, want %s", source.SourceExpiryAt, assignedExpiry)
	}
}

func TestUnit_TransitionRejectsDeletingSourceAsConflict(t *testing.T) {
	db := retentionTransitionDB(t)
	tenantID, mediaID := identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO media.video_sources(id,tenant_id,media_id,status,retention_anchor,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, identity.NewID(), tenantID, mediaID, "DELETING", "FIXED", time.Now(), time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	err := (BaselineTransition{}).Transition(context.Background(), db, tenantID, []identity.ID{mediaID})
	if !apperror.Is(err, apperror.Conflict) {
		t.Fatalf("transition error=%v, want typed conflict", err)
	}
}

func TestUnit_Issue003_InvalidatingSupersededVersionKeepsFirstReleaseInstant(t *testing.T) {
	db := retentionTransitionDB(t)
	tenantID, mediaID, versionID := identity.NewID(), identity.NewID(), identity.NewID()
	supersededAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	invalidatedAt := supersededAt.AddDate(0, 0, 300)
	now := invalidatedAt.Add(time.Minute)
	if err := db.Exec(`INSERT INTO media.video_sources(id,tenant_id,media_id,status,retention_anchor,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, identity.NewID(), tenantID, mediaID, "RETAINED", "ACTIVE_BASELINE", now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO media.video_policies(tenant_id,current_retention_days,superseded_baseline_retention_days,version,updated_at) VALUES(?,?,?,?,?)`, tenantID, 30, 365, 1, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO origins.origin_versions(id,tenant_id,status,superseded_at,invalidated_at,created_at,activated_at) VALUES(?,?,?,?,?,?,?)`, versionID, tenantID, "INVALIDATED", supersededAt, invalidatedAt, supersededAt.Add(-time.Hour), supersededAt.Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO origins.origin_evidence(id,tenant_id,origin_version_id,media_id) VALUES(?,?,?,?)`, identity.NewID(), tenantID, versionID, mediaID).Error; err != nil {
		t.Fatal(err)
	}
	if err := (BaselineTransition{Now: func() time.Time { return now }}).Transition(context.Background(), db, tenantID, []identity.ID{mediaID}); err != nil {
		t.Fatal(err)
	}
	var source database.VideoSource
	if err := db.Where("tenant_id=? AND media_id=?", tenantID, mediaID).First(&source).Error; err != nil {
		t.Fatal(err)
	}
	want := supersededAt.AddDate(0, 0, 365)
	if source.SourceExpiryAt == nil || !source.SourceExpiryAt.Equal(want) {
		t.Fatalf("issue-003 invalidation restarted the superseded window: expiry=%v, want %s", source.SourceExpiryAt, want)
	}
}

func retentionTransitionDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"media", "origins"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, ddl := range []string{
		`CREATE TABLE media.video_sources(id text primary key,tenant_id text,media_id text,status text,retention_anchor text,source_expiry_at datetime,retention_floor_at datetime,created_at datetime,updated_at datetime)`,
		`CREATE TABLE media.video_policies(tenant_id text primary key,current_retention_days integer,superseded_baseline_retention_days integer,version integer,updated_at datetime)`,
		`CREATE TABLE media.video_preparation_jobs(id text primary key,tenant_id text,media_id text)`,
		`CREATE TABLE origins.origin_versions(id text primary key,tenant_id text,status text,superseded_at datetime,invalidated_at datetime,video_preparation_id text,created_at datetime,activated_at datetime)`,
		`CREATE TABLE origins.origin_evidence(id text primary key,tenant_id text,origin_version_id text,media_id text)`,
		`CREATE TABLE origins.inventory_item_evidence(id text,tenant_id text,item_id text,media_id text)`,
		`CREATE TABLE origins.inventory_environments(id text,tenant_id text,decision text)`,
		`CREATE TABLE origins.inventory_items(id text,tenant_id text,origin_version_id text,environment_id text,decision text,merged_into_id text)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
