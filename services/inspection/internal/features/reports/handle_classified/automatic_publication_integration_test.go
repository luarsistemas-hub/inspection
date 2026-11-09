//go:build integration

package handle_classified

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	notificationrequest "inspection/services/inspection/internal/features/notifications/request"
	generatesnapshot "inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

// automaticHandlerTimeout is deliberately short: a lock cycle between the
// consumer transaction and the snapshot transaction never resolves by itself.
const automaticHandlerTimeout = 20 * time.Second

// deliverClassified runs the REAL classified handler (real snapshot, real
// publication ledger) through the real inbox consumer for one event.
func deliverClassified(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, deps notificationrequest.Dependencies, eventID, runID identity.ID, classification string) error {
	return deliverClassifiedWith(ctx, h, f, deps, eventID, runID, classification, func(handler *Dependencies) {
		handler.CreateSnapshotInTx = func(ctx context.Context, tx *gorm.DB, input generatesnapshot.Input) (generatesnapshot.Result, error) {
			return generatesnapshot.CreateInTx(ctx, tx, input, time.Now())
		}
	})
}

// deliverClassifiedWith lets a test choose the snapshot adapter wired into the handler.
func deliverClassifiedWith(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, deps notificationrequest.Dependencies, eventID, runID identity.ID, classification string, wire func(*Dependencies)) error {
	service, err := notificationrequest.Setup(deps)
	if err != nil {
		return err
	}
	handlerDeps := Dependencies{Notifications: service}
	wire(&handlerDeps)
	handler, err := Setup(handlerDeps)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	body, err := json.Marshal(events.Envelope[events.InspectionClassifiedPayload]{
		ID: eventID, Type: "inspection.classified.v1", SchemaVersion: 1, OccurredAt: now,
		TenantID: f.TenantID, AggregateID: f.InspectionID, CorrelationID: "automatic-publication-" + eventID.String(),
		Payload: events.InspectionClassifiedPayload{InspectionID: f.InspectionID, ClassificationRunID: runID, Classification: classification, ReasonCodes: []string{}},
	})
	if err != nil {
		return err
	}
	consumer := messaging.Consumer{DB: h.DB, Registry: events.DefaultRegistry(), Name: "automatic-publication-integration", Handle: handler}
	_, err = consumer.Process(ctx, body, 0)
	return err
}

func seedAutomaticClassification(ctx context.Context, t *testing.T, h *harness.Harness, f harness.Task06Fixture, classification string) identity.ID {
	t.Helper()
	runID := identity.NewID()
	now := time.Now().UTC()
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.PublicationPolicy{ID: identity.NewID(), TenantID: f.TenantID, Mode: "AUTOMATIC", Version: 4, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.ClassificationRun{ID: runID, TenantID: f.TenantID, InspectionID: f.InspectionID, PromptSnapshotID: f.PromptSnapshotID, Classification: classification, ReasonCodes: json.RawMessage(`[]`), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	return runID
}

func seedManualClassification(ctx context.Context, t *testing.T, h *harness.Harness, f harness.Task06Fixture, classification string) identity.ID {
	t.Helper()
	runID := identity.NewID()
	now := time.Now().UTC()
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.PublicationPolicy{ID: identity.NewID(), TenantID: f.TenantID, Mode: "MANUAL", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.ClassificationRun{ID: runID, TenantID: f.TenantID, InspectionID: f.InspectionID, PromptSnapshotID: f.PromptSnapshotID, Classification: classification, ReasonCodes: json.RawMessage(`[]`), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	return runID
}

func assertNoHang(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("automatic finalization hung (lock cycle between consumer and snapshot transactions): %v", err)
	}
	if err != nil {
		t.Fatalf("classified handler failed: %v", err)
	}
}

func publicationCounts(t *testing.T, h *harness.Harness, f harness.Task06Fixture) (snapshots, active, total int64) {
	t.Helper()
	for query, target := range map[string]*int64{
		"SELECT count(*) FROM reports.report_snapshots WHERE tenant_id=? AND inspection_id=?":                           &snapshots,
		"SELECT count(*) FROM reports.report_publications WHERE tenant_id=? AND inspection_id=? AND status='PUBLISHED'": &active,
		"SELECT count(*) FROM reports.report_publications WHERE tenant_id=? AND inspection_id=?":                        &total,
	} {
		if err := h.AdminDB.Raw(query, f.TenantID, f.InspectionID).Scan(target).Error; err != nil {
			t.Fatal(err)
		}
	}
	return snapshots, active, total
}

// TestAutomaticFinalizationPublishesThroughRealHandlerWithoutHang drives the
// real handler under the AUTOMATIC policy: snapshot and publication commit in
// the consumer's transaction, with the inspection lock the handler holds.
func TestAutomaticFinalizationPublishesThroughRealHandlerWithoutHang(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, deps notificationrequest.Dependencies) {
		ctx, cancel := context.WithTimeout(ctx, automaticHandlerTimeout)
		defer cancel()
		runID := seedAutomaticClassification(ctx, t, h, f, "ATTENTION")
		assertNoHang(t, deliverClassified(ctx, h, f, deps, identity.NewID(), runID, "ATTENTION"))
		snapshots, active, total := publicationCounts(t, h, f)
		if snapshots != 1 || active != 1 || total != 1 {
			t.Fatalf("snapshots=%d active=%d publications=%d; want 1/1/1", snapshots, active, total)
		}
		var outbox int64
		if err := h.AdminDB.Raw("SELECT count(*) FROM messaging.outbox WHERE tenant_id=? AND type='report.published.v1'", f.TenantID).Scan(&outbox).Error; err != nil || outbox != 1 {
			t.Fatalf("report.published.v1 events=%d err=%v; want 1", outbox, err)
		}
	})
}

// TestAutomaticFinalizationConcurrentDeliveriesDoNotDeadlock delivers the same
// event twice and two distinct events of one classification at once; none may
// hang or fail with a deadlock, and one release stays active.
func TestIT013AutomaticFinalizationConcurrentDeliveriesDoNotDeadlock(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, deps notificationrequest.Dependencies) {
		ctx, cancel := context.WithTimeout(ctx, automaticHandlerTimeout)
		defer cancel()
		runID := seedAutomaticClassification(ctx, t, h, f, "ATTENTION")
		sameEvent := identity.NewID()
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		for _, eventID := range []identity.ID{sameEvent, sameEvent, identity.NewID(), identity.NewID()} {
			wg.Add(1)
			go func(eventID identity.ID) {
				defer wg.Done()
				<-start
				errs <- deliverClassified(ctx, h, f, deps, eventID, runID, "ATTENTION")
			}(eventID)
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			assertNoHang(t, err)
		}
		snapshots, active, total := publicationCounts(t, h, f)
		if active != 1 || snapshots < 1 || total != snapshots {
			t.Fatalf("snapshots=%d active=%d publications=%d; want one active release per snapshot lineage", snapshots, active, total)
		}
	})
}

