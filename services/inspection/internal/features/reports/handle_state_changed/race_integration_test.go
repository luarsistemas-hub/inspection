//go:build integration

package handle_state_changed

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	handleready "inspection/services/inspection/internal/features/reports/handle_report_ready"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestReportProjectionAndCancellationSerialize(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("integration database URLs are required")
	}
	setupCtx, setupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	h, err := harness.NewFromEnv(setupCtx)
	setupCancel()
	if err != nil {
		t.Fatalf("open integration harness: %v", err)
	}
	defer func() { _ = h.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	reportHandler := reportReadyHandler(t)
	stateHandler, err := Setup(Dependencies{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("report holds share lock while cancellation waits", func(t *testing.T) {
		fixture := seedRaceFixture(t, h.AdminDB)
		entered, release := make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		releaseReport := func() { releaseOnce.Do(func() { close(release) }) }
		t.Cleanup(releaseReport)
		gatedReport := reportReadyHandlerWithNotification(t, entered, release)
		reportResult := make(chan error, 1)
		go func() {
			reportResult <- h.AdminDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				return gatedReport(ctx, tx, fixture.report)
			})
		}()
		waitSignal(t, ctx, entered)
		cancelBackendPID, cancelResult := runCancellation(t, ctx, h.AdminDB, stateHandler, fixture)
		waitForBlock(t, ctx, h.AdminDB, cancelBackendPID)
		releaseReport()
		waitResult(t, ctx, reportResult)
		waitResult(t, ctx, cancelResult)
		assertInvalidated(t, h.AdminDB, fixture.tenantID, fixture.inspectionID)
	})
	t.Run("cancellation holds update lock while report waits", func(t *testing.T) {
		fixture := seedRaceFixture(t, h.AdminDB)
		entered, release := make(chan struct{}), make(chan struct{})
		cancelPID := make(chan int, 1)
		cancelResult := make(chan error, 1)
		go func() {
			cancelResult <- h.AdminDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				var pid int
				if err := tx.Raw("SELECT pg_backend_pid()").Scan(&pid).Error; err != nil {
					return err
				}
				cancelPID <- pid
				if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.inspectionID).Update("status", "CANCELED").Error; err != nil {
					return err
				}
				if err := stateHandler(ctx, tx, fixture.state); err != nil {
					return err
				}
				close(entered)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
		}()
		waitSignal(t, ctx, entered)
		reportPID := make(chan int, 1)
		reportResult := make(chan error, 1)
		go func() {
			reportResult <- h.AdminDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				var pid int
				if err := tx.Raw("SELECT pg_backend_pid()").Scan(&pid).Error; err != nil {
					return err
				}
				reportPID <- pid
				return reportHandler(ctx, tx, fixture.report)
			})
		}()
		reportBackendPID := waitValue(t, ctx, reportPID)
		waitForBlock(t, ctx, h.AdminDB, reportBackendPID)
		close(release)
		waitResult(t, ctx, cancelResult)
		waitResult(t, ctx, reportResult)
		assertInvalidated(t, h.AdminDB, fixture.tenantID, fixture.inspectionID)
	})
	t.Run("state handler waits for report share lock on canceled inspection", func(t *testing.T) {
		fixture := seedRaceFixture(t, h.AdminDB)
		if err := h.AdminDB.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.inspectionID).Update("status", "CANCELED").Error; err != nil {
			t.Fatalf("set canceled inspection: %v", err)
		}
		entered, release := make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		releaseReport := func() { releaseOnce.Do(func() { close(release) }) }
		t.Cleanup(releaseReport)
		gatedReport := reportReadyHandlerWithNotification(t, entered, release)
		reportResult := make(chan error, 1)
		go func() {
			reportResult <- h.AdminDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				return gatedReport(ctx, tx, fixture.report)
			})
		}()
		waitSignal(t, ctx, entered)
		handlerPID := make(chan int, 1)
		handlerResult := make(chan error, 1)
		go func() {
			handlerResult <- h.AdminDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				var pid int
				if err := tx.Raw("SELECT pg_backend_pid()").Scan(&pid).Error; err != nil {
					return err
				}
				handlerPID <- pid
				return stateHandler(ctx, tx, fixture.state)
			})
		}()
		blockCtx, blockCancel := context.WithTimeout(ctx, 2*time.Second)
		waitForBlock(t, blockCtx, h.AdminDB, waitValue(t, blockCtx, handlerPID))
		blockCancel()
		releaseReport()
		waitResult(t, ctx, reportResult)
		waitResult(t, ctx, handlerResult)
		assertInvalidated(t, h.AdminDB, fixture.tenantID, fixture.inspectionID)
	})
}

type raceFixture struct {
	tenantID, inspectionID identity.ID
	report, state          events.RawEnvelope
}

