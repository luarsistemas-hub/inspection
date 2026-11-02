//go:build integration

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	classifyinspection "inspection/services/inspection/internal/features/analysis/classify_inspection"
	processcomparison "inspection/services/inspection/internal/features/analysis/process_comparison"
	requestcomparisons "inspection/services/inspection/internal/features/analysis/request_comparisons"
	requestinventoryjobs "inspection/services/inspection/internal/features/analysis/request_inventory_jobs"
	mediaprocessverified "inspection/services/inspection/internal/features/media/process_verified"
	consumeevents "inspection/services/inspection/internal/features/messaging/consume_events"
	alertdelivery "inspection/services/inspection/internal/features/notifications/alert_delivery"
	consumedelivery "inspection/services/inspection/internal/features/notifications/consume_delivery"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	recordinapp "inspection/services/inspection/internal/features/notifications/record_in_app"
	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	handleclassified "inspection/services/inspection/internal/features/reports/handle_classified"
	handlereportready "inspection/services/inspection/internal/features/reports/handle_report_ready"
	renderpdf "inspection/services/inspection/internal/features/reports/render_pdf"
	purgedata "inspection/services/inspection/internal/features/retention/purge_data"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/observability"
	"inspection/services/inspection/internal/platform/sensitivecontent"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
)

type eventHandler = func(context.Context, *gorm.DB, events.RawEnvelope) error

// aipObserver records what the real consumer decided for each delivery so the
// tests can wait on facts instead of sleeping: processed, duplicate or error
// per event, and the dead-letter publications of the queue.
type aipObserver struct {
	mu          sync.Mutex
	transaction map[identity.ID][]observability.TransactionObservation
	deliveries  []observability.DeliveryObservation
}

func (o *aipObserver) AfterTransaction(_ context.Context, envelope events.RawEnvelope, observation observability.TransactionObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.transaction == nil {
		o.transaction = map[identity.ID][]observability.TransactionObservation{}
	}
	o.transaction[envelope.ID] = append(o.transaction[envelope.ID], observation)
}

func (o *aipObserver) AfterDelivery(_ context.Context, observation observability.DeliveryObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.deliveries = append(o.deliveries, observation)
}

func (o *aipObserver) processed(id identity.ID) (processed, duplicates, failures int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, observation := range o.transaction[id] {
		switch {
		case observation.Err != nil:
			failures++
		case observation.Processed:
			processed++
		default:
			duplicates++
		}
	}
	return
}

func (o *aipObserver) deadLetters() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	count := 0
	for _, delivery := range o.deliveries {
		if delivery.Action == "dlq" && delivery.Result == "published" {
			count++
		}
	}
	return count
}

// aipQueue is one isolated queue with its production topology (named DLQ and
// retry queues) consumed by the production consumer and inbox. The queue is
// bound under a unique routing key and receives messages published directly to
// it, so the running worker stack never sees these test events.
type aipQueue struct {
	t        *testing.T
	h        *Harness
	contract messaging.QueueContract
	observer *aipObserver
	publish  *amqp.Channel
	confirms <-chan amqp.Confirmation
	ctx      context.Context
}

func startAIPQueue(t *testing.T, h *Harness, name, routingKey string, handler eventHandler) *aipQueue {
	t.Helper()
	// The routing key is unique to this queue and never an event type: neither a
	// publication of the running worker stack nor a retry or dead-letter
	// republication of this queue can reach a live queue, and the queue never
	// needs to be unbound from the shared exchange.
	contract, err := messaging.NewQueueContract("aip-test-"+identity.NewID().String()+"."+name, "aip-test."+identity.NewID().String()+"."+routingKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Declare(contract); err != nil {
		t.Fatalf("declare topology %s: %v", contract.Name, err)
	}
	t.Cleanup(func() {
		for _, queue := range append([]string{contract.Name, contract.DLQName}, contract.RetryNames[:]...) {
			if _, err := h.Channel.QueueDelete(queue, false, false, false); err != nil {
				t.Errorf("delete test queue %s: %v", queue, err)
			}
		}
	})
	observer := &aipObserver{}
	consumeChannel, err := h.Rabbit.Channel()
	if err != nil {
		t.Fatal(err)
	}
	publishChannel, err := h.Rabbit.Channel()
	if err != nil {
		t.Fatal(err)
	}
	if err := publishChannel.Confirm(false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	consumer := messaging.RabbitConsumer{
		Channel:      consumeChannel,
		Contract:     contract,
		ConsumerName: contract.Name,
		Observer:     observer,
		Handler:      messaging.Consumer{DB: h.DB, Registry: events.DefaultRegistry(), Name: contract.Name, Handle: handler, Observer: observer},
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = consumer.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Error("test consumer did not stop")
		}
		_ = consumeChannel.Close()
		_ = publishChannel.Close()
	})
	return &aipQueue{t: t, h: h, contract: contract, observer: observer, publish: publishChannel, confirms: publishChannel.NotifyPublish(make(chan amqp.Confirmation, 16)), ctx: ctx}
}

