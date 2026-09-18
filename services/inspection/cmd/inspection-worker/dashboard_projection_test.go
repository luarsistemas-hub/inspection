package main

import (
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDashboardProjectionUpsertPreservesIdentityOnReplay(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS dashboard").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.DashboardInspection{}); err != nil && !strings.Contains(err.Error(), "no such table: main.inspections") {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX dashboard.idx_dashboard_inspection ON inspections (tenant_id, inspection_id)").Error; err != nil {
		t.Fatal(err)
	}
	tenant, inspection, originalID := identity.NewID(), identity.NewID(), identity.NewID()
	first := database.DashboardInspection{ID: originalID, TenantID: tenant, InspectionID: inspection, AssetID: identity.NewID(), Classification: "NORMAL", Status: "PLANNED", Sequence: 1, UpdatedAt: time.Now()}
	if err := upsertDashboardInspection(db, first); err != nil {
		t.Fatal(err)
	}
	updated := first
	updated.ID = identity.NewID()
	updated.Classification, updated.Status, updated.Sequence = "ATTENTION", "COMPLETED", 2
	for range 2 {
		if err := upsertDashboardInspection(db, updated); err != nil {
			t.Fatal(err)
		}
	}
	var rows []database.DashboardInspection
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != originalID || rows[0].Classification != "ATTENTION" || rows[0].Status != "COMPLETED" || rows[0].Sequence != 2 {
		t.Fatalf("projection replay changed identity or failed to update: %+v", rows)
	}
}
