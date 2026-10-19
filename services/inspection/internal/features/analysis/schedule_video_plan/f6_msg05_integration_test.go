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
	generatesnapshot "inspection/services/inspection/internal/features/reports/generate_snapshot"
	handleclassified "inspection/services/inspection/internal/features/reports/handle_classified"
	handlereportready "inspection/services/inspection/internal/features/reports/handle_report_ready"
	renderpdf "inspection/services/inspection/internal/features/reports/render_pdf"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
)

type msg05NotificationStub struct{}

func (msg05NotificationStub) Send(context.Context, notificationcore.Notification) (notificationcore.NotificationResult, error) {
	return notificationcore.NotificationResult{ID: identity.NewID(), State: notificationcore.State("QUEUED")}, nil
}

func msg05Envelope(t *testing.T, tenantID, aggregateID identity.ID, eventType string, payload map[string]any) events.Envelope[map[string]any] {
	t.Helper()
	return events.Envelope[map[string]any]{ID: identity.NewID(), Type: eventType, SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: tenantID, AggregateID: aggregateID, CorrelationID: "vv-msg05-" + identity.NewID().String(), Payload: payload}
}

func deliverMSG05(t *testing.T, h *harness.Harness, name string, handler func(context.Context, *gorm.DB, events.RawEnvelope) error, envelope events.Envelope[map[string]any]) {
	t.Helper()
	consumer := messaging.Consumer{DB: h.DB, Registry: events.DefaultRegistry(), Name: name, Handle: handler}
	processed, err := processTask04Delivery(t, consumer, envelope)
	if err != nil || !processed {
		t.Fatalf("%s delivery processed=%t err=%v", envelope.Type, processed, err)
	}
}

