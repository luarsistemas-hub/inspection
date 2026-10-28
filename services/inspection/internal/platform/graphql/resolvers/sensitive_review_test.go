package resolvers

import (
	"context"
	"testing"

	"inspection/libs/identity"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAttentionMediaIDSelectsFailedSupplement(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, statement := range []string{
		"ATTACH DATABASE ':memory:' AS analysis",
		"ATTACH DATABASE ':memory:' AS media",
		"CREATE TABLE analysis.video_plans (id TEXT, tenant_id TEXT, inspection_id TEXT, status TEXT, created_at DATETIME, current_preparation_id TEXT)",
		"CREATE TABLE analysis.video_plan_preparations (tenant_id TEXT, plan_id TEXT, preparation_id TEXT, role TEXT, created_at DATETIME)",
		"CREATE TABLE media.video_preparation_jobs (id TEXT, tenant_id TEXT, media_id TEXT, status TEXT)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenant, inspection, plan := identity.NewID(), identity.NewID(), identity.NewID()
	mainPrep, supplementPrep := identity.NewID(), identity.NewID()
	mainMedia, supplementMedia := identity.NewID(), identity.NewID()
	if err := db.Exec("INSERT INTO analysis.video_plans VALUES (?,?,?,?,?,?)", plan, tenant, inspection, "BLOCKED", "2026-10-28T10:00:00Z", mainPrep).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		prep, media  identity.ID
		role, status string
	}{
		{mainPrep, mainMedia, "MAIN", "READY"},
		{supplementPrep, supplementMedia, "SUPPLEMENT", "FAILED"},
	} {
		if err := db.Exec("INSERT INTO analysis.video_plan_preparations VALUES (?,?,?,?,?)", tenant, plan, row.prep, row.role, "2026-10-28T10:00:00Z").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO media.video_preparation_jobs VALUES (?,?,?,?)", row.prep, tenant, row.media, row.status).Error; err != nil {
			t.Fatal(err)
		}
	}
	resolver := &queryResolver{&Resolver{DB: db}}
	if got := resolver.attentionMediaID(context.Background(), tenant, inspection); got == nil || *got != supplementMedia.String() {
		t.Fatalf("failed supplement media = %v, want %s", got, supplementMedia)
	}
	if err := db.Exec("UPDATE media.video_preparation_jobs SET status='READY' WHERE id=?", supplementPrep).Error; err != nil {
		t.Fatal(err)
	}
	if got := resolver.attentionMediaID(context.Background(), tenant, inspection); got == nil || *got != mainMedia.String() {
		t.Fatalf("fallback main media = %v, want %s", got, mainMedia)
	}
}
