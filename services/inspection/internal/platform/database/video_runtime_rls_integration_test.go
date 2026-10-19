//go:build integration

package database

import (
	"context"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestVideoApplicationRuntimeRoleEnforcesTenantRLSIT590(t *testing.T) {
	adminURL := os.Getenv("INSPECTION_TEST_DATABASE_URL")
	runtimeURL := os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL")
	if adminURL == "" || runtimeURL == "" {
		t.Skip("INSPECTION_TEST_DATABASE_URL and INSPECTION_TEST_RUNTIME_DATABASE_URL are required")
	}
	ctx := context.Background()
	admin, err := gorm.Open(postgres.Open(adminURL), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := (Migrator{DB: admin}).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	runtimeDB, err := gorm.Open(postgres.Open(runtimeURL), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var role string
	if err := runtimeDB.Raw("SELECT current_user").Scan(&role).Error; err != nil {
		t.Fatal(err)
	}
	if role != "inspection_runtime" {
		t.Fatalf("API, worker runtime, and scheduler DSN must use inspection_runtime, got %q", role)
	}
	tenants := []identity.ID{identity.NewID(), identity.NewID()}
	now := time.Now().UTC()
	for _, tenantID := range tenants {
		if err := admin.Create(&VideoPolicy{TenantID: tenantID, MaxDurationMinutes: 15, OriginRetentionDays: 30, CurrentRetentionDays: 5, Version: 1, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	runner := tenanttx.Runner{DB: runtimeDB}
	if err := runner.Within(ctx, tenants[0], func(tx *gorm.DB) error {
		var visible int64
		if err := tx.Model(&VideoPolicy{}).Where("tenant_id IN ?", tenants).Count(&visible).Error; err != nil {
			return err
		}
		if visible != 1 {
			t.Errorf("tenant A saw %d video policies, want only its own", visible)
		}
		updated := tx.Model(&VideoPolicy{}).Where("tenant_id=?", tenants[1]).Update("max_duration_minutes", 20)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 0 {
			t.Errorf("tenant A updated tenant B video policy")
		}
		own := tx.Model(&VideoPolicy{}).Where("tenant_id=?", tenants[0]).Update("max_duration_minutes", 12)
		if own.Error != nil || own.RowsAffected != 1 {
			t.Errorf("tenant A could not update its video policy: rows=%d err=%v", own.RowsAffected, own.Error)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	foreignPolicy := VideoPolicy{TenantID: tenants[1], MaxDurationMinutes: 10, OriginRetentionDays: 30, CurrentRetentionDays: 5, Version: 1, UpdatedAt: now}
	if err := runner.Within(ctx, tenants[0], func(tx *gorm.DB) error { return tx.Create(&foreignPolicy).Error }); err == nil {
		t.Fatal("tenant A inserted tenant B video policy")
	}
	var foreign VideoPolicy
	if err := admin.Where("tenant_id=?", tenants[1]).Take(&foreign).Error; err != nil || foreign.MaxDurationMinutes != 15 {
		t.Fatalf("cross-tenant policy changed: %+v err=%v", foreign, err)
	}
}