// send publishes one body straight to the queue and waits for the broker confirmation.
func (q *aipQueue) send(body []byte, correlation string) {
	q.t.Helper()
	if err := q.publish.PublishWithContext(q.ctx, "", q.contract.Name, true, false, amqp.Publishing{DeliveryMode: amqp.Persistent, ContentType: "application/json", Body: body, CorrelationId: correlation}); err != nil {
		q.t.Fatalf("publish to %s: %v", q.contract.Name, err)
	}
	select {
	case confirmation := <-q.confirms:
		if !confirmation.Ack {
			q.t.Fatalf("broker refused the message for %s", q.contract.Name)
		}
	case <-time.After(10 * time.Second):
		q.t.Fatalf("no publisher confirmation for %s", q.contract.Name)
	}
}

func (q *aipQueue) sendEnvelope(envelope events.RawEnvelope) []byte {
	q.t.Helper()
	body, err := json.Marshal(envelope)
	if err != nil {
		q.t.Fatal(err)
	}
	q.send(body, envelope.CorrelationID)
	return body
}

func (q *aipQueue) wait(condition func() bool, what string) {
	q.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := q.h.Eventually(ctx, func() (bool, error) { return condition(), nil }); err != nil {
		q.t.Fatalf("%s: %v", what, err)
	}
}

func (q *aipQueue) waitProcessed(id identity.ID) {
	q.t.Helper()
	q.wait(func() bool { processed, _, _ := q.observer.processed(id); return processed >= 1 }, "event "+id.String()+" was never processed")
}

func (q *aipQueue) waitDuplicate(id identity.ID) {
	q.t.Helper()
	q.wait(func() bool { _, duplicates, _ := q.observer.processed(id); return duplicates >= 1 }, "duplicate of "+id.String()+" was never observed")
}

// expectDeadLetter waits for the named DLQ to hold n messages and returns the
// newest one, asserting the safe, payload-free failure metadata.
func (q *aipQueue) expectDeadLetter(n int, sent []byte) amqp.Delivery {
	q.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if count, err := q.h.WaitQueue(ctx, q.contract.DLQName, n); err != nil || count != n {
		q.t.Fatalf("DLQ %s holds %d messages (err=%v), want %d", q.contract.DLQName, count, err, n)
	}
	var delivery amqp.Delivery
	var ok bool
	for i := 0; i < n; i++ {
		if delivery, ok, _ = q.h.Channel.Get(q.contract.DLQName, false); !ok {
			q.t.Fatalf("DLQ %s was empty on read", q.contract.DLQName)
		}
	}
	// Requeue so the count stays observable after the inspection.
	_ = delivery.Nack(true, true)
	if reason, _ := delivery.Headers["failure_reason"].(string); reason != "contract_or_exhausted" {
		q.t.Fatalf("DLQ failure reason = %q, want the safe contract_or_exhausted", reason)
	}
	if attempt, _ := delivery.Headers["attempt"].(int32); attempt != 1 {
		q.t.Fatalf("DLQ attempt header = %v, want 1", delivery.Headers["attempt"])
	}
	for key, value := range delivery.Headers {
		if text, isText := value.(string); isText && len(text) > 0 && key != "failure_reason" && bytes.Contains(sent, []byte(text)) && len(text) > 12 {
			q.t.Fatalf("DLQ header %q repeats message content", key)
		}
	}
	if q.observer.deadLetters() < n {
		q.t.Fatalf("consumer observed %d dead-letter publications, want %d", q.observer.deadLetters(), n)
	}
	return delivery
}

// expectMainQueueDrained verifies nothing is parked in the main or retry queues.
func (q *aipQueue) expectMainQueueDrained() {
	q.t.Helper()
	for _, name := range append([]string{q.contract.Name}, q.contract.RetryNames[:]...) {
		state, err := q.h.Channel.QueueInspect(name)
		if err != nil || state.Messages != 0 {
			q.t.Fatalf("queue %s holds %d messages (err=%v), want none", name, state.Messages, err)
		}
	}
}

func invalidMajor(envelope events.RawEnvelope) events.RawEnvelope {
	envelope.ID = identity.NewID()
	envelope.SchemaVersion += 1
	return envelope
}

// aipPipeline wires the production handlers of the terminal pipeline for one
// fixture tenant, exactly as the worker registers them. The AI provider and
// the notification provider are the only stubs.
type aipPipeline struct {
	t        *testing.T
	h        *Harness
	fixture  Task06Fixture
	handlers map[string]eventHandler
}

type aipNotifications struct{ sent int }

func (n *aipNotifications) Send(context.Context, notificationcore.Notification) (notificationcore.NotificationResult, error) {
	n.sent++
	return notificationcore.NotificationResult{}, nil
}

