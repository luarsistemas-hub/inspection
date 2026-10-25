//go:build integration

package core

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"inspection/libs/identity"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// retentionIT drives the real inventory activation against PostgreSQL with the
// production retention hook, so source anchors are observed end to end.
type retentionIT struct {
	t   *testing.T
	ctx context.Context
	h   *harness.Harness
	f   harness.Task06Fixture
	now time.Time
}

type itSource struct{ mediaID, preparationID, frameID identity.ID }

type itItem struct {
	decision string
	source   itSource
}

func newRetentionIT(t *testing.T) *retentionIT {
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
	return &retentionIT{t: t, ctx: ctx, h: h, f: f, now: time.Now().UTC().Truncate(time.Microsecond)}
}

func (r *retentionIT) within(fn func(*gorm.DB) error) {
	r.t.Helper()
	if err := r.h.WithinTenant(r.ctx, r.f.TenantID, fn); err != nil {
		r.t.Fatal(err)
	}
}

// source seeds a ready video source with a FIXED expiry and one retained frame.
func (r *retentionIT) source(responsibilityID identity.ID, expiry time.Time) itSource {
	r.t.Helper()
	s := itSource{mediaID: identity.NewID(), preparationID: identity.NewID(), frameID: identity.NewID()}
	digest := strings.Repeat("d", 64)
	days := 30
	r.within(func(tx *gorm.DB) error {
		if err := tx.Create(&database.MediaObject{ID: s.mediaID, TenantID: r.f.TenantID, ResponsibilityID: responsibilityID, ObjectKey: "retention-it/" + s.mediaID.String(), ContentType: "video/mp4", SHA256: digest, SizeBytes: 10, Status: "READY", MediaKind: "VIDEO", IdempotencyKey: "retention-it-" + s.mediaID.String(), RequirementKey: "video:main", AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: r.now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: s.preparationID, TenantID: r.f.TenantID, MediaID: s.mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: r.now, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &r.now, CreatedAt: r.now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: r.f.TenantID, MediaID: s.mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &r.now, RetentionDays: &days, SourceExpiryAt: &expiry, RetentionAnchor: "FIXED", Status: "RETAINED", CreatedAt: r.now, UpdatedAt: r.now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoFrame{ID: s.frameID, TenantID: r.f.TenantID, SourceMediaID: s.mediaID, PreparationID: s.preparationID, PTSMicros: 0, SHA256: digest, ObjectKey: "retention-it/frames/" + s.frameID.String(), Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 10, CreatedAt: r.now}).Error
	})
	return s
}

// asset clones the fixture asset so a second baseline can share sources.
func (r *retentionIT) asset() identity.ID {
	r.t.Helper()
	id := identity.NewID()
	r.within(func(tx *gorm.DB) error {
		var asset database.Asset
		if err := tx.Where("tenant_id=? AND id=?", r.f.TenantID, r.f.AssetID).Take(&asset).Error; err != nil {
			return err
		}
		asset.ID, asset.ExternalKey, asset.IdempotencyKey = id, "retention-it-"+id.String(), "retention-it-"+id.String()
		return tx.Create(&asset).Error
	})
	return id
}

// draft creates a READY inventory draft on the asset's origin whose items cite
// the given sources through retained video frames.
func (r *retentionIT) draft(assetID identity.ID, items ...itItem) (identity.ID, int64) {
	r.t.Helper()
	versionID := identity.NewID()
	var expected int64
	r.within(func(tx *gorm.DB) error {
		var origin database.Origin
		err := tx.Where("tenant_id=? AND asset_id=?", r.f.TenantID, assetID).Take(&origin).Error
		if err == gorm.ErrRecordNotFound {
			origin = database.Origin{ID: identity.NewID(), TenantID: r.f.TenantID, AssetID: assetID, Version: 1, CreatedAt: r.now, UpdatedAt: r.now}
			err = tx.Create(&origin).Error
		}
		if err != nil {
			return err
		}
		expected = origin.Version
		var count int64
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND origin_id=?", r.f.TenantID, origin.ID).Count(&count).Error; err != nil {
			return err
		}
		ready := "READY"
		if err := tx.Create(&database.OriginVersion{ID: versionID, TenantID: r.f.TenantID, OriginID: origin.ID, VersionNumber: int(count) + 1, Source: "CAPTURE", HasInventory: true, DraftState: &ready, Status: "DRAFT", MediaKind: "VIDEO", IdempotencyKey: "retention-it-draft-" + versionID.String(), CreatedAt: r.now}).Error; err != nil {
			return err
		}
		environmentID := identity.NewID()
		if err := tx.Create(&database.InventoryEnvironment{ID: environmentID, TenantID: r.f.TenantID, OriginVersionID: versionID, IdentityID: identity.NewID(), TypeCode: "KITCHEN", Name: "Cozinha", Expected: true, Decision: "ACCEPTED", Version: 1, CreatedAt: r.now, UpdatedAt: r.now}).Error; err != nil {
			return err
		}
		for _, spec := range items {
			itemID := identity.NewID()
			item := database.InventoryItem{ID: itemID, TenantID: r.f.TenantID, OriginVersionID: versionID, EnvironmentID: environmentID, IdentityID: identity.NewID(), TypeCode: "SINK", Name: "Pia " + spec.decision, Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: spec.decision, Version: 1, CreatedAt: r.now, UpdatedAt: r.now}
			if spec.decision == "ADDED" {
				item.Origin = "MANUAL"
			}
			if spec.decision == "REJECTED" {
				reason := "NOT_RELEVANT"
				item.RejectionReason = &reason
			}
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
			frameID := spec.source.frameID
			if err := tx.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: r.f.TenantID, ItemID: itemID, Kind: "VIDEO_FRAME", MediaID: spec.source.mediaID, FrameID: &frameID, CreatedAt: r.now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return versionID, expected
}

// activate runs the production inventory activation at the given instant.
func (r *retentionIT) activate(versionID identity.ID, expected int64, at time.Time) {
	r.t.Helper()
	service := Service{DB: r.h.DB, Now: func() time.Time { return at }, Retention: retentioncore.BaselineTransition{Now: func() time.Time { return at }}}
	if _, err := service.Activate(r.ctx, ActivateInput{TenantID: r.f.TenantID, OriginVersionID: versionID, ActorMembershipID: r.f.MembershipID, Purpose: "MOVE_IN", ExpectedOriginVersion: expected}); err != nil {
		r.t.Fatalf("activate %s: %v", versionID, err)
	}
}

func (r *retentionIT) videoSource(mediaID identity.ID) database.VideoSource {
	r.t.Helper()
	var source database.VideoSource
	r.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND media_id=?", r.f.TenantID, mediaID).Take(&source).Error
	})
	return source
}

func (r *retentionIT) assertAnchored(t *testing.T, id string, mediaID identity.ID, floor *time.Time) {
	t.Helper()
	source := r.videoSource(mediaID)
	if source.RetentionAnchor != "ACTIVE_BASELINE" || source.SourceExpiryAt != nil {
		t.Fatalf("%s source %s anchor=%s expiry=%v; want ACTIVE_BASELINE without expiry", id, mediaID, source.RetentionAnchor, source.SourceExpiryAt)
	}
	if floor != nil && (source.RetentionFloorAt == nil || !source.RetentionFloorAt.Equal(*floor)) {
		t.Fatalf("%s source %s floor=%v; want previous fixed expiry %v", id, mediaID, source.RetentionFloorAt, *floor)
	}
}

func (r *retentionIT) assertFixed(t *testing.T, id string, mediaID identity.ID, want time.Time) {
	t.Helper()
	source := r.videoSource(mediaID)
	if source.RetentionAnchor != "FIXED" || source.SourceExpiryAt == nil || !source.SourceExpiryAt.Equal(want) {
		t.Fatalf("%s source %s anchor=%s expiry=%v; want FIXED %v", id, mediaID, source.RetentionAnchor, source.SourceExpiryAt, want)
	}
}

// TestIntegration_UT310_UT311_UT319_ActivationSupersessionAndSharedSources drives
// two baselines through inventory activation: activation anchors every source
// cited by an included (ACCEPTED, EDITED or ADDED) item, a rejected item does not
// own its source, supersession assigns superseded_at+365 when no other active
// version references a source, and a shared source keeps its anchor until the
// last active owner is superseded, when the latest applicable expiry wins.
func TestIntegration_UT310_UT311_UT319_ActivationSupersessionAndSharedSources(t *testing.T) {
	r := newRetentionIT(t)
	initialExpiry := r.now.AddDate(0, 0, 30)
	shared := r.source(r.f.ResponsibilityID, initialExpiry)
	edited := r.source(r.f.ResponsibilityID, initialExpiry)
	added := r.source(r.f.ResponsibilityID, initialExpiry)
	rejected := r.source(r.f.ResponsibilityID, initialExpiry)
	successorOnly := r.source(r.f.ResponsibilityID, initialExpiry)
	assetA, assetB := r.f.AssetID, r.asset()

	a1, expectedA := r.draft(assetA, itItem{"ACCEPTED", shared}, itItem{"EDITED", edited}, itItem{"ADDED", added}, itItem{"REJECTED", rejected})
	r.activate(a1, expectedA, r.now)
	t.Run("UT-310 activation anchors sources of included items", func(t *testing.T) {
		for _, source := range []itSource{shared, edited, added} {
			r.assertAnchored(t, "UT-310", source.mediaID, &initialExpiry)
		}
		r.assertFixed(t, "UT-310 rejected item does not own its source", rejected.mediaID, initialExpiry)
	})

	b1, expectedB := r.draft(assetB, itItem{"ACCEPTED", shared})
	r.activate(b1, expectedB, r.now.Add(time.Hour))
	r.assertAnchored(t, "UT-319 second active owner", shared.mediaID, &initialExpiry)

	supersededA := r.now.AddDate(0, 0, 10)
	a2, expectedA := r.draft(assetA, itItem{"ACCEPTED", successorOnly})
	r.activate(a2, expectedA, supersededA)
	t.Run("UT-311 supersession without another active owner assigns superseded_at plus 365 days", func(t *testing.T) {
		var version database.OriginVersion
		r.within(func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=?", r.f.TenantID, a1).Take(&version).Error
		})
		if version.Status != "SUPERSEDED" || version.SupersededAt == nil || !version.SupersededAt.Equal(supersededA) {
			t.Fatalf("A1 status=%s superseded_at=%v; want SUPERSEDED at %v", version.Status, version.SupersededAt, supersededA)
		}
		for _, source := range []itSource{edited, added} {
			r.assertFixed(t, "UT-311", source.mediaID, supersededA.AddDate(0, 0, 365))
		}
		r.assertFixed(t, "UT-311 rejected source untouched", rejected.mediaID, initialExpiry)
		r.assertAnchored(t, "UT-311 successor source", successorOnly.mediaID, &initialExpiry)
	})
	t.Run("UT-319 shared source stays anchored while B1 is active", func(t *testing.T) {
		r.assertAnchored(t, "UT-319", shared.mediaID, &initialExpiry)
	})

	supersededB := r.now.AddDate(0, 0, 20)
	b2, expectedB := r.draft(assetB, itItem{"ACCEPTED", successorOnly})
	r.activate(b2, expectedB, supersededB)
	t.Run("UT-319 latest applicable fixed expiry wins after the last owner is superseded", func(t *testing.T) {
		r.assertFixed(t, "UT-319", shared.mediaID, supersededB.AddDate(0, 0, 365))
		r.assertAnchored(t, "UT-319 source still owned by A2 and B2", successorOnly.mediaID, &initialExpiry)
	})
}

