package purge_data

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestAIP_UT_045LegalHoldBlocksPurgeAndCompletedPurgeIsIdempotent covers UT-045
// of autonomous-inspection-platform.
func TestAIP_UT_045LegalHoldBlocksPurgeAndCompletedPurgeIsIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS retention").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE retention.purge_runs (id blob primary key, tenant_id blob not null, inspection_id blob not null, status text not null, manifest blob not null, completed_at datetime, created_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	tenantID, inspectionID := identity.NewID(), identity.NewID()
	closure := time.Now().UTC().AddDate(-6, 0, 0)
	clock, err := retentioncore.NewClock(retentioncore.EvidenceReports, closure, time.Time{}, retentioncore.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}

	held := clock
	held.LegalHold = true
	if _, err := Purge(context.Background(), db, tenantID, inspectionID, held); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("legal hold did not block the purge: %v", err)
	}
	premature, err := retentioncore.NewClock(retentioncore.EvidenceReports, time.Now().UTC(), time.Time{}, retentioncore.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Purge(context.Background(), db, tenantID, inspectionID, premature); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("premature purge was not refused: %v", err)
	}
	var runs int64
	if err := db.Model(&database.PurgeRun{}).Where("tenant_id=?", tenantID).Count(&runs).Error; err != nil || runs != 0 {
		t.Fatalf("a refused purge left %d runs (err=%v)", runs, err)
	}

	completedAt := time.Now().UTC().Truncate(time.Second)
	manifest := json.RawMessage(`{"originals":true,"parts":true,"derivatives":true,"reports":true,"associations":true}`)
	completed := database.PurgeRun{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, Status: "COMPLETED", Manifest: manifest, CompletedAt: &completedAt, CreatedAt: completedAt}
	if err := db.Create(&completed).Error; err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		got, err := Purge(context.Background(), db, tenantID, inspectionID, clock)
		if err != nil || got.ID != completed.ID || got.Status != "COMPLETED" || string(got.Manifest) != string(manifest) {
			t.Fatalf("repeat %d of a completed purge = %+v (%v), want the stored run unchanged", attempt, got, err)
		}
	}
	if err := db.Model(&database.PurgeRun{}).Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Count(&runs).Error; err != nil || runs != 1 {
		t.Fatalf("repeated purge created duplicate runs: %d (err=%v)", runs, err)
	}
	// A hold applied after completion still refuses new work rather than replaying.
	if _, err := Purge(context.Background(), db, tenantID, inspectionID, held); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("legal hold must be honoured even after completion: %v", err)
	}
}
