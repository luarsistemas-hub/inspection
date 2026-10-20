package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestIT191IT194IT195IT197FalsePositiveIsScopedAuditedAndIdempotent(t *testing.T) {
	f := newFalsePositiveFixture(t)
	if err := f.service.DeclareFalsePositive(context.Background(), f.tenantID, f.responsibilityID, f.mediaID, " "); code(err) != apperror.InvalidInput {
		t.Fatalf("blank false-positive reason accepted: %v", err)
	}
	if err := f.service.DeclareFalsePositive(context.Background(), f.tenantID, identity.NewID(), f.mediaID, "not mine"); code(err) != apperror.NotFound && code(err) != apperror.InvalidState {
		t.Fatalf("foreign responsibility changed media: %v", err)
	}
	if err := f.service.DeclareFalsePositive(context.Background(), f.tenantID, f.responsibilityID, f.mediaID, "participant reviewed image"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeclareFalsePositive(context.Background(), f.tenantID, f.responsibilityID, f.mediaID, "participant reviewed image"); err != nil {
		t.Fatalf("same declaration was not idempotent: %v", err)
	}
	if err := f.service.DeclareFalsePositive(context.Background(), f.tenantID, f.responsibilityID, f.mediaID, "different reason"); code(err) != apperror.Conflict {
		t.Fatalf("conflicting declaration was accepted: %v", err)
	}
	var media database.MediaObject
	if err := f.db.First(&media, "id=?", f.mediaID).Error; err != nil || media.Status != "READY" || !strings.Contains(string(media.Flags), "SENSITIVE_CONTENT_FALSE_POSITIVE") {
		t.Fatalf("false-positive media state is wrong: %+v %v", media, err)
	}
	var run database.ScreeningRun
	if err := f.db.First(&run, "media_id=?", f.mediaID).Error; err != nil || run.Status != "OVERRIDDEN" || run.FalsePositiveReason == "" {
		t.Fatalf("screening override was not persisted: %+v %v", run, err)
	}
	var audits int64
	if err := f.db.Model(&database.AuditEvent{}).Where("target_id=?", f.mediaID.String()).Count(&audits).Error; err != nil || audits != 1 {
		t.Fatalf("audit count=%d err=%v", audits, err)
	}
}

func TestSensitiveVideoFalsePositiveAtomicallyResumesSubmittedInspection(t *testing.T) {
	f := newFalsePositiveFixture(t)
	for _, schema := range []string{"inspections", "analysis"} {
		if err := f.db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.Responsibility{}, &database.Inspection{}, &database.VideoSource{}, &database.VideoPreparationJob{}, &database.VideoPlan{}} {
		if err := f.db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	now := time.Unix(2000, 0).UTC()
	actorID := identity.NewID()
	inspectionID, prepID, planID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := f.db.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", f.tenantID, f.responsibilityID).Update("status", "SUBMITTED").Error; err != nil {
		t.Fatal(err)
	}
	inspection := database.Inspection{ID: inspectionID, TenantID: f.tenantID, BusinessUnitID: identity.NewID(), AssetID: identity.NewID(), ParticipantID: identity.NewID(), TemplateID: identity.NewID(), TemplateVersionID: identity.NewID(), Source: "MANUAL", SourceKey: "source-key", Status: "REVIEW_REQUIRED", MediaKind: "VIDEO", DueAt: now, DeadlineAt: now.Add(time.Hour), ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{}`), Version: 1, CreatedAt: now, UpdatedAt: now}
	responsibility := database.Responsibility{ID: f.responsibilityID, TenantID: f.tenantID, InspectionID: inspectionID, ParticipantID: inspection.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}
	plan := database.VideoPlan{ID: planID, TenantID: f.tenantID, InspectionID: inspectionID, SubmissionID: identity.NewID(), CurrentPreparationID: prepID, Generation: 1, PlanDigest: "plan-digest", Status: "BLOCKED", NextAttemptAt: now, ErrorCode: "SENSITIVE_CONTENT", CreatedAt: now}
	job := database.VideoPreparationJob{ID: prepID, TenantID: f.tenantID, MediaID: f.mediaID, Status: "FAILED", Phase: "SCREENING", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, ErrorCode: "SENSITIVE_CONTENT", CreatedAt: now}
	source := database.VideoSource{ID: identity.NewID(), TenantID: f.tenantID, MediaID: f.mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 640, Height: 480, FrameRate: 30, ScreeningStatus: "BLOCKED", Status: "RETAINED", CreatedAt: now, UpdatedAt: now}
	media := database.MediaObject{ID: f.mediaID, TenantID: f.tenantID, ResponsibilityID: f.responsibilityID, ObjectKey: "video/source", ContentType: "video/mp4", SHA256: strings.Repeat("b", 64), SizeBytes: 100, Status: "SCREENED", MediaKind: "VIDEO", Flags: json.RawMessage(`[]`)}
	for _, row := range []any{&inspection, &responsibility, &plan, &job, &source, &media} {
		if err := f.db.Save(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: f.tenantID, CorrelationID: "review-correlation", Principal: requestctx.Principal{IdentityID: actorID, TenantID: f.tenantID, Internal: true}})
	if err := f.service.DeclareFalsePositive(ctx, f.tenantID, f.responsibilityID, f.mediaID, "Reviewed and cleared"); err != nil {
		t.Fatal(err)
	}
	var gotJob database.VideoPreparationJob
	var gotPlan database.VideoPlan
	var gotInspection database.Inspection
	var gotSource database.VideoSource
	var audit database.AuditEvent
	for _, check := range []struct {
		name string
		row  any
		id   identity.ID
	}{{"job", &gotJob, prepID}, {"plan", &gotPlan, planID}, {"inspection", &gotInspection, inspectionID}, {"source", &gotSource, source.ID}} {
		if err := f.db.First(check.row, "id=?", check.id).Error; err != nil {
			t.Fatalf("load %s: %v", check.name, err)
		}
	}
	if gotJob.Status != "QUEUED" || gotJob.PreparationVersion != 2 || gotPlan.Status != "BUILDING" || gotPlan.ErrorCode != "" || gotInspection.Status != "ANALYZING" || gotInspection.Version != 2 || gotSource.ScreeningStatus != "APPROVED" {
		t.Fatalf("atomic video resume incomplete: job=%+v plan=%+v inspection=%+v source=%+v", gotJob, gotPlan, gotInspection, gotSource)
	}
	if err := f.db.Where("target_id=? AND action=?", f.mediaID.String(), "media.sensitive_false_positive_declared").Take(&audit).Error; err != nil || audit.ActorID != actorID || audit.CorrelationID != "review-correlation" {
		t.Fatalf("review audit actor/correlation wrong: %+v %v", audit, err)
	}
}

func TestSensitiveVideoFalsePositiveUnavailableSourceLeavesReviewUnchanged(t *testing.T) {
	f := newFalsePositiveFixture(t)
	for _, schema := range []string{"inspections", "analysis"} {
		if err := f.db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.Responsibility{}, &database.Inspection{}, &database.VideoSource{}, &database.VideoPreparationJob{}, &database.VideoPlan{}} {
		if err := f.db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	now := time.Unix(2000, 0).UTC()
	inspectionID, prepID, planID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := f.db.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", f.tenantID, f.responsibilityID).Update("status", "SUBMITTED").Error; err != nil {
		t.Fatal(err)
	}
	inspection := database.Inspection{ID: inspectionID, TenantID: f.tenantID, BusinessUnitID: identity.NewID(), AssetID: identity.NewID(), ParticipantID: identity.NewID(), TemplateID: identity.NewID(), TemplateVersionID: identity.NewID(), Source: "MANUAL", SourceKey: "source-key", Status: "REVIEW_REQUIRED", MediaKind: "VIDEO", DueAt: now, DeadlineAt: now.Add(time.Hour), ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{}`), Version: 1, CreatedAt: now, UpdatedAt: now}
	responsibility := database.Responsibility{ID: f.responsibilityID, TenantID: f.tenantID, InspectionID: inspectionID, ParticipantID: inspection.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}
	plan := database.VideoPlan{ID: planID, TenantID: f.tenantID, InspectionID: inspectionID, SubmissionID: identity.NewID(), CurrentPreparationID: prepID, Generation: 1, PlanDigest: "plan-digest", Status: "BLOCKED", NextAttemptAt: now, ErrorCode: "SENSITIVE_CONTENT", CreatedAt: now}
	job := database.VideoPreparationJob{ID: prepID, TenantID: f.tenantID, MediaID: f.mediaID, Status: "FAILED", Phase: "SCREENING", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, ErrorCode: "SENSITIVE_CONTENT", CreatedAt: now}
	source := database.VideoSource{ID: identity.NewID(), TenantID: f.tenantID, MediaID: f.mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 640, Height: 480, FrameRate: 30, ScreeningStatus: "BLOCKED", Status: "DELETING", CreatedAt: now, UpdatedAt: now}
	media := database.MediaObject{ID: f.mediaID, TenantID: f.tenantID, ResponsibilityID: f.responsibilityID, ObjectKey: "video/source", ContentType: "video/mp4", SHA256: strings.Repeat("b", 64), SizeBytes: 100, Status: "SCREENED", MediaKind: "VIDEO", Flags: json.RawMessage(`[]`)}
	for _, row := range []any{&inspection, &responsibility, &plan, &job, &source, &media} {
		if err := f.db.Save(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	err := f.service.DeclareFalsePositive(context.Background(), f.tenantID, f.responsibilityID, f.mediaID, "Reviewed and cleared")
	if code(err) != apperror.SourceUnavailable {
		t.Fatalf("unavailable source error=%v; want SOURCE_UNAVAILABLE", err)
	}
	var gotRun database.ScreeningRun
	var gotSource database.VideoSource
	var gotPlan database.VideoPlan
	var gotInspection database.Inspection
	if err := f.db.First(&gotRun, "media_id=?", f.mediaID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&gotSource, "id=?", source.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&gotPlan, "id=?", planID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&gotInspection, "id=?", inspectionID).Error; err != nil {
		t.Fatal(err)
	}
	if gotRun.Status != "BLOCKED" || gotSource.ScreeningStatus != "BLOCKED" || gotPlan.Status != "BLOCKED" || gotInspection.Status != "REVIEW_REQUIRED" {
		t.Fatalf("unavailable original changed review state: run=%s source=%s plan=%s inspection=%s", gotRun.Status, gotSource.ScreeningStatus, gotPlan.Status, gotInspection.Status)
	}
}

type falsePositiveFixture struct {
	db                                  *gorm.DB
	service                             Service
	tenantID, responsibilityID, mediaID identity.ID
}

func newFalsePositiveFixture(t *testing.T) falsePositiveFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"capture", "media", "audit"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.CaptureDraft{}, &database.MediaObject{}, &database.ScreeningRun{}, &database.AuditEvent{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	now := time.Unix(1000, 0).UTC()
	tenantID, responsibilityID, mediaID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, Kind: "INSPECTION", TemplateVersionID: identity.NewID(), ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[]`), Status: "OPEN", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.MediaObject{ID: mediaID, TenantID: tenantID, ResponsibilityID: responsibilityID, ObjectKey: "private/original", ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 10, Status: "SCREENED", Flags: json.RawMessage(`[]`)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.ScreeningRun{ID: identity.NewID(), TenantID: tenantID, MediaID: mediaID, Status: "BLOCKED", ModelDigest: "model", Regions: json.RawMessage(`[{"kind":"FACE"}]`), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	return falsePositiveFixture{db: db, service: Service{DB: db, Now: func() time.Time { return now }, Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}}, tenantID: tenantID, responsibilityID: responsibilityID, mediaID: mediaID}
}