// TestIT013V3ConcurrentSameFactsCreateOneSnapshotAndPublication exercises the
// idempotency contract with a real pinned photo scope and concurrent events.
func TestIT013V3ConcurrentSameFactsCreateOneSnapshotAndPublication(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, deps notificationrequest.Dependencies) {
		seedTask04Scope(t, ctx, h, f, "OBSERVED", false)
		runID := seedAutomaticClassification(ctx, t, h, f, "NORMAL")
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for range 2 {
			wg.Add(1)
			go func(eventID identity.ID) {
				defer wg.Done()
				<-start
				errs <- deliverClassified(ctx, h, f, deps, eventID, runID, "NORMAL")
			}(identity.NewID())
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			assertNoHang(t, err)
		}
		snapshots, active, total := publicationCounts(t, h, f)
		if snapshots != 1 || active != 1 || total != 1 {
			t.Fatalf("v3 concurrent deliveries left snapshots=%d active=%d publications=%d; want exactly 1/1/1", snapshots, active, total)
		}
	})
}

// TestAutomaticFinalizationNeverPublishesCanceledOrInvalidatedInspections: the
// snapshot is still recorded, but the AUTOMATIC policy releases nothing.
func TestAutomaticFinalizationNeverPublishesCanceledOrInvalidatedInspections(t *testing.T) {
	for _, status := range []string{"CANCELED", "INVALIDATED"} {
		t.Run(status, func(t *testing.T) {
			withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, deps notificationrequest.Dependencies) {
				ctx, cancel := context.WithTimeout(ctx, automaticHandlerTimeout)
				defer cancel()
				runID := seedAutomaticClassification(ctx, t, h, f, "ATTENTION")
				if err := h.AdminDB.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", f.TenantID, f.InspectionID).Update("status", status).Error; err != nil {
					t.Fatal(err)
				}
				assertNoHang(t, deliverClassified(ctx, h, f, deps, identity.NewID(), runID, "ATTENTION"))
				snapshots, active, total := publicationCounts(t, h, f)
				if snapshots != 1 || active != 0 || total != 0 {
					t.Fatalf("%s inspection: snapshots=%d active=%d publications=%d; want 1/0/0", status, snapshots, active, total)
				}
			})
		})
	}
}

// TestAutomaticPolicyRejectsTheSeparateTransactionAdapter: releasing from a
// second transaction would invert the lock order, so the handler refuses to run
// AUTOMATIC with only the fallback adapter instead of silently downgrading.
func TestAutomaticPolicyRejectsTheSeparateTransactionAdapter(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, deps notificationrequest.Dependencies) {
		ctx, cancel := context.WithTimeout(ctx, automaticHandlerTimeout)
		defer cancel()
		runID := seedAutomaticClassification(ctx, t, h, f, "ATTENTION")
		err := deliverClassifiedWith(ctx, h, f, deps, identity.NewID(), runID, "ATTENTION", func(handler *Dependencies) {
			handler.CreateSnapshot = func(ctx context.Context, input generatesnapshot.Input) (generatesnapshot.Result, error) {
				return generatesnapshot.Create(ctx, h.DB, input, time.Now())
			}
		})
		if !errors.Is(err, ErrAutomaticPublicationNeedsTransaction) {
			t.Fatalf("error = %v, want ErrAutomaticPublicationNeedsTransaction", err)
		}
		if snapshots, _, total := publicationCounts(t, h, f); snapshots != 0 || total != 0 {
			t.Fatalf("the refused delivery left snapshots=%d publications=%d", snapshots, total)
		}
	})
}