func TestVV_IT_574PinnedVideoGenerationProducesOneReportLifecycleTransition(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, true)
	plan := runTask04Plan(t, fixture)
	if plan.Status != "SEALED" || plan.Generation != 1 {
		t.Fatalf("fixture plan status=%s generation=%d; want SEALED generation 1", plan.Status, plan.Generation)
	}
	if err := fixture.h.AdminDB.Model(&database.ComparisonJob{}).Where("tenant_id=? AND video_plan_id=?", fixture.tenantID, plan.ID).Update("status", "COMPLETED").Error; err != nil {
		t.Fatal(err)
	}
	for _, mediaID := range []identity.ID{fixture.currentMediaID(), func() identity.ID {
		var preparation database.VideoPreparationJob
		if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.originPreparation).Take(&preparation).Error; err != nil {
			t.Fatal(err)
		}
		return preparation.MediaID
	}()} {
		source := database.VideoSource{ID: identity.NewID(), TenantID: fixture.tenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1, Width: 1, Height: 1, FrameRate: 1, ScreeningStatus: "CLEAR", Status: "RETAINED", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		if err := fixture.h.AdminDB.Create(&source).Error; err != nil {
			t.Fatal(err)
		}
	}

	classify, err := classifyinspection.Setup(classifyinspection.Dependencies{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	classificationEvent := msg05Envelope(t, fixture.tenantID, fixture.inspectionID, "analysis.comparison_completed.v1", map[string]any{"inspectionId": fixture.inspectionID})
	deliverMSG05(t, fixture.h, "vv-it574-classify", classify, classificationEvent)

	var class database.ClassificationRun
	if err := fixture.h.AdminDB.Where("tenant_id=? AND inspection_id=? AND video_plan_id=?", fixture.tenantID, fixture.inspectionID, plan.ID).Take(&class).Error; err != nil {
		t.Fatal(err)
	}
	var classCount int64
	if err := fixture.h.AdminDB.Model(&database.ClassificationRun{}).Where("tenant_id=? AND inspection_id=? AND video_plan_id=?", fixture.tenantID, fixture.inspectionID, plan.ID).Count(&classCount).Error; err != nil || classCount != 1 {
		t.Fatalf("classification runs=%d err=%v; want exactly one for pinned generation", classCount, err)
	}

	createSnapshot, err := generatesnapshot.Setup(generatesnapshot.Dependencies{DB: fixture.h.DB, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	classified, err := handleclassified.Setup(handleclassified.Dependencies{Notifications: msg05NotificationStub{}, CreateSnapshot: createSnapshot, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	classifiedEvent := msg05Envelope(t, fixture.tenantID, fixture.inspectionID, "inspection.classified.v1", map[string]any{"inspectionId": fixture.inspectionID, "classificationRunId": class.ID, "classification": class.Classification, "reasonCodes": json.RawMessage(class.ReasonCodes), "videoPlanId": plan.ID})
	deliverMSG05(t, fixture.h, "vv-it574-classified", classified, classifiedEvent)
	var snapshot database.ReportSnapshot
	if err := fixture.h.AdminDB.Where("tenant_id=? AND inspection_id=?", fixture.tenantID, fixture.inspectionID).Take(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	var snapshotCount int64
	if err := fixture.h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=? AND video_plan_id=?", fixture.tenantID, fixture.inspectionID, plan.ID).Count(&snapshotCount).Error; err != nil || snapshotCount != 1 {
		t.Fatalf("snapshots for generation=%d err=%v; want one", snapshotCount, err)
	}

	render, err := renderpdf.Setup(renderpdf.Dependencies{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	snapshotEvent := msg05Envelope(t, fixture.tenantID, fixture.inspectionID, "report.snapshot_created.v1", map[string]any{"inspectionId": fixture.inspectionID, "snapshotId": snapshot.ID, "version": snapshot.VersionNumber})
	deliverMSG05(t, fixture.h, "vv-it574-render", render, snapshotEvent)
	var pdfJobs int64
	if err := fixture.h.AdminDB.Model(&database.ReportPDFJob{}).Where("tenant_id=? AND snapshot_id=?", fixture.tenantID, snapshot.ID).Count(&pdfJobs).Error; err != nil || pdfJobs != 2 {
		t.Fatalf("PDF jobs=%d err=%v; want one job per audience", pdfJobs, err)
	}

	ready, err := handlereportready.Setup(handlereportready.Dependencies{InAppNotification: func(context.Context, *gorm.DB, events.RawEnvelope) error { return nil }, UpsertTriage: handleclassified.UpsertTriageReviewCase, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	readyEvent := msg05Envelope(t, fixture.tenantID, fixture.inspectionID, "report.ready.v1", map[string]any{"inspectionId": fixture.inspectionID, "snapshotId": snapshot.ID})
	deliverMSG05(t, fixture.h, "vv-it574-ready", ready, readyEvent)
	var projections int64
	if err := fixture.h.AdminDB.Model(&database.DashboardInspection{}).Where("tenant_id=? AND inspection_id=?", fixture.tenantID, fixture.inspectionID).Count(&projections).Error; err != nil || projections != 1 {
		t.Fatalf("dashboard projections=%d err=%v; want exactly one", projections, err)
	}
	var linkedSnapshot database.ReportSnapshot
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, snapshot.ID).Take(&linkedSnapshot).Error; err != nil || linkedSnapshot.VideoPlanID == nil || *linkedSnapshot.VideoPlanID != plan.ID {
		t.Fatalf("snapshot generation link=%v err=%v; want plan %s", linkedSnapshot.VideoPlanID, err, plan.ID)
	}
}

func TestVV_IT_578PrematureLifecycleEventsRemainRetryableUntilPrerequisitesExist(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, true)
	plan := runTask04Plan(t, fixture)

	var job database.ComparisonJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND video_plan_id=?", fixture.tenantID, plan.ID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.AdminDB.Model(&database.ComparisonJob{}).Where("tenant_id=? AND id=?", fixture.tenantID, job.ID).Update("status", "QUEUED").Error; err != nil {
		t.Fatal(err)
	}
	class := database.ClassificationRun{ID: identity.NewID(), TenantID: fixture.tenantID, InspectionID: fixture.inspectionID, VideoPlanID: &plan.ID, PromptSnapshotID: identity.NewID(), Classification: "NORMAL", ReasonCodes: json.RawMessage(`[]`), CreatedAt: time.Now().UTC()}
	if err := fixture.h.AdminDB.Create(&class).Error; err != nil {
		t.Fatal(err)
	}
	classified, err := handleclassified.Setup(handleclassified.Dependencies{Notifications: msg05NotificationStub{}, CreateSnapshot: func(context.Context, generatesnapshot.Input) (generatesnapshot.Result, error) {
		return generatesnapshot.Result{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	classifiedEvent := msg05Envelope(t, fixture.tenantID, fixture.inspectionID, "inspection.classified.v1", map[string]any{"inspectionId": fixture.inspectionID, "classificationRunId": class.ID, "classification": class.Classification, "reasonCodes": json.RawMessage(class.ReasonCodes), "videoPlanId": plan.ID})
	body, err := json.Marshal(classifiedEvent)
	if err != nil {
		t.Fatal(err)
	}
	classifiedConsumer := messaging.Consumer{DB: fixture.h.DB, Registry: events.DefaultRegistry(), Name: "vv-it578-classified", Handle: classified}
	if processed, err := classifiedConsumer.Process(fixture.ctx, body, 0); processed || !errors.Is(err, handleclassified.ErrClassifiedPrerequisitePending) {
		t.Fatalf("premature classified event processed=%t err=%v; want retryable pending error", processed, err)
	}
	var receiptCount int64
	if err := fixture.h.AdminDB.Model(&database.InboxReceipt{}).Where("consumer=? AND event_id=?", "vv-it578-classified", classifiedEvent.ID).Count(&receiptCount).Error; err != nil || receiptCount != 0 {
		t.Fatalf("premature classified event receipts=%d err=%v; want 0", receiptCount, err)
	}
	if err := fixture.h.AdminDB.Model(&database.ComparisonJob{}).Where("tenant_id=? AND id=?", fixture.tenantID, job.ID).Update("status", "COMPLETED").Error; err != nil {
		t.Fatal(err)
	}
	seedTask04ReportVideoSources(t, fixture)
	if processed, err := classifiedConsumer.Process(fixture.ctx, body, 0); !processed || err != nil {
		t.Fatalf("classified event retry after terminal job processed=%t err=%v", processed, err)
	}

	ready, err := handlereportready.Setup(handlereportready.Dependencies{InAppNotification: func(context.Context, *gorm.DB, events.RawEnvelope) error { return nil }, UpsertTriage: handleclassified.UpsertTriageReviewCase})
	if err != nil {
		t.Fatal(err)
	}
	missingSnapshotID := identity.NewID()
	readyEvent := msg05Envelope(t, fixture.tenantID, fixture.inspectionID, "report.ready.v1", map[string]any{"inspectionId": fixture.inspectionID, "snapshotId": missingSnapshotID})
	readyBody, err := json.Marshal(readyEvent)
	if err != nil {
		t.Fatal(err)
	}
	readyConsumer := messaging.Consumer{DB: fixture.h.DB, Registry: events.DefaultRegistry(), Name: "vv-it578-ready", Handle: ready}
	if processed, err := readyConsumer.Process(fixture.ctx, readyBody, 0); processed || err == nil || errors.Is(err, messaging.ErrPermanent) {
		t.Fatalf("report.ready before snapshot processed=%t err=%v; want retryable database error", processed, err)
	}
	if err := fixture.h.AdminDB.Model(&database.InboxReceipt{}).Where("consumer=? AND event_id=?", "vv-it578-ready", readyEvent.ID).Count(&receiptCount).Error; err != nil || receiptCount != 0 {
		t.Fatalf("premature report.ready receipts=%d err=%v; want 0", receiptCount, err)
	}
	var projectionCount int64
	if err := fixture.h.AdminDB.Model(&database.DashboardInspection{}).Where("tenant_id=? AND inspection_id=?", fixture.tenantID, fixture.inspectionID).Count(&projectionCount).Error; err != nil || projectionCount != 0 {
		t.Fatalf("premature report.ready projections=%d err=%v; want 0", projectionCount, err)
	}
	snapshot := database.ReportSnapshot{ID: missingSnapshotID, TenantID: fixture.tenantID, InspectionID: fixture.inspectionID, VideoPlanID: &plan.ID, Mode: "HISTORICAL", Classification: class.Classification, JSONDigest: "digest", HTMLDigest: "digest", VersionNumber: 1, CanonicalJSON: json.RawMessage(`{}`), HTML: json.RawMessage(`{}`), CreatedAt: time.Now().UTC()}
	if err := fixture.h.AdminDB.Create(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	if processed, err := readyConsumer.Process(fixture.ctx, readyBody, 0); !processed || err != nil {
		t.Fatalf("report.ready retry after snapshot processed=%t err=%v", processed, err)
	}
	if err := fixture.h.AdminDB.Model(&database.DashboardInspection{}).Where("tenant_id=? AND inspection_id=?", fixture.tenantID, fixture.inspectionID).Count(&projectionCount).Error; err != nil || projectionCount != 1 {
		t.Fatalf("ready retry projections=%d err=%v; want one", projectionCount, err)
	}
}

func seedTask04ReportVideoSources(t *testing.T, fixture task04PlanFixture) {
	t.Helper()
	var origin database.VideoPreparationJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.originPreparation).Take(&origin).Error; err != nil {
		t.Fatal(err)
	}
	for _, mediaID := range []identity.ID{fixture.currentMediaID(), origin.MediaID} {
		source := database.VideoSource{ID: identity.NewID(), TenantID: fixture.tenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1, Width: 1, Height: 1, FrameRate: 1, ScreeningStatus: "CLEAR", Status: "RETAINED", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		if err := fixture.h.AdminDB.Create(&source).Error; err != nil {
			t.Fatal(err)
		}
	}
}
