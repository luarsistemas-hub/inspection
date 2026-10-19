//go:build integration

package resolvers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	originlist "inspection/services/inspection/internal/features/origins/list_versions"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestVV_IT_070PromotionReplayReturnsOriginalVersionID(t *testing.T) {
	fx := newFixedOriginGraphQLFixture(t, false)
	seedPromotableVideo(t, fx, "RETAINED", true)
	if err := originlist.Setup(originlist.Dependencies{DB: fx.h.DB, Bus: fx.bus, Authorizer: auth.Authorizer{}, Within: fx.h.WithinTenant}); err != nil {
		t.Fatal(err)
	}
	first := promoteVideoMutation(t, fx, "k5")
	second := promoteVideoMutation(t, fx, "k5")
	firstID := promotedVersionID(t, first)
	secondID := promotedVersionID(t, second)
	if firstID == "" || secondID != firstID {
		t.Fatalf("promotion replay returned IDs %q and %q; want the original version ID", firstID, secondID)
	}
}

func TestVV_IT_071PromotionBeforePreparationAndPlanReadyReturnsInvalidState(t *testing.T) {
	fx := newFixedOriginGraphQLFixture(t, false)
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.InspectionID).Updates(map[string]any{"status": "COMPLETED", "media_kind": "VIDEO"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	response := promoteVideoMutation(t, fx, "it071")
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"version":null`) || !strings.Contains(response, `"code":"INVALID_STATE"`) {
		t.Fatalf("promotion before preparation/plan readiness should return INVALID_STATE userError, got %s", response)
	}
}

func TestVV_IT_072PromotionAfterSourceExpiryUsesRetainedBankAndReportsExpiredAvailability(t *testing.T) {
	fx := newFixedOriginGraphQLFixture(t, false)
	seedPromotableVideo(t, fx, "EXPIRED", true)
	if err := originlist.Setup(originlist.Dependencies{DB: fx.h.DB, Bus: fx.bus, Authorizer: auth.Authorizer{}, Within: fx.h.WithinTenant}); err != nil {
		t.Fatal(err)
	}
	response := promoteVideoMutation(t, fx, "it072")
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"userErrors":[]`) || !strings.Contains(response, `"sourceAvailability":"EXPIRED"`) {
		t.Fatalf("expired source was not promoted from retained bank with EXPIRED availability: %s", response)
	}
	if !strings.Contains(response, `"preparationStatus":"READY"`) {
		t.Fatalf("promotion lost the retained READY frame bank: %s", response)
	}
}

func seedPromotableVideo(t *testing.T, fx fixedOriginGraphQLFixture, sourceStatus string, ready bool) (preparationID, frameID identity.ID) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	firstReady := now.Add(-40 * 24 * time.Hour)
	manifest := strings.Repeat("d", 64)
	attemptID, mediaID, submissionID := identity.NewID(), identity.NewID(), identity.NewID()
	preparationID, frameID, planID := identity.NewID(), identity.NewID(), identity.NewID()
	err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.InspectionID).Updates(map[string]any{"status": "COMPLETED", "media_kind": "VIDEO"}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fx.seed.TenantID, ResponsibilityID: fx.seed.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "it07x-attempt-" + mediaID.String(), PayloadDigest: manifest, AdmittedAt: firstReady, ExpiresAt: now.Add(time.Hour), CreatedAt: firstReady}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fx.seed.TenantID, ResponsibilityID: fx.seed.ResponsibilityID, ObjectKey: "it07x/" + mediaID.String(), ContentType: "video/mp4", SHA256: manifest, SizeBytes: 100, Status: "READY", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "it07x-media-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: firstReady}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: fx.seed.TenantID, DraftID: fx.seed.DraftID, ResponsibilityID: fx.seed.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: json.RawMessage(`{}`), PayloadDigest: manifest, SubmittedAt: firstReady}).Error; err != nil {
			return err
		}
		if ready {
			if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fx.seed.TenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: firstReady, SourceSHA256: manifest, ManifestDigest: manifest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &firstReady, CreatedAt: firstReady}).Error; err != nil {
				return err
			}
			days := 30
			expiry := firstReady.Add(30 * 24 * time.Hour)
			deletedAt := now
			if sourceStatus != "EXPIRED" {
				deletedAt = time.Time{}
			}
			source := database.VideoSource{ID: identity.NewID(), TenantID: fx.seed.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &firstReady, RetentionDays: &days, SourceExpiryAt: &expiry, Status: sourceStatus, CreatedAt: firstReady, UpdatedAt: now}
			if sourceStatus == "EXPIRED" {
				source.OriginalDeletedAt = &deletedAt
			}
			if err := tx.Create(&source).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: fx.seed.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: manifest, ObjectKey: "it07x/frame/" + frameID.String(), Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 10, CreatedAt: firstReady}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.VideoScopeItem{ID: identity.NewID(), TenantID: fx.seed.TenantID, PreparationID: preparationID, FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", Landmarks: json.RawMessage(`[]`), CreatedAt: firstReady}).Error; err != nil {
				return err
			}
			return tx.Create(&database.VideoPlan{ID: planID, TenantID: fx.seed.TenantID, InspectionID: fx.seed.InspectionID, SubmissionID: submissionID, CurrentPreparationID: preparationID, Generation: 1, PlanDigest: manifest, Status: "SEALED", SealedAt: &firstReady, CreatedAt: firstReady}).Error
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return preparationID, frameID
}

func promoteVideoMutation(t *testing.T, fx fixedOriginGraphQLFixture, clientMutationID string) string {
	t.Helper()
	query := `mutation { promoteInspectionVideo(input:{inspectionId:"` + fx.seed.InspectionID.String() + `",clientMutationId:"` + clientMutationID + `"}) { version { id mediaKind preparationStatus selectable sourceAvailability } userErrors { code field } clientMutationId } }`
	return executeTask06GraphQL(t, fx.server, query, fx.ctx)
}

func promotedVersionID(t *testing.T, response string) string {
	t.Helper()
	var payload struct {
		Data struct {
			Promote struct {
				Version *struct {
					ID string `json:"id"`
				} `json:"version"`
				UserErrors []json.RawMessage `json:"userErrors"`
			} `json:"promoteInspectionVideo"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil || len(payload.Errors) > 0 || payload.Data.Promote.Version == nil || len(payload.Data.Promote.UserErrors) != 0 {
		t.Fatalf("expected successful promotion response, got %s (decode error: %v)", response, err)
	}
	return payload.Data.Promote.Version.ID
}
