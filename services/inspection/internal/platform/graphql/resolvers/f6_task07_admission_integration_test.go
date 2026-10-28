//go:build integration

package resolvers

import (
	"context"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestVV_IT_379VideoCaptureUsesCurrentPolicyWithLegacyAdmissionDisabled(t *testing.T) {
	// v1 pinned until template schema 1 is removed
	fixture := newAPI03FailureFixtureWithOptions(t, "beginVideoCapture", harness.Task06FixtureOptions{SchemaVersion: 1, RequirementKey: "room-1"})
	now := time.Now().UTC().Truncate(time.Microsecond)
	t0 := now.Add(-24 * time.Hour)
	deadline := t0.Add(5 * 24 * time.Hour)
	retentionDays := 5
	mediaID := identity.NewID()
	if err := fixture.harness.AdminDB.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.tenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", RetentionDays: &retentionDays, SourceExpiryAt: &deadline, Status: "RETAINED", CreatedAt: t0, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.AdminDB.Exec(`INSERT INTO media.video_policies (tenant_id, max_duration_minutes, origin_retention_days, current_retention_days, version, last_mutation_id, last_payload_digest, updated_at) VALUES (?, 15, 30, 7, 2, 'vv-it-379-policy', '', ?) ON CONFLICT (tenant_id) DO UPDATE SET current_retention_days=7, version=2, updated_at=EXCLUDED.updated_at`, fixture.tenantID, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.AdminDB.Exec(`INSERT INTO platform.video_admission (id, enabled, version, changed_at, reason, mutation_id, payload_hash) VALUES (1, FALSE, 2, ?, 'legacy disabled', 'vv-it-379-legacy', '') ON CONFLICT (id) DO UPDATE SET enabled=FALSE, version=2, changed_at=EXCLUDED.changed_at, reason=EXCLUDED.reason, mutation_id=EXCLUDED.mutation_id, payload_hash=''`, now).Error; err != nil {
		t.Fatal(err)
	}
	begin := `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"vv-it-379-current-attempt"}) { attempt { kind policyVersion status } } }`
	started := executeAPI03WithCredentials(t, fixture, begin)
	if strings.Contains(started, `"errors"`) || !strings.Contains(started, `"kind":"CURRENT"`) || !strings.Contains(started, `"policyVersion":2`) {
		t.Fatalf("new VIDEO attempt did not use the current tenant policy: %s", started)
	}
	var source database.VideoSource
	if err := fixture.harness.AdminDB.Where("tenant_id=? AND media_id=?", fixture.tenantID, mediaID).Take(&source).Error; err != nil {
		t.Fatal(err)
	}
	if source.SourceExpiryAt == nil || !source.SourceExpiryAt.Equal(deadline) || !deadline.Equal(now.Add(4*24*time.Hour)) {
		t.Fatalf("C1 deadline moved after resume: expiry=%v, want T0+5d=%s", source.SourceExpiryAt, deadline)
	}
}

func TestVV_IT_368VideoRecaptureRemainsAvailableWithLegacyAdmissionDisabled(t *testing.T) {
	// v1 pinned until template schema 1 is removed
	fixture := newAPI03FailureFixtureWithOptions(t, "beginVideoCapture", harness.Task06FixtureOptions{SchemaVersion: 1, RequirementKey: "room-1"})
	if err := fixture.harness.AdminDB.Exec(`INSERT INTO platform.video_admission (id, enabled, version, changed_at) VALUES (1, FALSE, 1, ?) ON CONFLICT (id) DO UPDATE SET enabled=FALSE`, time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	continueV1 := `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"vv-it-368-accepted-v1"}) { attempt { kind status } } }`
	continued := executeAPI03WithCredentials(t, fixture, continueV1)
	if strings.Contains(continued, `"errors"`) || !strings.Contains(continued, `"kind":"CURRENT"`) || !strings.Contains(continued, `"status":"ACTIVE"`) {
		t.Fatalf("already accepted V1 could not continue during suspension: %s", continued)
	}
	var attemptID identity.ID
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		var attempt database.VideoAttempt
		if err := tx.Where("tenant_id=? AND responsibility_id=? AND idempotency_key=?", fixture.tenantID, fixture.responsibilityID, "vv-it-368-accepted-v1").Take(&attempt).Error; err != nil {
			return err
		}
		attemptID = attempt.ID
		if err := tx.Model(&attempt).Update("status", "REJECTED").Error; err != nil {
			return err
		}
		return tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Update("kind", "RECAPTURE").Error
	}); err != nil {
		t.Fatal(err)
	}

	newRecapture := `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"vv-it-368-recapture"}) { attempt { kind status } } }`
	recaptured := executeAPI03WithCredentials(t, fixture, newRecapture)
	if strings.Contains(recaptured, `"errors"`) || !strings.Contains(recaptured, `"kind":"SUPPLEMENT"`) || !strings.Contains(recaptured, `"status":"ACTIVE"`) {
		t.Fatalf("permitted recapture lost its VIDEO modality: %s", recaptured)
	}
	var attempts []database.VideoAttempt
	var draft database.CaptureDraft
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Order("created_at").Find(&attempts).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Take(&draft).Error
	}); err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].ID != attemptID || attempts[1].Kind != "SUPPLEMENT" || draft.MediaKind != "VIDEO" || draft.Kind != "RECAPTURE" {
		t.Fatalf("V1/recapture modality changed: attempts=%+v draft=%+v", attempts, draft)
	}
}

func TestVV_IT_369StoppedPreparationRemainsPendingOrFailedInStatus(t *testing.T) {
	fx := newAPI08Fixture(t)
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.InspectionID).Update("status", "ANALYZING").Error; err != nil {
			return err
		}
		return tx.Model(&database.Responsibility{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.ResponsibilityID).Update("status", "SUBMITTED").Error
	}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []struct{ status, expected string }{{"PROCESSING", `"status":"PROCESSING"`}, {"FAILED", `"status":"FAILED"`}} {
		t.Run(state.status, func(t *testing.T) {
			preparation := seedAPI08Preparation(t, fx, state.status, 1, "RETAINED")
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api08Query(fx, "videoPreparation", preparation.mediaID, 0, "", ""), fx.ctx, "", "")
			if strings.Contains(response, `"errors"`) || !strings.Contains(response, state.expected) || strings.Contains(response, `"status":"COMPLETED"`) {
				t.Fatalf("stopped decoder/gateway state was reported as completed analysis: %s", response)
			}
			var snapshots int64
			if err := fx.h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, fx.seed.InspectionID).Count(&snapshots).Error; err != nil {
				t.Fatal(err)
			}
			if snapshots != 0 {
				t.Fatalf("pending V1 unexpectedly has %d completed report snapshots", snapshots)
			}
		})
	}
}