// TestIntegration_UT316_AnchoredFloorIsNeverShortenedBySupersession anchors a
// source whose fixed expiry was T+30d, then supersedes it under a 1-day
// post-supersession policy: the persisted floor keeps T+30d.
func TestIntegration_UT316_AnchoredFloorIsNeverShortenedBySupersession(t *testing.T) {
	r := newRetentionIT(t)
	r.within(func(tx *gorm.DB) error {
		return tx.Create(&database.VideoPolicy{TenantID: r.f.TenantID, MaxDurationMinutes: 15, OriginRetentionDays: 30, CurrentRetentionDays: 30, SupersededBaselineRetentionDays: 1, Version: 1, UpdatedAt: r.now}).Error
	})
	fixedExpiry := r.now.AddDate(0, 0, 30)
	source := r.source(r.f.ResponsibilityID, fixedExpiry)
	replacement := r.source(r.f.ResponsibilityID, fixedExpiry)
	v1, expected := r.draft(r.f.AssetID, itItem{"ACCEPTED", source})
	r.activate(v1, expected, r.now)
	r.assertAnchored(t, "UT-316 floor persisted on activation", source.mediaID, &fixedExpiry)
	v2, expected := r.draft(r.f.AssetID, itItem{"ACCEPTED", replacement})
	r.activate(v2, expected, r.now.Add(time.Hour))
	r.assertFixed(t, "UT-316 supersession with 1-day policy never shortens T+30d", source.mediaID, fixedExpiry)
}

