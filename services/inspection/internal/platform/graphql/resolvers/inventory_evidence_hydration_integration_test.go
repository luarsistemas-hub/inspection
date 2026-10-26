//go:build integration

package resolvers

import (
	"context"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

// countingPresigner signs every key deterministically and counts calls.
type countingPresigner struct {
	objectstore.Client
	calls atomic.Int32
}

func (p *countingPresigner) PresignGet(_ context.Context, bucket, key string, _ time.Duration) (string, error) {
	p.calls.Add(1)
	return "https://signed.example/" + bucket + "/" + key, nil
}

// TestInventoryEvidenceHydrationIsBatchedAndKeepsPreservedFrames covers the
// review evidence enrichment: lookups are batched regardless of evidence
// count, each display object is signed once, frame links point at the origin
// version, and an unknown or mismatched evidence row never hides the others.
func TestInventoryEvidenceHydrationIsBatchedAndKeepsPreservedFrames(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	a, err := h.SeedVideoOriginFixture(ctx, harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	var frame database.VideoFrame
	if err := h.AdminDB.Where("tenant_id=? AND id=?", a.TenantID, a.FrameID).Take(&frame).Error; err != nil {
		t.Fatal(err)
	}
	var photo database.MediaObject
	if err := h.AdminDB.Where("tenant_id=? AND media_kind<>?", a.TenantID, "VIDEO").Take(&photo).Error; err != nil {
		t.Fatal(err)
	}
	displayKey := "tenant/" + a.TenantID.String() + "/display/" + photo.ID.String() + ".jpg"
	if err := h.AdminDB.Create(&database.MediaDerivative{ID: identity.NewID(), TenantID: a.TenantID, MediaID: photo.ID, ObjectKey: displayKey, Kind: "DISPLAY", SHA256: strings.Repeat("e", 64), ContentType: "image/jpeg", Width: 1, Height: 1, SizeBytes: 1, CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	// Source expiry must not remove the preserved frame link.
	expiredAt := time.Now().UTC().Add(-time.Minute)
	if err := h.AdminDB.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", a.TenantID, a.MediaID).Updates(map[string]any{"status": "EXPIRED", "source_expiry_at": expiredAt, "original_deleted_at": expiredAt}).Error; err != nil {
		t.Fatal(err)
	}

	frameID, foreignFrameID := a.FrameID.String(), identity.NewID().String()
	evidence := func(kind, mediaID string, frame *string) *graphql1.InventoryEvidence {
		return &graphql1.InventoryEvidence{ID: identity.NewID().String(), Kind: kind, MediaID: mediaID, FrameID: frame, Availability: "AVAILABLE"}
	}
	items := make([]*graphql1.InventoryItem, 0, 20)
	var frames, photos []*graphql1.InventoryEvidence
	for i := 0; i < 20; i++ {
		frameEvidence := evidence("VIDEO_FRAME", a.MediaID.String(), &frameID)
		photoEvidence := evidence("PHOTO", photo.ID.String(), nil)
		frames, photos = append(frames, frameEvidence), append(photos, photoEvidence)
		items = append(items, &graphql1.InventoryItem{Evidence: []*graphql1.InventoryEvidence{frameEvidence, photoEvidence}})
	}
	invalid := evidence("PHOTO", "not-a-uuid", nil)
	unknownFrame := evidence("VIDEO_FRAME", a.MediaID.String(), &foreignFrameID)
	mismatchedFrame := evidence("VIDEO_FRAME", photo.ID.String(), &frameID)
	items = append(items, &graphql1.InventoryItem{Evidence: []*graphql1.InventoryEvidence{invalid, unknownFrame, mismatchedFrame}})
	draft := &graphql1.InventoryDraft{Environments: []*graphql1.InventoryEnvironment{{Items: items}}}

	var queries atomic.Int32
	counted := h.DB.Session(&gorm.Session{NewDB: true})
	if err := counted.Callback().Query().After("gorm:query").Register("task07:count_queries", func(*gorm.DB) { queries.Add(1) }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = counted.Callback().Query().Remove("task07:count_queries") })
	presigner := &countingPresigner{}
	if err := hydrateInventoryEvidence(ctx, counted, objectstore.Store{Bucket: "bucket", Client: presigner}, a.TenantID, a.OriginVersionID, draft); err != nil {
		t.Fatal(err)
	}

	if got := queries.Load(); got < 1 || got > 2 {
		t.Fatalf("hydration issued %d queries for 63 evidence rows; want at most one batch per kind", got)
	}
	if got := presigner.calls.Load(); got != 1 {
		t.Fatalf("display object signed %d times; want once", got)
	}
	wantFrameURL := "/media/video-evidence/" + url.PathEscape(frameID) + "?snapshotId=" + url.QueryEscape(a.OriginVersionID.String())
	for i, item := range frames {
		if item.URL == nil || *item.URL != wantFrameURL || item.TimestampMs == nil || *item.TimestampMs != int(frame.PTSMicros/1000) {
			t.Fatalf("frame evidence %d url=%v timestamp=%v; want %s at %dms", i, item.URL, item.TimestampMs, wantFrameURL, frame.PTSMicros/1000)
		}
	}
	for i, item := range photos {
		if item.URL == nil || !strings.HasSuffix(*item.URL, displayKey) {
			t.Fatalf("photo evidence %d url=%v; want signed display url", i, item.URL)
		}
	}
	for name, item := range map[string]*graphql1.InventoryEvidence{"invalid media id": invalid, "unknown frame": unknownFrame, "frame of another media": mismatchedFrame} {
		if item.URL != nil {
			t.Fatalf("%s received url %q", name, *item.URL)
		}
	}
}
