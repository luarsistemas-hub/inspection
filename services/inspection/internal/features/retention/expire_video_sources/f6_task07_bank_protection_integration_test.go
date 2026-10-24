//go:build integration

package expire_video_sources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	origincore "inspection/services/inspection/internal/features/origins/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func newTask07OriginHarness(t *testing.T) (*harness.Harness, harness.VideoOriginFixture) {
	t.Helper()
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedVideoOriginFixture(ctx, harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	// The active O1 owns its bank as production activation records it.
	if err := h.AdminDB.Create(&database.VideoEvidenceReference{ID: identity.NewID(), TenantID: fixture.TenantID, PreparationID: fixture.PreparationID, OwnerType: "ORIGIN_VERSION", OwnerID: fixture.OriginVersionID, CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	return h, fixture
}

func addTask07ReportReferences(t *testing.T, h *harness.Harness, fixture harness.VideoOriginFixture, count int) []identity.ID {
	t.Helper()
	ids := make([]identity.ID, count)
	now := time.Now().UTC()
	rows := make([]database.VideoEvidenceReference, count)
	for i := range rows {
		ids[i] = identity.NewID()
		rows[i] = database.VideoEvidenceReference{ID: ids[i], TenantID: fixture.TenantID, PreparationID: fixture.PreparationID, OwnerType: "REPORT_SNAPSHOT", OwnerID: identity.NewID(), CreatedAt: now.Add(time.Duration(i) * time.Second)}
	}
	if err := h.AdminDB.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return ids
}

// assertTask07F01Provenance proves F01 keeps its row, lineage and exact bytes.
func assertTask07F01Provenance(t *testing.T, h *harness.Harness, fixture harness.VideoOriginFixture) {
	t.Helper()
	var frame database.VideoFrame
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.TenantID, fixture.FrameID).Take(&frame).Error; err != nil {
		t.Fatalf("F01 row was removed: %v", err)
	}
	if frame.SourceMediaID != fixture.MediaID || frame.PreparationID != fixture.PreparationID {
		t.Fatalf("F01 provenance changed: %+v", frame)
	}
	reader, err := h.Store.Client.Get(context.Background(), h.Store.Bucket, frame.ObjectKey)
	if err != nil {
		t.Fatalf("F01 bytes were removed: %v", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != frame.SHA256 {
		t.Fatalf("F01 bytes no longer match digest %s", frame.SHA256)
	}
	var scope int64
	if err := h.AdminDB.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND frame_id=?", fixture.TenantID, fixture.FrameID).Count(&scope).Error; err != nil {
		t.Fatal(err)
	}
	if scope != 1 {
		t.Fatalf("F01 observed scope rows=%d, want 1", scope)
	}
}

func TestVV_IT_323SweepOriginSourceKeepsF01ProvenanceForActiveOriginAndReport(t *testing.T) {
	h, fixture := newTask07OriginHarness(t)
	addTask07ReportReferences(t, h, fixture, 1)
	ctx := context.Background()
	var media database.MediaObject
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.TenantID, fixture.MediaID).Take(&media).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, media.ObjectKey, strings.NewReader("source"), 6, "video/mp4"); err != nil {
		t.Fatal(err)
	}
	due := time.Now().UTC().Add(-time.Hour)
	days := 30
	if err := h.AdminDB.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", fixture.TenantID, fixture.MediaID).Updates(map[string]any{"source_expiry_at": due, "retention_days": days, "status": "RETAINED"}).Error; err != nil {
		t.Fatal(err)
	}
	executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := executor.RunDue(ctx, fixture.TenantID, time.Now().UTC(), 10); err != nil || claimed != 1 {
		t.Fatalf("O1 source sweep claimed=%d err=%v; want 1", claimed, err)
	}
	if status := task04SourceStatus(t, h, fixture.TenantID, fixture.MediaID); status != "EXPIRED" {
		t.Fatalf("O1 source status=%s, want EXPIRED", status)
	}
	if purged, err := executor.PurgeInactiveOriginBanks(ctx, fixture.TenantID, 10); err != nil || purged != 0 {
		t.Fatalf("bank purge for active O1 = %d, %v; want 0", purged, err)
	}
	assertTask07F01Provenance(t, h, fixture)
}

func TestVV_IT_336InvalidateOriginWhileV1PinnedAndRPT1RetainedKeepsFrames(t *testing.T) {
	h, fixture := newTask07OriginHarness(t)
	ctx := context.Background()
	// V1 is pinned to O1 in the tenant that owns the origin.
	v1 := identity.NewID()
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var base database.Inspection
		if err := tx.Where("tenant_id=?", fixture.TenantID).Order("created_at").First(&base).Error; err != nil {
			return err
		}
		pinned := base
		pinned.ID = identity.NewID()
		pinned.SourceKey = "it336-v1-" + pinned.ID.String()
		pinned.Status = "SUBMITTED"
		v1 = pinned.ID
		if err := tx.Create(&pinned).Error; err != nil {
			return err
		}
		return tx.Create(&database.ReferenceSnapshot{ID: identity.NewID(), TenantID: fixture.TenantID, InspectionID: v1, ReferenceVersionID: &fixture.OriginVersionID, ComparisonMode: "FIXED_ORIGIN", Payload: json.RawMessage(`{}`), CreatedAt: time.Now().UTC()}).Error
	}); err != nil {
		t.Fatal(err)
	}
	addTask07ReportReferences(t, h, fixture, 1)
	if _, err := (origincore.Service{DB: h.DB}).Invalidate(ctx, fixture.TenantID, fixture.OriginVersionID); err != nil {
		t.Fatal(err)
	}
	var version database.OriginVersion
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.TenantID, fixture.OriginVersionID).Take(&version).Error; err != nil {
		t.Fatal(err)
	}
	if version.Status != "INVALIDATED" {
		t.Fatalf("O1 status=%s, want INVALIDATED", version.Status)
	}
	executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store})
	if err != nil {
		t.Fatal(err)
	}
	if purged, err := executor.PurgeInactiveOriginBanks(ctx, fixture.TenantID, 10); err != nil || purged != 0 {
		t.Fatalf("bank purge of invalidated O1 with RPT1 retained = %d, %v; want 0", purged, err)
	}
	assertTask07F01Provenance(t, h, fixture)
	var pinned database.ReferenceSnapshot
	if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", fixture.TenantID, v1).Take(&pinned).Error; err != nil || pinned.ReferenceVersionID == nil || *pinned.ReferenceVersionID != fixture.OriginVersionID {
		t.Fatalf("V1 pin changed after invalidation: %+v err=%v", pinned, err)
	}
}

