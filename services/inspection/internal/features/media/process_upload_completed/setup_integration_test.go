//go:build integration

package process_upload_completed

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

func receiptBody(t *testing.T, tenantID, mediaID, responsibilityID identity.ID, status string, extra map[string]any) []byte {
	t.Helper()
	payload := map[string]any{"mediaId": mediaID, "responsibilityId": responsibilityID, "status": status}
	for key, value := range extra {
		payload[key] = value
	}
	body, err := json.Marshal(events.Envelope[map[string]any]{ID: identity.NewID(), Type: "media.upload_completed.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: tenantID, AggregateID: mediaID, CorrelationID: "qa-receipt-" + mediaID.String(), Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestVideoReceiptInboxRoutesOnceAndPhotoRemainsPhotoIT554IT555IT556IT558(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	owner, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := Setup(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	consumer := messaging.Consumer{DB: h.AdminDB, Registry: events.DefaultRegistry(), Name: "qa-media-upload-completed", Handle: handler}
	photo := receiptBody(t, owner.TenantID, owner.MediaID, owner.ResponsibilityID, "VERIFIED", nil)
	if processed, err := consumer.Process(ctx, photo, 0); err != nil || !processed {
		t.Fatalf("photo receipt: processed=%t err=%v", processed, err)
	}
	mediaID, attemptID := identity.NewID(), identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := h.WithinTenant(ctx, owner.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: owner.TenantID, ResponsibilityID: owner.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "qa-receipt-" + mediaID.String(), PayloadDigest: strings.Repeat("a", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.MediaObject{ID: mediaID, TenantID: owner.TenantID, ResponsibilityID: owner.ResponsibilityID, ObjectKey: "qa-receipt/" + mediaID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("a", 64), SizeBytes: 123, Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "qa-media-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	valid := receiptBody(t, owner.TenantID, mediaID, owner.ResponsibilityID, "RECEIVED_PENDING_VALIDATION", nil)
	for _, item := range []struct {
		name string
		body []byte
	}{
		{"missing ID", receiptBody(t, owner.TenantID, identity.ID{}, owner.ResponsibilityID, "RECEIVED_PENDING_VALIDATION", nil)},
		{"foreign tenant", receiptBody(t, owner.TenantID, foreign.MediaID, foreign.ResponsibilityID, "RECEIVED_PENDING_VALIDATION", nil)},
		{"wrong responsibility", receiptBody(t, owner.TenantID, mediaID, foreign.ResponsibilityID, "RECEIVED_PENDING_VALIDATION", nil)},
		{"unexpected manifest digest", receiptBody(t, owner.TenantID, mediaID, owner.ResponsibilityID, "RECEIVED_PENDING_VALIDATION", map[string]any{"manifestDigest": strings.Repeat("b", 64)})},
	} {
		t.Run(item.name, func(t *testing.T) {
			if processed, err := consumer.Process(ctx, item.body, 0); processed || !errors.Is(err, messaging.ErrPermanent) {
				t.Fatalf("invalid receipt processed=%t err=%v", processed, err)
			}
		})
	}
	var before int64
	if err := h.AdminDB.Model(&database.VideoPreparationJob{}).Where("media_id IN ?", []identity.ID{owner.MediaID, foreign.MediaID, mediaID}).Count(&before).Error; err != nil || before != 0 {
		t.Fatalf("invalid/photo event scheduled preparation: count=%d err=%v", before, err)
	}
	if processed, err := consumer.Process(ctx, valid, 0); err != nil || !processed {
		t.Fatalf("video receipt: processed=%t err=%v", processed, err)
	}
	if processed, err := consumer.Process(ctx, valid, 0); err != nil || processed {
		t.Fatalf("duplicate inbox receipt: processed=%t err=%v", processed, err)
	}
	if processed, err := consumer.Process(ctx, receiptBody(t, owner.TenantID, mediaID, owner.ResponsibilityID, "RECEIVED_PENDING_VALIDATION", nil), 0); err != nil || !processed {
		t.Fatalf("new event for same receipt: processed=%t err=%v", processed, err)
	}
	var jobs int64
	if err := h.AdminDB.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND media_id=?", owner.TenantID, mediaID).Count(&jobs).Error; err != nil || jobs != 1 {
		t.Fatalf("video receipt scheduled %d jobs: %v", jobs, err)
	}
	// An event that arrives before its authoritative media row must roll back its
	// inbox claim, so the same delivery can be retried after the receipt commits.
	pendingID, pendingAttempt := identity.NewID(), identity.NewID()
	pending := receiptBody(t, owner.TenantID, pendingID, owner.ResponsibilityID, "RECEIVED_PENDING_VALIDATION", nil)
	if processed, err := consumer.Process(ctx, pending, 0); processed || err == nil || errors.Is(err, messaging.ErrPermanent) {
		t.Fatalf("early receipt was not deferred: processed=%t err=%v", processed, err)
	}
	if err := h.WithinTenant(ctx, owner.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.VideoAttempt{ID: pendingAttempt, TenantID: owner.TenantID, ResponsibilityID: owner.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "qa-receipt-" + pendingID.String(), PayloadDigest: strings.Repeat("c", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.MediaObject{ID: pendingID, TenantID: owner.TenantID, ResponsibilityID: owner.ResponsibilityID, ObjectKey: "qa-receipt/" + pendingID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("c", 64), SizeBytes: 123, Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &pendingAttempt, RequirementKey: "video:main", IdempotencyKey: "qa-media-" + pendingID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if processed, err := consumer.Process(ctx, pending, 0); err != nil || !processed {
		t.Fatalf("deferred receipt did not resume: processed=%t err=%v", processed, err)
	}
}

func TestVideoReceiptAdapterFailureRemainsRetryableIT557(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	owner, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	mediaID, attemptID := identity.NewID(), identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := h.WithinTenant(ctx, owner.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: owner.TenantID, ResponsibilityID: owner.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "it557-" + mediaID.String(), PayloadDigest: strings.Repeat("a", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.MediaObject{ID: mediaID, TenantID: owner.TenantID, ResponsibilityID: owner.ResponsibilityID, ObjectKey: "it557/" + mediaID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("a", 64), SizeBytes: 123, Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "it557-media-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	body := receiptBody(t, owner.TenantID, mediaID, owner.ResponsibilityID, "RECEIVED_PENDING_VALIDATION", nil)
	var eventMeta struct {
		ID identity.ID `json:"id"`
	}
	if err := json.Unmarshal(body, &eventMeta); err != nil {
		t.Fatal(err)
	}
	handler, err := Setup(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	consumer := messaging.Consumer{DB: h.AdminDB, Registry: events.DefaultRegistry(), Name: "qa-media-upload-completed-it557", Handle: handler}
	if err := h.AdminDB.Exec(`DROP TRIGGER IF EXISTS it557_fail_job_insert ON media.video_preparation_jobs`).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Exec(`DROP FUNCTION IF EXISTS media.it557_fail_job_insert()`).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Exec(`CREATE OR REPLACE FUNCTION media.it557_fail_job_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'it557 transient adapter outage'; END; $$`).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Exec(`CREATE TRIGGER it557_fail_job_insert BEFORE INSERT ON media.video_preparation_jobs FOR EACH ROW EXECUTE FUNCTION media.it557_fail_job_insert()`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = h.AdminDB.Exec(`DROP TRIGGER IF EXISTS it557_fail_job_insert ON media.video_preparation_jobs`).Error
		_ = h.AdminDB.Exec(`DROP FUNCTION IF EXISTS media.it557_fail_job_insert()`).Error
	})
	if processed, err := consumer.Process(ctx, body, 0); processed || err == nil || errors.Is(err, messaging.ErrPermanent) {
		t.Fatalf("database failure was not kept retryable: processed=%t err=%v", processed, err)
	}
	var inbox, jobs int64
	if err := h.AdminDB.Model(&database.InboxReceipt{}).Where("consumer=? AND event_id=?", consumer.Name, eventMeta.ID).Count(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND media_id=?", owner.TenantID, mediaID).Count(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if inbox != 0 || jobs != 0 {
		t.Fatalf("failed transaction reported completed work: inbox=%d jobs=%d", inbox, jobs)
	}
	if err := h.AdminDB.Exec(`DROP TRIGGER IF EXISTS it557_fail_job_insert ON media.video_preparation_jobs`).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Exec(`DROP FUNCTION IF EXISTS media.it557_fail_job_insert()`).Error; err != nil {
		t.Fatal(err)
	}
	if processed, err := consumer.Process(ctx, body, 0); err != nil || !processed {
		t.Fatalf("delivery did not recover after database adapter returned: processed=%t err=%v", processed, err)
	}
	if err := h.AdminDB.Model(&database.InboxReceipt{}).Where("consumer=? AND event_id=?", consumer.Name, eventMeta.ID).Count(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND media_id=?", owner.TenantID, mediaID).Count(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if inbox != 1 || jobs != 1 {
		t.Fatalf("recovered delivery did not commit exactly once: inbox=%d jobs=%d", inbox, jobs)
	}
}
