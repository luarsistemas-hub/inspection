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

type f6SubmissionFinalizer struct{}

func (f6SubmissionFinalizer) Finalize(context.Context, *gorm.DB, database.CaptureDraft, database.SubmissionVersion) error {
	return nil
}

func TestReplayOriginSubmitCaptureReturnsSameSubmissionIDIT040(t *testing.T) {
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
	attemptID, mediaID, invitationID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).Updates(map[string]any{"kind": "ORIGIN", "media_kind": "VIDEO", "status": "OPEN", "requirements": json.RawMessage(`[{"key":"video:main","required":true,"minimumCount":1,"maximumCount":1}]`)}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ProcessingAcceptance{ID: identity.NewID(), TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, DisclosureVersion: "video-v1", PhotoProcessing: false, VideoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "ORIGIN", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "it040-attempt", PayloadDigest: strings.Repeat("a", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "it040/source.mp4", ContentType: "video/mp4", SHA256: strings.Repeat("a", 64), SizeBytes: 10, Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "it040-media", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	pepper := []byte(strings.Repeat("p", 32))
	token, err := security.NewScopedToken(fixture.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf := "it040-csrf"
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
	if err := h.AdminDB.Create(&database.ExternalSession{ID: identity.NewID(), TenantID: fixture.TenantID, InvitationID: identity.Ptr(invitationID), ResponsibilityID: &fixture.ResponsibilityID, SessionDigest: hash[:], CSRFDigest: proof[:], ExpiresAt: expiresAt, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	service := capturecore.Service{DB: h.DB, Finalizer: f6SubmissionFinalizer{}}
	if err := capturesubmit.Setup(capturesubmit.Dependencies{Bus: bus, Service: service}); err != nil {
		t.Fatal(err)
	}
	server := captureGraphQLServerWithDB(bus, invitationcore.Service{DB: h.DB, Pepper: pepper}, h.DB)
	query := `mutation { submitCapture(input:{confirmIncomplete:false,clientMutationId:"k3"}) { submission { id } clientMutationId } }`
	credentials := requestctx.ExternalCredentials{SessionToken: token, CSRFToken: csrf}
	first := executeCaptureGraphQL(t, server, query, fixture.TenantID, credentials, true)
	second := executeCaptureGraphQL(t, server, query, fixture.TenantID, credentials, true)
	var ids []string
	for _, body := range []string{first, second} {
		var payload struct {
			Data struct {
				Submit struct {
					Submission struct {
						ID string `json:"id"`
					} `json:"submission"`
					ClientMutationID string `json:"clientMutationId"`
				} `json:"submitCapture"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(body), &payload); err != nil || payload.Data.Submit.Submission.ID == "" || payload.Data.Submit.ClientMutationID != "k3" {
			t.Fatalf("submitCapture k3 response missing immutable submission: %s (%v)", body, err)
		}
		ids = append(ids, payload.Data.Submit.Submission.ID)
	}
	if ids[0] != ids[1] {
		t.Fatalf("replayed submitCapture changed immutable submission ID: first=%s replay=%s", ids[0], ids[1])
	}
	var count int64
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.SubmissionVersion{}).Where("tenant_id=? AND draft_id=?", fixture.TenantID, fixture.DraftID).Count(&count).Error
	}); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("replay created %d submission rows, want exactly one", count)
	}
}
