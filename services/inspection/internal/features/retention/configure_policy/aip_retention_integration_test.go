//go:build integration

package configure_policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/features/retention/process_deletion_requests"
	purgedata "inspection/services/inspection/internal/features/retention/purge_data"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

const day = 24 * time.Hour

func closedFixture(t *testing.T, h *harness.Harness) harness.Task06Fixture {
	t.Helper()
	fixture, err := h.SeedTask06Fixture(context.Background(), harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

// handleDue runs the production retention.purge_due.v1 handler exactly as the
// worker does: on the runtime role inside the tenant transaction.
func handleDue(t *testing.T, h *harness.Harness, tenantID, inspectionID identity.ID) error {
	t.Helper()
	ctx := context.Background()
	return h.WithinTenant(ctx, tenantID, func(tx *gorm.DB) error {
		return purgedata.HandleDueEvent(ctx, tx, h.Store, dueEnvelope(t, tenantID, inspectionID))
	})
}

func savePolicy(t *testing.T, h *harness.Harness, tenantID identity.ID, evidence, operational, security int) (database.RetentionPolicy, error) {
	t.Helper()
	ctx := context.Background()
	var row database.RetentionPolicy
	err := h.WithinTenant(ctx, tenantID, func(tx *gorm.DB) error {
		saved, err := Save(ctx, tx, Input{TenantID: tenantID, EvidenceDays: evidence, OperationalDays: operational, SecurityDays: security})
		row = saved
		return err
	})
	return row, err
}

func countPolicies(t *testing.T, h *harness.Harness, tenantID identity.ID) int64 {
	t.Helper()
	var count int64
	if err := h.AdminDB.Model(&database.RetentionPolicy{}).Where("tenant_id=?", tenantID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

// IT-301: a negative, zero, malformed or disallowed period is rejected before
// any write, and the boundary values stay accepted.
func TestAIP_IT301InvalidRetentionPeriodsAreRejectedWithoutWrites(t *testing.T) {
	h := aipHarness(t)
	fixture := closedFixture(t, h)
	cases := []struct {
		name                            string
		evidence, operational, security int
		field                           string
	}{
		{"zero evidence", 0, 365, 0, "evidenceDays"},
		{"negative evidence", -5, 365, 0, "evidenceDays"},
		{"evidence above the ceiling", MaxDays + 1, 365, 0, "evidenceDays"},
		{"duration overflow", 200000, 365, 0, "evidenceDays"},
		{"zero operational", 1825, 0, 0, "operationalDays"},
		{"negative operational", 1825, -1, 0, "operationalDays"},
		{"operational above the ceiling", 1825, MaxDays + 1, 0, "operationalDays"},
		{"negative security", 1825, 365, -1, "securityDays"},
		{"security above the ceiling", 1825, 365, MaxDays + 1, "securityDays"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := savePolicy(t, h, fixture.TenantID, tc.evidence, tc.operational, tc.security)
			if !apperror.Is(err, apperror.InvalidInput) {
				t.Fatalf("Save(%d,%d,%d) error = %v, want INVALID_INPUT", tc.evidence, tc.operational, tc.security, err)
			}
			if _, field, message := apperror.Public(err); field != tc.field || !strings.Contains(message, "dias") {
				t.Fatalf("public error field=%q message=%q, want field %q and a Portuguese explanation", field, message, tc.field)
			}
		})
	}
	if count := countPolicies(t, h, fixture.TenantID); count != 0 {
		t.Fatalf("%d policy rows were written by rejected saves", count)
	}
	if _, err := Save(context.Background(), h.AdminDB, Input{EvidenceDays: 1825, OperationalDays: 365}); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("Save without a tenant error = %v, want INVALID_INPUT", err)
	}
	for _, boundary := range []struct{ evidence, operational, security int }{{MinDays, MinDays, 0}, {MaxDays, MaxDays, MaxDays}} {
		if _, err := savePolicy(t, h, fixture.TenantID, boundary.evidence, boundary.operational, boundary.security); err != nil {
			t.Fatalf("boundary policy %+v rejected: %v", boundary, err)
		}
	}
}

// IT-302: a tenant without a custom policy is governed by the platform
// defaults (five years for evidence, one year operational) and nothing is
// written to materialize them.
func TestAIP_IT302PlatformDefaultsApplyWithoutTenantPolicy(t *testing.T) {
	h := aipHarness(t)
	fixture := closedFixture(t, h)
	now := time.Now().UTC()
	justShort := newClosedInspection(t, h, fixture, now.Add(-5*365*day+day))
	justDue := newClosedInspection(t, h, fixture, now.Add(-5*365*day-day))
	closeBaseInspection(t, h, fixture, now.Add(-365*day))

	if err := handleDue(t, h, fixture.TenantID, justShort.ID); !errors.Is(err, gorm.ErrInvalidData) {
		t.Fatalf("inspection one day short of five years: err=%v, want gorm.ErrInvalidData (not yet eligible)", err)
	}
	wantFullyRetained(t, "one day short of the five-year default", presenceOf(t, h, fixture.TenantID, justShort))
	if err := handleDue(t, h, fixture.TenantID, justDue.ID); err != nil {
		t.Fatalf("inspection past the five-year default was not purged: %v", err)
	}
	wantFullyPurged(t, "five-year default", presenceOf(t, h, fixture.TenantID, justDue))
	var base database.Inspection
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Take(&base).Error; err != nil {
		t.Fatalf("one-year-old inspection must remain under the evidence default: %v", err)
	}
	if count := countPolicies(t, h, fixture.TenantID); count != 0 {
		t.Fatalf("defaults were materialized as %d tenant policy rows", count)
	}
}

// IT-303: a deletion backlog larger than one batch continues in bounded
// batches, each pass reports exactly what it processed, and unprocessed or
// blocked requests stay visible with their status instead of vanishing.
func TestAIP_IT303DeletionBatchesAreBoundedAndNeverSilentlyPartial(t *testing.T) {
	h := aipHarness(t)
	fixture := closedFixture(t, h)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	closedLongAgo := now.Add(-6 * 365 * day)
	const eligible = 7
	var due []aipInspection
	for i := 0; i < eligible; i++ {
		due = append(due, newClosedInspection(t, h, fixture, closedLongAgo))
	}
	held := newClosedInspection(t, h, fixture, closedLongAgo)
	early := newClosedInspection(t, h, fixture, now.Add(-365*day))
	all := append(append([]aipInspection{}, due...), held, early)
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.LegalHold{ID: identity.NewID(), TenantID: fixture.TenantID, InspectionID: &held.ID, Reason: "aip hold", Active: true, CreatedAt: now}).Error; err != nil {
			return err
		}
		for i, item := range all {
			id := item.ID
			if err := tx.Create(&database.DeletionRequest{ID: identity.NewID(), TenantID: fixture.TenantID, InspectionID: &id, Status: "REQUESTED", RequestedAt: now.Add(time.Duration(i) * time.Second)}).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	executor, err := process_deletion_requests.Setup(process_deletion_requests.Dependencies{DB: h.DB, Store: h.Store})
	if err != nil {
		t.Fatal(err)
	}
	statusCounts := func() map[string]int64 {
		var rows []struct {
			Status string
			Count  int64
		}
		if err := h.AdminDB.Model(&database.DeletionRequest{}).Select("status, count(*) AS count").Where("tenant_id=?", fixture.TenantID).Group("status").Scan(&rows).Error; err != nil {
			t.Fatal(err)
		}
		counts := map[string]int64{}
		for _, row := range rows {
			counts[row.Status] = row.Count
		}
		return counts
	}
	purged := func() int {
		count := 0
		for _, item := range due {
			if presenceOf(t, h, fixture.TenantID, item).PurgeRuns == 1 {
				count++
			}
		}
		return count
	}

	processed, err := executor.RunDue(ctx, fixture.TenantID, 3)
	if err != nil || processed != 3 {
		t.Fatalf("first batch processed=%d err=%v, want exactly 3", processed, err)
	}
	if got := statusCounts(); got["REQUESTED"] != int64(len(all)-3) || purged() != 3 {
		t.Fatalf("after one bounded batch: statuses=%v purged=%d, want %d still REQUESTED and 3 purged", got, purged(), len(all)-3)
	}
	if processed, err = executor.RunDue(ctx, fixture.TenantID, 3); err != nil || processed != 3 || purged() != 6 {
		t.Fatalf("second batch processed=%d purged=%d err=%v, want 3 and 6", processed, purged(), err)
	}
	if processed, err = executor.RunDue(ctx, fixture.TenantID, 3); err != nil || processed != 3 || purged() != eligible {
		t.Fatalf("third batch processed=%d purged=%d err=%v, want 3 and %d", processed, purged(), err, eligible)
	}
	got := statusCounts()
	if got["BLOCKED"] != 2 || got["REQUESTED"] != 0 {
		t.Fatalf("after the backlog: statuses=%v, want the held and the not-yet-due requests BLOCKED and visible", got)
	}
	var blocked []database.DeletionRequest
	if err := h.AdminDB.Where("tenant_id=? AND status='BLOCKED'", fixture.TenantID).Find(&blocked).Error; err != nil {
		t.Fatal(err)
	}
	reasons := map[string]bool{}
	for _, request := range blocked {
		reasons[request.Reason] = true
		if request.ProcessedAt == nil {
			t.Fatalf("blocked request %s has no processed_at", request.ID)
		}
	}
	if !reasons["Retenção legal impede a exclusão da vistoria."] || !reasons["O prazo de retenção ainda não terminou."] {
		t.Fatalf("blocked requests do not explain the restriction: %v", reasons)
	}
	wantFullyRetained(t, "legal hold", presenceOf(t, h, fixture.TenantID, held))
	wantFullyRetained(t, "retention not elapsed", presenceOf(t, h, fixture.TenantID, early))
	for index, item := range due {
		wantFullyPurged(t, fmt.Sprintf("batched inspection %d", index+1), presenceOf(t, h, fixture.TenantID, item))
	}
	// Blocked requests are re-evaluated every pass and never counted as done.
	if processed, err = executor.RunDue(ctx, fixture.TenantID, 10); err != nil || processed != 2 {
		t.Fatalf("blocked requests were not re-evaluated: processed=%d err=%v", processed, err)
	}
	if got := statusCounts(); got["BLOCKED"] != 2 {
		t.Fatalf("blocked requests changed state without a decision: %v", got)
	}
}

// IT-305: policy changes produce strictly increasing versions (even when
// saved concurrently), the latest version is the one effective policy at each
// evaluation, and the purge audit explains which clock applied.
func TestAIP_IT305PolicyChangesLeaveOneDeterminableEffectivePolicy(t *testing.T) {
	h := aipHarness(t)
	fixture := closedFixture(t, h)
	now := time.Now().UTC().Truncate(time.Microsecond)
	closedFourYears := now.Add(-4 * 365 * day)
	closedTwoYears := now.Add(-2 * 365 * day)
	closeBaseInspection(t, h, fixture, closedFourYears)
	later := newClosedInspection(t, h, fixture, closedTwoYears)

	if err := handleDue(t, h, fixture.TenantID, fixture.InspectionID); !errors.Is(err, gorm.ErrInvalidData) {
		t.Fatalf("four-year-old inspection under the five-year default: err=%v, want not eligible", err)
	}
	v1, err := savePolicy(t, h, fixture.TenantID, 3*365, 365, 0)
	if err != nil || v1.Version != 1 {
		t.Fatalf("first policy version=%d err=%v, want 1", v1.Version, err)
	}
	if err := handleDue(t, h, fixture.TenantID, fixture.InspectionID); err != nil {
		t.Fatalf("policy v1 (three years) did not make the four-year-old inspection eligible: %v", err)
	}
	if err := handleDue(t, h, fixture.TenantID, later.ID); !errors.Is(err, gorm.ErrInvalidData) {
		t.Fatalf("two-year-old inspection under policy v1: err=%v, want not eligible", err)
	}
	var audit database.AuditEvent
	if err := h.AdminDB.Where("tenant_id=? AND action='retention.purged'", fixture.TenantID).Take(&audit).Error; err != nil {
		t.Fatalf("purge audit event missing: %v", err)
	}
	wantDue := closedFourYears.Add(3 * 365 * day).UTC().Format(time.RFC3339)
	if !strings.Contains(audit.Reason, "class=EVIDENCE_REPORTS") || !strings.Contains(audit.Reason, "start="+closedFourYears.UTC().Format(time.RFC3339)) || !strings.Contains(audit.Reason, "due="+wantDue) {
		t.Fatalf("purge audit does not explain the applied clock: %q", audit.Reason)
	}
	if strings.Contains(audit.Reason, fixture.InspectionID.String()) || audit.TargetID != "purged" {
		t.Fatalf("purge audit identifies deleted data: target=%q reason=%q", audit.TargetID, audit.Reason)
	}

	v2, err := savePolicy(t, h, fixture.TenantID, 10*365, 365, 0)
	if err != nil || v2.Version != 2 {
		t.Fatalf("second policy version=%d err=%v, want 2", v2.Version, err)
	}
	if err := handleDue(t, h, fixture.TenantID, later.ID); !errors.Is(err, gorm.ErrInvalidData) {
		t.Fatalf("lengthening the policy must keep the two-year-old inspection: err=%v", err)
	}
	wantFullyRetained(t, "inspection retained under policy v2", presenceOf(t, h, fixture.TenantID, later))

	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, saveErr := savePolicy(t, h, fixture.TenantID, 1000+i, 365, 0)
			errs <- saveErr
		}(i)
	}
	wg.Wait()
	close(errs)
	for saveErr := range errs {
		if saveErr != nil {
			t.Fatalf("concurrent policy save failed: %v", saveErr)
		}
	}
	var versions []int64
	if err := h.AdminDB.Model(&database.RetentionPolicy{}).Where("tenant_id=?", fixture.TenantID).Order("version ASC").Pluck("version", &versions).Error; err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2+writers {
		t.Fatalf("policy versions=%v, want %d rows", versions, 2+writers)
	}
	for index, version := range versions {
		if version != int64(index+1) {
			t.Fatalf("policy versions=%v are not unique and consecutive", versions)
		}
	}
}

