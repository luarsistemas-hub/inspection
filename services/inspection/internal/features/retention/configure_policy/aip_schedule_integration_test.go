//go:build integration

package configure_policy

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	scheduleexpiry "inspection/services/inspection/internal/features/retention/schedule_expiry"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func dueEvents(t *testing.T, h interface {
	WithinTenant(context.Context, identity.ID, func(*gorm.DB) error) error
}, tenantID identity.ID) map[identity.ID]events.RawEnvelope {
	t.Helper()
	found := map[identity.ID]events.RawEnvelope{}
	if err := h.WithinTenant(context.Background(), tenantID, func(tx *gorm.DB) error {
		var rows []database.OutboxIntent
		if err := tx.Where("type='retention.purge_due.v1'").Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			var envelope events.RawEnvelope
			if err := json.Unmarshal(row.Payload, &envelope); err != nil {
				return err
			}
			var payload struct {
				InspectionID identity.ID `json:"inspectionId"`
			}
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				return err
			}
			found[payload.InspectionID] = envelope
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return found
}

// The scheduler runs on the non-privileged runtime role: discovery must use
// the tenant transaction, emit once per inspection, honor the effective
// policy, skip held or purged inspections and stay inside its tenant.
func TestAIP_ScheduleExpiryQueuesDueEventsUnderRuntimeRLS(t *testing.T) {
	h := aipHarness(t)
	ctx := context.Background()
	tenantA, tenantB := closedFixture(t, h), closedFixture(t, h)
	now := time.Now().UTC().Truncate(time.Microsecond)
	old := now.Add(-6 * 365 * day)
	var due []aipInspection
	for i := 0; i < 5; i++ {
		due = append(due, newClosedInspection(t, h, tenantA, old))
	}
	recent := newClosedInspection(t, h, tenantA, now.Add(-2*365*day))
	held := newClosedInspection(t, h, tenantA, old)
	foreign := newClosedInspection(t, h, tenantB, old)
	if err := h.WithinTenant(ctx, tenantA.TenantID, func(tx *gorm.DB) error {
		id := held.ID
		return tx.Create(&database.LegalHold{ID: identity.NewID(), TenantID: tenantA.TenantID, InspectionID: &id, Reason: "aip hold", Active: true, CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	// The fixture inspection of tenant A is still being processed.
	if err := h.AdminDB.Exec("UPDATE inspections.inspections SET status='IN_PROGRESS', updated_at=? WHERE tenant_id=? AND id=?", old, tenantA.TenantID, tenantA.InspectionID).Error; err != nil {
		t.Fatal(err)
	}

	emitted, err := scheduleexpiry.EmitDue(ctx, h.DB, tenantA.TenantID, now, 2)
	if err != nil || emitted != 2 {
		t.Fatalf("first bounded pass emitted=%d err=%v, want 2", emitted, err)
	}
	for _, want := range []int{2, 1, 0} {
		if emitted, err = scheduleexpiry.EmitDue(ctx, h.DB, tenantA.TenantID, now, 2); err != nil || emitted != want {
			t.Fatalf("next pass emitted=%d err=%v, want %d", emitted, err, want)
		}
	}
	queued := dueEvents(t, h, tenantA.TenantID)
	if len(queued) != len(due) {
		t.Fatalf("queued %d due events, want one per due inspection (%d): %v", len(queued), len(due), queued)
	}
	for _, item := range due {
		envelope, ok := queued[item.ID]
		if !ok || envelope.ID != scheduleexpiry.EventID(tenantA.TenantID, item.ID, now) || envelope.TenantID != tenantA.TenantID {
			t.Fatalf("inspection %s has no deterministic purge_due event: %+v", item.ID, envelope)
		}
	}
	for label, item := range map[string]aipInspection{"recent": recent, "held": held} {
		if _, ok := queued[item.ID]; ok {
			t.Fatalf("%s inspection was queued for purge", label)
		}
	}
	if _, ok := queued[tenantA.InspectionID]; ok {
		t.Fatal("an inspection still in progress was queued for purge")
	}
	if len(dueEvents(t, h, tenantB.TenantID)) != 0 {
		t.Fatal("tenant A pass queued events for tenant B")
	}
	// The queued events drive the real handler to a complete purge.
	for _, item := range due {
		if err := handleDue(t, h, tenantA.TenantID, item.ID); err != nil {
			t.Fatalf("queued event did not purge: %v", err)
		}
		wantFullyPurged(t, "scheduled purge", presenceOf(t, h, tenantA.TenantID, item))
	}
	if emitted, err = scheduleexpiry.EmitDue(ctx, h.DB, tenantA.TenantID, now, 10); err != nil || emitted != 0 {
		t.Fatalf("pass after the purges emitted=%d err=%v, want 0", emitted, err)
	}
	wantFullyRetained(t, "tenant B", presenceOf(t, h, tenantB.TenantID, foreign))
	wantFullyRetained(t, "held", presenceOf(t, h, tenantA.TenantID, held))
}

func TestAIP_ScheduleExpiryFollowsTheEffectivePolicy(t *testing.T) {
	h := aipHarness(t)
	ctx := context.Background()
	fixture := closedFixture(t, h)
	now := time.Now().UTC().Truncate(time.Microsecond)
	twoYears := newClosedInspection(t, h, fixture, now.Add(-2*365*day))
	if err := h.AdminDB.Exec("UPDATE inspections.inspections SET status='IN_PROGRESS' WHERE tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Error; err != nil {
		t.Fatal(err)
	}
	if emitted, err := scheduleexpiry.EmitDue(ctx, h.DB, fixture.TenantID, now, 10); err != nil || emitted != 0 {
		t.Fatalf("default policy emitted=%d err=%v, want 0 for a two-year-old inspection", emitted, err)
	}
	if _, err := savePolicy(t, h, fixture.TenantID, 365, 365, 0); err != nil {
		t.Fatal(err)
	}
	if emitted, err := scheduleexpiry.EmitDue(ctx, h.DB, fixture.TenantID, now, 10); err != nil || emitted != 1 {
		t.Fatalf("one-year policy emitted=%d err=%v, want 1", emitted, err)
	}
	if _, ok := dueEvents(t, h, fixture.TenantID)[twoYears.ID]; !ok {
		t.Fatal("the two-year-old inspection was not queued under the one-year policy")
	}
}
