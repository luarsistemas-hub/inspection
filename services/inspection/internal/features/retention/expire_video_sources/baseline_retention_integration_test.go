//go:build integration

package expire_video_sources_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	expiresources "inspection/services/inspection/internal/features/retention/expire_video_sources"
	processrequests "inspection/services/inspection/internal/features/retention/process_deletion_requests"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// lifecycleIT seeds stored video sources and drives the production inventory
// activation with the retention hook used by the API composition root.
type lifecycleIT struct {
	t   *testing.T
	ctx context.Context
	h   *harness.Harness
	f   harness.Task06Fixture
	now time.Time
}

type storedSource struct {
	mediaID, frameID identity.ID
	key              string
}

type lifecycleItem struct {
	decision string
	source   storedSource
}

func newLifecycleIT(t *testing.T) *lifecycleIT {
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
	f, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	return &lifecycleIT{t: t, ctx: ctx, h: h, f: f, now: time.Now().UTC().Truncate(time.Microsecond)}
}

func (l *lifecycleIT) within(fn func(*gorm.DB) error) {
	l.t.Helper()
	if err := l.h.WithinTenant(l.ctx, l.f.TenantID, fn); err != nil {
		l.t.Fatal(err)
	}
}

// source stores an original object and seeds a ready source with a FIXED expiry.
func (l *lifecycleIT) source(expiry time.Time) storedSource {
	l.t.Helper()
	s := storedSource{mediaID: identity.NewID(), frameID: identity.NewID()}
	s.key = "retention-lifecycle/" + s.mediaID.String() + ".mp4"
	body := "original-" + s.mediaID.String()
	if err := l.h.Store.Client.Put(l.ctx, l.h.Store.Bucket, s.key, strings.NewReader(body), int64(len(body)), "video/mp4"); err != nil {
		l.t.Fatal(err)
	}
	key := s.key
	l.t.Cleanup(func() { _ = l.h.Store.Delete(context.Background(), key) })
	digest := strings.Repeat("e", 64)
	preparationID := identity.NewID()
	days := 30
	ready := l.now.AddDate(0, 0, -1)
	l.within(func(tx *gorm.DB) error {
		if err := tx.Create(&database.MediaObject{ID: s.mediaID, TenantID: l.f.TenantID, ResponsibilityID: l.f.ResponsibilityID, ObjectKey: s.key, ContentType: "video/mp4", SHA256: digest, SizeBytes: int64(len(body)), Status: "READY", MediaKind: "VIDEO", IdempotencyKey: "retention-lifecycle-" + s.mediaID.String(), RequirementKey: "video:main", AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: ready}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: l.f.TenantID, MediaID: s.mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: ready, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &ready, CreatedAt: ready}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: l.f.TenantID, MediaID: s.mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &ready, RetentionDays: &days, SourceExpiryAt: &expiry, RetentionAnchor: "FIXED", Status: "RETAINED", CreatedAt: ready, UpdatedAt: ready}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoFrame{ID: s.frameID, TenantID: l.f.TenantID, SourceMediaID: s.mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: digest, ObjectKey: "retention-lifecycle/frames/" + s.frameID.String(), Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 10, CreatedAt: ready}).Error
	})
	return s
}

func (l *lifecycleIT) asset() identity.ID {
	l.t.Helper()
	id := identity.NewID()
	l.within(func(tx *gorm.DB) error {
		var asset database.Asset
		if err := tx.Where("tenant_id=? AND id=?", l.f.TenantID, l.f.AssetID).Take(&asset).Error; err != nil {
			return err
		}
		asset.ID, asset.ExternalKey, asset.IdempotencyKey = id, "retention-lifecycle-"+id.String(), "retention-lifecycle-"+id.String()
		return tx.Create(&asset).Error
	})
	return id
}