// IT-306: a purge interrupted after partial cleanup commits nothing in the
// database, leaves the request visible, and the retry completes the same scope.
func TestAIP_IT306PartialCleanupRetryCompletesSameScope(t *testing.T) {
	h := aipHarness(t)
	fixture := closedFixture(t, h)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	item := newClosedInspection(t, h, fixture, now.Add(-6*365*day))
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		id := item.ID
		return tx.Create(&database.DeletionRequest{ID: identity.NewID(), TenantID: fixture.TenantID, InspectionID: &id, Status: "REQUESTED", RequestedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	flaky := newFlakyStore(h.Store.Client, item.DerivativeKey, 1)
	failing, err := process_deletion_requests.Setup(process_deletion_requests.Dependencies{DB: h.DB, Store: objectstore.Store{Bucket: h.Store.Bucket, Client: flaky}})
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := failing.RunDue(ctx, fixture.TenantID, 5); err == nil || processed != 0 {
		t.Fatalf("interrupted purge processed=%d err=%v, want a surfaced failure", processed, err)
	}
	partial := presenceOf(t, h, fixture.TenantID, item)
	if partial.Inspections != 1 || partial.Media != 1 || partial.Derivatives != 1 || partial.Snapshots != 1 || partial.Artifacts != 1 || partial.PurgeRuns != 0 {
		t.Fatalf("interrupted purge committed database changes: %+v", partial)
	}
	var request database.DeletionRequest
	if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", fixture.TenantID, item.ID).Take(&request).Error; err != nil || request.Status != "REQUESTED" {
		t.Fatalf("request after the interruption: status=%q err=%v, want REQUESTED and visible", request.Status, err)
	}
	healthy, err := process_deletion_requests.Setup(process_deletion_requests.Dependencies{DB: h.DB, Store: h.Store})
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := healthy.RunDue(ctx, fixture.TenantID, 5); err != nil || processed != 1 {
		t.Fatalf("retry processed=%d err=%v, want 1", processed, err)
	}
	wantFullyPurged(t, "retry after partial cleanup", presenceOf(t, h, fixture.TenantID, item))
}

// IT-307: repeated processing keeps the data absent, never fails the repeated
// event, and the audit trail and events record one purge only.
func TestAIP_IT307RepeatedPurgeKeepsDataAbsentAndAuditsOnce(t *testing.T) {
	h := aipHarness(t)
	fixture := closedFixture(t, h)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	item := newClosedInspection(t, h, fixture, now.Add(-6*365*day))
	envelope := dueEnvelope(t, fixture.TenantID, item.ID)
	handle := func(tx *gorm.DB) error { return purgedata.HandleDueEvent(ctx, tx, h.Store, envelope) }
	if err := h.WithinTenant(ctx, fixture.TenantID, handle); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	first := presenceOf(t, h, fixture.TenantID, item)
	wantFullyPurged(t, "first delivery", first)
	var firstRun database.PurgeRun
	if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", fixture.TenantID, item.ID).Take(&firstRun).Error; err != nil {
		t.Fatal(err)
	}
	for label, event := range map[string]func(*gorm.DB) error{
		"same event again": handle,
		"new event for the same inspection": func(tx *gorm.DB) error {
			return purgedata.HandleDueEvent(ctx, tx, h.Store, dueEnvelope(t, fixture.TenantID, item.ID))
		},
	} {
		if err := h.WithinTenant(ctx, fixture.TenantID, event); err != nil {
			t.Fatalf("%s must be a no-op, got %v", label, err)
		}
	}
	// Direct reconciliation of an already completed purge returns the stored run.
	clock := mustClock(t, now.Add(-6*365*day))
	repeated, err := purgedata.PurgeWithStore(ctx, h.AdminDB, h.Store, fixture.TenantID, item.ID, clock)
	if err != nil || repeated.ID != firstRun.ID {
		t.Fatalf("repeated PurgeWithStore returned run %s (err=%v), want the original %s", repeated.ID, err, firstRun.ID)
	}
	wantFullyPurged(t, "after repeats", presenceOf(t, h, fixture.TenantID, item))
	var audits, outbox int64
	if err := h.AdminDB.Model(&database.AuditEvent{}).Where("tenant_id=? AND action='retention.purged'", fixture.TenantID).Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='retention.purged.v1'", fixture.TenantID).Count(&outbox).Error; err != nil {
		t.Fatal(err)
	}
	if audits != 1 || outbox != 1 {
		t.Fatalf("repeated purge recorded audit=%d purged-events=%d, want exactly one of each", audits, outbox)
	}
}

