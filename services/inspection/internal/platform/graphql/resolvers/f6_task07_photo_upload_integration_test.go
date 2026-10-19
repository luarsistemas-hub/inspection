//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	mediacore "inspection/services/inspection/internal/features/media/core"
	mediacreate "inspection/services/inspection/internal/features/media/create_upload"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

type photoUploadFixture struct {
	h           *harness.Harness
	seed        harness.Task06Fixture
	server      *handler.Server
	credentials requestctx.ExternalCredentials
}

// newPhotoUploadFixture opens a PHOTO capture draft with an external session.
func newPhotoUploadFixture(t *testing.T, draftKind string) photoUploadFixture {
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
	seed, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", seed.TenantID, seed.ResponsibilityID).Updates(map[string]any{"status": "OPEN", "media_kind": "PHOTO", "kind": draftKind}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	pepper := []byte(strings.Repeat("p", 32))
	token, err := security.NewScopedToken(seed.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf := "vv-photo-upload-csrf"
	hash := security.HashToken(token)
	proof, err := security.CSRFProof(hash, csrf, pepper)
	if err != nil {
		t.Fatal(err)
	}
	invitation := database.Invitation{ID: identity.NewID(), TenantID: seed.TenantID, ResponsibilityID: seed.ResponsibilityID, TokenHash: hash[:], DeliveryIntents: json.RawMessage(`[]`), Status: "ACTIVE", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := h.AdminDB.Create(&invitation).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Create(&database.ExternalSession{ID: identity.NewID(), TenantID: seed.TenantID, InvitationID: invitation.ID, ResponsibilityID: seed.ResponsibilityID, SessionDigest: hash[:], CSRFDigest: proof[:], ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := mediacreate.Setup(mediacreate.Dependencies{Bus: bus, Service: mediacore.Service{DB: h.DB, Store: objectstore.Store{Bucket: h.Store.Bucket, Client: h.Store.Client}, Within: h.WithinTenant}}); err != nil {
		t.Fatal(err)
	}
	return photoUploadFixture{h: h, seed: seed, server: captureGraphQLServer(bus, invitationcore.Service{DB: h.DB, Pepper: pepper}), credentials: requestctx.ExternalCredentials{SessionToken: token, CSRFToken: csrf}}
}

func (fx photoUploadFixture) upload(t *testing.T, contentType string, size int64, attemptID *identity.ID, key string) string {
	t.Helper()
	attempt := ""
	if attemptID != nil {
		attempt = `,attemptId:"` + attemptID.String() + `"`
	}
	mutation := `mutation { createMediaUpload(input:{contentType:"` + contentType + `",sizeBytes:` + strconv.FormatInt(size, 10) + `,sha256:"` + strings.Repeat("a", 64) + `"` + attempt + `,clientMutationId:"` + key + `"}) { upload { mediaId } } }`
	return executeCaptureGraphQL(t, fx.server, mutation, fx.seed.TenantID, fx.credentials, true)
}

func (fx photoUploadFixture) mediaCount(t *testing.T) int64 {
	t.Helper()
	var count int64
	if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=?", fx.seed.TenantID, fx.seed.ResponsibilityID).Count(&count).Error
	}); err != nil {
		t.Fatal(err)
	}
	return count
}

// forgeVideoAttempt plants an attempt that PHOTO admission would never issue.
func (fx photoUploadFixture) forgeVideoAttempt(t *testing.T) identity.ID {
	t.Helper()
	now := time.Now().UTC()
	id := identity.NewID()
	if err := fx.h.AdminDB.Create(&database.VideoAttempt{ID: id, TenantID: fx.seed.TenantID, ResponsibilityID: fx.seed.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "ACTIVE", IdempotencyKey: "vv-photo-forged-" + id.String(), ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	return id
}

func assertPhotoUploadRejected(t *testing.T, fx photoUploadFixture, before int64, response, code string) {
	t.Helper()
	if !strings.Contains(response, `"code":"`+code+`"`) || strings.Contains(response, `"upload":{"mediaId"`) {
		t.Fatalf("upload response=%s; want %s without an upload", response, code)
	}
	if after := fx.mediaCount(t); after != before {
		t.Fatalf("rejected upload changed media rows %d -> %d", before, after)
	}
}

func TestVV_IT_357VideoUploadForPhotoInspectionIsInvalidInput(t *testing.T) {
	fx := newPhotoUploadFixture(t, "INSPECTION")
	before := fx.mediaCount(t)
	assertPhotoUploadRejected(t, fx, before, fx.upload(t, "video/mp4", 1024, nil, "vv-it-357-plain"), "INVALID_INPUT")
	attempt := fx.forgeVideoAttempt(t)
	assertPhotoUploadRejected(t, fx, before, fx.upload(t, "video/mp4", 1024, &attempt, "vv-it-357-attempt"), "INVALID_INPUT")
}

func TestVV_IT_359PhotoAboveTwentyMebibytesKeepsPhotoRejection(t *testing.T) {
	fx := newPhotoUploadFixture(t, "INSPECTION")
	before := fx.mediaCount(t)
	response := fx.upload(t, "image/jpeg", objectstore.MaxOriginalBytes+1, nil, "vv-it-359-large")
	assertPhotoUploadRejected(t, fx, before, response, "INVALID_INPUT")
	if !strings.Contains(response, "sizeBytes") {
		t.Fatalf("oversized photo was not rejected by the photo size ceiling: %s", response)
	}
	if objectstore.MaxOriginalBytes != 20<<20 || objectstore.MaxVideoBytes <= objectstore.MaxOriginalBytes {
		t.Fatalf("photo ceiling=%d video ceiling=%d; want 20 MiB photo limit below the video allowance", objectstore.MaxOriginalBytes, objectstore.MaxVideoBytes)
	}
	// The same photo at exactly the existing ceiling is still admitted.
	if ok := fx.upload(t, "image/jpeg", objectstore.MaxOriginalBytes, nil, "vv-it-359-ceiling"); !strings.Contains(ok, `"upload":{"mediaId"`) {
		t.Fatalf("photo at the 20 MiB ceiling was rejected: %s", ok)
	}
}

func TestVV_IT_355VideoUploadInPhotoRecaptureIsRejected(t *testing.T) {
	fx := newPhotoUploadFixture(t, "RECAPTURE")
	before := fx.mediaCount(t)
	assertPhotoUploadRejected(t, fx, before, fx.upload(t, "video/mp4", 1024, nil, "vv-it-355-plain"), "INVALID_INPUT")
	attempt := fx.forgeVideoAttempt(t)
	assertPhotoUploadRejected(t, fx, before, fx.upload(t, "video/mp4", 1024, &attempt, "vv-it-355-attempt"), "INVALID_INPUT")
}