// activate creates a READY inventory draft citing the sources and activates it
// through inventory core at the given instant, returning the version id.
func (l *lifecycleIT) activate(assetID identity.ID, at time.Time, items ...lifecycleItem) identity.ID {
	l.t.Helper()
	versionID := identity.NewID()
	var expected int64
	l.within(func(tx *gorm.DB) error {
		var origin database.Origin
		err := tx.Where("tenant_id=? AND asset_id=?", l.f.TenantID, assetID).Take(&origin).Error
		if err == gorm.ErrRecordNotFound {
			origin = database.Origin{ID: identity.NewID(), TenantID: l.f.TenantID, AssetID: assetID, Version: 1, CreatedAt: l.now, UpdatedAt: l.now}
			err = tx.Create(&origin).Error
		}
		if err != nil {
			return err
		}
		expected = origin.Version
		var count int64
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND origin_id=?", l.f.TenantID, origin.ID).Count(&count).Error; err != nil {
			return err
		}
		ready := "READY"
		if err := tx.Create(&database.OriginVersion{ID: versionID, TenantID: l.f.TenantID, OriginID: origin.ID, VersionNumber: int(count) + 1, Source: "CAPTURE", HasInventory: true, DraftState: &ready, Status: "DRAFT", MediaKind: "VIDEO", IdempotencyKey: "retention-lifecycle-" + versionID.String(), CreatedAt: l.now}).Error; err != nil {
			return err
		}
		environmentID := identity.NewID()
		if err := tx.Create(&database.InventoryEnvironment{ID: environmentID, TenantID: l.f.TenantID, OriginVersionID: versionID, IdentityID: identity.NewID(), TypeCode: "KITCHEN", Name: "Cozinha", Expected: true, Decision: "ACCEPTED", Version: 1, CreatedAt: l.now, UpdatedAt: l.now}).Error; err != nil {
			return err
		}
		for _, spec := range items {
			item := database.InventoryItem{ID: identity.NewID(), TenantID: l.f.TenantID, OriginVersionID: versionID, EnvironmentID: environmentID, IdentityID: identity.NewID(), TypeCode: "SINK", Name: "Pia", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: spec.decision, Version: 1, CreatedAt: l.now, UpdatedAt: l.now}
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
			frameID := spec.source.frameID
			if err := tx.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: l.f.TenantID, ItemID: item.ID, Kind: "VIDEO_FRAME", MediaID: spec.source.mediaID, FrameID: &frameID, CreatedAt: l.now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	service := inventorycore.Service{DB: l.h.DB, Now: func() time.Time { return at }, Retention: retentioncore.BaselineTransition{Now: func() time.Time { return at }}}
	if _, err := service.Activate(l.ctx, inventorycore.ActivateInput{TenantID: l.f.TenantID, OriginVersionID: versionID, ActorMembershipID: l.f.MembershipID, Purpose: "MOVE_IN", ExpectedOriginVersion: expected}); err != nil {
		l.t.Fatalf("activate inventory version: %v", err)
	}
	return versionID
}

func (l *lifecycleIT) videoSource(mediaID identity.ID) database.VideoSource {
	l.t.Helper()
	var source database.VideoSource
	l.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND media_id=?", l.f.TenantID, mediaID).Take(&source).Error
	})
	return source
}

func (l *lifecycleIT) objectExists(key string) bool {
	_, err := l.h.Store.Client.Head(l.ctx, l.h.Store.Bucket, key)
	return err == nil
}

func (l *lifecycleIT) assertRetained(id string, s storedSource) {
	l.t.Helper()
	source := l.videoSource(s.mediaID)
	if source.Status != "RETAINED" || source.OriginalDeletedAt != nil || !l.objectExists(s.key) {
		l.t.Fatalf("%s source status=%s deleted_at=%v object=%t; want RETAINED with original intact", id, source.Status, source.OriginalDeletedAt, l.objectExists(s.key))
	}
}

func (l *lifecycleIT) assertExpired(id string, s storedSource) {
	l.t.Helper()
	source := l.videoSource(s.mediaID)
	if source.Status != "EXPIRED" || source.OriginalDeletedAt == nil || l.objectExists(s.key) {
		l.t.Fatalf("%s source status=%s deleted_at=%v object=%t; want EXPIRED with original removed", id, source.Status, source.OriginalDeletedAt, l.objectExists(s.key))
	}
}

func (l *lifecycleIT) contest(versionID identity.ID) identity.ID {
	l.t.Helper()
	id := identity.NewID()
	l.within(func(tx *gorm.DB) error {
		return tx.Create(&database.CounterpartyInvitation{ID: id, TenantID: l.f.TenantID, OriginVersionID: versionID, ParticipantID: l.f.ParticipantID, TokenHash: []byte("lifecycle-" + id.String()), Channels: []byte(`[]`), DeadlineAt: l.now.Add(time.Hour), Response: "CONTESTED", Resolution: "OPEN", CreatedAt: l.now, UpdatedAt: l.now}).Error
	})
	return id
}

func (l *lifecycleIT) forceState(mediaID identity.ID, updates map[string]any) {
	l.t.Helper()
	if err := l.h.AdminDB.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", l.f.TenantID, mediaID).Updates(updates).Error; err != nil {
		l.t.Fatal(err)
	}
}

