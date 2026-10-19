//go:build integration

package schedule_video_plan

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	classifyinspection "inspection/services/inspection/internal/features/analysis/classify_inspection"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	generateSnapshot "inspection/services/inspection/internal/features/reports/generate_snapshot"
	handleClassified "inspection/services/inspection/internal/features/reports/handle_classified"
	handleReady "inspection/services/inspection/internal/features/reports/handle_report_ready"
	renderPDF "inspection/services/inspection/internal/features/reports/render_pdf"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	amqp "github.com/rabbitmq/amqp091-go"
)

type msg05MatrixNotification struct{}

func (msg05MatrixNotification) Send(context.Context, notificationcore.Notification) (notificationcore.NotificationResult, error) {
	return notificationcore.NotificationResult{ID: identity.NewID(), State: notificationcore.State("QUEUED")}, nil
}

type msg05Lifecycle struct {
	fixture   task04PlanFixture
	plan      database.VideoPlan
	class     database.ClassificationRun
	snapshot  database.ReportSnapshot
	consumers map[string]messaging.Consumer
	events    map[string]events.Envelope[map[string]any]
}

func newMSG05Lifecycle(t *testing.T, suffix string) msg05Lifecycle {
	t.Helper()
	f := newTask04PlanFixture(t, false, true)
	plan := runTask04Plan(t, f)
	parkTask04Plans(t, f)
	if err := f.h.AdminDB.Model(&database.ComparisonJob{}).Where("tenant_id=? AND video_plan_id=?", f.tenantID, plan.ID).Update("status", "COMPLETED").Error; err != nil {
		t.Fatal(err)
	}
	seedTask04ReportVideoSources(t, f)
	classify, err := classifyinspection.Setup(classifyinspection.Dependencies{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	createSnapshot, err := generateSnapshot.Setup(generateSnapshot.Dependencies{DB: f.h.DB, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	classified, err := handleClassified.Setup(handleClassified.Dependencies{Notifications: msg05MatrixNotification{}, CreateSnapshot: createSnapshot, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	render, err := renderPDF.Setup(renderPDF.Dependencies{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	ready, err := handleReady.Setup(handleReady.Dependencies{InAppNotification: func(context.Context, *gorm.DB, events.RawEnvelope) error { return nil }, UpsertTriage: handleClassified.UpsertTriageReviewCase, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"analysis.comparison_completed.v1", "inspection.classified.v1", "report.snapshot_created.v1", "report.ready.v1"}
	consumers := map[string]messaging.Consumer{
		names[0]: {DB: f.h.DB, Registry: events.DefaultRegistry(), Name: suffix + "-classify", Handle: classify},
		names[1]: {DB: f.h.DB, Registry: events.DefaultRegistry(), Name: suffix + "-classified", Handle: classified},
		names[2]: {DB: f.h.DB, Registry: events.DefaultRegistry(), Name: suffix + "-render", Handle: render},
		names[3]: {DB: f.h.DB, Registry: events.DefaultRegistry(), Name: suffix + "-ready", Handle: ready},
	}
	result := msg05Lifecycle{fixture: f, plan: plan, consumers: consumers, events: map[string]events.Envelope[map[string]any]{}}
	result.events[names[0]] = msg05Envelope(t, f.tenantID, f.inspectionID, names[0], map[string]any{"inspectionId": f.inspectionID})
	if processed, err := processTask04Delivery(t, consumers[names[0]], result.events[names[0]]); !processed || err != nil {
		t.Fatalf("comparison completed: %t %v", processed, err)
	}
	if err := f.h.AdminDB.Where("tenant_id=? AND inspection_id=? AND video_plan_id=?", f.tenantID, f.inspectionID, plan.ID).Take(&result.class).Error; err != nil {
		t.Fatal(err)
	}
	result.events[names[1]] = msg05Envelope(t, f.tenantID, f.inspectionID, names[1], map[string]any{"inspectionId": f.inspectionID, "classificationRunId": result.class.ID, "classification": result.class.Classification, "reasonCodes": json.RawMessage(result.class.ReasonCodes), "videoPlanId": plan.ID})
	if processed, err := processTask04Delivery(t, consumers[names[1]], result.events[names[1]]); !processed || err != nil {
		t.Fatalf("inspection classified: %t %v", processed, err)
	}
	if err := f.h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.tenantID, f.inspectionID).Take(&result.snapshot).Error; err != nil {
		t.Fatal(err)
	}
	result.events[names[2]] = msg05Envelope(t, f.tenantID, f.inspectionID, names[2], map[string]any{"inspectionId": f.inspectionID, "snapshotId": result.snapshot.ID, "version": result.snapshot.VersionNumber})
	if processed, err := processTask04Delivery(t, consumers[names[2]], result.events[names[2]]); !processed || err != nil {
		t.Fatalf("snapshot created: %t %v", processed, err)
	}
	result.events[names[3]] = msg05Envelope(t, f.tenantID, f.inspectionID, names[3], map[string]any{"inspectionId": f.inspectionID, "snapshotId": result.snapshot.ID})
	if processed, err := processTask04Delivery(t, consumers[names[3]], result.events[names[3]]); !processed || err != nil {
		t.Fatalf("report ready: %t %v", processed, err)
	}
	return result
}

func TestVV_IT_576LifecycleEventsDeduplicateThroughRabbitOutboxInbox(t *testing.T) {
	l := newMSG05Lifecycle(t, "vv-it576-"+identity.NewID().String())
	h := l.fixture.h
	ctx, cancel := context.WithCancel(l.fixture.ctx)
	defer cancel()
	// Dispatcher publishes every due outbox row in this disposable database.
	// Park unrelated fixture events, then enqueue only these four deliveries.
	if err := h.MigrationDB.Model(&database.OutboxIntent{}).Where("status='PENDING'").Update("next_attempt_at", time.Now().UTC().Add(24*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	type rabbitTarget struct {
		envelope events.Envelope[map[string]any]
		consumer messaging.Consumer
		contract messaging.QueueContract
		channel  *amqp.Channel
		done     chan error
	}
	targets := make([]rabbitTarget, 0, len(l.events))
	for eventType, original := range l.events {
		envelope := original
		consumer := l.consumers[eventType]
		queue, err := messaging.NewQueueContract(consumer.Name, eventType, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.Declare(queue); err != nil {
			t.Fatal(err)
		}
		channel, err := h.Rabbit.Channel()
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, rabbitTarget{envelope: envelope, consumer: consumer, contract: queue, channel: channel, done: make(chan error, 1)})
		target := &targets[len(targets)-1]
		go func() {
			target.done <- (messaging.RabbitConsumer{Channel: target.channel, Contract: target.contract, Handler: target.consumer, ConsumerName: target.consumer.Name}).Run(ctx)
		}()
	}
	defer func() {
		for _, target := range targets {
			cancel()
			_ = target.channel.Close()
			for _, queue := range append([]string{target.contract.Name, target.contract.DLQName}, target.contract.RetryNames[:]...) {
				_, _ = h.Channel.QueueDelete(queue, false, false, false)
			}
		}
	}()
	for _, target := range targets {
		if err := messaging.AddOutbox(h.MigrationDB, target.envelope); err != nil {
			t.Fatal(err)
		}
	}
	if count, err := (messaging.Dispatcher{DB: h.MigrationDB, Publisher: h.Publisher}).Dispatch(ctx); err != nil || count != len(targets) {
		t.Fatalf("outbox dispatch count=%d err=%v; want %d", count, err, len(targets))
	}
	for _, target := range targets {
		body, err := json.Marshal(target.envelope)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.Publisher.PublishConfirmed(ctx, messaging.Publication{RoutingKey: target.envelope.Type, Body: body, Mandatory: true, CorrelationID: target.envelope.CorrelationID}); err != nil {
			t.Fatalf("publish duplicate %s: %v", target.envelope.Type, err)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		complete := true
		for _, target := range targets {
			var receipts int64
			if err := h.AdminDB.Model(&database.InboxReceipt{}).Where("consumer=? AND event_id=?", target.consumer.Name, target.envelope.ID).Count(&receipts).Error; err != nil {
				t.Fatal(err)
			}
			stats, err := h.Channel.QueueInspect(target.contract.Name)
			if err != nil {
				t.Fatal(err)
			}
			complete = complete && receipts == 1 && stats.Messages == 0
		}
		if complete {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Rabbit inbox did not drain all duplicate lifecycle events")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	for _, target := range targets {
		if err := <-target.done; err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Rabbit consumer exit: %v", err)
		}
	}
	var classifications, snapshots, pdfJobs, projections, triage int64
	checks := []struct {
		model any
		query string
		args  []any
		into  *int64
		want  int64
	}{
		{&database.ClassificationRun{}, "tenant_id=? AND inspection_id=? AND video_plan_id=?", []any{l.fixture.tenantID, l.fixture.inspectionID, l.plan.ID}, &classifications, 1},
		{&database.ReportSnapshot{}, "tenant_id=? AND inspection_id=? AND video_plan_id=?", []any{l.fixture.tenantID, l.fixture.inspectionID, l.plan.ID}, &snapshots, 1},
		{&database.ReportPDFJob{}, "tenant_id=? AND snapshot_id=?", []any{l.fixture.tenantID, l.snapshot.ID}, &pdfJobs, 2},
		{&database.DashboardInspection{}, "tenant_id=? AND inspection_id=?", []any{l.fixture.tenantID, l.fixture.inspectionID}, &projections, 1},
		{&database.TriageCase{}, "tenant_id=? AND inspection_id=?", []any{l.fixture.tenantID, l.fixture.inspectionID}, &triage, 1},
	}
	for _, c := range checks {
		if err := l.fixture.h.AdminDB.Model(c.model).Where(c.query, c.args...).Count(c.into).Error; err != nil || *c.into != c.want {
			t.Fatalf("%T count=%d err=%v; want %d", c.model, *c.into, err, c.want)
		}
	}
}

func TestVV_IT_575LifecycleInvalidPayloadsDoNotMutate(t *testing.T) {
	l := newMSG05Lifecycle(t, "vv-it575-"+identity.NewID().String())
	foreign, err := l.fixture.h.SeedTask06Fixture(l.fixture.ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	otherInspectionID := identity.NewID()
	var otherInspection database.Inspection
	if err := l.fixture.h.AdminDB.Where("tenant_id=? AND id=?", l.fixture.tenantID, l.fixture.inspectionID).Take(&otherInspection).Error; err != nil {
		t.Fatal(err)
	}
	otherInspection.ID = otherInspectionID
	otherInspection.SourceKey = "f6-msg05-other-" + otherInspectionID.String()
	otherInspection.Version = 1
	if err := l.fixture.h.AdminDB.Create(&otherInspection).Error; err != nil {
		t.Fatal(err)
	}
	wrongMembershipSnapshot := l.snapshot
	wrongMembershipSnapshot.ID = identity.NewID()
	wrongMembershipSnapshot.InspectionID = otherInspectionID
	wrongMembershipSnapshot.VideoPlanID = nil
	wrongMembershipSnapshot.VersionNumber = l.snapshot.VersionNumber + 10
	if err := l.fixture.h.AdminDB.Create(&wrongMembershipSnapshot).Error; err != nil {
		t.Fatal(err)
	}
	baseline := msg05DownstreamCounts(t, l.fixture)
	bad := map[string]events.Envelope[map[string]any]{
		"comparison missing inspection": msg05Envelope(t, l.fixture.tenantID, l.fixture.inspectionID, "analysis.comparison_completed.v1", map[string]any{}),
		"comparison foreign tenant":     msg05Envelope(t, foreign.TenantID, l.fixture.inspectionID, "analysis.comparison_completed.v1", map[string]any{"inspectionId": l.fixture.inspectionID}),
		"classified missing run":        msg05Envelope(t, l.fixture.tenantID, l.fixture.inspectionID, "inspection.classified.v1", map[string]any{"inspectionId": l.fixture.inspectionID, "videoPlanId": l.plan.ID}),
		"classified wrong generation":   msg05Envelope(t, l.fixture.tenantID, l.fixture.inspectionID, "inspection.classified.v1", map[string]any{"inspectionId": l.fixture.inspectionID, "classificationRunId": l.class.ID, "classification": l.class.Classification, "reasonCodes": json.RawMessage(l.class.ReasonCodes), "videoPlanId": identity.NewID()}),
		"snapshot missing id":           msg05Envelope(t, l.fixture.tenantID, l.fixture.inspectionID, "report.snapshot_created.v1", map[string]any{"inspectionId": l.fixture.inspectionID}),
		"snapshot wrong membership":     msg05Envelope(t, l.fixture.tenantID, l.fixture.inspectionID, "report.snapshot_created.v1", map[string]any{"inspectionId": l.fixture.inspectionID, "snapshotId": wrongMembershipSnapshot.ID, "version": wrongMembershipSnapshot.VersionNumber}),
		"ready missing snapshot":        msg05Envelope(t, l.fixture.tenantID, l.fixture.inspectionID, "report.ready.v1", map[string]any{"inspectionId": l.fixture.inspectionID}),
		"ready foreign snapshot":        msg05Envelope(t, l.fixture.tenantID, l.fixture.inspectionID, "report.ready.v1", map[string]any{"inspectionId": l.fixture.inspectionID, "snapshotId": wrongMembershipSnapshot.ID}),
	}
	for name, envelope := range bad {
		t.Run(name, func(t *testing.T) {
			typ := envelope.Type
			consumer := l.consumers[typ]
			consumer.Name = "vv-it575-" + identity.NewID().String()
			processed, err := processTask04Delivery(t, consumer, envelope)
			if processed || err == nil {
				t.Fatalf("invalid %s processed=%t err=%v", typ, processed, err)
			}
			var receipt int64
			if e := l.fixture.h.AdminDB.Model(&database.InboxReceipt{}).Where("consumer=? AND event_id=?", consumer.Name, envelope.ID).Count(&receipt).Error; e != nil || receipt != 0 {
				t.Fatalf("invalid event receipt=%d err=%v", receipt, e)
			}
			if after := msg05DownstreamCounts(t, l.fixture); after != baseline {
				t.Fatalf("invalid event mutated downstream rows: before=%v after=%v", baseline, after)
			}
		})
	}
}

func msg05DownstreamCounts(t *testing.T, f task04PlanFixture) [7]int64 {
	t.Helper()
	var counts [7]int64
	models := []any{&database.ClassificationRun{}, &database.ReportSnapshot{}, &database.ReportPDFJob{}, &database.DashboardInspection{}, &database.TriageCase{}, &database.TriageCaseEvent{}, &database.RecipientNotification{}}
	for i, model := range models {
		if err := f.h.AdminDB.Model(model).Where("tenant_id=?", f.tenantID).Count(&counts[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	return counts
}

func TestVV_IT_577LifecycleUnavailableAdaptersRemainRetryable(t *testing.T) {
	l := newMSG05Lifecycle(t, "vv-it577-"+identity.NewID().String())
	fail := errors.New("notification unavailable")
	failed, err := handleReady.Setup(handleReady.Dependencies{InAppNotification: func(context.Context, *gorm.DB, events.RawEnvelope) error { return fail }, UpsertTriage: handleClassified.UpsertTriageReviewCase, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	envelope := msg05Envelope(t, l.fixture.tenantID, l.fixture.inspectionID, "report.ready.v1", map[string]any{"inspectionId": l.fixture.inspectionID, "snapshotId": l.snapshot.ID})
	consumer := messaging.Consumer{DB: l.fixture.h.DB, Registry: events.DefaultRegistry(), Name: "vv-it577-ready-" + identity.NewID().String(), Handle: failed}
	processed, err := processTask04Delivery(t, consumer, envelope)
	if processed || err == nil || errors.Is(err, messaging.ErrPermanent) {
		t.Fatalf("failed notification processed=%t err=%v; want retryable", processed, err)
	}
	var receipts int64
	if err := l.fixture.h.AdminDB.Model(&database.InboxReceipt{}).Where("consumer=? AND event_id=?", consumer.Name, envelope.ID).Count(&receipts).Error; err != nil || receipts != 0 {
		t.Fatalf("failed notification receipts=%d err=%v; want 0", receipts, err)
	}
	var projections int64
	if err := l.fixture.h.AdminDB.Model(&database.DashboardInspection{}).Where("tenant_id=? AND inspection_id=?", l.fixture.tenantID, l.fixture.inspectionID).Count(&projections).Error; err != nil || projections != 1 {
		t.Fatalf("failed notification projection count=%d err=%v; want prior row only", projections, err)
	}
	ready, err := handleReady.Setup(handleReady.Dependencies{InAppNotification: func(context.Context, *gorm.DB, events.RawEnvelope) error { return nil }, UpsertTriage: handleClassified.UpsertTriageReviewCase, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	consumer.Handle = ready
	if processed, err := processTask04Delivery(t, consumer, envelope); !processed || err != nil {
		t.Fatalf("recovery delivery processed=%t err=%v", processed, err)
	}
	for eventType, original := range l.events {
		name := "vv-it577-db-" + identity.NewID().String()
		delivery := original
		delivery.ID = identity.NewID()
		dbConsumer := l.consumers[eventType]
		dbConsumer.Name = name
		body, err := json.Marshal(delivery)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if processed, err := dbConsumer.Process(ctx, body, 0); processed || err == nil || errors.Is(err, messaging.ErrPermanent) {
			t.Fatalf("%s with cancelled database context processed=%t err=%v; want retryable failure", eventType, processed, err)
		}
		var receiptCount int64
		if err := l.fixture.h.AdminDB.Model(&database.InboxReceipt{}).Where("consumer=? AND event_id=?", name, delivery.ID).Count(&receiptCount).Error; err != nil || receiptCount != 0 {
			t.Fatalf("%s failed database delivery receipts=%d err=%v; want 0", eventType, receiptCount, err)
		}
		if processed, err := processTask04Delivery(t, dbConsumer, delivery); !processed || err != nil {
			t.Fatalf("%s database recovery processed=%t err=%v", eventType, processed, err)
		}
	}
}

type msg05FailNotification struct{ err error }

func (n msg05FailNotification) Send(context.Context, notificationcore.Notification) (notificationcore.NotificationResult, error) {
	return notificationcore.NotificationResult{}, n.err
}
