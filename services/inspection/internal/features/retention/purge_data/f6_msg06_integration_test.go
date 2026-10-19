//go:build integration

package purge_data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

func TestVV_IT_580PurgeDueRejectsMalformedAndForeignTenantEventsWithoutMutation(t *testing.T) {
	h := newMSG06Harness(t)
	ctx := context.Background()
	owner, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	consumer := purgeDueConsumer(h.DB, h.Store, "vv-it580-"+identity.NewID().String())
	cases := []struct {
		name    string
		tenant  identity.ID
		payload map[string]any
	}{
		{name: "missing required inspectionId", tenant: owner.TenantID, payload: map[string]any{"unexpected": "value"}},
		{name: "foreign tenant inspectionId", tenant: owner.TenantID, payload: map[string]any{"inspectionId": foreign.InspectionID}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := purgeDueBody(t, tc.tenant, tc.payload)
			if _, err := consumer.Process(ctx, body, 0); !errors.Is(err, messaging.ErrPermanent) {
				t.Fatalf("Process() error=%v; want permanent rejection", err)
			}
		})
	}
	purgedConsumer := messaging.Consumer{DB: h.DB, Registry: events.DefaultRegistry(), Name: "vv-it580-purged-" + identity.NewID().String(), Handle: HandlePurgedEvent}
	invalidPurged := []byte(fmt.Sprintf(`{"id":%q,"type":"retention.purged.v1","schemaVersion":1,"occurredAt":%q,"tenantId":%q,"correlationId":"vv-it580-purged","payload":x}`, identity.NewID().String(), time.Now().UTC().Format(time.RFC3339Nano), owner.TenantID.String()))
	if _, err := purgedConsumer.Process(ctx, invalidPurged, 0); !errors.Is(err, messaging.ErrPermanent) {
		t.Fatalf("malformed retention.purged.v1 error=%v; want permanent rejection", err)
	}
	// retention.purge_due.v1 carries inspectionId but no manifest or membership
	// digest. retention.purged.v1 also carries neither identity nor digest, so
	// only malformed JSON can be rejected by its no-op validation consumer.
	for _, fixture := range []harness.Task06Fixture{owner, foreign} {
		assertInspectionUnpurged(t, h, fixture.TenantID, fixture.InspectionID)
	}
}

