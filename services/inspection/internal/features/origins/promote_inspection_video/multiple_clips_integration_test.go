//go:build integration

package promote_inspection_video

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
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestPromotionRejectsMultipleClipsUsingTheActiveSealedPlan(t *testing.T) {
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
	planID, submissionID := identity.NewID(), identity.NewID()
	preparations := []identity.ID{identity.NewID(), identity.NewID()}
	mediaIDs, attemptIDs := []identity.ID{identity.NewID(), identity.NewID()}, []identity.ID{identity.NewID(), identity.NewID()}
	now := time.Now().UTC()
	err = h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Update("media_kind", "VIDEO").Error; err != nil {
			return err
		}
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: fixture.TenantID, DraftID: fixture.DraftID, ResponsibilityID: fixture.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: json.RawMessage(`{}`), PayloadDigest: strings.Repeat("a", 64), SubmittedAt: now}).Error; err != nil {
			return err
		}
		for i := range preparations {
			if err := tx.Create(&database.VideoAttempt{ID: attemptIDs[i], TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "multi-clip-attempt-" + attemptIDs[i].String(), PayloadDigest: strings.Repeat("a", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.MediaObject{ID: mediaIDs[i], TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "multi-clips/" + mediaIDs[i].String(), ContentType: "video/mp4", SHA256: strings.Repeat("a", 64), SizeBytes: 100, Status: "READY", MediaKind: "VIDEO", VideoAttemptID: &attemptIDs[i], RequirementKey: "video:clip:" + preparations[i].String(), IdempotencyKey: "multi-clip-media-" + mediaIDs[i].String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.VideoPreparationJob{ID: preparations[i], TenantID: fixture.TenantID, MediaID: mediaIDs[i], SubmissionID: &submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: strings.Repeat("a", 64), ManifestDigest: strings.Repeat("a", 64), PendingObjectKeys: []byte(`[]`), FirstReadyAt: &now, CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(&database.VideoPlan{ID: planID, TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, SubmissionID: submissionID, CurrentPreparationID: preparations[0], Generation: 1, PlanDigest: "active-sealed-plan", Status: "SEALED", SealedAt: &now, NextAttemptAt: now, CreatedAt: now}).Error; err != nil {
			return err
		}
		for i, preparationID := range preparations {
			role := "SUPPLEMENT"
			if i == 0 {
				role = "MAIN"
			}
			if err := tx.Create(&database.VideoPlanPreparation{TenantID: fixture.TenantID, PlanID: planID, PreparationID: preparationID, Role: role, CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Update("active_video_plan_id", planID).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := Setup(Dependencies{DB: h.DB, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	ctx = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: fixture.TenantID, Principal: requestctx.Principal{IdentityID: fixture.IdentityID, MembershipID: fixture.MembershipID, TenantID: fixture.TenantID, Roles: []string{auth.TenantAdmin}}})
	_, err = bus.Send(ctx, Command{TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, ClientMutationID: "multiple-clips"})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.InvalidState || appErr.Safe != "Esta vistoria tem mais de um vídeo. Use a ação “Usar esta vistoria como origem” para promover todos os dados." {
		t.Fatalf("multiple clip error = %v", err)
	}
	var count int64
	if err := h.AdminDB.Model(&database.OriginVersion{}).Where("tenant_id=? AND idempotency_key=?", fixture.TenantID, "video-promotion:"+fixture.InspectionID.String()).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("partial video origin was created (%d rows)", count)
	}
}
