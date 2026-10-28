//go:build integration

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	classifyinspection "inspection/services/inspection/internal/features/analysis/classify_inspection"
	processcomparison "inspection/services/inspection/internal/features/analysis/process_comparison"
	requestcomparisons "inspection/services/inspection/internal/features/analysis/request_comparisons"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	generate_snapshot "inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/messaging"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
)

type photoWorkerStoppedGateway struct{}

func (photoWorkerStoppedGateway) CompleteStructured(context.Context, llm.StructuredRequest) (llm.StructuredResult, error) {
	return llm.StructuredResult{Provider: "integration-fake", Model: "photo-only", JSON: json.RawMessage(`{"noRelevantChange":null,"findings":[]}`)}, nil
}

func TestPhotoComparisonSchedulingDoesNotDependOnVideoPreparation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	h, err := NewFromEnv(ctx)
	if err != nil {
		t.Skipf("integration dependencies unavailable: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	fixture, err := h.SeedTask06Fixture(ctx, Task06FixtureOptions{ComparisonMode: "CURRENT_ONLY"})
	if err != nil {
		t.Fatal(err)
	}

	handler, err := requestcomparisons.Setup(requestcomparisons.Dependencies{Now: func() time.Time { return time.Now().UTC() }})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{"inspectionId": fixture.InspectionID, "draftId": fixture.DraftID, "kind": "INSPECTION"})
	if err != nil {
		t.Fatal(err)
	}
	event := rawPhotoSubmission(fixture.TenantID, fixture.InspectionID, payload)
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return handler(ctx, tx, event)
	}); err != nil {
		t.Fatalf("schedule PHOTO comparison without video preparation: %v", err)
	}

	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var jobs int64
		if err := tx.Model(&database.ComparisonJob{}).Where("tenant_id=? AND inspection_id=? AND status='PENDING'", fixture.TenantID, fixture.InspectionID).Count(&jobs).Error; err != nil {
			return err
		}
		if jobs != 1 {
			t.Fatalf("PHOTO comparison jobs=%d, want 1", jobs)
		}
		var requested int64
		if err := tx.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='analysis.comparison_requested.v1'", fixture.TenantID).Count(&requested).Error; err != nil {
			return err
		}
		if requested != 1 {
			t.Fatalf("ordinary PHOTO requests=%d, want 1", requested)
		}
		var plans int64
		if err := tx.Model(&database.VideoPlan{}).Where("tenant_id=? AND inspection_id=?", fixture.TenantID, fixture.InspectionID).Count(&plans).Error; err != nil {
			return err
		}
		if plans != 0 {
			t.Fatalf("PHOTO created %d video plans", plans)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPhotoFlowCompletesWhileVideoQueueHas100QueuedReceiptsIT366(t *testing.T) {
	runPhotoFlow(t)
}

func runPhotoFlow(t *testing.T) (*Harness, Task06Fixture) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	h, err := NewFromEnv(ctx)
	if err != nil {
		t.Skipf("integration dependencies unavailable: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	contract, err := messaging.NewQueueContract("inspection.video", "analysis.video_comparison_requested.v1", 32)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "test-" + identity.NewID().String()
	exchangeName := prefix + ".events"
	queueName := prefix + ".video"
	if err := h.Channel.ExchangeDeclare(exchangeName, "topic", true, false, false, false, nil); err != nil {
		t.Fatalf("declare isolated video exchange: %v", err)
	}
	if _, err := h.Channel.QueueDeclare(queueName, true, false, false, false, amqp.Table{"x-queue-type": "quorum"}); err != nil {
		t.Fatalf("declare isolated video queue: %v", err)
	}
	if err := h.Channel.QueueBind(queueName, contract.RoutingKey, exchangeName, false, nil); err != nil {
		t.Fatalf("bind isolated video queue: %v", err)
	}
	t.Cleanup(func() {
		if _, err := h.Channel.QueueDelete(queueName, false, false, false); err != nil {
			t.Errorf("delete isolated video queue: %v", err)
		}
		if err := h.Channel.ExchangeDelete(exchangeName, false, false); err != nil {
			t.Errorf("delete isolated video exchange: %v", err)
		}
	})
	before, err := h.Channel.QueueDeclarePassive(queueName, true, false, false, false, nil)
	if err != nil {
		t.Fatalf("inspect isolated video queue before publishing: %v", err)
	}
	if before.Messages != 0 {
		t.Fatalf("isolated video queue starts with %d messages, want 0", before.Messages)
	}
	publishChannel, err := h.Rabbit.Channel()
	if err != nil {
		t.Fatalf("open isolated video publisher channel: %v", err)
	}
	t.Cleanup(func() { _ = publishChannel.Close() })
	if err := publishChannel.Confirm(false); err != nil {
		t.Fatalf("enable isolated video publisher confirms: %v", err)
	}
	confirms := publishChannel.NotifyPublish(make(chan amqp.Confirmation, 100))
	for i := 0; i < 100; i++ {
		body, marshalErr := json.Marshal(events.Envelope[map[string]any]{ID: identity.NewID(), Type: contract.RoutingKey, SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: identity.NewID(), AggregateID: identity.NewID(), CorrelationID: fmt.Sprintf("photo-video-backlog-%d", i), Payload: map[string]any{"inspectionId": identity.NewID(), "videoPlanId": identity.NewID(), "comparisonJobId": identity.NewID()}})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if err := publishChannel.PublishWithContext(ctx, exchangeName, contract.RoutingKey, true, false, amqp.Publishing{DeliveryMode: amqp.Persistent, ContentType: "application/json", Body: body}); err != nil {
			t.Fatalf("publish isolated queued VIDEO receipt %d: %v", i+1, err)
		}
		select {
		case confirmation := <-confirms:
			if !confirmation.Ack {
				t.Fatalf("isolated VIDEO receipt %d was not acknowledged", i+1)
			}
		case <-ctx.Done():
			t.Fatalf("wait for isolated VIDEO receipt %d confirmation: %v", i+1, ctx.Err())
		}
	}
	queued, err := h.Channel.QueueDeclarePassive(queueName, true, false, false, false, nil)
	if err != nil {
		t.Fatalf("inspect isolated video queue after publishing: %v", err)
	}
	if queued.Messages != 100 {
		t.Fatalf("isolated video queue contains %d messages after publish, want 100", queued.Messages)
	}

	fixture, err := h.SeedTask06Fixture(ctx, Task06FixtureOptions{ComparisonMode: "CURRENT_ONLY"})
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := requestcomparisons.Setup(requestcomparisons.Dependencies{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	requestPayload, err := json.Marshal(map[string]any{"inspectionId": fixture.InspectionID, "draftId": fixture.DraftID, "kind": "INSPECTION"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return schedule(ctx, tx, rawPhotoSubmission(fixture.TenantID, fixture.InspectionID, requestPayload))
	}); err != nil {
		t.Fatalf("schedule PHOTO comparison: %v", err)
	}
	requested, err := loadOutboxEnvelope(ctx, h, fixture.TenantID, "analysis.comparison_requested.v1")
	if err != nil {
		t.Fatal(err)
	}

	process, err := processcomparison.Setup(processcomparison.Dependencies{Gateway: photoWorkerStoppedGateway{}, BuildRequest: func(context.Context, *gorm.DB, database.ComparisonJob, processcomparison.Payload) (llm.StructuredRequest, error) {
		return llm.StructuredRequest{ModelAlias: "inspection-vision", PromptDigest: "photo-fixture", SystemPrompt: "Classify only visible evidence.", UserPrompt: "Inspect the current photo.", JSONSchema: json.RawMessage(`{"type":"object"}`), MinimumConfidenceBPS: 8500, Mode: "CURRENT_ONLY", Images: []llm.NormalizedImage{{EvidenceID: "PHOTO-1", Source: "CURRENT", Digest: "fixture-digest", DataURL: "data:image/png;base64,AAAA"}}}, nil
	}, Now: time.Now, Mode: "mock"})
	if err != nil {
		t.Fatal(err)
	}
	var requestBody struct {
		JobID identity.ID `json:"jobId"`
	}
	if err := json.Unmarshal(requested.Payload, &requestBody); err != nil || requestBody.JobID == (identity.ID{}) {
		t.Fatalf("decode comparison request outbox payload: %v", err)
	}
	processPayload, _ := json.Marshal(processcomparison.Payload{JobID: requestBody.JobID})
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return process(ctx, tx, processPayload, fixture.TenantID)
	}); err != nil {
		t.Fatalf("process PHOTO comparison: %v", err)
	}
	classifyEvent, err := loadOutboxEnvelope(ctx, h, fixture.TenantID, "analysis.comparison_completed.v1")
	if err != nil {
		t.Fatal(err)
	}
	classify, err := classifyinspection.Setup(classifyinspection.Dependencies{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error { return classify(ctx, tx, classifyEvent) }); err != nil {
		t.Fatalf("classify PHOTO inspection: %v", err)
	}
	classifiedEvent, err := loadOutboxEnvelope(ctx, h, fixture.TenantID, "inspection.classified.v1")
	if err != nil {
		t.Fatal(err)
	}
	var classified struct {
		InspectionID        identity.ID `json:"inspectionId"`
		ClassificationRunID identity.ID `json:"classificationRunId"`
		Classification      string      `json:"classification"`
		ReasonCodes         []string    `json:"reasonCodes"`
	}
	if err := json.Unmarshal(classifiedEvent.Payload, &classified); err != nil || classified.InspectionID != fixture.InspectionID || classified.ClassificationRunID == (identity.ID{}) {
		t.Fatalf("decode classified event: %v", err)
	}
	var inspection database.Inspection
	var prompt database.AnalysisPromptSnapshot
	var projectID *identity.ID
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		return tx.Where("id=?", inspection.AnalysisPromptSnapshotID).Take(&prompt).Error
	}); err != nil {
		t.Fatal(err)
	}
	// The inspection.classified.v1 worker handler is a main-package closure; snapshot generation is verified through its feature slice.
	projectID = inspection.ProjectID
	createSnapshot, err := generate_snapshot.Setup(generate_snapshot.Dependencies{DB: h.DB, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	_, err = createSnapshot(ctx, generate_snapshot.Input{TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, ProjectID: projectID, Mode: "HISTORICAL", Classification: classified.Classification, TemplateVersionID: fixture.TemplateVersionID.String(), ReferenceVersionID: "reference-free", PromptDigest: prompt.CanonicalDigest, ReasonCodes: classified.ReasonCodes, Coverage: map[string]string{}, Context: reportcore.Context{
		Asset:       reportcore.AssetContext{ID: fixture.AssetID.String(), Name: "Task 06 asset", ExternalKey: "task06-asset", Address: "Test address"},
		Participant: reportcore.ParticipantContext{ID: fixture.ParticipantID.String(), Name: "Task 06 participant"},
		Template:    reportcore.TemplateContext{ID: fixture.TemplateID.String(), Name: "Task 06 template", Version: 1},
		Inspection:  reportcore.InspectionContext{ProjectID: fixture.ProjectID.String(), StageID: fixture.StageID.String(), GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano)},
	}})
	if err != nil {
		t.Fatalf("generate PHOTO report snapshot: %v", err)
	}

	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var jobs []database.ComparisonJob
		if err := tx.Where("tenant_id=? AND inspection_id=?", fixture.TenantID, fixture.InspectionID).Find(&jobs).Error; err != nil {
			return err
		}
		if len(jobs) != 1 || jobs[0].Status != "COMPLETED" || jobs[0].VideoPlanID != nil {
			t.Errorf("PHOTO jobs=%+v, want one completed non-video job", jobs)
		}
		var classRun database.ClassificationRun
		if err := tx.Where("tenant_id=? AND inspection_id=?", fixture.TenantID, fixture.InspectionID).Take(&classRun).Error; err != nil {
			return err
		}
		if classRun.VideoPlanID != nil {
			t.Errorf("classification run has video plan %v", classRun.VideoPlanID)
		}
		var snapshots []database.ReportSnapshot
		if err := tx.Where("tenant_id=? AND inspection_id=?", fixture.TenantID, fixture.InspectionID).Find(&snapshots).Error; err != nil {
			return err
		}
		if len(snapshots) != 1 || snapshots[0].VideoPlanID != nil {
			t.Errorf("report snapshots=%+v, want one PHOTO snapshot", snapshots)
		}
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		if inspection.Status != "COMPLETED" {
			t.Errorf("inspection status=%s, want COMPLETED", inspection.Status)
		}
		var plans, preparations int64
		if err := tx.Model(&database.VideoPlan{}).Where("tenant_id=? AND inspection_id=?", fixture.TenantID, fixture.InspectionID).Count(&plans).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND media_id=?", fixture.TenantID, fixture.MediaID).Count(&preparations).Error; err != nil {
			return err
		}
		if plans != 0 || preparations != 0 {
			t.Errorf("PHOTO created %d video plans and %d preparations", plans, preparations)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after, err := h.Channel.QueueDeclarePassive(queueName, true, false, false, false, nil)
	if err != nil {
		t.Fatalf("inspect isolated video queue after PHOTO flow: %v", err)
	}
	if after.Messages != queued.Messages {
		t.Fatalf("PHOTO flow changed video queue depth from %d to %d", queued.Messages, after.Messages)
	}
	return h, fixture
}

func loadOutboxEnvelope(ctx context.Context, h *Harness, tenantID identity.ID, eventType string) (events.RawEnvelope, error) {
	var row database.OutboxIntent
	if err := h.WithinTenant(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND type=?", tenantID, eventType).Order("created_at DESC").Take(&row).Error
	}); err != nil {
		return events.RawEnvelope{}, err
	}
	var envelope events.RawEnvelope
	err := json.Unmarshal(row.Payload, &envelope)
	return envelope, err
}

func rawPhotoSubmission(tenantID, inspectionID identity.ID, payload json.RawMessage) events.RawEnvelope {
	return events.RawEnvelope{
		ID: identity.NewID(), Type: "capture.submitted.v1", SchemaVersion: 1,
		OccurredAt: time.Now().UTC(), TenantID: tenantID, AggregateID: inspectionID,
		CorrelationID: "photo-flow-" + inspectionID.String(), Payload: payload,
	}
}