func (l *lifecycleIT) expiry() *expiresources.Executor {
	l.t.Helper()
	executor, err := expiresources.Setup(expiresources.Dependencies{DB: l.h.DB, Store: l.h.Store, Now: func() time.Time { return l.now }})
	if err != nil {
		l.t.Fatal(err)
	}
	return executor
}

// TestIntegration_UT312_RunDueSkipsAnchoredAndDeletesOnlyDueFixedSources runs the
// expiry executor over every anchor/expiry combination on PostgreSQL.
func TestIntegration_UT312_RunDueSkipsAnchoredAndDeletesOnlyDueFixedSources(t *testing.T) {
	l := newLifecycleIT(t)
	past := l.now.Add(-time.Hour)
	staleAnchor := l.source(l.now.AddDate(0, 0, 30))
	l.forceState(staleAnchor.mediaID, map[string]any{"retention_anchor": "ACTIVE_BASELINE", "source_expiry_at": nil})
	due := l.source(past)
	notDue := l.source(l.now.Add(time.Hour))
	held := l.source(past)
	heldBatch := make([]storedSource, 30)
	for i := range heldBatch {
		heldBatch[i] = l.source(past)
	}
	l.within(func(tx *gorm.DB) error {
		if err := tx.Create(&database.LegalHold{ID: identity.NewID(), TenantID: l.f.TenantID, SourceMediaID: &held.mediaID, Reason: "UT-312", Active: true, CreatedAt: l.now}).Error; err != nil {
			return err
		}
		for i := range heldBatch {
			mediaID := heldBatch[i].mediaID
			if err := tx.Create(&database.LegalHold{ID: identity.NewID(), TenantID: l.f.TenantID, SourceMediaID: &mediaID, Reason: "issue-004 head-of-line", Active: true, CreatedAt: l.now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	referenced := l.source(l.now.AddDate(0, 0, 30))
	l.activate(l.f.AssetID, l.now, lifecycleItem{"ACCEPTED", referenced})
	// A stale FIXED date must not win over a live baseline reference.
	l.forceState(referenced.mediaID, map[string]any{"retention_anchor": "FIXED", "source_expiry_at": past})

	claimed, err := l.expiry().RunDue(l.ctx, l.f.TenantID, l.now, 25)
	if err != nil || claimed != 1 {
		t.Fatalf("UT-312 RunDue claimed=%d err=%v; want only the due FIXED unheld unreferenced source", claimed, err)
	}
	l.assertExpired("UT-312 due FIXED source", due)
	l.assertRetained("UT-312 ACTIVE_BASELINE source", staleAnchor)
	l.assertRetained("UT-312 not yet due", notDue)
	l.assertRetained("UT-312 legal hold", held)
	for _, item := range heldBatch {
		l.assertRetained("issue-004 held source", item)
	}
	l.assertRetained("UT-312 active baseline reference", referenced)
}

// TestIntegration_IT036_BaselineRetentionLifecycleAndHolds covers activation,
// supersession expiry, the expiry job and the contestation and legal holds.
func TestIntegration_IT036_BaselineRetentionLifecycleAndHolds(t *testing.T) {
	l := newLifecycleIT(t)
	initial := l.now.AddDate(0, 0, 30)
	contested, legallyHeld, free := l.source(initial), l.source(initial), l.source(initial)
	v1 := l.activate(l.f.AssetID, l.now, lifecycleItem{"ACCEPTED", contested}, lifecycleItem{"EDITED", legallyHeld}, lifecycleItem{"ADDED", free})
	for _, s := range []storedSource{contested, legallyHeld, free} {
		if source := l.videoSource(s.mediaID); source.RetentionAnchor != "ACTIVE_BASELINE" || source.SourceExpiryAt != nil {
			t.Fatalf("IT-036 activation anchor=%s expiry=%v; want ACTIVE_BASELINE without expiry", source.RetentionAnchor, source.SourceExpiryAt)
		}
	}
	farFuture := l.now.AddDate(5, 0, 0)
	if claimed, err := l.expiry().RunDue(l.ctx, l.f.TenantID, farFuture, 100); err != nil || claimed != 0 {
		t.Fatalf("IT-036 expiry job claimed %d anchored sources (err=%v)", claimed, err)
	}

	supersededAt := l.now.Add(time.Hour)
	successor := l.source(initial)
	l.activate(l.f.AssetID, supersededAt, lifecycleItem{"ACCEPTED", successor})
	want := supersededAt.AddDate(0, 0, 365)
	for _, s := range []storedSource{contested, legallyHeld, free} {
		if source := l.videoSource(s.mediaID); source.RetentionAnchor != "FIXED" || source.SourceExpiryAt == nil || !source.SourceExpiryAt.Equal(want) {
			t.Fatalf("IT-036 supersession anchor=%s expiry=%v; want FIXED %v", source.RetentionAnchor, source.SourceExpiryAt, want)
		}
	}
	invitationID := l.contest(v1)
	l.within(func(tx *gorm.DB) error {
		return tx.Create(&database.LegalHold{ID: identity.NewID(), TenantID: l.f.TenantID, SourceMediaID: &legallyHeld.mediaID, Reason: "IT-036", Active: true, CreatedAt: l.now}).Error
	})
	// The open contestation on v1 holds every source v1 owns; resolving it
	// releases the unheld ones while the legal hold keeps its own source.
	afterExpiry := want.Add(time.Minute)
	if claimed, err := l.expiry().RunDue(l.ctx, l.f.TenantID, afterExpiry, 100); err != nil || claimed != 0 {
		t.Fatalf("IT-036 open contestation: claimed=%d err=%v; want no deletion", claimed, err)
	}
	for _, s := range []storedSource{contested, legallyHeld, free} {
		l.assertRetained("IT-036 open contestation blocks deletion", s)
	}
	if source := l.videoSource(successor.mediaID); source.RetentionAnchor != "ACTIVE_BASELINE" {
		t.Fatalf("IT-036 active successor source anchor=%s", source.RetentionAnchor)
	}

	l.within(func(tx *gorm.DB) error {
		return tx.Model(&database.CounterpartyInvitation{}).Where("tenant_id=? AND id=?", l.f.TenantID, invitationID).Updates(map[string]any{"resolution": "DECLINED", "resolved_at": l.now}).Error
	})
	if claimed, err := l.expiry().RunDue(l.ctx, l.f.TenantID, afterExpiry, 100); err != nil || claimed != 2 {
		t.Fatalf("IT-036 resolved contestation: claimed=%d err=%v; want the two unheld sources", claimed, err)
	}
	l.assertExpired("IT-036 resolved contestation releases the hold", contested)
	l.assertExpired("IT-036 unheld fixed expired source", free)
	l.assertRetained("IT-036 legal hold blocks deletion", legallyHeld)
	l.assertRetained("IT-036 active baseline source", successor)
}

// TestIntegration_IT052_SharedSourceSurvivesConcurrentExpiryAndDeletionClaims
// shares one comparative-inspection video between two active baselines on two
// assets; stale FIXED state never lets either executor delete the original.
func TestIntegration_IT052_SharedSourceSurvivesConcurrentExpiryAndDeletionClaims(t *testing.T) {
	l := newLifecycleIT(t)
	shared := l.source(l.now.AddDate(0, 0, 30))
	assetA, assetB := l.f.AssetID, l.asset()
	l.activate(assetA, l.now, lifecycleItem{"ACCEPTED", shared})
	vb := l.activate(assetB, l.now, lifecycleItem{"ACCEPTED", shared})
	l.activate(assetA, l.now.Add(time.Hour), lifecycleItem{"ACCEPTED", l.source(l.now.AddDate(0, 0, 30))})
	if source := l.videoSource(shared.mediaID); source.RetentionAnchor != "ACTIVE_BASELINE" || source.SourceExpiryAt != nil {
		t.Fatalf("IT-052 superseding one owner assigned expiry: anchor=%s expiry=%v", source.RetentionAnchor, source.SourceExpiryAt)
	}

	processor, err := processrequests.Setup(processrequests.Dependencies{DB: l.h.DB, Store: l.h.Store, Now: func() time.Time { return l.now }})
	if err != nil {
		t.Fatal(err)
	}
	race := func(stage string) identity.ID {
		t.Helper()
		requestID := identity.NewID()
		l.within(func(tx *gorm.DB) error {
			return tx.Create(&database.DeletionRequest{ID: requestID, TenantID: l.f.TenantID, SourceMediaID: &shared.mediaID, Status: "REQUESTED", RequestedAt: l.now}).Error
		})
		l.forceState(shared.mediaID, map[string]any{"retention_anchor": "FIXED", "source_expiry_at": l.now.AddDate(0, 0, -1)})
		var wg sync.WaitGroup
		errs := make([]error, 2)
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, errs[0] = l.expiry().RunDue(l.ctx, l.f.TenantID, l.now, 100)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, errs[1] = processor.RunDue(l.ctx, l.f.TenantID, 100)
		}()
		close(start)
		wg.Wait()
		if errs[0] != nil || errs[1] != nil {
			t.Fatalf("%s concurrent claims expiry=%v deletion=%v", stage, errs[0], errs[1])
		}
		l.assertRetained(stage, shared)
		var request database.DeletionRequest
		l.within(func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=?", l.f.TenantID, requestID).Take(&request).Error
		})
		if request.Status != "BLOCKED" {
			t.Fatalf("%s deletion request status=%s reason=%q; want BLOCKED", stage, request.Status, request.Reason)
		}
		return requestID
	}
	race("IT-052 active version reference")

	// A stale claim whose lease expired is re-checked under the source lock and
	// released instead of deleting bytes still owned by an active version.
	l.forceState(shared.mediaID, map[string]any{"status": "DELETING", "delete_lease_token": identity.NewID(), "delete_lease_until": l.now.Add(-time.Minute)})
	if claimed, err := l.expiry().RunDue(l.ctx, l.f.TenantID, l.now, 100); err != nil || claimed != 0 {
		t.Fatalf("IT-052 stale claim recheck claimed=%d err=%v", claimed, err)
	}
	l.assertRetained("IT-052 stale claim released", shared)

	// After the last owner is superseded, an open contestation on it still blocks both claims.
	l.contest(vb)
	l.activate(assetB, l.now.Add(2*time.Hour), lifecycleItem{"ACCEPTED", l.source(l.now.AddDate(0, 0, 30))})
	if source := l.videoSource(shared.mediaID); source.RetentionAnchor != "FIXED" || source.SourceExpiryAt == nil {
		t.Fatalf("IT-052 last owner superseded anchor=%s expiry=%v; want FIXED", source.RetentionAnchor, source.SourceExpiryAt)
	}
	race("IT-052 open contestation reference")
}

// TestIntegration_IT052_AnchoredDeletionRequestBlocksWithoutStoppingTheQueue
// keeps a request for an anchored source BLOCKED and still processes the
// REQUESTED rows queued behind it in the same batch.
func TestIntegration_IT052_AnchoredDeletionRequestBlocksWithoutStoppingTheQueue(t *testing.T) {
	l := newLifecycleIT(t)
	anchored := l.source(l.now.AddDate(0, 0, 30))
	free := l.source(l.now.AddDate(0, 0, 30))
	l.activate(l.f.AssetID, l.now, lifecycleItem{"ACCEPTED", anchored})
	blockedEarlier, anchoredRequest, freeRequest := identity.NewID(), identity.NewID(), identity.NewID()
	processedAt := l.now.Add(-time.Hour)
	l.within(func(tx *gorm.DB) error {
		for _, request := range []database.DeletionRequest{
			{ID: blockedEarlier, TenantID: l.f.TenantID, SourceMediaID: &anchored.mediaID, Status: "BLOCKED", Reason: "active baseline prevents source deletion", RequestedAt: l.now.Add(-2 * time.Hour), ProcessedAt: &processedAt},
			{ID: anchoredRequest, TenantID: l.f.TenantID, SourceMediaID: &anchored.mediaID, Status: "REQUESTED", RequestedAt: l.now.Add(-time.Minute)},
			{ID: freeRequest, TenantID: l.f.TenantID, SourceMediaID: &free.mediaID, Status: "REQUESTED", RequestedAt: l.now},
		} {
			if err := tx.Create(&request).Error; err != nil {
				return err
			}
		}
		return nil
	})
	processor, err := processrequests.Setup(processrequests.Dependencies{DB: l.h.DB, Store: l.h.Store, Now: func() time.Time { return l.now }})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := processor.RunDue(l.ctx, l.f.TenantID, 10)
	if err != nil || processed != 3 {
		t.Fatalf("IT-052 deletion batch processed=%d err=%v; want all three requests handled", processed, err)
	}
	statuses := map[identity.ID]database.DeletionRequest{}
	l.within(func(tx *gorm.DB) error {
		var rows []database.DeletionRequest
		if err := tx.Where("tenant_id=?", l.f.TenantID).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			statuses[row.ID] = row
		}
		return nil
	})
	for _, id := range []identity.ID{blockedEarlier, anchoredRequest} {
		if row := statuses[id]; row.Status != "BLOCKED" || !strings.Contains(row.Reason, "active baseline") {
			t.Fatalf("IT-052 anchored request %s status=%s reason=%q; want BLOCKED by active baseline", id, row.Status, row.Reason)
		}
	}
	if row := statuses[freeRequest]; row.Status != "COMPLETED" {
		t.Fatalf("IT-052 request queued behind the anchored one status=%s reason=%q; want COMPLETED", row.Status, row.Reason)
	}
	l.assertRetained("IT-052 anchored source", anchored)
	l.assertExpired("IT-052 unowned requested source", free)
}
