//go:build integration

package resolvers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	mediacore "inspection/services/inspection/internal/features/media/core"
	mediareconcile "inspection/services/inspection/internal/features/media/reconcile_upload"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
)

func TestOriginUploadResumeStatusContainsOnlyConfirmedPartIT039(t *testing.T) {
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
	attemptID, invitationID := identity.NewID(), identity.NewID()
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).Updates(map[string]any{"kind": "ORIGIN", "media_kind": "VIDEO", "status": "OPEN"}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "ORIGIN", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: objectstore.MaxVideoBytes, AcquisitionSource: "CAMERA", Status: "ACTIVE", IdempotencyKey: "it039-" + attemptID.String(), PayloadDigest: "it039", AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	pepper := []byte(strings.Repeat("u", 32))
	token, err := security.NewScopedToken(fixture.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf := "it039-csrf"
	hash := security.HashToken(token)
	proof, err := security.CSRFProof(hash, csrf, pepper)
	if err != nil {
		t.Fatal(err)
	}
	invitation := database.Invitation{ID: invitationID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, TokenHash: hash[:], DeliveryIntents: json.RawMessage(`[]`), Status: "ACTIVE", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := h.AdminDB.Create(&invitation).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Create(&database.ExternalSession{ID: identity.NewID(), TenantID: fixture.TenantID, InvitationID: identity.Ptr(invitationID), ResponsibilityID: &fixture.ResponsibilityID, SessionDigest: hash[:], CSRFDigest: proof[:], ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	video := bytes.Repeat([]byte("x"), objectstore.PartSizeBytes+17)
	digest := sha256.Sum256(video)
	service := mediacore.Service{DB: h.DB, Store: h.Store, Within: h.WithinTenant}
	upload, err := service.CreateVideoWithAttempt(ctx, fixture.TenantID, fixture.ResponsibilityID, attemptID, "video/mp4", hex.EncodeToString(digest[:]), int64(len(video)), "it039-upload")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = h.Store.Client.AbortMultipart(context.Background(), h.Store.Bucket, upload.ObjectKey, upload.UploadID)
	})
	signed, err := service.Presign(ctx, fixture.TenantID, fixture.ResponsibilityID, upload.MediaID, []int{1})
	if err != nil || len(signed) != 1 {
		t.Fatalf("presign part 1: %v (%d URLs)", err, len(signed))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, signed[0].URL, bytes.NewReader(video[:objectstore.PartSizeBytes]))
	if err != nil {
		t.Fatal(err)
	}
	response, err := h.HTTP.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("ETag") == "" {
		t.Fatalf("upload part 1: status=%d etag=%q", response.StatusCode, response.Header.Get("ETag"))
	}
	bus := mediator.New()
	if err := mediareconcile.Setup(mediareconcile.Dependencies{Bus: bus, Service: service}); err != nil {
		t.Fatal(err)
	}
	server := captureGraphQLServer(bus, invitationcore.Service{DB: h.DB, Pepper: pepper})
	query := `query { videoUploadStatus(mediaId:"` + upload.MediaID.String() + `") { status receivedParts confirmedParts { partNumber etag sizeBytes } missingParts } }`
	body := executeCaptureGraphQL(t, server, query, fixture.TenantID, requestctx.ExternalCredentials{SessionToken: token, CSRFToken: csrf}, true)
	if strings.Contains(body, `"errors"`) || !strings.Contains(body, `"receivedParts":[1]`) || !strings.Contains(body, `"partNumber":1`) || !strings.Contains(body, `"sizeBytes":5242880`) || !strings.Contains(body, `"missingParts":[2]`) || strings.Contains(body, `"partNumber":2`) {
		t.Fatalf("resumed origin upload exposed unconfirmed part state: %s", body)
	}
}
