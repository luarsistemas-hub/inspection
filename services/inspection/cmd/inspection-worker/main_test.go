package main

import (
	"errors"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestResolveClassifiedVideoPlanUsesEventGeneration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS analysis").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE analysis.video_plans (id text PRIMARY KEY, tenant_id text NOT NULL, inspection_id text NOT NULL, status text NOT NULL, generation integer NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	tenantID, inspectionID := identity.NewID(), identity.NewID()
	older := database.VideoPlan{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, Status: "SEALED", Generation: 1}
	newer := database.VideoPlan{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, Status: "SEALED", Generation: 2}
	for _, plan := range []database.VideoPlan{older, newer} {
		if err := db.Exec(`INSERT INTO analysis.video_plans (id,tenant_id,inspection_id,status,generation) VALUES (?,?,?,?,?)`, plan.ID, tenantID, inspectionID, plan.Status, plan.Generation).Error; err != nil {
			t.Fatal(err)
		}
	}
	plan, err := resolveClassifiedVideoPlan(db, tenantID, inspectionID, &older.ID)
	if err != nil {
		t.Fatalf("resolve event-pinned G1: %v", err)
	}
	if plan.ID != older.ID {
		t.Fatalf("resolved plan %s, want event plan %s", plan.ID, older.ID)
	}
	if _, err := resolveClassifiedVideoPlan(db, tenantID, inspectionID, nil); !errors.Is(err, messaging.ErrPermanent) {
		t.Fatalf("missing videoPlanId error = %v, want permanent rejection", err)
	}
}
