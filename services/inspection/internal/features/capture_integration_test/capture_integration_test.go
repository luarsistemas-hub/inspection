package capture_integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	capturefinalize "inspection/services/inspection/internal/features/capture/finalize_submission"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	mediacore "inspection/services/inspection/internal/features/media/core"
	origincore "inspection/services/inspection/internal/features/origins/core"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/sensitivecontent"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTask5IT071ToIT100IT141ToIT200IT221ToIT240IT311ToIT320(t *testing.T) {
	db := captureDB(t)
	within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	tenantID, assetID, templateID, templateVersionID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	delivery := []invitationcore.DeliveryIntent{{Channel: "EMAIL", Destination: "capture@example.com"}}
	requirements := []capturecore.Requirement{{Key: "overview", Required: true, ImpossibilityAllowed: true, MinimumMedia: 1, MaximumMedia: 2, DescriptionRequired: true, CaptureSourcePolicy: "CAMERA_DEFAULT"}}
	origins := origincore.Service{DB: db, Within: within, Now: func() time.Time { return now }}

	invited, err := origins.Invite(context.Background(), origincore.InviteInput{TenantID: tenantID, AssetID: assetID, TemplateID: templateID, TemplateVersionID: templateVersionID, Delivery: delivery, Requirements: requirements, Policy: json.RawMessage(`{"gpsRequired":false,"allowGallery":true}`), ExpiresAt: now.Add(time.Hour), IdempotencyKey: "origin-1"})
	if err != nil || invited.LinkToken == "" {
		t.Fatalf("origin invitation failed: %+v %v", invited, err)
	}
	replayed, err := origins.Invite(context.Background(), origincore.InviteInput{TenantID: tenantID, AssetID: assetID, TemplateID: templateID, TemplateVersionID: templateVersionID, Delivery: delivery, Requirements: requirements, ExpiresAt: now.Add(time.Hour), IdempotencyKey: "origin-1"})
	if err != nil || replayed.VersionID != invited.VersionID || replayed.LinkToken != "" {
		t.Fatalf("origin replay was not idempotent: %+v %v", replayed, err)
	}

	body := jpegBody(t)
	digest := sha256.Sum256(body)
	client := &objectClient{body: body, contentType: "image/jpeg", checksum: hex.EncodeToString(digest[:])}
	store := objectstore.Store{Bucket: "private", Client: client, Clock: func() time.Time { return now }}
	mediaService := mediacore.Service{DB: db, Store: store, Detector: sensitivecontent.NewEmbeddedDetector(), Within: within, Now: func() time.Time { return now }}
	if _, err := mediaService.CreateWithKey(context.Background(), tenantID, invited.ResponsibilityID, "text/plain", hex.EncodeToString(digest[:]), int64(len(body)), "bad"); code(err) != apperror.InvalidInput {
		t.Fatalf("unsupported media accepted: %v", err)
	}
	upload, err := mediaService.CreateWithKey(context.Background(), tenantID, invited.ResponsibilityID, "image/jpeg", hex.EncodeToString(digest[:]), int64(len(body)), "media-1")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := mediaService.CreateWithKey(context.Background(), tenantID, invited.ResponsibilityID, "image/jpeg", hex.EncodeToString(digest[:]), int64(len(body)), "media-1")
	if err != nil || duplicate.MediaID != upload.MediaID {
		t.Fatalf("media replay failed: %+v %v", duplicate, err)
	}
	if _, err := mediaService.Presign(context.Background(), tenantID, identity.NewID(), upload.MediaID, []int{1}); err == nil {
		t.Fatal("another responsibility presigned media")
	}
	completed, err := mediaService.Complete(context.Background(), tenantID, invited.ResponsibilityID, upload.MediaID, []objectstore.Part{{Number: 1, ETag: "etag"}})
	if err != nil || completed.Status != "VERIFIED" {
		t.Fatalf("media verification failed: %+v %v", completed, err)
	}
	if _, err := mediaService.Screen(context.Background(), tenantID, upload.MediaID, body); err != nil {
		t.Fatal(err)
	}

	recaptureService := recapturecore.Service{DB: db, Within: within, Now: func() time.Time { return now }}
	finalizer, err := capturefinalize.Setup(capturefinalize.Dependencies{Origins: origins, Recapture: recaptureService})
	if err != nil {
		t.Fatal(err)
	}
	capture := capturecore.Service{DB: db, Within: within, Now: func() time.Time { return now }, Finalizer: finalizer}
	optionalPolicy := capturecore.GPSPolicy{AllowGallery: true}
	if _, err := capture.SaveMetadata(context.Background(), capturecore.MetadataInput{TenantID: tenantID, ResponsibilityID: identity.NewID(), MediaID: upload.MediaID, RequirementKey: "overview", Description: "safe overview", CaptureSource: "CAMERA", WindowStartedAt: now, Policy: optionalPolicy}); err == nil {
		t.Fatal("another responsibility changed capture metadata")
	}
	media, err := capture.SaveMetadata(context.Background(), capturecore.MetadataInput{TenantID: tenantID, ResponsibilityID: invited.ResponsibilityID, MediaID: upload.MediaID, RequirementKey: "overview", Description: "safe overview", CaptureSource: "CAMERA", WindowStartedAt: now, Policy: optionalPolicy})
	if err != nil || media.RequirementKey != "overview" {
		t.Fatalf("metadata failed: %+v %v", media, err)
	}
	if _, err := capture.SaveMetadata(context.Background(), capturecore.MetadataInput{TenantID: tenantID, ResponsibilityID: invited.ResponsibilityID, MediaID: upload.MediaID, RequirementKey: "overview", Description: "safe overview", CaptureSource: "CAMERA", WindowStartedAt: now, Policy: optionalPolicy}); err != nil {
		t.Fatalf("metadata replay failed: %v", err)
	}
	if _, err := capture.SaveMetadata(context.Background(), capturecore.MetadataInput{TenantID: tenantID, ResponsibilityID: invited.ResponsibilityID, MediaID: upload.MediaID, RequirementKey: "overview", Description: "conflicting edit", CaptureSource: "CAMERA", WindowStartedAt: now, Policy: optionalPolicy}); code(err) != apperror.Conflict {
		t.Fatalf("conflicting metadata edit was not rejected: %v", err)
	}
	acceptance := database.ProcessingAcceptance{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: invited.ResponsibilityID, DisclosureVersion: "pt-BR-1", PhotoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now}
	if err := db.Create(&acceptance).Error; err != nil {
		t.Fatal(err)
	}
	session := database.ExternalSession{ID: identity.NewID(), TenantID: tenantID, InvitationID: invited.InvitationID, ResponsibilityID: invited.ResponsibilityID, SessionDigest: bytes.Repeat([]byte{1}, 32), CSRFDigest: bytes.Repeat([]byte{2}, 32), ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	submission, err := capture.Submit(context.Background(), tenantID, invited.ResponsibilityID, false)
	if err != nil || !submission.Complete || len(submission.Payload) == 0 {
		t.Fatalf("origin submission failed: %+v %v", submission, err)
	}
	repeated, err := capture.Submit(context.Background(), tenantID, invited.ResponsibilityID, false)
	if err != nil || repeated.ID != submission.ID {
		t.Fatalf("submission replay failed: %+v %v", repeated, err)
	}
	var version database.OriginVersion
	if err := db.First(&version, "id=?", invited.VersionID).Error; err != nil || version.Status != "ACTIVE" {
		t.Fatalf("origin did not activate: %+v %v", version, err)
	}
	if err := db.First(&session, "id=?", session.ID).Error; err != nil || session.RevokedAt == nil {
		t.Fatalf("terminal submission did not revoke session: %+v %v", session, err)
	}

	inspectionID := identity.NewID()
	inspection := database.Inspection{ID: inspectionID, TenantID: tenantID, BusinessUnitID: identity.NewID(), AssetID: assetID, ParticipantID: identity.NewID(), TemplateID: templateID, TemplateVersionID: templateVersionID, AnalysisProfileVersionID: identity.NewID(), Source: "MANUAL", SourceKey: "recapture-source", Status: "SUBMITTED", ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{}`), DueAt: now, DeadlineAt: now.Add(time.Hour), Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&inspection).Error; err != nil {
		t.Fatal(err)
	}
	originalResponsibilityID := identity.NewID()
	originalDraft := database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: originalResponsibilityID, Kind: "INSPECTION", TemplateVersionID: templateVersionID, ReferencePayload: json.RawMessage(`{"origin":"fixed"}`), PolicyPayload: json.RawMessage(`{"gpsRequired":false}`), Requirements: mustJSON(requirements), Status: "SUBMITTED", Version: 2, CreatedAt: now, UpdatedAt: now}
	originalMedia := database.MediaObject{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: originalResponsibilityID, ObjectKey: "tenant/original", ContentType: "image/jpeg", SHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(body)), Status: "READY", RequirementKey: "overview", Description: "original overview", CaptureSource: "CAMERA", Flags: json.RawMessage(`[]`), CreatedAt: now}
	for _, row := range []any{&database.Responsibility{ID: originalResponsibilityID, TenantID: tenantID, InspectionID: inspectionID, ParticipantID: inspection.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}, &originalDraft, &originalMedia} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	request, err := recaptureService.Request(context.Background(), recapturecore.RequestInput{TenantID: tenantID, InspectionID: inspectionID, Items: []recapturecore.Item{{RequirementKey: "overview", OriginalMediaID: &originalMedia.ID, Reason: "blurred"}}, Delivery: delivery, DeadlineAt: now.Add(time.Hour), IdempotencyKey: "recapture-1"})
	if err != nil || request.LinkToken == "" {
		t.Fatalf("recapture request failed: %+v %v", request, err)
	}
	replacement := database.MediaObject{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: request.ResponsibilityID, ObjectKey: "tenant/replacement", ContentType: "image/jpeg", SHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(body)), Status: "READY", RequirementKey: "overview", Description: "clear replacement", CaptureSource: "CAMERA", Flags: json.RawMessage(`[]`), CreatedAt: now}
	answer := database.RequirementAnswer{ID: identity.NewID(), TenantID: tenantID, DraftID: findDraft(t, db, request.ResponsibilityID).ID, RequirementKey: "overview", MediaIDs: mustJSON([]identity.ID{replacement.ID}), Flags: json.RawMessage(`[]`), Version: 1, CreatedAt: now, UpdatedAt: now}
	for _, row := range []any{&replacement, &answer, &database.ProcessingAcceptance{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: request.ResponsibilityID, DisclosureVersion: "pt-BR-1", PhotoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now}} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := capture.Submit(context.Background(), tenantID, request.ResponsibilityID, false); err != nil {
		t.Fatalf("recapture submission failed: %v", err)
	}
	if err := db.First(&replacement, "id=?", replacement.ID).Error; err != nil || replacement.ReplacesMediaID == nil || *replacement.ReplacesMediaID != originalMedia.ID {
		t.Fatalf("replacement lineage missing: %+v %v", replacement, err)
	}
	var finalized database.RecaptureRequest
	if err := db.First(&finalized, "id=?", request.RequestID).Error; err != nil || finalized.Status != "SUBMITTED" {
		t.Fatalf("recapture not finalized: %+v %v", finalized, err)
	}
}

func captureDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"audit", "messaging", "invitations", "origins", "capture", "media", "recapture", "inspections"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	models := []any{&database.AuditEvent{}, &database.OutboxIntent{}, &database.Invitation{}, &database.ExternalSession{}, &database.ProcessingAcceptance{}, &database.Origin{}, &database.OriginVersion{}, &database.OriginEvidence{}, &database.CaptureDraft{}, &database.RequirementAnswer{}, &database.SubmissionVersion{}, &database.MediaObject{}, &database.MultipartUpload{}, &database.MediaDerivative{}, &database.ScreeningRun{}, &database.RecaptureRequest{}, &database.RecaptureRequirement{}, &database.Inspection{}, &database.Responsibility{}}
	for _, model := range models {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	return db
}

type objectClient struct {
	body        []byte
	contentType string
	checksum    string
}

func (c *objectClient) CreateMultipart(context.Context, string, string, string) (string, error) {
	return identity.NewID().String(), nil
}
func (c *objectClient) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "https://signed.invalid", nil
}
func (c *objectClient) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (c *objectClient) AbortMultipart(context.Context, string, string, string) error { return nil }
func (c *objectClient) Head(context.Context, string, string) (objectstore.Head, error) {
	return objectstore.Head{Size: int64(len(c.body)), ContentType: c.contentType, ChecksumSHA256: c.checksum}, nil
}
func (c *objectClient) Get(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(c.body)), nil
}
func (c *objectClient) Put(context.Context, string, string, io.Reader, int64, string) error {
	return nil
}

func jpegBody(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	var output bytes.Buffer
	if err := jpeg.Encode(&output, img, nil); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func findDraft(t *testing.T, db *gorm.DB, responsibilityID identity.ID) database.CaptureDraft {
	t.Helper()
	var draft database.CaptureDraft
	if err := db.Where("responsibility_id=?", responsibilityID).First(&draft).Error; err != nil {
		t.Fatal(err)
	}
	return draft
}

func mustJSON(value any) json.RawMessage { payload, _ := json.Marshal(value); return payload }

func code(err error) apperror.Code {
	if err == nil {
		return ""
	}
	value, _, _ := apperror.Public(err)
	return value
}
