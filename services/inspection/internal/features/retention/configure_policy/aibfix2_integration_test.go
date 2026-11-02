//go:build integration

package configure_policy

import (
	"context"
	"errors"
	"testing"
	"time"

	"inspection/libs/identity"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	processdeletions "inspection/services/inspection/internal/features/retention/process_deletion_requests"
	purgedata "inspection/services/inspection/internal/features/retention/purge_data"
	scheduleexpiry "inspection/services/inspection/internal/features/retention/schedule_expiry"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// AIBFIX2-M1: only a terminal inspection has a retention clock. An old
// PLANNED/IN_PROGRESS inspection never purges through a purge_due event or a
// deletion request, nor does an inspection of a closed project that was reopened
// into ANALYZING.
func TestAIBFIX2_M1NonTerminalInspectionsAreNeverPurged(t *testing.T) {
	h := aipHarness(t)
	ctx := context.Background()
	fixture := closedFixture(t, h)
	now := time.Now().UTC().Truncate(time.Microsecond)
	old := now.Add(-6 * 365 * day)
	executor, err := processdeletions.Setup(processdeletions.Dependencies{DB: h.DB, Store: h.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := savePolicy(t, h, fixture.TenantID, 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"PLANNED", "IN_PROGRESS", "ANALYZING"} {
		item := newClosedInspection(t, h, fixture, old)
		setClosure(t, h, fixture.TenantID, item.ID, old, status)
		for _, class := range []string{"EVIDENCE_REPORTS", "OPERATIONAL_LOCATION"} {
			err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
				return purgedata.HandleDueEvent(ctx, tx, h.Store, dueEnvelopeOfClass(t, fixture.TenantID, item.ID, class))
			})
			if !errors.Is(err, gorm.ErrInvalidData) {
				t.Fatalf("%s inspection, %s purge_due: err=%v, want not eligible", status, class, err)
			}
		}
		request := database.DeletionRequest{ID: identity.NewID(), TenantID: fixture.TenantID, InspectionID: &item.ID, Status: "REQUESTED", RequestedAt: now}
		if err := h.AdminDB.Create(&request).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := executor.RunDue(ctx, fixture.TenantID, 100); err != nil {
			t.Fatal(err)
		}
		var stored database.DeletionRequest
		if err := h.AdminDB.Where("id=?", request.ID).Take(&stored).Error; err != nil || stored.Status != "BLOCKED" {
			t.Fatalf("%s inspection: deletion request %+v err=%v, want BLOCKED", status, stored, err)
		}
		wantFullyRetained(t, status+" inspection", presenceOf(t, h, fixture.TenantID, item))
	}
	if emitted, err := scheduleexpiry.EmitDue(ctx, h.DB, fixture.TenantID, now, 100); err != nil || emitted != 0 {
		t.Fatalf("EmitDue for non-terminal inspections emitted=%d err=%v, want 0", emitted, err)
	}

	// A closed project reopened into ANALYZING work: its old inspection stays.
	reopened := newClosedInspection(t, h, fixture, old)
	attachToProject(t, h, fixture, reopened.ID)
	closedAt := old
	setProject(t, h, fixture, "CLOSED", &closedAt)
	setClosure(t, h, fixture.TenantID, reopened.ID, old, "ANALYZING")
	if err := h.AdminDB.Exec("UPDATE inspections.inspections SET project_id=?, stage_id=? WHERE tenant_id=? AND id=?", fixture.ProjectID, fixture.StageID, fixture.TenantID, reopened.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := handleDue(t, h, fixture.TenantID, reopened.ID); !errors.Is(err, gorm.ErrInvalidData) {
		t.Fatalf("project inspection back in ANALYZING: err=%v, want not eligible", err)
	}
	wantFullyRetained(t, "project inspection in ANALYZING", presenceOf(t, h, fixture.TenantID, reopened))
}

// AIBFIX2-M2: an inspection completed after its project closed counts from its
// own completion, never from the earlier project closure.
func TestAIBFIX2_M2InspectionCompletedAfterProjectClosureCountsFromItsOwnCompletion(t *testing.T) {
	h := aipHarness(t)
	ctx := context.Background()
	fixture := closedFixture(t, h)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := h.AdminDB.Exec("UPDATE inspections.inspections SET status='IN_PROGRESS', deadline_at=? WHERE tenant_id=? AND id=?", now.Add(30*day), fixture.TenantID, fixture.InspectionID).Error; err != nil {
		t.Fatal(err)
	}
	item := newClosedInspection(t, h, fixture, now.Add(-day))
	attachToProject(t, h, fixture, item.ID)
	sixYears := now.Add(-6 * 365 * day)
	setProject(t, h, fixture, "CLOSED", &sixYears)

	// The project closed six years ago but the inspection finished yesterday.
	if _, ok, err := closureOf(t, h, fixture.TenantID, item.ID); err != nil || !ok {
		t.Fatalf("closure ok=%v err=%v", ok, err)
	}
	if emitted, err := scheduleexpiry.EmitDue(ctx, h.DB, fixture.TenantID, now, 10); err != nil || emitted != 0 {
		t.Fatalf("late-completed inspection emitted=%d err=%v, want 0", emitted, err)
	}
	if discovered(t, h, now)[fixture.TenantID] {
		t.Fatal("discovery used the project closure instead of the inspection's own completion")
	}
	if err := handleDue(t, h, fixture.TenantID, item.ID); !errors.Is(err, gorm.ErrInvalidData) {
		t.Fatalf("late-completed inspection: err=%v, want not eligible", err)
	}
	wantFullyRetained(t, "late-completed", presenceOf(t, h, fixture.TenantID, item))

	// Once its own completion is six years old it is due.
	setClosure(t, h, fixture.TenantID, item.ID, sixYears, "COMPLETED")
	attachToProject(t, h, fixture, item.ID)
	closure, _, err := closureOf(t, h, fixture.TenantID, item.ID)
	if err != nil || !closure.Equal(sixYears) {
		t.Fatalf("closure = %s err=%v, want %s", closure, err, sixYears)
	}
	if err := handleDue(t, h, fixture.TenantID, item.ID); err != nil {
		t.Fatalf("inspection completed and project closed six years ago: %v", err)
	}
	wantFullyPurged(t, "both six years old", presenceOf(t, h, fixture.TenantID, item))
}

func closureOf(t *testing.T, h interface {
	WithinTenant(context.Context, identity.ID, func(*gorm.DB) error) error
}, tenantID, inspectionID identity.ID) (time.Time, bool, error) {
	t.Helper()
	var (
		closure time.Time
		ok      bool
	)
	err := h.WithinTenant(context.Background(), tenantID, func(tx *gorm.DB) error {
		var inner error
		closure, ok, inner = retentioncore.Closure(context.Background(), tx, tenantID, inspectionID)
		return inner
	})
	return closure, ok, err
}

// AIBFIX2-B1: the closure is read with the inspection locked FOR UPDATE and its
// project FOR SHARE, so a project Reopen cannot slip in between the closure
// check and the purge; the Reopen waits and then wins for the next pass.
func TestAIBFIX2_B1ReopenWaitsForTheRetentionLocks(t *testing.T) {
	h := aipHarness(t)
	ctx := context.Background()
	fixture := closedFixture(t, h)
	now := time.Now().UTC().Truncate(time.Microsecond)
	item := newClosedInspection(t, h, fixture, now.Add(-6*365*day))
	attachToProject(t, h, fixture, item.ID)
	sixYears := now.Add(-6 * 365 * day)
	setProject(t, h, fixture, "CLOSED", &sixYears)

	locked, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := retentioncore.LockForRetention(ctx, tx, fixture.TenantID, item.ID); err != nil {
				return err
			}
			closure, ok, err := retentioncore.Closure(ctx, tx, fixture.TenantID, item.ID)
			if err != nil || !ok || !closure.Equal(sixYears) {
				return errors.New("closure not visible under the lock")
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	reopen := func() error {
		return h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Exec("SET LOCAL lock_timeout = '400ms'").Error; err != nil {
				return err
			}
			return tx.Exec("UPDATE projects.projects SET status='ACTIVE', closed_at=NULL WHERE tenant_id=? AND id=?", fixture.TenantID, fixture.ProjectID).Error
		})
	}
	if err := reopen(); err == nil {
		t.Fatal("the project was reopened while the retention transaction held its lock")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := reopen(); err != nil {
		t.Fatalf("reopen after the retention transaction ended: %v", err)
	}
	if err := handleDue(t, h, fixture.TenantID, item.ID); !errors.Is(err, gorm.ErrInvalidData) {
		t.Fatalf("purge after the reopening: err=%v, want not eligible", err)
	}
	wantFullyRetained(t, "reopened", presenceOf(t, h, fixture.TenantID, item))
}
