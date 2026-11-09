//go:build integration

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	classifyinspection "inspection/services/inspection/internal/features/analysis/classify_inspection"
	"inspection/services/inspection/internal/features/analysis/prompt"
	notificationrequest "inspection/services/inspection/internal/features/notifications/request"
	generatesnapshot "inspection/services/inspection/internal/features/reports/generate_snapshot"
	handleclassified "inspection/services/inspection/internal/features/reports/handle_classified"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/notifications"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TestTask04VideoInspectionWithPinnedScopeKeepsLegacyReport creates a VIDEO
// inspection through the real Service.Create (which pins a scope_snapshots
// row), classifies its sealed plan with the real classifier and finalizes it
// with the real classified handler. The report must stay v1/v2: no checklist,
// no v3 sections, and the tenant's own publication policy (MANUAL here) instead
// of the automatic publication reserved for descriptive v3 reports.
func TestTask04VideoInspectionWithPinnedScopeKeepsLegacyReport(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	for _, task := range []prompt.Task{prompt.InventoryCatalog, prompt.InventoryCompare} {
		definition, digest, err := prompt.CanonicalizeTask(string(task), prompt.DefaultDefinitionForTask(string(task)))
		if err != nil {
			t.Fatal(err)
		}
		row := database.AnalysisPrompt{ID: identity.NewID(), AnalysisType: prompt.RealEstate, Task: string(task), DefinitionJSON: definition, CanonicalDigest: digest, Revision: 1, UpdatedBy: identity.NewID(), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		if err := h.AdminDB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "analysis_type"}, {Name: "task"}}, DoNothing: true}).Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := h.AdminDB.Exec("INSERT INTO platform.video_admission (id, enabled, version, changed_at) VALUES (1, FALSE, 1, ?) ON CONFLICT (id) DO UPDATE SET enabled=FALSE", time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}

	fx := newTask3CreateFixture(t, h, "VIDEO", 2, false)
	view := fx.create(t, catalog.ChecklistOnly, nil, "task04-video-"+identity.NewID().String())
	tenantID, inspectionID := fx.seed.TenantID, view.Inspection.ID
	var pinned database.InspectionScopeSnapshot
	if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Take(&pinned).Error; err != nil {
		t.Fatalf("Service.Create did not pin the video inspection scope: %v", err)
	}
	var inspection database.Inspection
	if err := h.AdminDB.Where("tenant_id=? AND id=?", tenantID, inspectionID).Take(&inspection).Error; err != nil || inspection.MediaKind != "VIDEO" {
		t.Fatalf("created inspection media kind=%q err=%v", inspection.MediaKind, err)
	}

	// The fixture resolves the template through the mediator only; the report
	// context reads it from the database, so persist the same template rows.
	if err := h.WithinTenant(ctx, tenantID, func(tx *gorm.DB) error {
		createdAt := time.Now().UTC()
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&database.Template{ID: fx.version.Template.ID, TenantID: tenantID, Key: "task04-video-" + fx.version.Template.ID.String(), Name: "Modelo de vídeo", SegmentVersionID: fx.seed.SegmentVersionID, ActiveVersionID: &fx.templateVersionID, Version: 1, CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			return err
		}
		definition := fx.version.Version.DefinitionJSON
		sum := sha256.Sum256(definition)
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&database.TemplateVersion{ID: fx.templateVersionID, TenantID: tenantID, TemplateID: fx.version.Template.ID, VersionNumber: 1, SchemaVersion: 2, DefinitionJSON: definition, CanonicalDigest: hex.EncodeToString(sum[:]), Status: "ACTIVE", IdempotencyKey: "task04-video-" + fx.templateVersionID.String(), PublishedAt: createdAt, CreatedBy: fx.seed.IdentityID}).Error
	}); err != nil {
		t.Fatal(err)
	}

	// Seed the submitted capture and a sealed plan whose only component ended
	// INCONCLUSIVE, the smallest terminal video plan the classifier accepts.
	now := time.Now().UTC().Truncate(time.Microsecond)
	digest := strings.Repeat("ab", 32)
	mediaID, submissionID, preparationID, frameID, componentID, planID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	if err := h.WithinTenant(ctx, tenantID, func(tx *gorm.DB) error {
		var draft database.CaptureDraft
		if err := tx.Where("tenant_id=? AND responsibility_id=?", tenantID, view.Responsibility.ID).Take(&draft).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", tenantID, draft.ID).Update("status", "SUBMITTED").Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: tenantID, ResponsibilityID: view.Responsibility.ID, ObjectKey: "task04-video/" + mediaID.String(), ContentType: "video/mp4", SHA256: digest, SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "task04-video-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"mediaKind": "VIDEO", "mediaId": mediaID})
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: tenantID, DraftID: draft.ID, ResponsibilityID: view.Responsibility.ID, VersionNumber: 1, Complete: true, Payload: payload, PayloadDigest: digest, SubmittedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: tenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, Attempts: 1, NextAttemptAt: now, ManifestDigest: digest, PendingObjectKeys: []byte("[]"), FirstReadyAt: &now, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: tenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: digest, ObjectKey: "task04-video/frame-" + frameID.String() + ".jpg", Width: 1, Height: 1, Role: "BASELINE", SizeBytes: 1, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoScopeItem{ID: componentID, TenantID: tenantID, PreparationID: preparationID, FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlan{ID: planID, TenantID: tenantID, InspectionID: inspectionID, SubmissionID: submissionID, CurrentPreparationID: preparationID, Generation: 1, PlanDigest: digest, Status: "SEALED", NextAttemptAt: now, SealedAt: &now, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlanItem{ID: identity.NewID(), TenantID: tenantID, PlanID: planID, ComponentID: componentID, RequirementKey: "video:kitchen", Outcome: "INCONCLUSIVE", Reason: "", InputDigest: digest, OriginFrameIDs: json.RawMessage(`[]`), CurrentFrameIDs: json.RawMessage(`[]`), CoreGroups: json.RawMessage(`[]`), CurrentGroups: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", tenantID, inspectionID).Updates(map[string]any{"active_video_plan_id": planID, "status": "SUBMITTED"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Where("tenant_id=?", tenantID).Delete(&database.PublicationPolicy{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Create(&database.PublicationPolicy{ID: identity.NewID(), TenantID: tenantID, Mode: "MANUAL", Version: 9, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}

	classify, err := classifyinspection.Setup(classifyinspection.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	classifyPayload, _ := json.Marshal(map[string]any{"inspectionId": inspectionID})
	if err := h.WithinTenant(ctx, tenantID, func(tx *gorm.DB) error {
		return classify(ctx, tx, events.RawEnvelope{ID: identity.NewID(), Type: "analysis.comparison_completed.v1", SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: inspectionID, CorrelationID: "task04-video-classify", Payload: classifyPayload})
	}); err != nil {
		t.Fatalf("real classifier rejected the sealed video plan: %v", err)
	}
	var run database.ClassificationRun
	if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Order("created_at DESC").Take(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.VideoPlanID == nil || *run.VideoPlanID != planID {
		t.Fatalf("classification run video plan=%v, want %s", run.VideoPlanID, planID)
	}

	providers, err := notifications.NewProviderResolver(notifications.ProviderTwilio)
	if err != nil {
		t.Fatal(err)
	}
	notificationService, err := notificationrequest.Setup(notificationrequest.Dependencies{DB: h.DB, Providers: providers, EnabledChannels: map[string]bool{"EMAIL": true}})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := handleclassified.Setup(handleclassified.Dependencies{Notifications: notificationService, CreateSnapshotInTx: func(ctx context.Context, tx *gorm.DB, input generatesnapshot.Input) (generatesnapshot.Result, error) {
		return generatesnapshot.CreateInTx(ctx, tx, input, time.Now())
	}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(events.Envelope[events.InspectionClassifiedPayload]{ID: identity.NewID(), Type: "inspection.classified.v1", SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: inspectionID, CorrelationID: "task04-video-report", Payload: events.InspectionClassifiedPayload{InspectionID: inspectionID, ClassificationRunID: run.ID, VideoPlanID: &planID, Classification: run.Classification, ReasonCodes: []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	// Round 8: a video report whose frames fail integrity keeps the historical
	// dead-letter behavior (messaging.ErrPermanent, no receipt, nothing
	// committed); only v3 photo reports are acknowledged as permanent failures.
	failingHandler, err := handleclassified.Setup(handleclassified.Dependencies{Notifications: notificationService, CreateSnapshotInTx: func(context.Context, *gorm.DB, generatesnapshot.Input) (generatesnapshot.Result, error) {
		return generatesnapshot.Result{}, generatesnapshot.ErrFrameIntegrity
	}})
	if err != nil {
		t.Fatal(err)
	}
	failingEventID := identity.NewID()
	failingBody, err := json.Marshal(events.Envelope[events.InspectionClassifiedPayload]{ID: failingEventID, Type: "inspection.classified.v1", SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: inspectionID, CorrelationID: "task04-video-frame-integrity", Payload: events.InspectionClassifiedPayload{InspectionID: inspectionID, ClassificationRunID: run.ID, VideoPlanID: &planID, Classification: run.Classification, ReasonCodes: []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	failingConsumer := messaging.Consumer{DB: h.DB, Registry: events.DefaultRegistry(), Name: "task04-video-frame-integrity", Handle: failingHandler}
	if processed, err := failingConsumer.Process(ctx, failingBody, 0); processed || !errors.Is(err, messaging.ErrPermanent) {
		t.Fatalf("video frame integrity failure processed=%v err=%v; want messaging.ErrPermanent (dead letter) as before v3", processed, err)
	}
	var receipts, snapshots int64
	if err := h.AdminDB.Model(&database.InboxReceipt{}).Where("tenant_id=? AND event_id=?", tenantID, failingEventID).Count(&receipts).Error; err != nil || receipts != 0 {
		t.Fatalf("dead-lettered video event left %d receipts (err=%v)", receipts, err)
	}
	if err := h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Count(&snapshots).Error; err != nil || snapshots != 0 {
		t.Fatalf("dead-lettered video event committed %d snapshots (err=%v)", snapshots, err)
	}

	consumer := messaging.Consumer{DB: h.DB, Registry: events.DefaultRegistry(), Name: "task04-video-report", Handle: handler}
	if processed, err := consumer.Process(ctx, body, 0); err != nil || !processed {
		t.Fatalf("classified video report processed=%v err=%v", processed, err)
	}

	var stored database.ReportSnapshot
	if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Take(&stored).Error; err != nil {
		t.Fatalf("video classification did not produce a report: %v", err)
	}
	if stored.SchemaVersion != 1 && stored.SchemaVersion != 2 {
		t.Fatalf("video report schema=%d, want legacy v1/v2", stored.SchemaVersion)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(stored.CanonicalJSON, &document); err != nil {
		t.Fatal(err)
	}
	for _, section := range []string{"checklist", "processing", "scope", "handover", "provenance", "verificationCode"} {
		if _, present := document[section]; present {
			t.Fatalf("video report carries v3 section %q: %s", section, stored.CanonicalJSON)
		}
	}
	if stored.PublicationPolicyVersion != 9 {
		t.Fatalf("video report policy version=%d, want the tenant policy version 9", stored.PublicationPolicyVersion)
	}
	var publications int64
	if err := h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND snapshot_id=?", tenantID, stored.ID).Count(&publications).Error; err != nil {
		t.Fatal(err)
	}
	if publications != 0 {
		t.Fatalf("MANUAL tenant policy was overridden: video report published %d times", publications)
	}
}