func seedRaceFixture(t *testing.T, db *gorm.DB) raceFixture {
	t.Helper()
	tenantID, inspectionID := identity.NewID(), identity.NewID()
	t.Cleanup(func() {
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SET LOCAL session_replication_role = replica").Error; err != nil {
				return err
			}
			for _, model := range []any{&database.TriageCaseEvent{}, &database.TriageCase{}, &database.DashboardInspection{}, &database.ClassificationRun{}, &database.ReportSnapshot{}, &database.Inspection{}} {
				if err := tx.Unscoped().Where("tenant_id=?", tenantID).Delete(model).Error; err != nil {
					return fmt.Errorf("clean race fixture %T: %w", model, err)
				}
			}
			return nil
		}); err != nil {
			t.Errorf("clean race fixture: %v", err)
		}
	})
	now := time.Now().UTC()
	inspection := database.Inspection{ID: inspectionID, TenantID: tenantID, BusinessUnitID: identity.NewID(), AssetID: identity.NewID(), ParticipantID: identity.NewID(), TemplateID: identity.NewID(), TemplateVersionID: identity.NewID(), Source: "MANUAL", SourceKey: "sec-race-" + inspectionID.String(), Status: "COMPLETED", MediaKind: "PHOTO", DueAt: now.Add(time.Hour), DeadlineAt: now.Add(2 * time.Hour), ReminderInstants: []byte("[]"), ContextSnapshot: []byte("{}"), CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&inspection).Error; err != nil {
		t.Fatalf("seed inspection: %v", err)
	}
	snapshot := database.ReportSnapshot{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, Mode: "STANDARD", Classification: "CRITICAL", JSONDigest: "digest", HTMLDigest: "digest", CanonicalJSON: []byte("{}"), HTML: []byte(`{"html":"<html></html>"}`), VersionNumber: 1, CreatedAt: now}
	if err := db.Create(&snapshot).Error; err != nil {
		t.Fatalf("seed report snapshot: %v", err)
	}
	classification := database.ClassificationRun{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, Classification: "CRITICAL", ReasonCodes: []byte("[]"), CreatedAt: now}
	if err := db.Create(&classification).Error; err != nil {
		t.Fatalf("seed classification: %v", err)
	}
	readyPayload, _ := json.Marshal(map[string]any{"inspectionId": inspectionID, "snapshotId": snapshot.ID})
	statePayload, _ := json.Marshal(map[string]any{"inspectionId": inspectionID, "to": "CANCELED"})
	return raceFixture{tenantID: tenantID, inspectionID: inspectionID, report: events.RawEnvelope{TenantID: tenantID, Type: "report.ready.v1", Payload: readyPayload}, state: events.RawEnvelope{TenantID: tenantID, Type: "inspection.state_changed.v1", Payload: statePayload}}
}

func reportReadyHandler(t *testing.T) func(context.Context, *gorm.DB, events.RawEnvelope) error {
	t.Helper()
	handler, err := handleready.Setup(handleready.Dependencies{InAppNotification: func(context.Context, *gorm.DB, events.RawEnvelope) error { return nil }, UpsertTriage: func(context.Context, *gorm.DB, identity.ID, identity.ID, database.ClassificationRun, int, time.Time) error {
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func reportReadyHandlerWithNotification(t *testing.T, entered, release chan struct{}) func(context.Context, *gorm.DB, events.RawEnvelope) error {
	t.Helper()
	handler, err := handleready.Setup(handleready.Dependencies{InAppNotification: func(ctx context.Context, _ *gorm.DB, _ events.RawEnvelope) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, UpsertTriage: func(context.Context, *gorm.DB, identity.ID, identity.ID, database.ClassificationRun, int, time.Time) error {
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func runCancellation(t *testing.T, ctx context.Context, db *gorm.DB, handler func(context.Context, *gorm.DB, events.RawEnvelope) error, fixture raceFixture) (int, <-chan error) {
	pid := make(chan int, 1)
	result := make(chan error, 1)
	go func() {
		result <- db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var backendPID int
			if err := tx.Raw("SELECT pg_backend_pid()").Scan(&backendPID).Error; err != nil {
				return err
			}
			pid <- backendPID
			if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.inspectionID).Update("status", "CANCELED").Error; err != nil {
				return err
			}
			return handler(ctx, tx, fixture.state)
		})
	}()
	return waitValue(t, ctx, pid), result
}

func waitForBlock(t *testing.T, ctx context.Context, db *gorm.DB, blockedPID int) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err := db.WithContext(ctx).Raw("SELECT cardinality(pg_blocking_pids(?)) > 0", blockedPID).Scan(&blocked).Error; err != nil {
			t.Fatalf("query blocking pids: %v", err)
		}
		if blocked {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for PostgreSQL lock: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitSignal(t *testing.T, ctx context.Context, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatalf("timed out waiting for signal: %v", ctx.Err())
	}
}

func waitValue[T any](t *testing.T, ctx context.Context, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-ctx.Done():
		t.Fatalf("timed out waiting for value: %v", ctx.Err())
		var zero T
		return zero
	}
}

func waitResult(t *testing.T, ctx context.Context, result <-chan error) {
	t.Helper()
	if err := waitValue(t, ctx, result); err != nil {
		t.Fatalf("transaction failed: %v", err)
	}
}

func assertInvalidated(t *testing.T, db *gorm.DB, tenantID, inspectionID identity.ID) {
	t.Helper()
	var projection database.DashboardInspection
	if err := db.Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Take(&projection).Error; err != nil {
		t.Fatalf("read dashboard projection: %v", err)
	}
	if !projection.Invalidated || projection.Status != "CANCELED" {
		t.Fatalf("dashboard projection invalidated=%t status=%q, want true/CANCELED", projection.Invalidated, projection.Status)
	}
}
