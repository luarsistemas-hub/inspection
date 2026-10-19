//go:build integration

package schedule_video_plan

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestVV_IT_560InvalidVideoPreparedEventsAreRejectedWithoutMutation(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, true)
	scheduler := newTask04Scheduler(t, fixture, &countingTask04Matcher{calls: new(int)}, &countingTask04Matcher{calls: new(int)})
	consumer := messaging.Consumer{DB: fixture.h.DB, Registry: events.DefaultRegistry(), Name: "vv-it560-" + identity.NewID().String(), Handle: scheduler.Handle}
	foreign, err := fixture.h.SeedTask06Fixture(fixture.ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	valid := task04PreparedPayload(fixture)
	cases := map[string]events.Envelope[map[string]any]{
		"missing preparation id":   task04PreparedEnvelope(fixture.tenantID, map[string]any{"mediaId": valid["mediaId"], "manifestDigest": valid["manifestDigest"]}),
		"foreign tenant id":        task04PreparedEnvelope(foreign.TenantID, valid),
		"manifest digest mismatch": task04PreparedEnvelope(fixture.tenantID, map[string]any{"mediaId": valid["mediaId"], "preparationId": valid["preparationId"], "manifestDigest": "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"}),
	}
	for name, envelope := range cases {
		t.Run(name, func(t *testing.T) {
			body, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			processed, err := consumer.Process(fixture.ctx, body, 0)
			if processed || !errors.Is(err, messaging.ErrPermanent) {
				t.Fatalf("invalid event processed=%t err=%v; want permanent rejection", processed, err)
			}
			assertTask04NoPreparedEffects(t, fixture, consumer.Name, envelope.ID)
		})
	}
}

func TestVV_IT_561DuplicateVideoPreparedEventKeepsOnePlanThroughOutboxAndInbox(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_RABBITMQ_URL") == "" {
		t.Skip("INSPECTION_TEST_RABBITMQ_URL is required for isolated MSG02 integration")
	}
	fixture := newTask04PlanFixture(t, false, true)
	parkTask04Plans(t, fixture)
	h := fixture.h
	ctx, cancel := context.WithCancel(fixture.ctx)
	defer cancel()
	scheduler := newTask04Scheduler(t, fixture, &countingTask04Matcher{calls: new(int)}, &countingTask04Matcher{calls: new(int)})
	queueName := "vv-it561-" + identity.NewID().String()
	contract, err := messaging.NewQueueContract(queueName, "media.video_prepared.v1", 1)
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
	deferTask04ForeignOutbox(t, fixture)
	envelope := task04PreparedEnvelope(fixture.tenantID, task04PreparedPayload(fixture))
	if err := messaging.AddOutbox(h.MigrationDB, envelope); err != nil {
		t.Fatal(err)
	}
	consumer := messaging.Consumer{DB: h.DB, Registry: events.DefaultRegistry(), Name: queueName, Handle: scheduler.Handle}
	runDone := make(chan error, 1)
	go func() {
		runDone <- (messaging.RabbitConsumer{Channel: consumeChannel, Contract: contract, Handler: consumer, ConsumerName: queueName}).Run(ctx)
	}()
	if count, err := (messaging.Dispatcher{DB: h.MigrationDB, Publisher: h.Publisher}).Dispatch(ctx); err != nil || count < 1 {
		t.Fatalf("outbox dispatch count=%d err=%v; want the prepared event", count, err)
	}
	duplicate, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Publisher.PublishConfirmed(ctx, messaging.Publication{RoutingKey: envelope.Type, Body: duplicate, Mandatory: true, CorrelationID: envelope.CorrelationID}); err != nil {
		t.Fatalf("publish duplicate prepared event: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var plans, receipts int64
		if err := h.AdminDB.Model(&database.VideoPlan{}).Where("tenant_id=? AND submission_id=?", fixture.tenantID, fixture.submissionID).Count(&plans).Error; err != nil {
			t.Fatal(err)
		}
		if err := h.AdminDB.Model(&database.InboxReceipt{}).Where("consumer=? AND event_id=?", queueName, envelope.ID).Count(&receipts).Error; err != nil {
			t.Fatal(err)
		}
		stats, err := h.Channel.QueueInspect(contract.Name)
		if err != nil {
			t.Fatal(err)
		}
		if plans == 1 && receipts == 1 && stats.Messages == 0 && stats.Consumers == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("duplicate delivery state plans=%d receipts=%d queued=%d; want one plan and one receipt", plans, receipts, stats.Messages)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Give the duplicate time to be acknowledged as an inbox replay.
	time.Sleep(500 * time.Millisecond)
	var plans int64
	if err := h.AdminDB.Model(&database.VideoPlan{}).Where("tenant_id=? AND inspection_id=?", fixture.tenantID, fixture.inspectionID).Count(&plans).Error; err != nil {
		t.Fatal(err)
	}
	if plans != 1 {
		t.Fatalf("duplicate prepared event created %d plans", plans)
	}
	cancel()
	if err := <-runDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Rabbit consumer exit: %v", err)
	}
}