// TestIntegration_UT317_RetentionAvailabilityLabelsAreBatched reads every label
// from PostgreSQL, including holds inherited through the inspection and through
// an owning origin version, and a contestation on an EDITED item, with a fixed
// number of statements regardless of evidence count.
func TestIntegration_UT317_RetentionAvailabilityLabelsAreBatched(t *testing.T) {
	r := newRetentionIT(t)
	fixedExpiry := time.Date(2031, 3, 4, 23, 30, 0, 0, time.UTC)
	otherResponsibility := r.otherInspectionResponsibility()
	anchored := r.source(otherResponsibility, fixedExpiry)
	contested := r.source(otherResponsibility, fixedExpiry)
	directHold := r.source(otherResponsibility, fixedExpiry)
	inspectionHold := r.source(r.f.ResponsibilityID, fixedExpiry)
	inventoryVersionHold := r.source(otherResponsibility, fixedExpiry)
	legacyVersionHold := r.source(otherResponsibility, fixedExpiry)
	expired := r.source(otherResponsibility, fixedExpiry)
	fixed := r.source(otherResponsibility, fixedExpiry)

	// A contested, superseded inventory version owns `contested` through an EDITED item.
	contestedVersion, expected := r.draft(r.f.AssetID, itItem{"EDITED", contested})
	r.activate(contestedVersion, expected, r.now)
	heldVersion, expected := r.draft(r.f.AssetID, itItem{"ACCEPTED", inventoryVersionHold})
	r.activate(heldVersion, expected, r.now.Add(time.Minute))
	anchoredVersion, expected := r.draft(r.asset(), itItem{"ACCEPTED", anchored})
	r.activate(anchoredVersion, expected, r.now)
	legacyVersion := identity.NewID()
	r.within(func(tx *gorm.DB) error {
		var origin database.Origin
		if err := tx.Where("tenant_id=? AND asset_id=?", r.f.TenantID, r.f.AssetID).Take(&origin).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.OriginVersion{ID: legacyVersion, TenantID: r.f.TenantID, OriginID: origin.ID, VersionNumber: 50, Status: "INVALIDATED", MediaKind: "VIDEO", VideoPreparationID: &legacyVersionHold.preparationID, IdempotencyKey: "ut317-legacy-" + legacyVersion.String(), CreatedAt: r.now, InvalidatedAt: &r.now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.CounterpartyInvitation{ID: identity.NewID(), TenantID: r.f.TenantID, OriginVersionID: contestedVersion, ParticipantID: r.f.ParticipantID, TokenHash: []byte("ut317-" + contestedVersion.String()), Channels: []byte(`[]`), DeadlineAt: r.now.Add(time.Hour), Response: "CONTESTED", Resolution: "OPEN", CreatedAt: r.now, UpdatedAt: r.now}).Error; err != nil {
			return err
		}
		for _, hold := range []database.LegalHold{
			{ID: identity.NewID(), TenantID: r.f.TenantID, SourceMediaID: &directHold.mediaID, Reason: "UT-317 source", Active: true, CreatedAt: r.now},
			{ID: identity.NewID(), TenantID: r.f.TenantID, InspectionID: &r.f.InspectionID, Reason: "UT-317 inspection", Active: true, CreatedAt: r.now},
			{ID: identity.NewID(), TenantID: r.f.TenantID, OriginVersionID: &heldVersion, Reason: "UT-317 inventory version", Active: true, CreatedAt: r.now},
			{ID: identity.NewID(), TenantID: r.f.TenantID, OriginVersionID: &legacyVersion, Reason: "UT-317 legacy version", Active: true, CreatedAt: r.now},
			{ID: identity.NewID(), TenantID: r.f.TenantID, SourceMediaID: &expired.mediaID, Reason: "UT-317 expired", Active: true, CreatedAt: r.now},
		} {
			if err := tx.Create(&hold).Error; err != nil {
				return err
			}
		}
		return tx.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", r.f.TenantID, expired.mediaID).Updates(map[string]any{"status": "EXPIRED", "original_deleted_at": r.now}).Error
	})

	cases := []struct {
		name   string
		source itSource
		want   string
	}{
		{"UT-317 active baseline", anchored, "WHILE_BASELINE_ACTIVE"},
		{"UT-317 open contestation on EDITED item", contested, "CONTESTATION_HOLD"},
		{"UT-317 direct legal hold", directHold, "LEGAL_HOLD"},
		{"UT-317 hold inherited through inspection", inspectionHold, "LEGAL_HOLD"},
		{"UT-317 hold inherited through inventory origin version", inventoryVersionHold, "LEGAL_HOLD"},
		{"UT-317 hold inherited through legacy origin version", legacyVersionHold, "LEGAL_HOLD"},
		{"UT-317 expired frames preserved", expired, "EXPIRED_FRAMES_PRESERVED"},
		{"UT-317 fixed date", fixed, "2031-03-04"},
	}
	var evidence []database.InventoryItemEvidence
	for _, tc := range cases {
		frameID := tc.source.frameID
		evidence = append(evidence, database.InventoryItemEvidence{ID: identity.NewID(), TenantID: r.f.TenantID, Kind: "VIDEO_FRAME", MediaID: tc.source.mediaID, FrameID: &frameID})
	}
	var statements atomic.Int64
	var counting atomic.Bool
	count := func(*gorm.DB) {
		if counting.Load() {
			statements.Add(1)
		}
	}
	if err := r.h.DB.Callback().Query().Before("gorm:query").Register("ut317:count_query", count); err != nil {
		t.Fatal(err)
	}
	if err := r.h.DB.Callback().Row().Before("gorm:row").Register("ut317:count_row", count); err != nil {
		t.Fatal(err)
	}
	load := func(rows []database.InventoryItemEvidence) (map[identity.ID]string, int64) {
		var labels map[identity.ID]string
		statements.Store(0)
		r.within(func(tx *gorm.DB) error {
			counting.Store(true)
			defer counting.Store(false)
			var err error
			labels, err = LoadRetentionAvailability(tx, r.f.TenantID, rows)
			return err
		})
		return labels, statements.Load()
	}
	labels, manyQueries := load(evidence)
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := labels[evidence[i].ID]; got != tc.want {
				t.Fatalf("label=%q, want %q", got, tc.want)
			}
		})
	}
	_, singleQueries := load(evidence[:1])
	if manyQueries == 0 || manyQueries != singleQueries || manyQueries > 3 {
		t.Fatalf("UT-317 LoadRetentionAvailability issued %d statements for %d evidence rows and %d for one; want the same bounded batch (<=3)", manyQueries, len(evidence), singleQueries)
	}
}

// otherInspectionResponsibility creates a responsibility in a second inspection
// so an inspection-level hold on the fixture inspection does not cover it.
func (r *retentionIT) otherInspectionResponsibility() identity.ID {
	r.t.Helper()
	responsibilityID := identity.NewID()
	r.within(func(tx *gorm.DB) error {
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", r.f.TenantID, r.f.InspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		inspection.ID = identity.NewID()
		inspection.SourceKey = "retention-it-" + inspection.ID.String()
		inspection.ProjectID, inspection.StageID = nil, nil
		if err := tx.Create(&inspection).Error; err != nil {
			return err
		}
		return tx.Create(&database.Responsibility{ID: responsibilityID, TenantID: r.f.TenantID, InspectionID: inspection.ID, ParticipantID: r.f.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: r.now, UpdatedAt: r.now}).Error
	})
	return responsibilityID
}
