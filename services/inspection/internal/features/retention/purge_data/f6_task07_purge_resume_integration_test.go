//go:build integration

package purge_data

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
)

// failKeyDeleteClient fails deletion of one key while failing is set.
type failKeyDeleteClient struct {
	objectstore.Client
	failKey string
	failing bool
	deleted []string
}

func (c *failKeyDeleteClient) Delete(ctx context.Context, bucket, key string) error {
	if c.failing && key == c.failKey {
		return errors.New("injected interruption after thumbnail removal")
	}
	deleter, ok := c.Client.(objectstore.Deleter)
	if !ok {
		return objectstore.ErrInvalid
	}
	if err := deleter.Delete(ctx, bucket, key); err != nil {
		return err
	}
	c.deleted = append(c.deleted, key)
	return nil
}

func TestVV_IT_333InterruptedPurgeAfterThumbnailRemovalResumesWithoutCompletedClaim(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	if err := makeInspectionDue(ctx, h, fixture.TenantID, fixture.InspectionID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	mediaID, snapshotID := identity.NewID(), identity.NewID()
	originalKey := "it333/" + mediaID.String() + ".jpg"
	thumbnailKey := "it333/" + mediaID.String() + "-thumb.jpg"
	artifactKey := "it333/" + snapshotID.String() + ".pdf"
	for _, key := range []string{originalKey, thumbnailKey, artifactKey} {
		if err := h.Store.Client.Put(ctx, h.Store.Bucket, key, strings.NewReader("bytes"), 5, "application/octet-stream"); err != nil {
			t.Fatal(err)
		}
		key := key
		t.Cleanup(func() { _ = h.Store.Delete(context.Background(), key) })
	}
	digest := strings.Repeat("a", 64)
	for _, row := range []any{
		&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: originalKey, ContentType: "image/jpeg", SHA256: digest, SizeBytes: 5, Status: "READY", MediaKind: "PHOTO", RequirementKey: "room-1", IdempotencyKey: "it333-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now},
		&database.MediaDerivative{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, ObjectKey: thumbnailKey, Kind: "THUMBNAIL", SHA256: digest, ContentType: "image/jpeg", Width: 2, Height: 2, SizeBytes: 5, CreatedAt: now},
		&database.ReportSnapshot{ID: snapshotID, TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, Mode: "FIXED_ORIGIN", Classification: "SATISFACTORY", JSONDigest: digest, HTMLDigest: digest, VersionNumber: 1, CanonicalJSON: json.RawMessage(`{}`), HTML: json.RawMessage(`"<p></p>"`), CreatedAt: now},
		&database.ReportArtifact{ID: identity.NewID(), TenantID: fixture.TenantID, SnapshotID: snapshotID, Kind: "PDF", ObjectKey: artifactKey, SHA256: digest, Status: "READY", CreatedAt: now},
	} {
		if err := h.AdminDB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	client := &failKeyDeleteClient{Client: h.Store.Client, failKey: artifactKey, failing: true}
	store := h.Store
	store.Client = client
	clock := mustDueClock(t, now.Add(-6*365*24*time.Hour))
	if _, err := PurgeWithStore(ctx, h.MigrationDB, store, fixture.TenantID, fixture.InspectionID, clock); err == nil {
		t.Fatal("interrupted purge reported success")
	}
	if !containsKey(client.deleted, thumbnailKey) {
		t.Fatalf("interruption did not happen after thumbnail removal: deleted=%v", client.deleted)
	}
	assertInspectionUnpurged(t, h, fixture.TenantID, fixture.InspectionID)
	var completed, purgedEvents int64
	if err := h.MigrationDB.Model(&database.PurgeRun{}).Where("tenant_id=? AND inspection_id=? AND status='COMPLETED'", fixture.TenantID, fixture.InspectionID).Count(&completed).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.MigrationDB.Model(&database.AuditEvent{}).Where("tenant_id=? AND action='retention.purged'", fixture.TenantID).Count(&purgedEvents).Error; err != nil {
		t.Fatal(err)
	}
	if completed != 0 || purgedEvents != 0 {
		t.Fatalf("interrupted purge claimed cleanup: completedRuns=%d purgedAudits=%d", completed, purgedEvents)
	}
	var derivatives, artifacts int64
	if err := h.AdminDB.Model(&database.MediaDerivative{}).Where("tenant_id=? AND media_id=?", fixture.TenantID, mediaID).Count(&derivatives).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.ReportArtifact{}).Where("tenant_id=? AND snapshot_id=?", fixture.TenantID, snapshotID).Count(&artifacts).Error; err != nil {
		t.Fatal(err)
	}
	if derivatives != 1 || artifacts != 1 {
		t.Fatalf("interrupted purge removed manifest rows: derivatives=%d artifacts=%d", derivatives, artifacts)
	}

	client.failing = false
	run, err := PurgeWithStore(ctx, h.MigrationDB, store, fixture.TenantID, fixture.InspectionID, clock)
	if err != nil {
		t.Fatalf("resume purge: %v", err)
	}
	if run.Status != "COMPLETED" {
		t.Fatalf("resumed purge status=%s, want COMPLETED", run.Status)
	}
	for _, key := range []string{originalKey, thumbnailKey, artifactKey} {
		if _, err := h.Store.Client.Head(ctx, h.Store.Bucket, key); err == nil {
			t.Fatalf("resumed purge left object %s", key)
		}
	}
	if !containsKey(client.deleted, artifactKey) {
		t.Fatalf("resume did not reconcile the remaining artifact key: deleted=%v", client.deleted)
	}
}

func containsKey(keys []string, key string) bool {
	for _, item := range keys {
		if item == key {
			return true
		}
	}
	return false
}