func TestVV_IT_562UnavailableDatabaseKeepsVideoPreparedEventRetryable(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, true)
	parkTask04Plans(t, fixture)
	scheduler := newTask04Scheduler(t, fixture, &countingTask04Matcher{calls: new(int)}, &countingTask04Matcher{calls: new(int)})
	sqlDB, err := fixture.h.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	unavailable, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := unavailable.Callback().Query().Before("gorm:query").Register("vv_it562:unavailable", func(db *gorm.DB) {
		if db.Statement.Table == "video_preparation_jobs" {
			_ = db.AddError(apperror.New(apperror.DependencyUnavailable, "database", "database unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	envelope := task04PreparedEnvelope(fixture.tenantID, task04PreparedPayload(fixture))
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	name := "vv-it562-" + identity.NewID().String()
	processed, err := (messaging.Consumer{DB: unavailable, Registry: events.DefaultRegistry(), Name: name, Handle: scheduler.Handle}).Process(fixture.ctx, body, 0)
	if processed || err == nil || errors.Is(err, messaging.ErrPermanent) {
		t.Fatalf("unavailable adapter processed=%t err=%v; want retryable failure", processed, err)
	}
	assertTask04NoPreparedEffects(t, fixture, name, envelope.ID)
	processed, err = (messaging.Consumer{DB: fixture.h.DB, Registry: events.DefaultRegistry(), Name: name, Handle: scheduler.Handle}).Process(fixture.ctx, body, 0)
	if !processed || err != nil {
		t.Fatalf("retry after recovery processed=%t err=%v", processed, err)
	}
	var plans int64
	if err := fixture.h.AdminDB.Model(&database.VideoPlan{}).Where("tenant_id=? AND submission_id=?", fixture.tenantID, fixture.submissionID).Count(&plans).Error; err != nil {
		t.Fatal(err)
	}
	if plans != 1 {
		t.Fatalf("recovered delivery created %d plans, want 1", plans)
	}
}

func task04PreparedPayload(fixture task04PlanFixture) map[string]any {
	return map[string]any{"mediaId": fixture.currentMediaID().String(), "preparationId": fixture.currentPreparation.String(), "manifestDigest": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
}

func task04PreparedEnvelope(tenantID identity.ID, payload map[string]any) events.Envelope[map[string]any] {
	id := identity.NewID()
	return events.Envelope[map[string]any]{ID: id, Type: "media.video_prepared.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: tenantID, AggregateID: id, CorrelationID: "vv-msg02-" + id.String(), Payload: payload}
}

func assertTask04NoPreparedEffects(t *testing.T, fixture task04PlanFixture, consumer string, eventID identity.ID) {
	t.Helper()
	var plans, jobs, receipts int64
	if err := fixture.h.AdminDB.Model(&database.VideoPlan{}).Where("tenant_id=? AND inspection_id=?", fixture.tenantID, fixture.inspectionID).Count(&plans).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.AdminDB.Model(&database.ComparisonJob{}).Where("tenant_id=? AND inspection_id=?", fixture.tenantID, fixture.inspectionID).Count(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.AdminDB.Model(&database.InboxReceipt{}).Where("consumer=? AND event_id=?", consumer, eventID).Count(&receipts).Error; err != nil {
		t.Fatal(err)
	}
	if plans != 0 || jobs != 0 || receipts != 0 {
		t.Fatalf("rejected prepared event left plans=%d jobs=%d receipts=%d", plans, jobs, receipts)
	}
}

// parkTask04Plans keeps the BUILDING plan created by a prepared event from
// being claimed by another test's cross-tenant RunDue.
func parkTask04Plans(t *testing.T, fixture task04PlanFixture) {
	t.Cleanup(func() {
		_ = fixture.h.AdminDB.Model(&database.VideoPlan{}).Where("tenant_id=? AND inspection_id=?", fixture.tenantID, fixture.inspectionID).Update("next_attempt_at", time.Now().UTC().Add(24*time.Hour)).Error
	})
}
