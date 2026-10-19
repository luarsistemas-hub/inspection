//go:build integration

package read_video_evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestVV_IT_171ReportKeepsResolvingF01FromPinnedP1AfterP2(t *testing.T) {
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
	var f01 database.VideoFrame
	if err := h.AdminDB.Where("tenant_id=? AND id=?", a.TenantID, a.FrameID).Take(&f01).Error; err != nil {
		t.Fatal(err)
	}
	// P2 reprocesses the same source and selects frames at different PTS.
	now := time.Now().UTC()
	p2, p2Frame := identity.NewID(), identity.NewID()
	p2Bytes := []byte("p2-frame-at-other-pts")
	p2Digest := sha256.Sum256(p2Bytes)
	p2Key := "it171/" + p2Frame.String() + ".jpg"
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, p2Key, strings.NewReader(string(p2Bytes)), int64(len(p2Bytes)), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Store.Delete(context.Background(), p2Key) })
	if err := h.WithinTenant(ctx, a.TenantID, func(tx *gorm.DB) error {
		var p1 database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", a.TenantID, a.PreparationID).Take(&p1).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: p2, TenantID: a.TenantID, MediaID: a.MediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: p1.Generation + 1, ProfileDigest: p1.ProfileDigest, NextAttemptAt: now, SourceSHA256: p1.SourceSHA256, ManifestDigest: strings.Repeat("b", 64), PendingObjectKeys: []byte(`[]`), FirstReadyAt: &now, CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoFrame{ID: p2Frame, TenantID: a.TenantID, SourceMediaID: a.MediaID, PreparationID: p2, PTSMicros: f01.PTSMicros + 2_500_000, SHA256: hex.EncodeToString(p2Digest[:]), ObjectKey: p2Key, Width: 1, Height: 1, Role: "BASELINE", SizeBytes: int64(len(p2Bytes)), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	metadata := requestctx.Metadata{TenantID: a.TenantID, Principal: requestctx.Principal{IdentityID: a.ResponsibilityID, TenantID: a.TenantID, Internal: true}}
	mux := http.NewServeMux()
	if _, err := Setup(mux, Dependencies{DB: h.DB, Store: h.Store, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return metadata, true }}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/media/video-evidence/"+f01.ID.String()+"?responsibilityId="+a.ResponsibilityID.String(), nil))
	digest := sha256.Sum256(response.Body.Bytes())
	if response.Code != http.StatusOK || hex.EncodeToString(digest[:]) != f01.SHA256 || response.Header().Get("ETag") != `"`+f01.SHA256+`"` {
		t.Fatalf("RPT1 link for F01 after P2 status=%d digest=%s etag=%s; want exact P1 bytes %s", response.Code, hex.EncodeToString(digest[:]), response.Header().Get("ETag"), f01.SHA256)
	}
	var stored database.VideoFrame
	if err := h.AdminDB.Where("tenant_id=? AND id=?", a.TenantID, f01.ID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.PreparationID != a.PreparationID || stored.PTSMicros != f01.PTSMicros {
		t.Fatalf("F01 lineage moved to %s at %d", stored.PreparationID, stored.PTSMicros)
	}
}
