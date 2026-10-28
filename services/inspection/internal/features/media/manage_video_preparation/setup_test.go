package manage_video_preparation

import (
	"context"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPreparationStatusAndRetryAreFencedAndAudited(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, schema := range []string{"media", "audit", "analysis", "inspections"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.VideoPreparationJob{}, &database.MediaObject{}, &database.VideoSource{}, &database.VideoFrame{}, &database.AuditEvent{}, &database.VideoPlan{}, &database.VideoPlanPreparation{}, &database.Inspection{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenantID, mediaID, actorID := identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	if err := db.Create(&database.MediaObject{ID: mediaID, TenantID: tenantID, ResponsibilityID: identity.NewID(), ObjectKey: "tenant/video/source", ContentType: "video/mp4", SHA256: "digest", SizeBytes: 10, Status: "VERIFIED", MediaKind: "VIDEO", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	submissionID := identity.NewID()
	job := database.VideoPreparationJob{ID: identity.NewID(), TenantID: tenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "FAILED", Phase: "SCREENING", PreparationVersion: 1, Attempts: 4, NextAttemptAt: now, ErrorCode: "STORE_UNAVAILABLE", CreatedAt: now}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
	if err := Setup(Dependencies{DB: db, Bus: bus, Authorizer: auth.Authorizer{}, Within: within}); err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, CorrelationID: "video-preparation-test", Principal: requestctx.Principal{IdentityID: actorID, TenantID: tenantID, Roles: []string{auth.TenantAdmin}}})
	statusRaw, err := bus.Ask(ctx, Query{TenantID: tenantID, MediaID: mediaID})
	if err != nil {
		t.Fatal(err)
	}
	status := statusRaw.(Status)
	if status.Stage != "SCREENING" || status.ErrorCode != "STORE_UNAVAILABLE" || !status.Retryable || !status.SourceAvailable || status.PreparationVersion != 1 {
		t.Fatalf("unexpected status: %+v", status)
	}
	if _, err := bus.Ask(context.Background(), CaptureQuery{TenantID: tenantID, MediaID: mediaID}); err == nil {
		t.Fatal("capture preparation query accepted without external credentials")
	}
	external := requestctx.WithExternalCredentials(context.Background(), requestctx.ExternalCredentials{SessionToken: "validated-at-boundary", CSRFToken: "proof"})
	captureRaw, err := bus.Ask(external, CaptureQuery{TenantID: tenantID, MediaID: mediaID})
	if err != nil || captureRaw.(Status).Stage != status.Stage {
		t.Fatalf("external capture preparation read failed: status=%v err=%v", captureRaw, err)
	}
	retriedRaw, err := bus.Send(ctx, RetryCommand{TenantID: tenantID, MediaID: mediaID, ExpectedPreparationVersion: 1, MutationID: "retry-1"})
	if err != nil {
		t.Fatal(err)
	}
	retried := retriedRaw.(Status)
	if retried.Stage != "SCREENING" || retried.PreparationVersion != 2 || retried.ErrorCode != "" || retried.Attempts != 0 {
		t.Fatalf("unexpected retry: %+v", retried)
	}
	if _, err := bus.Send(ctx, RetryCommand{TenantID: tenantID, MediaID: mediaID, ExpectedPreparationVersion: 1, MutationID: "retry-2"}); err == nil {
		t.Fatal("stale preparation version was accepted")
	}
	readyMediaID := identity.NewID()
	if err := db.Create(&database.MediaObject{ID: readyMediaID, TenantID: tenantID, ResponsibilityID: identity.NewID(), ObjectKey: "tenant/video/ready", ContentType: "video/mp4", SHA256: "ready-digest", SizeBytes: 10, Status: "READY", MediaKind: "VIDEO", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	readyJob := database.VideoPreparationJob{ID: identity.NewID(), TenantID: tenantID, MediaID: readyMediaID, Status: "READY", Phase: "READY", PreparationVersion: 7, FirstReadyAt: &now, CreatedAt: now}
	if err := db.Create(&readyJob).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Send(ctx, RetryCommand{TenantID: tenantID, MediaID: readyMediaID, ExpectedPreparationVersion: 7, MutationID: "retry-ready"}); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("retry of READY source should return INVALID_STATE, got %v", err)
	}
	anchor := now.Add(-24 * time.Hour)
	if err := db.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", tenantID, readyJob.ID).Update("first_ready_at", anchor).Error; err != nil {
		t.Fatal(err)
	}
	profileDigest := video.DefaultProfile().Digest()
	command := ReprocessCommand{TenantID: tenantID, MediaID: readyMediaID, ExpectedPreparationVersion: 7, ProfileDigest: profileDigest, MutationID: "reprocess-ready"}
	reprocessedRaw, err := bus.Send(ctx, command)
	if err != nil {
		t.Fatalf("explicit reprocess of READY source: %v", err)
	}
	reprocessed := reprocessedRaw.(Status)
	if reprocessed.PreparationID == readyJob.ID || reprocessed.Stage != "RECEIVED" || reprocessed.FirstFrameReadyAt == nil || !reprocessed.FirstFrameReadyAt.Equal(anchor) {
		t.Fatalf("reprocess did not create a new preparation with preserved anchor: %+v", reprocessed)
	}
	var generation database.VideoPreparationJob
	if err := db.Where("tenant_id=? AND id=?", tenantID, reprocessed.PreparationID).Take(&generation).Error; err != nil || generation.Generation != 1 || generation.ProfileDigest != profileDigest {
		t.Fatalf("reprocessed row=%+v err=%v", generation, err)
	}
	replayRaw, err := bus.Send(ctx, command)
	if err != nil || replayRaw.(Status).PreparationID != reprocessed.PreparationID {
		t.Fatalf("reprocess replay = %v, %v; want original preparation %s", replayRaw, err, reprocessed.PreparationID)
	}
	if err := db.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", tenantID, reprocessed.PreparationID).Update("status", "READY").Error; err != nil {
		t.Fatal(err)
	}
	nextGenerationRaw, err := bus.Send(ctx, ReprocessCommand{TenantID: tenantID, MediaID: readyMediaID, ExpectedPreparationVersion: 1, ProfileDigest: profileDigest, MutationID: "reprocess-ready-again"})
	if err != nil {
		t.Fatalf("second generation with same profile: %v", err)
	}
	var nextGeneration database.VideoPreparationJob
	if err := db.Where("tenant_id=? AND id=?", tenantID, nextGenerationRaw.(Status).PreparationID).Take(&nextGeneration).Error; err != nil || nextGeneration.Generation != 2 {
		t.Fatalf("same-profile generation=%+v err=%v, want generation 2", nextGeneration, err)
	}
	if _, err := bus.Send(ctx, ReprocessCommand{TenantID: tenantID, MediaID: readyMediaID, ExpectedPreparationVersion: 7, ProfileDigest: "unknown", MutationID: "reprocess-unknown"}); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("unknown profile should return INVALID_INPUT, got %v", err)
	}
	if _, err := bus.Send(ctx, ReprocessCommand{TenantID: tenantID, MediaID: readyMediaID, ExpectedPreparationVersion: 6, ProfileDigest: profileDigest, MutationID: "reprocess-stale"}); !apperror.Is(err, apperror.Conflict) {
		t.Fatalf("stale reprocess should return CONFLICT, got %v", err)
	}
	activeMediaID := identity.NewID()
	if err := db.Create(&database.MediaObject{ID: activeMediaID, TenantID: tenantID, ResponsibilityID: identity.NewID(), ObjectKey: "tenant/video/active", ContentType: "video/mp4", SHA256: "active-digest", SizeBytes: 10, Status: "VERIFIED", MediaKind: "VIDEO", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.VideoPreparationJob{ID: identity.NewID(), TenantID: tenantID, MediaID: activeMediaID, Status: "PROCESSING", Phase: "EXTRACTING", PreparationVersion: 1, Generation: 1, ProfileDigest: profileDigest, NextAttemptAt: now, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Send(ctx, ReprocessCommand{TenantID: tenantID, MediaID: activeMediaID, ExpectedPreparationVersion: 1, ProfileDigest: profileDigest, MutationID: "reprocess-active"}); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("active reprocess should return INVALID_STATE, got %v", err)
	}
	expiredMediaID := identity.NewID()
	if err := db.Create(&database.MediaObject{ID: expiredMediaID, TenantID: tenantID, ResponsibilityID: identity.NewID(), ObjectKey: "tenant/video/expired", ContentType: "video/mp4", SHA256: "expired-digest", SizeBytes: 10, Status: "VERIFIED", MediaKind: "VIDEO", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.VideoPreparationJob{ID: identity.NewID(), TenantID: tenantID, MediaID: expiredMediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, ProfileDigest: profileDigest, NextAttemptAt: now, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.VideoSource{ID: identity.NewID(), TenantID: tenantID, MediaID: expiredMediaID, ScreeningStatus: "APPROVED", Status: "EXPIRED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Send(ctx, ReprocessCommand{TenantID: tenantID, MediaID: expiredMediaID, ExpectedPreparationVersion: 1, ProfileDigest: profileDigest, MutationID: "reprocess-expired"}); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("expired original requiring decode should return INVALID_STATE, got %v", err)
	}
	anchor, expiry := now.Add(-48*time.Hour), now.Add(28*24*time.Hour)
	if err := db.Create(&database.VideoSource{ID: identity.NewID(), TenantID: tenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 640, Height: 480, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &anchor, RetentionDays: ptr(30), SourceExpiryAt: &expiry, Status: "EXPIRED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.VideoFrame{ID: identity.NewID(), TenantID: tenantID, SourceMediaID: mediaID, PreparationID: job.ID, PTSMicros: 0, SHA256: "frame-digest", ObjectKey: "tenant/frame/0", Width: 640, Height: 480, Role: "BASELINE", SizeBytes: 10, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", tenantID, job.ID).Updates(map[string]any{"status": "FAILED", "phase": "DISCOVERING", "preparation_version": 2, "attempts": 4, "first_ready_at": anchor, "error_code": "PROVIDER_UNAVAILABLE"}).Error; err != nil {
		t.Fatal(err)
	}
	expiredRaw, err := bus.Ask(ctx, Query{TenantID: tenantID, MediaID: mediaID})
	if err != nil {
		t.Fatal(err)
	}
	expired := expiredRaw.(Status)
	if expired.SourceAvailable || !expired.Retryable || expired.FirstFrameReadyAt == nil || !expired.FirstFrameReadyAt.Equal(anchor) {
		t.Fatalf("expired-source status lost retained-bank recovery: %+v", expired)
	}
	recoveredRaw, err := bus.Send(ctx, RetryCommand{TenantID: tenantID, MediaID: mediaID, ExpectedPreparationVersion: 2, MutationID: "retry-3"})
	if err != nil {
		t.Fatalf("retry with retained bank: %v", err)
	}
	recovered := recoveredRaw.(Status)
	if recovered.Stage != "DISCOVERING" || recovered.PreparationVersion != 3 {
		t.Fatalf("retry did not resume semantic checkpoint: %+v", recovered)
	}
	var audits int64
	if err := db.Model(&database.AuditEvent{}).Where("tenant_id=? AND action=?", tenantID, "video.preparation_retried").Count(&audits).Error; err != nil || audits != 2 {
		t.Fatalf("retry audit count=%d err=%v", audits, err)
	}
}

func ptr[T any](value T) *T { return &value }
