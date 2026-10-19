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
	buildrequest "inspection/services/inspection/internal/features/analysis/build_request"
	processcomparison "inspection/services/inspection/internal/features/analysis/process_comparison"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type countingTask04Gateway struct {
	inner *task04ConditionGateway
	calls int
}

func (g *countingTask04Gateway) CompleteStructured(ctx context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
	g.calls++
	return g.inner.CompleteStructured(ctx, request)
}

type task04VideoRequest struct {
	fixture task04PlanFixture
	job     database.ComparisonJob
	gateway *countingTask04Gateway
	handle  func(context.Context, *gorm.DB, events.RawEnvelope) error
}

func newTask04VideoRequest(t *testing.T) task04VideoRequest {
	t.Helper()
	fixture := newTask04PlanFixture(t, false, true)
	plan := runTask04Plan(t, fixture)
	prepareTask04FrameObjects(t, fixture)
	var job database.ComparisonJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND video_plan_id=?", fixture.tenantID, plan.ID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	builder, err := buildrequest.Setup(buildrequest.Dependencies{Store: fixture.h.Store})
	if err != nil {
		t.Fatal(err)
	}
	gateway := &countingTask04Gateway{inner: &task04ConditionGateway{categories: []string{"CONSERVATION"}}}
	processor, err := processcomparison.Setup(processcomparison.Dependencies{Gateway: gateway, BuildRequest: builder, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	return task04VideoRequest{fixture: fixture, job: job, gateway: gateway, handle: processcomparison.VideoRequestHandler(processor)}
}

func (r task04VideoRequest) envelope(tenantID identity.ID, payload map[string]any) events.Envelope[map[string]any] {
	id := identity.NewID()
	return events.Envelope[map[string]any]{ID: id, Type: "analysis.video_comparison_requested.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: tenantID, AggregateID: r.job.ID, CorrelationID: "vv-msg04-" + id.String(), Payload: payload}
}

func (r task04VideoRequest) payload() map[string]any {
	return map[string]any{"comparisonJobId": r.job.ID.String(), "videoPlanId": r.job.VideoPlanID.String(), "videoPlanItemId": r.job.VideoPlanItemID.String()}
}

func (r task04VideoRequest) consumer(db *gorm.DB, name string) messaging.Consumer {
	return messaging.Consumer{DB: db, Registry: events.DefaultRegistry(), Name: name, Handle: r.handle}
}

func (r task04VideoRequest) state(t *testing.T) (string, int64) {
	t.Helper()
	var job database.ComparisonJob
	if err := r.fixture.h.AdminDB.Where("tenant_id=? AND id=?", r.fixture.tenantID, r.job.ID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	var runs int64
	if err := r.fixture.h.AdminDB.Model(&database.AnalysisRun{}).Where("tenant_id=? AND job_id=?", r.fixture.tenantID, r.job.ID).Count(&runs).Error; err != nil {
		t.Fatal(err)
	}
	return job.Status, runs
}

func (r task04VideoRequest) receipts(t *testing.T, consumer string, eventID identity.ID) int64 {
	t.Helper()
	var receipts int64
	if err := r.fixture.h.AdminDB.Model(&database.InboxReceipt{}).Where("consumer=? AND event_id=?", consumer, eventID).Count(&receipts).Error; err != nil {
		t.Fatal(err)
	}
	return receipts
}

func processTask04Delivery(t *testing.T, consumer messaging.Consumer, envelope events.Envelope[map[string]any]) (bool, error) {
	t.Helper()
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return consumer.Process(context.Background(), body, 0)
}

func TestVV_IT_569VideoComparisonRequestRunsPinnedFrameRequestOnce(t *testing.T) {
	request := newTask04VideoRequest(t)
	envelope := request.envelope(request.fixture.tenantID, request.payload())
	if processed, err := processTask04Delivery(t, request.consumer(request.fixture.h.DB, "inspection.video"), envelope); !processed || err != nil {
		t.Fatalf("video comparison request processed=%t err=%v", processed, err)
	}
	status, runs := request.state(t)
	if status != "COMPLETED" || runs != 1 || request.gateway.calls != 1 {
		t.Fatalf("pinned condition request status=%s runs=%d gatewayCalls=%d; want COMPLETED/1/1", status, runs, request.gateway.calls)
	}
	var origin, current int
	for _, image := range request.gateway.inner.request.Images {
		switch image.Source {
		case "ORIGIN":
			origin++
		case "CURRENT":
			current++
		}
	}
	if origin == 0 || current == 0 {
		t.Fatalf("condition request lost the pinned origin/current frames: %+v", request.gateway.inner.request.Images)
	}
}

func TestVV_IT_570InvalidVideoComparisonRequestsAreRejectedWithoutMutation(t *testing.T) {
	request := newTask04VideoRequest(t)
	foreign, err := request.fixture.h.SeedTask06Fixture(request.fixture.ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	valid := request.payload()
	cases := map[string]events.Envelope[map[string]any]{
		"missing item id":     request.envelope(request.fixture.tenantID, map[string]any{"comparisonJobId": valid["comparisonJobId"], "videoPlanId": valid["videoPlanId"]}),
		"foreign tenant id":   request.envelope(foreign.TenantID, valid),
		"membership mismatch": request.envelope(request.fixture.tenantID, map[string]any{"comparisonJobId": valid["comparisonJobId"], "videoPlanId": valid["videoPlanId"], "videoPlanItemId": identity.NewID().String()}),
	}
	for name, envelope := range cases {
		t.Run(name, func(t *testing.T) {
			name := "vv-it570-" + identity.NewID().String()
			processed, err := processTask04Delivery(t, request.consumer(request.fixture.h.DB, name), envelope)
			if processed || !errors.Is(err, messaging.ErrPermanent) {
				t.Fatalf("invalid request processed=%t err=%v; want permanent rejection", processed, err)
			}
			if status, runs := request.state(t); status == "COMPLETED" || runs != 0 || request.gateway.calls != 0 || request.receipts(t, name, envelope.ID) != 0 {
				t.Fatalf("invalid request mutated state: status=%s runs=%d calls=%d", status, runs, request.gateway.calls)
			}
		})
	}
}

func TestVV_IT_571DuplicateVideoComparisonRequestKeepsOneRunThroughOutboxAndInbox(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_RABBITMQ_URL") == "" {
		t.Skip("INSPECTION_TEST_RABBITMQ_URL is required for isolated MSG04 integration")
	}
	request := newTask04VideoRequest(t)
	h := request.fixture.h
	ctx, cancel := context.WithCancel(request.fixture.ctx)
	defer cancel()
	queueName := "vv-it571-" + identity.NewID().String()
	contract, err := messaging.NewQueueContract(queueName, "analysis.video_comparison_requested.v1", 1)
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
	deferTask04ForeignOutbox(t, request.fixture)
	envelope := request.envelope(request.fixture.tenantID, request.payload())
	if err := messaging.AddOutbox(h.MigrationDB, envelope); err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	go func() {
		runDone <- (messaging.RabbitConsumer{Channel: consumeChannel, Contract: contract, Handler: request.consumer(h.DB, queueName), ConsumerName: queueName}).Run(ctx)
	}()
	if count, err := (messaging.Dispatcher{DB: h.MigrationDB, Publisher: h.Publisher}).Dispatch(ctx); err != nil || count < 1 {
		t.Fatalf("outbox dispatch count=%d err=%v", count, err)
	}
	duplicate, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Publisher.PublishConfirmed(ctx, messaging.Publication{RoutingKey: envelope.Type, Body: duplicate, Mandatory: true, CorrelationID: envelope.CorrelationID}); err != nil {
		t.Fatalf("publish duplicate video request: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		status, runs := request.state(t)
		stats, err := h.Channel.QueueInspect(contract.Name)
		if err != nil {
			t.Fatal(err)
		}
		if status == "COMPLETED" && runs == 1 && request.receipts(t, queueName, envelope.ID) == 1 && stats.Messages == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("duplicate delivery state status=%s runs=%d queued=%d", status, runs, stats.Messages)
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	if _, runs := request.state(t); runs != 1 || request.gateway.calls != 1 {
		t.Fatalf("duplicate delivery runs=%d gatewayCalls=%d; want one logical condition run", runs, request.gateway.calls)
	}
	cancel()
	if err := <-runDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Rabbit consumer exit: %v", err)
	}
}

func TestVV_IT_572UnavailableDatabaseKeepsVideoComparisonRequestRetryable(t *testing.T) {
	request := newTask04VideoRequest(t)
	sqlDB, err := request.fixture.h.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	unavailable, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := unavailable.Callback().Query().Before("gorm:query").Register("vv_it572:unavailable", func(db *gorm.DB) {
		if db.Statement.Table == "comparison_jobs" {
			_ = db.AddError(apperror.New(apperror.DependencyUnavailable, "database", "database unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	envelope := request.envelope(request.fixture.tenantID, request.payload())
	name := "vv-it572-" + identity.NewID().String()
	processed, err := processTask04Delivery(t, request.consumer(unavailable, name), envelope)
	if processed || err == nil || errors.Is(err, messaging.ErrPermanent) {
		t.Fatalf("unavailable adapter processed=%t err=%v; want retryable failure", processed, err)
	}
	if status, runs := request.state(t); status == "COMPLETED" || runs != 0 || request.gateway.calls != 0 || request.receipts(t, name, envelope.ID) != 0 {
		t.Fatalf("unavailable adapter reported work: status=%s runs=%d calls=%d", status, runs, request.gateway.calls)
	}
	if processed, err := processTask04Delivery(t, request.consumer(request.fixture.h.DB, name), envelope); !processed || err != nil {
		t.Fatalf("redelivery after recovery processed=%t err=%v", processed, err)
	}
	if status, runs := request.state(t); status != "COMPLETED" || runs != 1 || request.gateway.calls != 1 {
		t.Fatalf("recovered request status=%s runs=%d calls=%d", status, runs, request.gateway.calls)
	}
}

func TestVV_IT_573VideoComparisonRequestBeforeSealedPlanIsDeferred(t *testing.T) {
	request := newTask04VideoRequest(t)
	// The request overtakes the plan seal: the plan is still BUILDING.
	if err := request.fixture.h.AdminDB.Model(&database.VideoPlan{}).Where("tenant_id=? AND id=?", request.fixture.tenantID, *request.job.VideoPlanID).Update("status", "BUILDING").Error; err != nil {
		t.Fatal(err)
	}
	parkTask04Plans(t, request.fixture)
	envelope := request.envelope(request.fixture.tenantID, request.payload())
	name := "vv-it573-" + identity.NewID().String()
	processed, err := processTask04Delivery(t, request.consumer(request.fixture.h.DB, name), envelope)
	if processed || !errors.Is(err, processcomparison.ErrVideoPlanPending) || errors.Is(err, messaging.ErrPermanent) {
		t.Fatalf("request before seal processed=%t err=%v; want retryable pending", processed, err)
	}
	if status, runs := request.state(t); status == "COMPLETED" || runs != 0 || request.gateway.calls != 0 || request.receipts(t, name, envelope.ID) != 0 {
		t.Fatalf("deferred request invented readiness: status=%s runs=%d calls=%d", status, runs, request.gateway.calls)
	}
	if err := request.fixture.h.AdminDB.Model(&database.VideoPlan{}).Where("tenant_id=? AND id=?", request.fixture.tenantID, *request.job.VideoPlanID).Update("status", "SEALED").Error; err != nil {
		t.Fatal(err)
	}
	if processed, err := processTask04Delivery(t, request.consumer(request.fixture.h.DB, name), envelope); !processed || err != nil {
		t.Fatalf("redelivery after seal processed=%t err=%v", processed, err)
	}
	if status, runs := request.state(t); status != "COMPLETED" || runs != 1 || request.gateway.calls != 1 {
		t.Fatalf("request after seal status=%s runs=%d calls=%d", status, runs, request.gateway.calls)
	}
}

// deferTask04ForeignOutbox keeps pending events of other tests in the shared
// disposable database out of this test's dispatch, which publishes every due
// outbox row.
func deferTask04ForeignOutbox(t *testing.T, fixture task04PlanFixture) {
	t.Helper()
	if err := fixture.h.MigrationDB.Model(&database.OutboxIntent{}).Where("tenant_id<>? AND status='PENDING'", fixture.tenantID).Update("next_attempt_at", time.Now().UTC().Add(24*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
}