func TestVV_IT_581PurgeDueOutboxRabbitInboxDeduplicates(t *testing.T) {
	h := newMSG06Harness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	if err := makeInspectionDue(ctx, h, fixture.TenantID, fixture.InspectionID); err != nil {
		t.Fatal(err)
	}
	queueName := "vv-it581-" + identity.NewID().String()
	contract, err := messaging.NewQueueContract(queueName, "retention.purge_due.v1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Declare(contract); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, name := range append([]string{contract.Name, contract.DLQName}, contract.RetryNames[:]...) {
			_, _ = h.Channel.QueueDelete(name, false, false, false)
		}
	})
	consumeChannel, err := h.Rabbit.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer consumeChannel.Close()
	envelope := events.Envelope[map[string]any]{ID: identity.NewID(), Type: "retention.purge_due.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: fixture.TenantID, AggregateID: fixture.InspectionID, CorrelationID: "vv-it581-" + fixture.InspectionID.String(), Payload: map[string]any{"inspectionId": fixture.InspectionID}}
	if err := messaging.AddOutbox(h.MigrationDB, envelope); err != nil {
		t.Fatal(err)
	}
	consumer := messaging.Consumer{DB: h.DB, Registry: events.DefaultRegistry(), Name: queueName, Handle: func(ctx context.Context, tx *gorm.DB, raw events.RawEnvelope) error {
		return HandleDueEvent(ctx, tx, h.Store, raw)
	}}
	runDone := make(chan error, 1)
	go func() {
		runDone <- (messaging.RabbitConsumer{Channel: consumeChannel, Contract: contract, Handler: consumer, ConsumerName: queueName}).Run(ctx)
	}()
	dispatcher := messaging.Dispatcher{DB: h.MigrationDB, Publisher: h.Publisher}
	if count, err := dispatcher.Dispatch(ctx); err != nil || count != 1 {
		t.Fatalf("outbox dispatch count=%d err=%v; want 1", count, err)
	}
	duplicate, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Publisher.PublishConfirmed(ctx, messaging.Publication{RoutingKey: envelope.Type, Body: duplicate, Mandatory: true, CorrelationID: envelope.CorrelationID}); err != nil {
		t.Fatalf("publish duplicate retention event: %v", err)
	}
	if err := waitMSG06(t, 5*time.Second, func() (bool, error) {
		var runCount, inboxCount, purgedEvents int64
		if err := h.MigrationDB.Model(&database.PurgeRun{}).Where("tenant_id=? AND inspection_id=? AND status='COMPLETED'", fixture.TenantID, fixture.InspectionID).Count(&runCount).Error; err != nil {
			return false, err
		}
		if err := h.MigrationDB.Model(&database.InboxReceipt{}).Where("consumer=? AND event_id=?", queueName, envelope.ID).Count(&inboxCount).Error; err != nil {
			return false, err
		}
		if err := h.MigrationDB.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='retention.purged.v1'", fixture.TenantID).Count(&purgedEvents).Error; err != nil {
			return false, err
		}
		stats, err := h.Channel.QueueInspect(contract.Name)
		if err != nil {
			return false, err
		}
		return runCount == 1 && inboxCount == 1 && purgedEvents == 1 && stats.Messages == 0, nil
	}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-runDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Rabbit consumer exit: %v", err)
	}
}

func TestVV_IT_582PurgeStoreFailureKeepsRetryableStateAndRetryCompletes(t *testing.T) {
	h := newMSG06Harness(t)
	ctx := context.Background()
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	if err := makeInspectionDue(ctx, h, fixture.TenantID, fixture.InspectionID); err != nil {
		t.Fatal(err)
	}
	client := &failDeleteClient{Client: h.Store.Client, fail: true}
	store := h.Store
	store.Client = client
	clock := mustDueClock(t, time.Now().Add(-6*365*24*time.Hour))
	if _, err := PurgeWithStore(ctx, h.MigrationDB, store, fixture.TenantID, fixture.InspectionID, clock); err == nil {
		t.Fatal("PurgeWithStore() succeeded while object-store Delete fails")
	}
	assertInspectionUnpurged(t, h, fixture.TenantID, fixture.InspectionID)
	var incomplete int64
	if err := h.MigrationDB.Model(&database.PurgeRun{}).Where("tenant_id=? AND inspection_id=? AND status='COMPLETED'", fixture.TenantID, fixture.InspectionID).Count(&incomplete).Error; err != nil {
		t.Fatal(err)
	}
	if incomplete != 0 {
		t.Fatalf("failed object deletion reported completed purge: %d", incomplete)
	}
	client.fail = false
	if _, err := PurgeWithStore(ctx, h.MigrationDB, store, fixture.TenantID, fixture.InspectionID, clock); err != nil {
		t.Fatalf("retry purge after object store recovery: %v", err)
	}
	var completed int64
	if err := h.MigrationDB.Model(&database.PurgeRun{}).Where("tenant_id=? AND inspection_id=? AND status='COMPLETED'", fixture.TenantID, fixture.InspectionID).Count(&completed).Error; err != nil {
		t.Fatal(err)
	}
	if completed != 1 {
		t.Fatalf("completed purge count=%d, want 1", completed)
	}
}