// IT-308: the retention start is the authoritative closure instant, not the
// creation date, and it is recalculated whenever the closure is corrected.
func TestAIP_IT308LateClosureRecalculatesRetentionStart(t *testing.T) {
	h := aipHarness(t)
	fixture := closedFixture(t, h)
	now := time.Now().UTC().Truncate(time.Microsecond)
	item := newClosedInspection(t, h, fixture, now)
	// Created seven years ago, but the closure was only recorded now.
	if err := h.AdminDB.Exec("UPDATE inspections.inspections SET created_at=? WHERE tenant_id=? AND id=?", now.Add(-7*365*day), fixture.TenantID, item.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := handleDue(t, h, fixture.TenantID, item.ID); !errors.Is(err, gorm.ErrInvalidData) {
		t.Fatalf("late-closed inspection older than five years purged from its creation date: err=%v", err)
	}
	wantFullyRetained(t, "closure recorded today", presenceOf(t, h, fixture.TenantID, item))

	setClosure(t, h, fixture.TenantID, item.ID, now.Add(-4*365*day), "COMPLETED")
	if err := handleDue(t, h, fixture.TenantID, item.ID); !errors.Is(err, gorm.ErrInvalidData) {
		t.Fatalf("closure four years ago must not yet be eligible: err=%v", err)
	}
	wantFullyRetained(t, "closure four years ago", presenceOf(t, h, fixture.TenantID, item))

	correctedClosure := now.Add(-5*365*day - day)
	setClosure(t, h, fixture.TenantID, item.ID, correctedClosure, "COMPLETED")
	if err := handleDue(t, h, fixture.TenantID, item.ID); err != nil {
		t.Fatalf("recalculated closure five years ago was not purged: %v", err)
	}
	wantFullyPurged(t, "authoritative closure", presenceOf(t, h, fixture.TenantID, item))
	var audit database.AuditEvent
	if err := h.AdminDB.Where("tenant_id=? AND action='retention.purged'", fixture.TenantID).Take(&audit).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(audit.Reason, "start="+correctedClosure.UTC().Format(time.RFC3339)) {
		t.Fatalf("purge audit does not show the authoritative closure instant: %q", audit.Reason)
	}
}

// IT-309: reopening recalculates future eligibility; stale due events cannot
// purge a reopened inspection, and data purged earlier is never restored.
func TestAIP_IT309ReopenRecalculatesEligibilityWithoutRestoringDeletedData(t *testing.T) {
	h := aipHarness(t)
	fixture := closedFixture(t, h)
	now := time.Now().UTC().Truncate(time.Microsecond)
	expired := newClosedInspection(t, h, fixture, now.Add(-6*365*day))
	reopened := newClosedInspection(t, h, fixture, now.Add(-6*365*day))

	if err := handleDue(t, h, fixture.TenantID, expired.ID); err != nil {
		t.Fatalf("expired inspection was not purged: %v", err)
	}
	wantFullyPurged(t, "lawfully expired before the reopening", presenceOf(t, h, fixture.TenantID, expired))

	// The relationship reopens before the due event for the second inspection
	// is processed: the stale event must not delete the reopened work.
	setClosure(t, h, fixture.TenantID, reopened.ID, now, "SUBMITTED")
	if err := handleDue(t, h, fixture.TenantID, reopened.ID); !errors.Is(err, gorm.ErrInvalidData) {
		t.Fatalf("stale due event purged a reopened inspection: err=%v", err)
	}
	wantFullyRetained(t, "reopened", presenceOf(t, h, fixture.TenantID, reopened))
	// Reopening never restores data that was already lawfully deleted.
	if err := handleDue(t, h, fixture.TenantID, expired.ID); err != nil {
		t.Fatalf("event for the already purged inspection after the reopening: %v", err)
	}
	wantFullyPurged(t, "still absent after the reopening", presenceOf(t, h, fixture.TenantID, expired))

	// Closing again starts a new clock from the new closure instant.
	if _, err := savePolicy(t, h, fixture.TenantID, 1, 365, 0); err != nil {
		t.Fatal(err)
	}
	setClosure(t, h, fixture.TenantID, reopened.ID, now.Add(-2*day), "COMPLETED")
	if err := handleDue(t, h, fixture.TenantID, reopened.ID); err != nil {
		t.Fatalf("recalculated eligibility after the second closure: %v", err)
	}
	wantFullyPurged(t, "second closure", presenceOf(t, h, fixture.TenantID, reopened))
}

// IT-310: a large expiry stays tenant isolated, observable and complete
// across every data class.
func TestAIP_IT310MassExpiryIsTenantIsolatedObservableAndComplete(t *testing.T) {
	h := aipHarness(t)
	tenantA, tenantB := closedFixture(t, h), closedFixture(t, h)
	now := time.Now().UTC().Truncate(time.Microsecond)
	expiredAt := now.Add(-6 * 365 * day)
	const large = 12
	var tenantAItems []aipInspection
	for i := 0; i < large; i++ {
		tenantAItems = append(tenantAItems, newClosedInspection(t, h, tenantA, expiredAt))
	}
	foreignExpired := newClosedInspection(t, h, tenantB, expiredAt)
	foreignRecent := newClosedInspection(t, h, tenantB, now.Add(-365*day))

	// A due event of tenant A can never reach tenant B's data.
	err := handleDue(t, h, tenantA.TenantID, foreignExpired.ID)
	if !errors.Is(err, messaging.ErrPermanent) {
		t.Fatalf("cross-tenant due event err=%v, want a permanent failure", err)
	}
	wantFullyRetained(t, "tenant B after a tenant A event", presenceOf(t, h, tenantB.TenantID, foreignExpired))

	for _, item := range tenantAItems {
		if err := handleDue(t, h, tenantA.TenantID, item.ID); err != nil {
			t.Fatalf("mass expiry purge failed: %v", err)
		}
	}
	for index, item := range tenantAItems {
		wantFullyPurged(t, fmt.Sprintf("tenant A inspection %d", index+1), presenceOf(t, h, tenantA.TenantID, item))
	}
	wantFullyRetained(t, "tenant B expired but unscheduled", presenceOf(t, h, tenantB.TenantID, foreignExpired))
	wantFullyRetained(t, "tenant B recent", presenceOf(t, h, tenantB.TenantID, foreignRecent))
	counts := func(tenant identity.ID) (runs, audits, events int64) {
		for dest, query := range map[*int64]struct {
			model any
			where string
		}{
			&runs:   {&database.PurgeRun{}, "tenant_id=? AND status='COMPLETED'"},
			&audits: {&database.AuditEvent{}, "tenant_id=? AND action='retention.purged'"},
			&events: {&database.OutboxIntent{}, "tenant_id=? AND type='retention.purged.v1'"},
		} {
			if err := h.AdminDB.Model(query.model).Where(query.where, tenant).Count(dest).Error; err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	if runs, audits, events := counts(tenantA.TenantID); runs != large || audits != large || events != large {
		t.Fatalf("tenant A observability: runs=%d audits=%d events=%d, want %d each", runs, audits, events, large)
	}
	if runs, audits, events := counts(tenantB.TenantID); runs != 0 || audits != 0 || events != 0 {
		t.Fatalf("tenant B recorded purge activity it never requested: runs=%d audits=%d events=%d", runs, audits, events)
	}
}

func mustClock(t *testing.T, closure time.Time) retentioncore.Clock {
	t.Helper()
	clock, err := retentioncore.NewClock(retentioncore.EvidenceReports, closure, time.Time{}, retentioncore.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return clock
}

// IT-390: a purge honors an active legal hold, then (after the release)
// removes every personal association, blob and report of the inspection and
// keeps only a deidentified audit trail.
func TestAIP_IT390PurgeHonorsHoldThenDeletesEverythingAndKeepsDeidentifiedAudit(t *testing.T) {
	h := aipHarness(t)
	fixture := closedFixture(t, h)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	item := newClosedInspection(t, h, fixture, now.Add(-6*365*day))
	holdID := identity.NewID()
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		inspection := item.ID
		if err := tx.Create(&database.LegalHold{ID: holdID, TenantID: fixture.TenantID, InspectionID: &inspection, Reason: "aip hold", Active: true, CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: fixture.TenantID, ActorID: fixture.IdentityID, Action: "inspection.created", TargetType: "inspection", TargetID: item.ID.String(), Outcome: "COMPLETED", Reason: "criada para a vistoria " + item.ID.String(), CorrelationID: "aip-it390", OccurredAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}

	if err := handleDue(t, h, fixture.TenantID, item.ID); err != nil {
		t.Fatalf("a due event under a legal hold must be acknowledged without deleting: %v", err)
	}
	wantFullyRetained(t, "active legal hold", presenceOf(t, h, fixture.TenantID, item))
	if _, err := purgedata.PurgeWithStore(ctx, h.AdminDB, h.Store, fixture.TenantID, item.ID, mustClock(t, now.Add(-6*365*day))); err == nil {
		t.Fatal("a direct purge ignored the active legal hold")
	}
	wantFullyRetained(t, "active legal hold after a direct purge", presenceOf(t, h, fixture.TenantID, item))

	if err := h.AdminDB.Model(&database.LegalHold{}).Where("tenant_id=? AND id=?", fixture.TenantID, holdID).Updates(map[string]any{"active": false, "released_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := handleDue(t, h, fixture.TenantID, item.ID); err != nil {
		t.Fatalf("purge after the release: %v", err)
	}
	wantFullyPurged(t, "after the release", presenceOf(t, h, fixture.TenantID, item))

	var run database.PurgeRun
	if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", fixture.TenantID, item.ID).Take(&run).Error; err != nil {
		t.Fatal(err)
	}
	var manifest retentioncore.Manifest
	if err := json.Unmarshal(run.Manifest, &manifest); err != nil || !manifest.Complete() || run.Status != "COMPLETED" || run.CompletedAt == nil {
		t.Fatalf("purge manifest = %+v (err=%v) status=%s, want every data class completed", manifest, err, run.Status)
	}
	var audits []database.AuditEvent
	if err := h.AdminDB.Where("tenant_id=?", fixture.TenantID).Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	sawPurge := false
	for _, audit := range audits {
		if strings.Contains(audit.TargetID, item.ID.String()) || strings.Contains(audit.Reason, item.ID.String()) {
			t.Fatalf("audit event %s still identifies the purged inspection: target=%q reason=%q", audit.Action, audit.TargetID, audit.Reason)
		}
		if audit.Action == "inspection.created" && (audit.ActorID != (identity.ID{}) || audit.TargetID != "purged") {
			t.Fatalf("the earlier audit trail was not deidentified: %+v", audit)
		}
		if audit.Action == "retention.purged" {
			sawPurge = true
		}
	}
	if !sawPurge {
		t.Fatal("the purge left no deidentified completion audit")
	}
}