func TestVV_IT_337ReleasingNewestOfHundredReportReferencesKeepsP1Protected(t *testing.T) {
	h, fixture := newTask07OriginHarness(t)
	ctx := context.Background()
	reports := addTask07ReportReferences(t, h, fixture, 100)
	if _, err := (origincore.Service{DB: h.DB}).Invalidate(ctx, fixture.TenantID, fixture.OriginVersionID); err != nil {
		t.Fatal(err)
	}
	newest := reports[len(reports)-1]
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.TenantID, newest).Delete(&database.VideoEvidenceReference{}).Error; err != nil {
		t.Fatal(err)
	}
	var live int64
	if err := h.AdminDB.Model(&database.VideoEvidenceReference{}).Where("tenant_id=? AND preparation_id=?", fixture.TenantID, fixture.PreparationID).Count(&live).Error; err != nil {
		t.Fatal(err)
	}
	if live != 99 {
		t.Fatalf("live P1 dependencies=%d, want 99", live)
	}
	executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store})
	if err != nil {
		t.Fatal(err)
	}
	if purged, err := executor.PurgeInactiveOriginBanks(ctx, fixture.TenantID, 100); err != nil || purged != 0 {
		t.Fatalf("bank purge with 99 live reports = %d, %v; want 0", purged, err)
	}
	assertTask07F01Provenance(t, h, fixture)
}

func TestVV_IT_175SupersededP1StaysProtectedWhileRPT1ReferencesIt(t *testing.T) {
	h, fixture := newTask07OriginHarness(t)
	ctx := context.Background()
	addTask07ReportReferences(t, h, fixture, 1)
	now := time.Now().UTC()
	p2, v2, f2 := identity.NewID(), identity.NewID(), identity.NewID()
	p2Key := "it175/" + f2.String() + ".jpg"
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, p2Key, strings.NewReader("p2"), 2, "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Store.Delete(context.Background(), p2Key) })
	// P2 re-prepares the same source and supersedes P1 as the active bank.
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var p1 database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, fixture.PreparationID).Take(&p1).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: p2, TenantID: fixture.TenantID, MediaID: fixture.MediaID, Status: "READY", Phase: "READY", PreparationVersion: p1.PreparationVersion + 1, Generation: p1.Generation + 1, NextAttemptAt: now, SourceSHA256: p1.SourceSHA256, ManifestDigest: p1.ManifestDigest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: p1.FirstReadyAt, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: f2, TenantID: fixture.TenantID, SourceMediaID: fixture.MediaID, PreparationID: p2, PTSMicros: 2_500_000, SHA256: strings.Repeat("2", 64), ObjectKey: p2Key, Width: 1, Height: 1, Role: "BASELINE", Quality: "GOOD", SizeBytes: 2, CreatedAt: now}).Error; err != nil {
			return err
		}
		var v1 database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, fixture.OriginVersionID).Take(&v1).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", fixture.TenantID, v1.ID).Updates(map[string]any{"status": "SUPERSEDED"}).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND owner_type='ORIGIN_VERSION' AND owner_id=?", fixture.TenantID, v1.ID).Delete(&database.VideoEvidenceReference{}).Error; err != nil {
			return err
		}
		var responsibility database.Responsibility
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, v1.ResponsibilityID).Take(&responsibility).Error; err != nil {
			return err
		}
		responsibility.ID = identity.NewID()
		if err := tx.Create(&responsibility).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.OriginVersion{ID: v2, TenantID: fixture.TenantID, OriginID: fixture.OriginID, VersionNumber: 2, ResponsibilityID: &responsibility.ID, Status: "ACTIVE", MediaKind: "VIDEO", VideoPreparationID: &p2, ScopeManifestDigest: v1.ScopeManifestDigest, IdempotencyKey: "it175-v2-" + v2.String(), CreatedAt: now, SubmittedAt: &now, ActivatedAt: &now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.Origin{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.OriginID).Update("active_version_id", v2).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoEvidenceReference{ID: identity.NewID(), TenantID: fixture.TenantID, PreparationID: p2, OwnerType: "ORIGIN_VERSION", OwnerID: v2, CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store})
	if err != nil {
		t.Fatal(err)
	}
	if purged, err := executor.PurgeInactiveOriginBanks(ctx, fixture.TenantID, 10); err != nil || purged != 0 {
		t.Fatalf("purge of superseded P1 cited by RPT1 = %d, %v; want 0", purged, err)
	}
	assertTask07F01Provenance(t, h, fixture)
	if frames, exists := task04BankState(t, h, fixture.TenantID, p2, p2Key); frames != 1 || !exists {
		t.Fatalf("active P2 bank after refused purge: frames=%d object=%t", frames, exists)
	}
}