func TestVV_IT_583PurgeDueBeforeRetentionClockMaturesIsDeferredThenCompletes(t *testing.T) {
	h := newMSG06Harness(t)
	ctx := context.Background()
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	consumer := purgeDueConsumer(h.DB, h.Store, "vv-it583-"+identity.NewID().String())
	body := purgeDueBody(t, fixture.TenantID, map[string]any{"inspectionId": fixture.InspectionID})
	if _, err := consumer.Process(ctx, body, 0); err == nil || errors.Is(err, messaging.ErrPermanent) {
		t.Fatalf("early event error=%v; want retryable deferral", err)
	}
	assertInspectionUnpurged(t, h, fixture.TenantID, fixture.InspectionID)
	if err := makeInspectionDue(ctx, h, fixture.TenantID, fixture.InspectionID); err != nil {
		t.Fatal(err)
	}
	if processed, err := consumer.Process(ctx, body, 0); err != nil || !processed {
		t.Fatalf("replayed due event processed=%v err=%v; want committed purge", processed, err)
	}
	var completed int64
	if err := h.MigrationDB.Model(&database.PurgeRun{}).Where("tenant_id=? AND inspection_id=? AND status='COMPLETED'", fixture.TenantID, fixture.InspectionID).Count(&completed).Error; err != nil {
		t.Fatal(err)
	}
	if completed != 1 {
		t.Fatalf("completed purge count=%d, want 1", completed)
	}
}

func purgeDueConsumer(db *gorm.DB, store objectstore.Store, name string) messaging.Consumer {
	return messaging.Consumer{DB: db, Registry: events.DefaultRegistry(), Name: name, Handle: func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		return HandleDueEvent(ctx, tx, store, envelope)
	}}
}

func purgeDueBody(t *testing.T, tenantID identity.ID, payload map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(events.Envelope[map[string]any]{ID: identity.NewID(), Type: "retention.purge_due.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: tenantID, CorrelationID: "vv-msg06-" + identity.NewID().String(), Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func newMSG06Harness(t *testing.T) *harness.Harness {
	t.Helper()
	for _, key := range []string{"INSPECTION_TEST_DATABASE_URL", "INSPECTION_TEST_RUNTIME_DATABASE_URL", "INSPECTION_TEST_RABBITMQ_URL"} {
		if os.Getenv(key) == "" {
			t.Skip(key + " is required for isolated MSG06 integration")
		}
	}
	h, err := harness.NewFromEnv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

func makeInspectionDue(ctx context.Context, h *harness.Harness, tenantID, inspectionID identity.ID) error {
	return h.WithinTenant(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", tenantID, inspectionID).Update("updated_at", time.Now().UTC().Add(-6*365*24*time.Hour)).Error
	})
}

func mustDueClock(t *testing.T, closedAt time.Time) retentioncore.Clock {
	t.Helper()
	clock, err := retentioncore.NewClock(retentioncore.EvidenceReports, closedAt, time.Time{}, retentioncore.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return clock
}

func assertInspectionUnpurged(t *testing.T, h *harness.Harness, tenantID, inspectionID identity.ID) {
	t.Helper()
	if err := h.WithinTenant(context.Background(), tenantID, func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", tenantID, inspectionID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("inspection rows=%d; want unchanged row", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var runs int64
	if err := h.MigrationDB.Model(&database.PurgeRun{}).Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Count(&runs).Error; err != nil {
		t.Fatal(err)
	}
	if runs != 0 {
		t.Fatalf("purge runs=%d after rejected/deferred/failed event; want 0", runs)
	}
}

func waitMSG06(t *testing.T, timeout time.Duration, check func() (bool, error)) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ok, err := check()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for MSG06 result")
}

type failDeleteClient struct {
	objectstore.Client
	fail bool
}

func (c *failDeleteClient) Delete(ctx context.Context, bucket, key string) error {
	if c.fail {
		return errors.New("injected object-store outage")
	}
	deleter, ok := c.Client.(objectstore.Deleter)
	if !ok {
		return objectstore.ErrInvalid
	}
	return deleter.Delete(ctx, bucket, key)
}
