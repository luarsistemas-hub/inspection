//go:build integration

package purge_data

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	promoteinspectionvideo "inspection/services/inspection/internal/features/origins/promote_inspection_video"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

// TestVV_IT_332PromotionAndPurgeRaceNeverDeletesProtectedBank overlaps origin
// promotion with inspection purge. Both serialize on the inspection lock, so
// either promotion commits its reference first and purge keeps the bank, or
// purge wins and promotion refuses the vanished bank.
func TestVV_IT_332PromotionAndPurgeRaceNeverDeletesProtectedBank(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	purgeDB := h.MigrationDB
	if purgeDB == nil {
		purgeDB = h.AdminDB
	}
	for _, order := range []string{"promotion-first", "purge-first", "concurrent", "concurrent", "concurrent"} {
		t.Run(order, func(t *testing.T) {
			bank := seedTask04PromotableBank(t, h)
			bus := mediator.New()
			if err := promoteinspectionvideo.Setup(promoteinspectionvideo.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Now: time.Now}); err != nil {
				t.Fatal(err)
			}
			promoteCtx := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: bank.fixture.TenantID, Principal: requestctx.Principal{IdentityID: bank.fixture.IdentityID, MembershipID: bank.fixture.MembershipID, TenantID: bank.fixture.TenantID, Roles: []string{auth.TenantAdmin}}})
			clock := retentioncore.Clock{Class: retentioncore.EvidenceReports, StartsAt: time.Now().UTC().Add(-time.Hour), DueAt: time.Now().UTC().Add(-time.Minute)}
			var promoteErr, purgeErr error
			promote := func() {
				_, promoteErr = bus.Send(promoteCtx, promoteinspectionvideo.Command{TenantID: bank.fixture.TenantID, InspectionID: bank.fixture.InspectionID, ClientMutationID: "vv-it-332"})
			}
			purge := func() {
				_, purgeErr = PurgeWithStore(ctx, purgeDB, h.Store, bank.fixture.TenantID, bank.fixture.InspectionID, clock)
			}
			switch order {
			case "promotion-first":
				promote()
				purge()
			case "purge-first":
				purge()
				promote()
			default:
				start := make(chan struct{})
				var wg sync.WaitGroup
				wg.Add(2)
				go func() { defer wg.Done(); <-start; promote() }()
				go func() { defer wg.Done(); <-start; purge() }()
				close(start)
				wg.Wait()
			}
			if purgeErr != nil {
				t.Fatalf("purge error: %v", purgeErr)
			}
			var versions []database.OriginVersion
			if err := h.AdminDB.Where("tenant_id=? AND video_preparation_id=? AND status='ACTIVE'", bank.fixture.TenantID, bank.preparationID).Find(&versions).Error; err != nil {
				t.Fatal(err)
			}
			var frames, references int64
			if err := h.AdminDB.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", bank.fixture.TenantID, bank.preparationID).Count(&frames).Error; err != nil {
				t.Fatal(err)
			}
			if err := h.AdminDB.Model(&database.VideoEvidenceReference{}).Where("tenant_id=? AND preparation_id=? AND owner_type='ORIGIN_VERSION'", bank.fixture.TenantID, bank.preparationID).Count(&references).Error; err != nil {
				t.Fatal(err)
			}
			objectExists := true
			if reader, _, err := (objectstore.VideoStore{Store: h.Store}).Open(ctx, bank.frameKey); err != nil {
				objectExists = false
			} else {
				_ = reader.Close()
			}
			if promoteErr == nil {
				if len(versions) != 1 || references != 1 || frames != 1 || !objectExists {
					t.Fatalf("promoted bank lost protected evidence: versions=%d references=%d frames=%d object=%t", len(versions), references, frames, objectExists)
				}
			} else if len(versions) != 0 || references != 0 {
				t.Fatalf("refused promotion left origin state: err=%v versions=%d references=%d", promoteErr, len(versions), references)
			}
			if order == "promotion-first" && promoteErr != nil {
				t.Fatalf("promotion before purge failed: %v", promoteErr)
			}
			if order == "purge-first" && (promoteErr == nil || frames != 0 || objectExists) {
				t.Fatalf("purge before promotion: promoteErr=%v frames=%d object=%t; want refusal of the purged bank", promoteErr, frames, objectExists)
			}
		})
	}
}

type task04PromotableBank struct {
	fixture       harness.Task06Fixture
	preparationID identity.ID
	frameKey      string
}

func seedTask04PromotableBank(t *testing.T, h *harness.Harness) task04PromotableBank {
	t.Helper()
	ctx := context.Background()
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	firstReady, expiry := now.Add(-time.Hour), now.Add(5*24*time.Hour)
	attemptID, mediaID, submissionID := identity.NewID(), identity.NewID(), identity.NewID()
	preparationID, frameID, planID := identity.NewID(), identity.NewID(), identity.NewID()
	digest := strings.Repeat("f", 64)
	frameKey := "it332/" + frameID.String() + ".jpg"
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, frameKey, strings.NewReader("frame"), 5, "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Store.Delete(context.Background(), frameKey) })
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Update("media_kind", "VIDEO").Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "it332-" + attemptID.String(), PayloadDigest: digest, AdmittedAt: firstReady, ExpiresAt: now, CreatedAt: firstReady}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "it332/" + mediaID.String() + ".mp4", ContentType: "video/mp4", SHA256: digest, SizeBytes: 100, Status: "READY", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "it332-video-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: firstReady}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: fixture.TenantID, DraftID: fixture.DraftID, ResponsibilityID: fixture.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: json.RawMessage(`{}`), PayloadDigest: digest, SubmittedAt: firstReady}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: firstReady, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &firstReady, CreatedAt: firstReady}).Error; err != nil {
			return err
		}
		days := 5
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &firstReady, RetentionDays: &days, SourceExpiryAt: &expiry, Status: "RETAINED", CreatedAt: firstReady, UpdatedAt: firstReady}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: digest, ObjectKey: frameKey, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 5, CreatedAt: firstReady}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoScopeItem{ID: identity.NewID(), TenantID: fixture.TenantID, PreparationID: preparationID, FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", Landmarks: json.RawMessage(`[]`), CreatedAt: firstReady}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlan{ID: planID, TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, SubmissionID: submissionID, CurrentPreparationID: preparationID, Generation: 1, PlanDigest: digest, Status: "SEALED", SealedAt: &firstReady, NextAttemptAt: firstReady, CreatedAt: firstReady}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoEvidenceReference{ID: identity.NewID(), TenantID: fixture.TenantID, PreparationID: preparationID, OwnerType: "VIDEO_PLAN", OwnerID: planID, CreatedAt: firstReady}).Error
	}); err != nil {
		t.Fatal(err)
	}
	return task04PromotableBank{fixture: fixture, preparationID: preparationID, frameKey: frameKey}
}