func newAIPPipeline(ctx context.Context, t *testing.T, h *Harness) *aipPipeline {
	t.Helper()
	fixture, err := h.SeedTask06Fixture(ctx, Task06FixtureOptions{ComparisonMode: "CURRENT_ONLY"})
	if err != nil {
		t.Fatal(err)
	}
	p := &aipPipeline{t: t, h: h, fixture: fixture}
	requestJobs, err := requestcomparisons.Setup(requestcomparisons.Dependencies{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	requestInventory, err := requestinventoryjobs.Setup(requestinventoryjobs.Dependencies{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	processJob, err := processcomparison.Setup(processcomparison.Dependencies{Gateway: photoWorkerStoppedGateway{}, BuildRequest: func(context.Context, *gorm.DB, database.ComparisonJob, processcomparison.Payload) (llm.StructuredRequest, error) {
		return llm.StructuredRequest{ModelAlias: "inspection-vision", PromptDigest: "events-fixture", SystemPrompt: "Classify only visible evidence.", UserPrompt: "Inspect the current photo.", JSONSchema: json.RawMessage(`{"type":"object"}`), MinimumConfidenceBPS: 8500, Mode: "CURRENT_ONLY", Images: []llm.NormalizedImage{{EvidenceID: "EVENT-1", Source: "CURRENT", Digest: "fixture-digest", DataURL: "data:image/png;base64,AAAA"}}}, nil
	}, Now: time.Now, Mode: "mock"})
	if err != nil {
		t.Fatal(err)
	}
	classify, err := classifyinspection.Setup(classifyinspection.Dependencies{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	createSnapshot, err := generate_snapshot.Setup(generate_snapshot.Dependencies{DB: h.DB, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	classified, err := handleclassified.Setup(handleclassified.Dependencies{Notifications: &aipNotifications{}, CreateSnapshot: createSnapshot, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	render, err := renderpdf.Setup(renderpdf.Dependencies{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	ready, err := handlereportready.Setup(handlereportready.Dependencies{InAppNotification: recordinapp.Setup(), UpsertTriage: handleclassified.UpsertTriageReviewCase, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := alertdelivery.Setup(alertdelivery.Dependencies{Notifications: &aipNotifications{}})
	if err != nil {
		t.Fatal(err)
	}
	p.handlers = map[string]eventHandler{
		"capture.submitted.v1": func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
			if err := requestJobs(ctx, tx, envelope); err != nil {
				return err
			}
			return requestInventory(ctx, tx, envelope)
		},
		"analysis.comparison_requested.v1": func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
			var payload struct {
				JobID identity.ID `json:"jobId"`
			}
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.JobID == (identity.ID{}) {
				return messaging.ErrPermanent
			}
			processPayload, _ := json.Marshal(processcomparison.Payload{JobID: payload.JobID})
			return processJob(ctx, tx, processPayload, envelope.TenantID)
		},
		"analysis.comparison_completed.v1":   classify,
		"inspection.classified.v1":           classified,
		"report.snapshot_created.v1":         render,
		"report.ready.v1":                    ready,
		"notification.delivery_requested.v2": consumedelivery.Setup(),
		"notification.delivery_terminal.v1":  terminal,
		"project.stage_changed.v1":           consumeevents.HandleLifecycleEvent,
		"retention.purge_due.v1": func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
			return purgedata.HandleDueEvent(ctx, tx, h.Store, envelope)
		},
		"retention.purged.v1": purgedata.HandlePurgedEvent,
	}
	return p
}

// run executes a production handler once, directly, to produce the state that
// the event under test starts from.
func (p *aipPipeline) run(ctx context.Context, eventType string, envelope events.RawEnvelope) {
	p.t.Helper()
	if err := p.h.WithinTenant(ctx, p.fixture.TenantID, func(tx *gorm.DB) error { return p.handlers[eventType](ctx, tx, envelope) }); err != nil {
		p.t.Fatalf("prepare with %s: %v", eventType, err)
	}
}

// emitted returns the newest outbox event of one type as the dispatcher would publish it.
func (p *aipPipeline) emitted(ctx context.Context, eventType string) events.RawEnvelope {
	p.t.Helper()
	envelope, err := loadOutboxEnvelope(ctx, p.h, p.fixture.TenantID, eventType)
	if err != nil {
		p.t.Fatalf("no %s in the outbox: %v", eventType, err)
	}
	return envelope
}

// triage runs the real media.verified.v1 consumer so the capture is READY with its derivatives.
func (p *aipPipeline) triage(ctx context.Context) {
	p.t.Helper()
	photo := photoFlowPNG(p.t)
	var original database.MediaObject
	if err := p.h.AdminDB.Where("tenant_id=? AND id=?", p.fixture.TenantID, p.fixture.MediaID).Take(&original).Error; err != nil {
		p.t.Fatal(err)
	}
	if err := p.h.Store.Client.Put(ctx, p.h.Store.Bucket, original.ObjectKey, bytes.NewReader(photo), int64(len(photo)), "image/png"); err != nil {
		p.t.Fatal(err)
	}
	if err := p.h.AdminDB.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", p.fixture.TenantID, p.fixture.DraftID).Update("status", "OPEN").Error; err != nil {
		p.t.Fatal(err)
	}
	for _, step := range []map[string]any{{"status": "RECEIVED"}, {"sha256": digest(photo), "size_bytes": int64(len(photo))}, {"status": "VERIFIED"}} {
		if err := p.h.AdminDB.Model(&database.MediaObject{}).Where("tenant_id=? AND id=?", p.fixture.TenantID, p.fixture.MediaID).Updates(step).Error; err != nil {
			p.t.Fatal(err)
		}
	}
	verified, err := mediaprocessverified.Setup(mediaprocessverified.Dependencies{Store: objectstore.Store{Client: p.h.Store.Client, Bucket: p.h.Store.Bucket}, Detector: sensitivecontent.NewDisabledDetector()})
	if err != nil {
		p.t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"mediaId": p.fixture.MediaID})
	envelope := events.RawEnvelope{ID: identity.NewID(), Type: "media.verified.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: p.fixture.TenantID, AggregateID: p.fixture.MediaID, CorrelationID: "aip-events-verified", Payload: payload}
	if err := p.h.WithinTenant(ctx, p.fixture.TenantID, func(tx *gorm.DB) error { return verified(ctx, tx, envelope) }); err != nil {
		p.t.Fatalf("triage: %v", err)
	}
	if err := p.h.AdminDB.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", p.fixture.TenantID, p.fixture.DraftID).Update("status", "SUBMITTED").Error; err != nil {
		p.t.Fatal(err)
	}
}

// submitted is the capture.submitted.v1 event the capture slice emits.
func (p *aipPipeline) submitted() events.RawEnvelope {
	payload, _ := json.Marshal(map[string]any{"inspectionId": p.fixture.InspectionID, "draftId": p.fixture.DraftID, "kind": "INSPECTION"})
	return rawPhotoSubmission(p.fixture.TenantID, p.fixture.InspectionID, payload)
}

func (p *aipPipeline) count(model any, query string, args ...any) int64 {
	p.t.Helper()
	var count int64
	if err := p.h.AdminDB.Model(model).Where(query, args...).Count(&count).Error; err != nil {
		p.t.Fatal(err)
	}
	return count
}

func (p *aipPipeline) inspectionCount(model any) int64 {
	return p.count(model, "tenant_id=? AND inspection_id=?", p.fixture.TenantID, p.fixture.InspectionID)
}

func (p *aipPipeline) outbox(eventType string) int64 {
	return p.count(&database.OutboxIntent{}, "tenant_id=? AND type=?", p.fixture.TenantID, eventType)
}

func aipHarnessForEvents(t *testing.T) (*Harness, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	h, err := NewFromEnv(ctx)
	if err != nil {
		t.Skipf("integration dependencies unavailable: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h, ctx
}

// exerciseContract publishes the real emitted event through a named queue:
// the valid event produces its outcome once, a duplicate delivery changes
// nothing, an event of an unknown major reaches the named DLQ with a safe
// reason without touching state, and (when badPayload is set) a valid
// envelope with an unusable payload is dead-lettered with its inbox receipt
// rolled back. state returns a fingerprint of the observable outcome.
func exerciseContract(t *testing.T, p *aipPipeline, eventType string, valid events.RawEnvelope, badPayload *events.RawEnvelope, state func() string, wantAfterFirst func(before, after string)) {
	t.Helper()
	queue := startAIPQueue(t, p.h, "contract", eventType, p.handlers[eventType])
	before := state()
	body := queue.sendEnvelope(valid)
	queue.waitProcessed(valid.ID)
	afterFirst := state()
	wantAfterFirst(before, afterFirst)
	var receipts int64
	receiptCount := func(id identity.ID) int64 {
		var count int64
		if err := p.h.AdminDB.Model(&database.InboxReceipt{}).Where("tenant_id=? AND consumer=? AND event_id=?", p.fixture.TenantID, queue.contract.Name, id).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		return count
	}
	if receipts = receiptCount(valid.ID); receipts != 1 {
		t.Fatalf("inbox receipts after the first delivery = %d, want 1", receipts)
	}

	// Duplicate delivery of the same event.
	queue.send(body, valid.CorrelationID)
	queue.waitDuplicate(valid.ID)
	if got := state(); got != afterFirst {
		t.Fatalf("a duplicate delivery changed the outcome:\nbefore: %s\nafter:  %s", afterFirst, got)
	}
	if processed, duplicates, failures := queue.observer.processed(valid.ID); processed != 1 || duplicates != 1 || failures != 0 {
		t.Fatalf("deliveries of %s: processed=%d duplicate=%d failed=%d, want 1/1/0", valid.ID, processed, duplicates, failures)
	}
	if receiptCount(valid.ID) != 1 {
		t.Fatal("a duplicate delivery wrote a second inbox receipt")
	}

	// Unknown major: named DLQ, safe reason, no state change, nothing parked in retry.
	unknown := invalidMajor(valid)
	badBody := queue.sendEnvelope(unknown)
	queue.expectDeadLetter(1, badBody)
	if got := state(); got != afterFirst {
		t.Fatalf("an invalid-major event changed the outcome:\nbefore: %s\nafter:  %s", afterFirst, got)
	}
	if receiptCount(unknown.ID) != 0 {
		t.Fatal("an invalid-major event reached the inbox")
	}
	if badPayload != nil {
		invalid := *badPayload
		invalid.ID = identity.NewID()
		invalidBody := queue.sendEnvelope(invalid)
		queue.expectDeadLetter(2, invalidBody)
		if got := state(); got != afterFirst {
			t.Fatalf("an unusable payload changed the outcome:\nbefore: %s\nafter:  %s", afterFirst, got)
		}
		if receiptCount(invalid.ID) != 0 {
			t.Fatal("a rejected payload kept its inbox receipt")
		}
	}
	queue.expectMainQueueDrained()
}

func fingerprint(parts ...any) string { return fmt.Sprint(parts...) }

// IT-561 and IT-562: capture.submitted.v1.
func TestAIP_IT561IT562CaptureSubmittedCreatesOneJobAndNamedDLQ(t *testing.T) {
	h, ctx := aipHarnessForEvents(t)
	p := newAIPPipeline(ctx, t, h)
	p.triage(ctx)
	submitted := p.submitted()
	state := func() string {
		return fingerprint(p.inspectionCount(&database.ComparisonJob{}), p.outbox("analysis.comparison_requested.v1"))
	}
	bad := submitted
	bad.Payload = json.RawMessage(`{"kind":"INSPECTION"}`)
	exerciseContract(t, p, "capture.submitted.v1", submitted, &bad, state, func(before, after string) {
		if before != "0 0" || after != "1 1" {
			t.Fatalf("outcome jobs/requests = %q -> %q, want one comparison job and one request", before, after)
		}
	})
}

// IT-567 and IT-568: analysis.comparison_requested.v1.
func TestAIP_IT567IT568ComparisonRequestedCompletesTheJobOnceAndNamedDLQ(t *testing.T) {
	h, ctx := aipHarnessForEvents(t)
	p := newAIPPipeline(ctx, t, h)
	p.triage(ctx)
	p.run(ctx, "capture.submitted.v1", p.submitted())
	requested := p.emitted(ctx, "analysis.comparison_requested.v1")
	state := func() string {
		return fingerprint(p.count(&database.ComparisonJob{}, "tenant_id=? AND inspection_id=? AND status='COMPLETED'", p.fixture.TenantID, p.fixture.InspectionID),
			p.count(&database.AnalysisRun{}, "tenant_id=? AND job_id IN (SELECT id FROM analysis.comparison_jobs WHERE tenant_id=? AND inspection_id=?)", p.fixture.TenantID, p.fixture.TenantID, p.fixture.InspectionID),
			p.outbox("analysis.comparison_completed.v1"))
	}
	bad := requested
	bad.Payload = json.RawMessage(`{"inspectionId":"` + p.fixture.InspectionID.String() + `"}`)
	exerciseContract(t, p, "analysis.comparison_requested.v1", requested, &bad, state, func(before, after string) {
		if before != "0 0 0" || after != "1 1 1" {
			t.Fatalf("outcome completed/analysis/completed-events = %q -> %q, want 1 1 1", before, after)
		}
	})
}

// IT-569 and IT-570: analysis.comparison_completed.v1.
func TestAIP_IT569IT570ComparisonCompletedClassifiesOnceAndNamedDLQ(t *testing.T) {
	h, ctx := aipHarnessForEvents(t)
	p := newAIPPipeline(ctx, t, h)
	p.triage(ctx)
	p.run(ctx, "capture.submitted.v1", p.submitted())
	p.run(ctx, "analysis.comparison_requested.v1", p.emitted(ctx, "analysis.comparison_requested.v1"))
	completed := p.emitted(ctx, "analysis.comparison_completed.v1")
	state := func() string {
		return fingerprint(p.inspectionCount(&database.ClassificationRun{}), p.outbox("inspection.classified.v1"))
	}
	bad := completed
	bad.Payload = json.RawMessage(`{}`)
	exerciseContract(t, p, "analysis.comparison_completed.v1", completed, &bad, state, func(before, after string) {
		if before != "0 0" || after != "1 1" {
			t.Fatalf("outcome classification/classified-events = %q -> %q, want 1 1", before, after)
		}
	})
}

// IT-571 and IT-572: inspection.classified.v1.
func TestAIP_IT571IT572ClassifiedCreatesOneReportSnapshotAndNamedDLQ(t *testing.T) {
	h, ctx := aipHarnessForEvents(t)
	p := newAIPPipeline(ctx, t, h)
	p.triage(ctx)
	p.run(ctx, "capture.submitted.v1", p.submitted())
	p.run(ctx, "analysis.comparison_requested.v1", p.emitted(ctx, "analysis.comparison_requested.v1"))
	p.run(ctx, "analysis.comparison_completed.v1", p.emitted(ctx, "analysis.comparison_completed.v1"))
	classified := p.emitted(ctx, "inspection.classified.v1")
	state := func() string {
		return fingerprint(p.inspectionCount(&database.ReportSnapshot{}), p.outbox("report.snapshot_created.v1"))
	}
	bad := classified
	bad.Payload = json.RawMessage(`{"inspectionId":"` + p.fixture.InspectionID.String() + `"}`)
	exerciseContract(t, p, "inspection.classified.v1", classified, &bad, state, func(before, after string) {
		if before != "0 0" || after != "1 1" {
			t.Fatalf("outcome snapshots/snapshot-events = %q -> %q, want 1 1", before, after)
		}
	})
}

// IT-573 and IT-574: report.snapshot_created.v1.
func TestAIP_IT573IT574SnapshotCreatedSchedulesPDFWorkOnceAndNamedDLQ(t *testing.T) {
	h, ctx := aipHarnessForEvents(t)
	p := newAIPPipeline(ctx, t, h)
	p.triage(ctx)
	p.run(ctx, "capture.submitted.v1", p.submitted())
	p.run(ctx, "analysis.comparison_requested.v1", p.emitted(ctx, "analysis.comparison_requested.v1"))
	p.run(ctx, "analysis.comparison_completed.v1", p.emitted(ctx, "analysis.comparison_completed.v1"))
	p.run(ctx, "inspection.classified.v1", p.emitted(ctx, "inspection.classified.v1"))
	snapshotCreated := p.emitted(ctx, "report.snapshot_created.v1")
	jobs := func() int64 {
		return p.count(&database.ReportPDFJob{}, "tenant_id=? AND snapshot_id IN (SELECT id FROM reports.report_snapshots WHERE tenant_id=? AND inspection_id=?)", p.fixture.TenantID, p.fixture.TenantID, p.fixture.InspectionID)
	}
	state := func() string { return fingerprint(jobs(), p.inspectionCount(&database.ReportSnapshot{})) }
	bad := snapshotCreated
	bad.Payload = json.RawMessage(`{"inspectionId":"` + p.fixture.InspectionID.String() + `"}`)
	exerciseContract(t, p, "report.snapshot_created.v1", snapshotCreated, &bad, state, func(before, after string) {
		if jobs() < 1 || before == after {
			t.Fatalf("the snapshot event scheduled no durable PDF work: %q -> %q", before, after)
		}
	})
}

// IT-575 and IT-576: report.ready.v1.
func TestAIP_IT575IT576ReportReadyProjectsDashboardAndTriageOnceAndNamedDLQ(t *testing.T) {
	h, ctx := aipHarnessForEvents(t)
	p := newAIPPipeline(ctx, t, h)
	p.triage(ctx)
	p.run(ctx, "capture.submitted.v1", p.submitted())
	p.run(ctx, "analysis.comparison_requested.v1", p.emitted(ctx, "analysis.comparison_requested.v1"))
	p.run(ctx, "analysis.comparison_completed.v1", p.emitted(ctx, "analysis.comparison_completed.v1"))
	p.run(ctx, "inspection.classified.v1", p.emitted(ctx, "inspection.classified.v1"))
	var snapshot database.ReportSnapshot
	if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", p.fixture.TenantID, p.fixture.InspectionID).Take(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"inspectionId": p.fixture.InspectionID, "snapshotId": snapshot.ID})
	ready := events.RawEnvelope{ID: identity.NewID(), Type: "report.ready.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: p.fixture.TenantID, AggregateID: p.fixture.InspectionID, CorrelationID: "aip-report-ready", Payload: payload}
	state := func() string {
		var projection database.DashboardInspection
		classification := ""
		if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", p.fixture.TenantID, p.fixture.InspectionID).Take(&projection).Error; err == nil {
			classification = projection.Classification
		}
		return fingerprint(p.inspectionCount(&database.DashboardInspection{}), classification, p.inspectionCount(&database.TriageCase{}))
	}
	bad := ready
	bad.Payload = json.RawMessage(`{"inspectionId":"` + p.fixture.InspectionID.String() + `"}`)
	exerciseContract(t, p, "report.ready.v1", ready, &bad, state, func(before, after string) {
		var class database.ClassificationRun
		if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", p.fixture.TenantID, p.fixture.InspectionID).Take(&class).Error; err != nil {
			t.Fatal(err)
		}
		// A triage case is opened only for ATTENTION or CRITICAL results; a
		// NORMAL result projects to the dashboard and opens none.
		triage := 0
		if class.Classification == "ATTENTION" || class.Classification == "CRITICAL" {
			triage = 1
		}
		if want := fingerprint(1, class.Classification, triage); after != want || before == after {
			t.Fatalf("report.ready outcome = %q -> %q, want one dashboard row classified %s and %d triage case(s)", before, after, class.Classification, triage)
		}
	})
}

// IT-577 and IT-578: notification.delivery_requested (v2 is the current major).
func TestAIP_IT577IT578DeliveryRequestedValidatesItsReferenceOnceAndNamedDLQ(t *testing.T) {
	h, ctx := aipHarnessForEvents(t)
	p := newAIPPipeline(ctx, t, h)
	now := time.Now().UTC().Truncate(time.Microsecond)
	delivery := database.Delivery{ID: identity.NewID(), TenantID: p.fixture.TenantID, IntentID: identity.NewID(), InspectionID: &p.fixture.InspectionID, Status: "PENDING", LogicalTemplate: "critical-alert", SelectedProvider: "smtp", CreatedAt: now, UpdatedAt: now}
	if err := h.WithinTenant(ctx, p.fixture.TenantID, func(tx *gorm.DB) error { return tx.Create(&delivery).Error }); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"notificationId": delivery.ID})
	requested := events.RawEnvelope{ID: identity.NewID(), Type: "notification.delivery_requested.v2", SchemaVersion: 2, OccurredAt: now, TenantID: p.fixture.TenantID, AggregateID: delivery.ID, CorrelationID: "aip-delivery-requested", Payload: payload}
	state := func() string {
		var stored database.Delivery
		if err := h.AdminDB.Where("tenant_id=? AND id=?", p.fixture.TenantID, delivery.ID).Take(&stored).Error; err != nil {
			t.Fatal(err)
		}
		return fingerprint(stored.Status, stored.UpdatedAt.UTC().Format(time.RFC3339Nano), p.count(&database.Delivery{}, "tenant_id=?", p.fixture.TenantID))
	}
	bad := requested
	bad.Payload, _ = json.Marshal(map[string]any{"notificationId": identity.NewID()})
	exerciseContract(t, p, "notification.delivery_requested.v2", requested, &bad, state, func(before, after string) {
		if before != after {
			t.Fatalf("the reference check changed the delivery: %q -> %q", before, after)
		}
	})
}

// IT-579 and IT-580 name notification.channel_status.v1, which no longer
// exists: terminal delivery outcomes are published as
// notification.delivery_terminal.v1 (notifications/callbacks). The same
// contract guarantees are asserted for that event with its production consumer.
func TestAIP_IT579IT580DeliveryTerminalReplacesChannelStatusAndKeepsNamedDLQ(t *testing.T) {
	if _, err := events.DefaultRegistry().Validate([]byte(`{"id":"` + identity.NewID().String() + `","type":"notification.channel_status.v1","schemaVersion":1,"occurredAt":"2026-01-01T00:00:00Z","tenantId":"` + identity.NewID().String() + `","correlationId":"c","payload":{}}`)); err == nil {
		t.Fatal("notification.channel_status.v1 is registered again; IT-579/IT-580 are no longer obsolete")
	}
	h, ctx := aipHarnessForEvents(t)
	p := newAIPPipeline(ctx, t, h)
	deliveryID := identity.NewID()
	payload, _ := json.Marshal(map[string]any{"deliveryId": deliveryID, "inspectionId": p.fixture.InspectionID, "logicalTemplate": "reminder", "state": "FAILED", "failureCode": "PROVIDER_REJECTED"})
	terminal := events.RawEnvelope{ID: identity.NewID(), Type: "notification.delivery_terminal.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: p.fixture.TenantID, AggregateID: deliveryID, CorrelationID: "aip-delivery-terminal", Payload: payload}
	state := func() string {
		return fingerprint(p.count(&database.RecipientNotification{}, "tenant_id=?", p.fixture.TenantID), p.count(&database.Delivery{}, "tenant_id=?", p.fixture.TenantID))
	}
	bad := terminal
	bad.Payload, _ = json.Marshal(map[string]any{"deliveryId": deliveryID, "logicalTemplate": "reminder", "state": "DELIVERED"})
	exerciseContract(t, p, "notification.delivery_terminal.v1", terminal, &bad, state, func(before, after string) {
		if before != after {
			t.Fatalf("a terminal outcome without an onboarding owner changed state: %q -> %q", before, after)
		}
	})
}

// IT-581 and IT-582: project.stage_changed.v1 is consumed by the lifecycle
// consumer; the timeline is derived from the authoritative stages, so the
// event is acknowledged exactly once without any additional write.
func TestAIP_IT581IT582StageChangedIsConsumedOnceAndNamedDLQ(t *testing.T) {
	h, ctx := aipHarnessForEvents(t)
	p := newAIPPipeline(ctx, t, h)
	payload, _ := json.Marshal(map[string]any{"projectId": p.fixture.ProjectID, "stageId": p.fixture.StageID, "from": "AVAILABLE", "to": "IN_PROGRESS", "reason": ""})
	changed := events.RawEnvelope{ID: identity.NewID(), Type: "project.stage_changed.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: p.fixture.TenantID, AggregateID: p.fixture.StageID, CorrelationID: "aip-stage-changed", Payload: payload}
	state := func() string {
		var stage database.ProjectStage
		if err := h.AdminDB.Where("tenant_id=? AND id=?", p.fixture.TenantID, p.fixture.StageID).Take(&stage).Error; err != nil {
			t.Fatal(err)
		}
		return fingerprint(stage.Status, stage.Version, p.count(&database.OutboxIntent{}, "tenant_id=?", p.fixture.TenantID), p.count(&database.StageTransition{}, "tenant_id=?", p.fixture.TenantID))
	}
	exerciseContract(t, p, "project.stage_changed.v1", changed, nil, state, func(before, after string) {
		if before != after {
			t.Fatalf("consuming a stage change rewrote the authoritative stage or emitted events: %q -> %q", before, after)
		}
	})
}

// IT-583 and IT-584: retention.purge_due.v1 purges one eligible inspection.
func TestAIP_IT583IT584PurgeDuePurgesOnceAndNamedDLQ(t *testing.T) {
	h, ctx := aipHarnessForEvents(t)
	p := newAIPPipeline(ctx, t, h)
	// The fixture inspection belongs to an ACTIVE project, which has no retention
	// clock; the due inspection is standalone and closed six years ago.
	closedAt := time.Now().UTC().Add(-6 * 365 * 24 * time.Hour)
	if err := h.AdminDB.Exec("UPDATE inspections.inspections SET status='COMPLETED', updated_at=?, project_id=NULL, stage_id=NULL WHERE tenant_id=? AND id=?", closedAt, p.fixture.TenantID, p.fixture.InspectionID).Error; err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"inspectionId": p.fixture.InspectionID})
	due := events.RawEnvelope{ID: identity.NewID(), Type: "retention.purge_due.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: p.fixture.TenantID, AggregateID: p.fixture.InspectionID, CorrelationID: "retention-" + p.fixture.InspectionID.String(), Payload: payload}
	state := func() string {
		return fingerprint(p.count(&database.Inspection{}, "tenant_id=? AND id=?", p.fixture.TenantID, p.fixture.InspectionID),
			p.count(&database.MediaObject{}, "tenant_id=? AND id=?", p.fixture.TenantID, p.fixture.MediaID),
			p.count(&database.PurgeRun{}, "tenant_id=? AND inspection_id=? AND status='COMPLETED'", p.fixture.TenantID, p.fixture.InspectionID),
			p.outbox("retention.purged.v1"))
	}
	bad := due
	bad.Payload = json.RawMessage(`{}`)
	exerciseContract(t, p, "retention.purge_due.v1", due, &bad, state, func(before, after string) {
		if before != "1 1 0 0" || after != "0 0 1 1" {
			t.Fatalf("purge outcome inspection/media/purge-run/purged-events = %q -> %q, want 1 1 0 0 -> 0 0 1 1", before, after)
		}
		if exists, err := p.h.Store.Client.Head(ctx, p.h.Store.Bucket, "tenant/"+p.fixture.TenantID.String()+"/media/"+p.fixture.MediaID.String()); err == nil {
			t.Fatalf("the private original survived the purge: %+v", exists)
		}
	})
}

// IT-585 and IT-586: retention.purged.v1 is an informational, deidentified
// notice: consumed once, no state written, malformed payloads dead-lettered.
func TestAIP_IT585IT586PurgedNoticeIsConsumedOnceAndNamedDLQ(t *testing.T) {
	h, ctx := aipHarnessForEvents(t)
	p := newAIPPipeline(ctx, t, h)
	payload, _ := json.Marshal(map[string]any{"classes": []string{"originals", "parts", "derivatives", "reports", "associations"}})
	purged := events.RawEnvelope{ID: identity.NewID(), Type: "retention.purged.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: p.fixture.TenantID, AggregateID: p.fixture.InspectionID, CorrelationID: "aip-purged", Payload: payload}
	state := func() string {
		return fingerprint(p.count(&database.Inspection{}, "tenant_id=?", p.fixture.TenantID), p.count(&database.PurgeRun{}, "tenant_id=?", p.fixture.TenantID), p.count(&database.OutboxIntent{}, "tenant_id=?", p.fixture.TenantID))
	}
	bad := purged
	bad.Payload = json.RawMessage(`["not","an","object"]`)
	exerciseContract(t, p, "retention.purged.v1", purged, &bad, state, func(before, after string) {
		if before != after {
			t.Fatalf("an informational notice changed state: %q -> %q", before, after)
		}
	})
}
