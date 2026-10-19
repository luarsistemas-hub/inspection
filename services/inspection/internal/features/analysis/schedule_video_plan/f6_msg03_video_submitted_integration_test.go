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

func task04SubmittedPayload(fixture task04PlanFixture) map[string]any {
	return map[string]any{"draftId": fixture.draftID.String(), "responsibilityId": fixture.responsibilityID.String(), "submissionId": fixture.submissionID.String(), "mediaId": fixture.currentMediaID().String(), "preparationStatus": "PENDING_VALIDATION"}
}

func task04SubmittedEnvelope(tenantID identity.ID, payload map[string]any) events.Envelope[map[string]any] {
	id := identity.NewID()
	return events.Envelope[map[string]any]{ID: id, Type: "capture.video_submitted.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: tenantID, AggregateID: id, CorrelationID: "vv-msg03-" + id.String(), Payload: payload}
}

func task04Deliver(t *testing.T, consumer messaging.Consumer, envelope events.Envelope[map[string]any]) (bool, error) {
	t.Helper()
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return consumer.Process(context.Background(), body, 0)
}

func TestVV_IT_565InvalidVideoSubmittedEventsAreRejectedWithoutMutation(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, true)
	scheduler := newTask04Scheduler(t, fixture, &countingTask04Matcher{calls: new(int)}, &countingTask04Matcher{calls: new(int)})
	foreign, err := fixture.h.SeedTask06Fixture(fixture.ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	valid := task04SubmittedPayload(fixture)
	missing := task04SubmittedPayload(fixture)
	delete(missing, "submissionId")
	mismatched := task04SubmittedPayload(fixture)
	mismatched["mediaId"] = identity.NewID().String()
	cases := map[string]events.Envelope[map[string]any]{
		"missing submission id":       task04SubmittedEnvelope(fixture.tenantID, missing),
		"foreign tenant id":           task04SubmittedEnvelope(foreign.TenantID, valid),
		"mismatched media membership": task04SubmittedEnvelope(fixture.tenantID, mismatched),
	}
	for name, envelope := range cases {
		t.Run(name, func(t *testing.T) {
			consumer := messaging.Consumer{DB: fixture.h.DB, Registry: events.DefaultRegistry(), Name: "vv-it565-" + identity.NewID().String(), Handle: scheduler.Handle}
			processed, err := task04Deliver(t, consumer, envelope)
			if processed || !errors.Is(err, messaging.ErrPermanent) {
				t.Fatalf("invalid submission processed=%t err=%v; want permanent rejection", processed, err)
			}
			assertTask04NoPreparedEffects(t, fixture, consumer.Name, envelope.ID)
		})
	}
}

func TestVV_IT_566DuplicateVideoSubmittedEventKeepsOnePlanThroughOutboxAndInbox(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_RABBITMQ_URL") == "" {
		t.Skip("INSPECTION_TEST_RABBITMQ_URL is required for isolated MSG03 integration")
	}
	fixture := newTask04PlanFixture(t, false, true)
	parkTask04Plans(t, fixture)
	h := fixture.h
	ctx, cancel := context.WithCancel(fixture.ctx)
	defer cancel()
	scheduler := newTask04Scheduler(t, fixture, &countingTask04Matcher{calls: new(int)}, &countingTask04Matcher{calls: new(int)})
	queueName := "vv-it566-" + identity.NewID().String()
	contract, err := messaging.NewQueueContract(queueName, "capture.video_submitted.v1", 1)
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
	envelope := task04SubmittedEnvelope(fixture.tenantID, task04SubmittedPayload(fixture))
	if err := messaging.AddOutbox(h.MigrationDB, envelope); err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	go func() {
		runDone <- (messaging.RabbitConsumer{Channel: consumeChannel, Contract: contract, Handler: messaging.Consumer{DB: h.DB, Registry: events.DefaultRegistry(), Name: queueName, Handle: scheduler.Handle}, ConsumerName: queueName}).Run(ctx)
	}()
	if count, err := (messaging.Dispatcher{DB: h.MigrationDB, Publisher: h.Publisher}).Dispatch(ctx); err != nil || count < 1 {
		t.Fatalf("outbox dispatch count=%d err=%v", count, err)
	}
	duplicate, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Publisher.PublishConfirmed(ctx, messaging.Publication{RoutingKey: envelope.Type, Body: duplicate, Mandatory: true, CorrelationID: envelope.CorrelationID}); err != nil {
		t.Fatal(err)
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
		if plans == 1 && receipts == 1 && stats.Messages == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("duplicate submission plans=%d receipts=%d queued=%d", plans, receipts, stats.Messages)
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	var plans int64
	if err := h.AdminDB.Model(&database.VideoPlan{}).Where("tenant_id=? AND inspection_id=?", fixture.tenantID, fixture.inspectionID).Count(&plans).Error; err != nil {
		t.Fatal(err)
	}
	if plans != 1 {
		t.Fatalf("duplicate submission created %d plans", plans)
	}
	cancel()
	if err := <-runDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Rabbit consumer exit: %v", err)
	}
}

func TestVV_IT_567UnavailableDatabaseKeepsVideoSubmittedEventRetryable(t *testing.T) {
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
	if err := unavailable.Callback().Query().Before("gorm:query").Register("vv_it567:unavailable", func(db *gorm.DB) {
		if db.Statement.Table == "submission_versions" {
			_ = db.AddError(apperror.New(apperror.DependencyUnavailable, "database", "database unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	envelope := task04SubmittedEnvelope(fixture.tenantID, task04SubmittedPayload(fixture))
	name := "vv-it567-" + identity.NewID().String()
	processed, err := task04Deliver(t, messaging.Consumer{DB: unavailable, Registry: events.DefaultRegistry(), Name: name, Handle: scheduler.Handle}, envelope)
	if processed || err == nil || errors.Is(err, messaging.ErrPermanent) {
		t.Fatalf("unavailable adapter processed=%t err=%v; want retryable failure", processed, err)
	}
	assertTask04NoPreparedEffects(t, fixture, name, envelope.ID)
	processed, err = task04Deliver(t, messaging.Consumer{DB: fixture.h.DB, Registry: events.DefaultRegistry(), Name: name, Handle: scheduler.Handle}, envelope)
	if !processed || err != nil {
		t.Fatalf("redelivery after recovery processed=%t err=%v", processed, err)
	}
	var plans int64
	if err := fixture.h.AdminDB.Model(&database.VideoPlan{}).Where("tenant_id=? AND submission_id=?", fixture.tenantID, fixture.submissionID).Count(&plans).Error; err != nil {
		t.Fatal(err)
	}
	if plans != 1 {
		t.Fatalf("recovered submission created %d plans, want 1", plans)
	}
}
