//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	capturesubmit "inspection/services/inspection/internal/features/capture/submit"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
)

func TestVV_IT_219SubmitCaptureBeforeQualityChecksReturnsPreparationPending(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	attemptID, mediaID, invitationID, preparationID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).Updates(map[string]any{"kind": "ORIGIN", "media_kind": "VIDEO", "status": "OPEN", "requirements": json.RawMessage(`[{"key":"video:main","required":true,"minimumCount":1,"maximumCount":1}]`)}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ProcessingAcceptance{ID: identity.NewID(), TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, DisclosureVersion: "video-v1", PhotoProcessing: false, VideoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "ORIGIN", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "it219-attempt", PayloadDigest: strings.Repeat("a", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "it219/source.mp4", ContentType: "video/mp4", SHA256: strings.Repeat("a", 64), SizeBytes: 10, Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "it219-media", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		// The source was received, but validation and quality checks have
		// not run: the preparation is still queued in its RECEIVED phase.
		return tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, Status: "QUEUED", Phase: "RECEIVED", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, PendingObjectKeys: []byte(`[]`), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	pepper := []byte(strings.Repeat("p", 32))
	token, err := security.NewScopedToken(fixture.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf := "it219-csrf"
	hash := security.HashToken(token)
	proof, err := security.CSRFProof(hash, csrf, pepper)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := now.Add(time.Hour)
	invitation := database.Invitation{ID: invitationID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, TokenHash: hash[:], DeliveryIntents: json.RawMessage(`[]`), Status: "ACTIVE", ExpiresAt: expiresAt, CreatedAt: now}
	if err := h.AdminDB.Create(&invitation).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Create(&database.ExternalSession{ID: identity.NewID(), TenantID: fixture.TenantID, InvitationID: invitationID, ResponsibilityID: fixture.ResponsibilityID, SessionDigest: hash[:], CSRFDigest: proof[:], ExpiresAt: expiresAt, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	service := capturecore.Service{DB: h.DB, Finalizer: f6SubmissionFinalizer{}}
	if err := capturesubmit.Setup(capturesubmit.Dependencies{Bus: bus, Service: service}); err != nil {
		t.Fatal(err)
	}
	server := captureGraphQLServerWithDB(bus, invitationcore.Service{DB: h.DB, Pepper: pepper}, h.DB)
	query := `mutation { submitCapture(input:{confirmIncomplete:false,clientMutationId:"k219"}) { submission { id preparationPending } userErrors { code } clientMutationId } }`
	credentials := requestctx.ExternalCredentials{SessionToken: token, CSRFToken: csrf}
	decode := func(body string) (string, bool) {
		t.Helper()
		var payload struct {
			Data struct {
				Submit struct {
					Submission *struct {
						ID                 string `json:"id"`
						PreparationPending bool   `json:"preparationPending"`
					} `json:"submission"`
					UserErrors []struct {
						Code string `json:"code"`
					} `json:"userErrors"`
				} `json:"submitCapture"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(body), &payload); err != nil || payload.Data.Submit.Submission == nil || len(payload.Data.Submit.UserErrors) != 0 {
			t.Fatalf("submitCapture before quality checks did not succeed: %s (%v)", body, err)
		}
		return payload.Data.Submit.Submission.ID, payload.Data.Submit.Submission.PreparationPending
	}
	submissionID, pending := decode(executeCaptureGraphQL(t, server, query, fixture.TenantID, credentials, true))
	if !pending {
		t.Fatal("submitCapture accepted a RECEIVED source without preparationPending=true")
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", fixture.TenantID, preparationID).Updates(map[string]any{"status": "READY", "phase": "READY"}).Error
	}); err != nil {
		t.Fatal(err)
	}
	replayID, pending := decode(executeCaptureGraphQL(t, server, query, fixture.TenantID, credentials, true))
	if replayID != submissionID || pending {
		t.Fatalf("replay after READY = %s pending=%t, want %s pending=false", replayID, pending, submissionID)
	}
}
