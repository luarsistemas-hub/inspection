//go:build integration

package read_video_evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	report "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
)

type expiredSourceFrameClient struct {
	readGuardClient
	frame []byte
}

func (c *expiredSourceFrameClient) Get(_ context.Context, _, _ string) (io.ReadCloser, error) {
	c.reads++
	return io.NopCloser(strings.NewReader(string(c.frame))), nil
}

func (c *expiredSourceFrameClient) GetRange(_ context.Context, _, _ string, _, _ int64) (io.ReadCloser, error) {
	c.reads++
	return io.NopCloser(strings.NewReader(string(c.frame))), nil
}

func TestExpiredVideoSourceRejectsDecodeButRetainedFrameRemainsReadableIT160(t *testing.T) {
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
	mediaID, attemptID, submissionID, preparationID, frameID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	expiredAt := now.Add(-time.Hour)
	frameBytes := []byte("retained-frame-data")
	digest := sha256.Sum256(frameBytes)
	digestText := hex.EncodeToString(digest[:])
	frameKey := "video-frames/it160/" + frameID.String() + ".jpg"
	store := &expiredSourceFrameClient{frame: frameBytes}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "it160-attempt", PayloadDigest: digestText, AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "video-sources/it160/" + mediaID.String(), ContentType: "video/mp4", SHA256: digestText, SizeBytes: int64(len(frameBytes)), Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "it160-media", AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: fixture.TenantID, DraftID: fixture.DraftID, ResponsibilityID: fixture.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: []byte(`{}`), PayloadDigest: digestText, SubmittedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: digestText, ManifestDigest: digestText, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &now, CreatedAt: now}).Error; err != nil {
			return err
		}
		retentionDays := 5
		return tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &now, RetentionDays: &retentionDays, SourceExpiryAt: &expiredAt, Status: "EXPIRED", CreatedAt: now, UpdatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Create(&database.VideoFrame{ID: frameID, TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: digestText, ObjectKey: frameKey, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: int64(len(frameBytes)), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	principal := requestctx.Principal{IdentityID: fixture.ResponsibilityID, TenantID: fixture.TenantID, Internal: true}
	metadata := requestctx.Metadata{TenantID: fixture.TenantID, Principal: principal}
	mux := http.NewServeMux()
	if _, err := Setup(mux, Dependencies{DB: h.DB, Store: objectstore.Store{Bucket: h.Store.Bucket, Client: store}, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return metadata, true }, Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	sourceRequest := httptest.NewRequest(http.MethodGet, "/media/videos/"+mediaID.String()+"/content?responsibilityId="+fixture.ResponsibilityID.String(), nil)
	sourceResponse := httptest.NewRecorder()
	mux.ServeHTTP(sourceResponse, sourceRequest)
	if sourceResponse.Code != http.StatusGone || store.reads != 0 {
		t.Fatalf("expired original decode status=%d object-store reads=%d; want 410 and no source read", sourceResponse.Code, store.reads)
	}
	frameRequest := httptest.NewRequest(http.MethodGet, "/media/video-evidence/"+frameID.String()+"?responsibilityId="+fixture.ResponsibilityID.String(), nil)
	frameResponse := httptest.NewRecorder()
	mux.ServeHTTP(frameResponse, frameRequest)
	if frameResponse.Code != http.StatusOK || frameResponse.Body.String() != string(frameBytes) {
		t.Fatalf("retained frame status=%d body=%q; want HTTP 200 with verified frame bytes", frameResponse.Code, frameResponse.Body.String())
	}
}

func TestResponsibilitySessionCannotReadAnotherResponsibilityOriginFrameIT081(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	origin, err := h.SeedVideoOriginFixture(ctx, harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	inspectionID, responsibilityID, draftID, invitationID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	if err := h.WithinTenant(ctx, origin.TenantID, func(tx *gorm.DB) error {
		var source database.Inspection
		if err := tx.Where("tenant_id=? AND asset_id=?", origin.TenantID, origin.AssetID).First(&source).Error; err != nil {
			return err
		}
		source.ID, source.SourceKey, source.CreatedAt, source.UpdatedAt = inspectionID, "it081-r2", now, now
		source.Version = 1
		if err := tx.Create(&source).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Responsibility{ID: responsibilityID, TenantID: origin.TenantID, InspectionID: inspectionID, ParticipantID: source.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.CaptureDraft{ID: draftID, TenantID: origin.TenantID, ResponsibilityID: responsibilityID, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: source.TemplateVersionID, ReferencePayload: json.RawMessage(`{"comparisonMode":"FIXED_ORIGIN","mediaKind":"VIDEO","scope":[]}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[{"key":"video:main","evidenceKind":"VIDEO","required":true,"minimumCount":1,"maximumCount":1}]`), Status: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.ProcessingAcceptance{ID: identity.NewID(), TenantID: origin.TenantID, ResponsibilityID: responsibilityID, DisclosureVersion: "video-v1", VideoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	pepper := []byte(strings.Repeat("q", 32))
	token, err := security.NewScopedToken(origin.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf := "it081-csrf"
	hash := security.HashToken(token)
	proof, err := security.CSRFProof(hash, csrf, pepper)
	if err != nil {
		t.Fatal(err)
	}
	expires := now.Add(time.Hour)
	if err := h.AdminDB.Create(&database.Invitation{ID: invitationID, TenantID: origin.TenantID, ResponsibilityID: responsibilityID, TokenHash: hash[:], DeliveryIntents: json.RawMessage(`[]`), Status: "ACTIVE", ExpiresAt: expires, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Create(&database.ExternalSession{ID: identity.NewID(), TenantID: origin.TenantID, InvitationID: invitationID, ResponsibilityID: responsibilityID, SessionDigest: hash[:], CSRFDigest: proof[:], ExpiresAt: expires, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	invitations := invitationcore.Service{DB: h.DB, Pepper: pepper}
	mux := http.NewServeMux()
	_, err = Setup(mux, Dependencies{DB: h.DB, Store: h.Store, Now: func() time.Time { return now }, Authenticate: func(request *http.Request) (requestctx.Metadata, bool) {
		credentials, ok := requestctx.ExternalCredentialsFromContext(request.Context())
		if !ok {
			return requestctx.Metadata{}, false
		}
		responsibility, err := invitations.ValidateSession(request.Context(), credentials.SessionToken, credentials.CSRFToken)
		if err != nil || responsibility != responsibilityID {
			return requestctx.Metadata{}, false
		}
		return requestctx.Metadata{TenantID: origin.TenantID, Principal: requestctx.Principal{IdentityID: responsibility, TenantID: origin.TenantID, Internal: true}}, true
	}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/media/video-evidence/"+origin.FrameID.String()+"?responsibilityId="+responsibilityID.String(), nil)
	request = request.WithContext(requestctx.WithExternalCredentials(request.Context(), requestctx.ExternalCredentials{SessionToken: token, CSRFToken: csrf}))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("R2 session read R1 frame: status=%d body=%q, want 404", response.Code, response.Body.String())
	}
}

func TestCustomerSnapshotFrameRequiresPublicationAndExactEvidence(t *testing.T) {
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
	frameID, otherFrameID, mediaID, attemptID, submissionID, preparationID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	unpublishedID, unrelatedID, publishedID := identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	digest := strings.Repeat("d", 64)
	matchingJSON, err := json.Marshal(report.Snapshot{Evidence: []report.Evidence{{ID: frameID.String(), FrameID: frameID.String(), SourceMediaID: mediaID.String(), PreparationID: preparationID.String(), FrameDigest: digest, DisplayDigest: digest}}})
	if err != nil {
		t.Fatal(err)
	}
	unrelatedJSON, err := json.Marshal(report.Snapshot{Evidence: []report.Evidence{{FrameID: identity.NewID().String(), SourceMediaID: mediaID.String(), PreparationID: preparationID.String(), FrameDigest: digest, DisplayDigest: digest}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		attempt := database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "snapshot-authorization", PayloadDigest: digest, AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		media := database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "tenant/" + fixture.TenantID.String() + "/media/" + mediaID.String(), ContentType: "video/mp4", SHA256: digest, SizeBytes: 100, Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "snapshot-authorization-video", AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: now}
		if err := tx.Create(&media).Error; err != nil {
			return err
		}
		source := database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1000000, Width: 2, Height: 2, FrameRate: 1, ScreeningStatus: "APPROVED", Status: "RETAINED", CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&source).Error; err != nil {
			return err
		}
		submission := database.SubmissionVersion{ID: submissionID, TenantID: fixture.TenantID, DraftID: fixture.DraftID, ResponsibilityID: fixture.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: []byte(`{}`), PayloadDigest: digest, SubmittedAt: now}
		if err := tx.Create(&submission).Error; err != nil {
			return err
		}
		preparation := database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &now, CreatedAt: now}
		if err := tx.Create(&preparation).Error; err != nil {
			return err
		}
		frame := database.VideoFrame{ID: frameID, TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: digest, ObjectKey: "video-frames/" + fixture.TenantID.String() + "/" + frameID.String() + ".jpg", Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 10, CreatedAt: now}
		if err := tx.Create(&frame).Error; err != nil {
			return err
		}
		frame.ID, frame.ObjectKey = otherFrameID, "video-frames/"+fixture.TenantID.String()+"/"+otherFrameID.String()+".jpg"
		frame.PTSMicros = 1000
		if err := tx.Create(&frame).Error; err != nil {
			return err
		}
		for _, item := range []struct {
			id       identity.ID
			document []byte
		}{{unpublishedID, matchingJSON}, {unrelatedID, unrelatedJSON}, {publishedID, matchingJSON}} {
			snapshot := database.ReportSnapshot{ID: item.id, TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, Mode: "VIDEO", Classification: "NO_RELEVANT_CHANGE", JSONDigest: digest, HTMLDigest: digest, VersionNumber: 1, PDFRenderVersion: 1, CanonicalJSON: item.document, HTML: []byte(`{}`), CreatedAt: now}
			if err := tx.Create(&snapshot).Error; err != nil {
				return err
			}
		}
		publication := database.ReportPublication{ID: identity.NewID(), TenantID: fixture.TenantID, SnapshotID: publishedID, InspectionID: fixture.InspectionID, PolicyVersion: 1, Status: "PUBLISHED", ActorID: fixture.IdentityID, Version: 1, PublishedAt: &now, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&publication).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	principal := requestctx.Principal{IdentityID: fixture.IdentityID, MembershipID: fixture.MembershipID, TenantID: fixture.TenantID, Roles: []string{auth.CustomerViewer}, Scopes: []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: fixture.BusinessUnitID}}}
	metadata := requestctx.Metadata{TenantID: fixture.TenantID, Principal: principal}
	store := &readGuardClient{}
	mux := http.NewServeMux()
	if _, err := Setup(mux, Dependencies{DB: h.DB, Store: objectstore.Store{Bucket: h.Store.Bucket, Client: store}, Authorizer: auth.Authorizer{}, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return metadata, true }}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		name     string
		snapshot identity.ID
		frame    identity.ID
		status   int
	}{{"unpublished", unpublishedID, frameID, http.StatusNotFound}, {"unrelated snapshot", unrelatedID, frameID, http.StatusNotFound}, {"frame absent from published snapshot", publishedID, otherFrameID, http.StatusNotFound}, {"published matching frame", publishedID, frameID, http.StatusOK}} {
		t.Run(item.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodHead, "/media/video-evidence/"+item.frame.String()+"?snapshotId="+item.snapshot.String(), nil)
			request = request.WithContext(requestctx.WithMetadata(request.Context(), metadata))
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != item.status {
				t.Fatalf("status=%d, want %d", response.Code, item.status)
			}
		})
	}
	request := httptest.NewRequest(http.MethodHead, "/media/video-evidence/"+frameID.String()+"?responsibilityId="+fixture.ResponsibilityID.String(), nil)
	request = request.WithContext(requestctx.WithMetadata(request.Context(), metadata))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("customer responsibility link status=%d, want 404", response.Code)
	}
	if store.reads != 0 {
		t.Fatalf("authorization made %d object-store reads", store.reads)
	}
}

func TestForeignTenantFrameIDNeverReadsObjectStoreUT063(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	owner, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	frameID, mediaID, attemptID, submissionID, preparationID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	digest := strings.Repeat("c", 64)
	if err := h.WithinTenant(ctx, owner.TenantID, func(tx *gorm.DB) error {
		attempt := database.VideoAttempt{ID: attemptID, TenantID: owner.TenantID, ResponsibilityID: owner.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "foreign-frame", PayloadDigest: digest, AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		media := database.MediaObject{ID: mediaID, TenantID: owner.TenantID, ResponsibilityID: owner.ResponsibilityID, ObjectKey: "tenant/" + owner.TenantID.String() + "/media/" + mediaID.String(), ContentType: "video/mp4", SHA256: digest, SizeBytes: 100, Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "foreign-frame-video", AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: now}
		if err := tx.Create(&media).Error; err != nil {
			return err
		}
		submission := database.SubmissionVersion{ID: submissionID, TenantID: owner.TenantID, DraftID: owner.DraftID, ResponsibilityID: owner.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: []byte(`{}`), PayloadDigest: digest, SubmittedAt: now}
		if err := tx.Create(&submission).Error; err != nil {
			return err
		}
		preparation := database.VideoPreparationJob{ID: preparationID, TenantID: owner.TenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &now, CreatedAt: now}
		if err := tx.Create(&preparation).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoFrame{
			ID: frameID, TenantID: owner.TenantID, SourceMediaID: mediaID,
			PreparationID: preparationID, PTSMicros: 0,
			SHA256: digest, ObjectKey: "video-frames/" + owner.TenantID.String() + "/" + frameID.String() + ".jpg",
			Width: 1, Height: 1, Role: "BASELINE", SizeBytes: 10, CreatedAt: now,
		}).Error
	}); err != nil {
		t.Fatal(err)
	}
	store := &readGuardClient{}
	mux := http.NewServeMux()
	principal := requestctx.Principal{IdentityID: viewer.IdentityID, MembershipID: viewer.MembershipID, TenantID: viewer.TenantID}
	if _, err := Setup(mux, Dependencies{
		DB: h.DB, Store: objectstore.Store{Bucket: h.Store.Bucket, Client: store},
		Authenticate: func(*http.Request) (requestctx.Metadata, bool) {
			return requestctx.Metadata{TenantID: viewer.TenantID, Principal: principal}, true
		},
	}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/media/video-evidence/"+frameID.String()+"?responsibilityId="+viewer.ResponsibilityID.String(), nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("foreign tenant frame status=%d, want 404", response.Code)
	}
	if store.reads != 0 {
		t.Fatalf("foreign tenant frame request made %d object-store reads", store.reads)
	}
}

type contextBlockingVideoClient struct {
	testObjectClient
	started chan struct{}
	closed  chan struct{}
}

func (c contextBlockingVideoClient) Get(ctx context.Context, _, _ string) (io.ReadCloser, error) {
	reader := &contextBlockingReader{ctx: ctx, started: c.started, closed: c.closed}
	return reader, nil
}

func newContextBlockingVideoClient() contextBlockingVideoClient {
	return contextBlockingVideoClient{started: make(chan struct{}, 1), closed: make(chan struct{})}
}

type contextBlockingReader struct {
	ctx     context.Context
	started chan struct{}
	closed  chan struct{}
}

func (r *contextBlockingReader) Read([]byte) (int, error) {
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func (r *contextBlockingReader) Close() error {
	close(r.closed)
	return nil
}

func (c contextBlockingVideoClient) Head(context.Context, string, string) (objectstore.Head, error) {
	return objectstore.Head{Size: 8, ContentType: "video/mp4"}, nil
}

func TestDisconnectReleasesPlaybackLeaseIT552(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	h, fixture, item := seedPlaybackLeaseFixture(t)
	client := newContextBlockingVideoClient()
	handler := &Handler{deps: Dependencies{DB: h.DB, Store: objectstore.Store{Bucket: h.Store.Bucket, Client: client}, Now: time.Now, StreamTimeout: time.Minute}, slots: make(chan struct{}, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.stream(response, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx), item, 0, 7, false, "video/mp4", 8)
		close(done)
	}()
	select {
	case <-client.started:
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("source read did not start")
	}
	if got := countPlaybackLeases(t, h, fixture.TenantID, fixture.MediaID); got != 1 {
		cancel()
		t.Fatalf("playback leases while stream active=%d, want 1", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("stream did not return after client disconnect")
	}
	if len(handler.slots) != 0 {
		t.Fatalf("playback slot count after disconnect=%d, want 0", len(handler.slots))
	}
	select {
	case <-client.closed:
	default:
		t.Fatal("storage reader was not closed after client disconnect")
	}
	if got := countPlaybackLeases(t, h, fixture.TenantID, fixture.MediaID); got != 0 {
		t.Fatalf("playback leases after disconnect=%d, want 0", got)
	}
}

func TestFifthPlaybackStreamDoesNotAcquireLeaseIT548(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	h, fixture, item := seedPlaybackLeaseFixture(t)
	client := newContextBlockingVideoClient()
	handler := &Handler{deps: Dependencies{DB: h.DB, Store: objectstore.Store{Bucket: h.Store.Bucket, Client: client}, Now: time.Now, StreamTimeout: time.Minute}, slots: make(chan struct{}, 4)}
	for range 4 {
		handler.slots <- struct{}{}
	}
	response := httptest.NewRecorder()
	handler.stream(response, httptest.NewRequest(http.MethodGet, "/", nil), item, 0, 7, false, "video/mp4", 8)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("fifth stream status=%d, want 429", response.Code)
	}
	if got := countPlaybackLeases(t, h, fixture.TenantID, fixture.MediaID); got != 0 {
		t.Fatalf("fifth stream acquired %d playback leases, want 0", got)
	}
}

func TestInjectedStreamTimeoutReleasesPlaybackLeaseIT551(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	h, fixture, item := seedPlaybackLeaseFixture(t)
	client := newContextBlockingVideoClient()
	handler := &Handler{deps: Dependencies{DB: h.DB, Store: objectstore.Store{Bucket: h.Store.Bucket, Client: client}, Now: time.Now, StreamTimeout: 30 * time.Millisecond}, slots: make(chan struct{}, 4)}
	response := httptest.NewRecorder()
	handler.stream(response, httptest.NewRequest(http.MethodGet, "/", nil), item, 0, 7, false, "video/mp4", 8)
	if got := countPlaybackLeases(t, h, fixture.TenantID, fixture.MediaID); got != 0 {
		t.Fatalf("playback leases after timeout=%d, want 0", got)
	}
}

func TestActiveLegalHoldAllowsExpiredDeadlinePlayback(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	h, fixture, item := seedPlaybackLeaseFixture(t)
	deadline := time.Now().UTC().Add(-time.Hour)
	if err := h.WithinTenant(context.Background(), fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", fixture.TenantID, fixture.MediaID).Update("source_expiry_at", deadline).Error; err != nil {
			return err
		}
		return tx.Create(&database.LegalHold{ID: identity.NewID(), TenantID: fixture.TenantID, SourceMediaID: idPtr(fixture.MediaID), Reason: "preserve source for test", Active: true, CreatedAt: time.Now().UTC()}).Error
	}); err != nil {
		t.Fatal(err)
	}
	client := newContextBlockingVideoClient()
	handler := &Handler{deps: Dependencies{DB: h.DB, Store: objectstore.Store{Bucket: h.Store.Bucket, Client: client}, Now: time.Now, StreamTimeout: 30 * time.Millisecond}, slots: make(chan struct{}, 4)}
	response := httptest.NewRecorder()
	handler.stream(response, httptest.NewRequest(http.MethodGet, "/", nil), item, 0, 7, false, "video/mp4", 8)
	select {
	case <-client.started:
	default:
		t.Fatal("source read did not start while legal hold was active")
	}
	if got := countPlaybackLeases(t, h, fixture.TenantID, fixture.MediaID); got != 0 {
		t.Fatalf("playback leases after held-source test=%d, want 0", got)
	}
}

func TestExpiredSourceReturns410(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	h, fixture, item := seedPlaybackLeaseFixture(t)
	deadline := time.Now().UTC().Add(-time.Hour)
	if err := h.WithinTenant(context.Background(), fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", fixture.TenantID, fixture.MediaID).Update("source_expiry_at", deadline).Error
	}); err != nil {
		t.Fatal(err)
	}
	handler := &Handler{deps: Dependencies{DB: h.DB, Now: time.Now, StreamTimeout: time.Minute}, slots: make(chan struct{}, 4)}
	response := httptest.NewRecorder()
	handler.stream(response, httptest.NewRequest(http.MethodGet, "/", nil), item, 0, 0, false, "", 0)
	if response.Code != http.StatusGone {
		t.Fatalf("expired source status=%d, want 410", response.Code)
	}
}

type failingVideoHeadClient struct {
	testObjectClient
	reads      int
	leaseSeen  bool
	checkLease func() (int64, error)
}

func (c *failingVideoHeadClient) Head(context.Context, string, string) (objectstore.Head, error) {
	c.reads++
	if c.checkLease != nil {
		count, err := c.checkLease()
		c.leaseSeen = err == nil && count == 1
	}
	return objectstore.Head{}, errors.New("injected object-store head failure")
}

func TestStorageFailureBeforeHeadersReturns503AndReleasesLeaseIT549(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	h, fixture, _ := seedPlaybackLeaseFixture(t)
	ctx := context.Background()
	mediaID, attemptID := identity.NewID(), identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	digest := strings.Repeat("e", 64)
	expiresAt := now.Add(time.Hour)
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		attempt := database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "it549-attempt", PayloadDigest: digest, AdmittedAt: now, ExpiresAt: expiresAt, CreatedAt: now}
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		media := database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "video-sources/it549/" + mediaID.String(), ContentType: "video/mp4", SHA256: digest, SizeBytes: 100, Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "it549-media", AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: now}
		if err := tx.Create(&media).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", SourceExpiryAt: &expiresAt, Status: "RETAINED", CreatedAt: now, UpdatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	client := &failingVideoHeadClient{checkLease: func() (int64, error) {
		var count int64
		err := h.WithinTenant(context.Background(), fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Model(&database.VideoSourceLease{}).Where("tenant_id=? AND source_media_id=? AND purpose='PLAYBACK'", fixture.TenantID, mediaID).Count(&count).Error
		})
		return count, err
	}}
	metadata := requestctx.Metadata{
		TenantID: fixture.TenantID,
		Principal: requestctx.Principal{
			IdentityID: fixture.ResponsibilityID,
			TenantID:   fixture.TenantID,
			Internal:   true,
		},
	}
	mux := http.NewServeMux()
	if _, err := Setup(mux, Dependencies{
		DB:           h.DB,
		Store:        objectstore.Store{Bucket: h.Store.Bucket, Client: client},
		Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return metadata, true },
	}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/media/videos/"+mediaID.String()+"/content?responsibilityId="+fixture.ResponsibilityID.String(), nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("storage failure status=%d, want 503", response.Code)
	}
	if response.Body.Len() != 0 {
		t.Fatalf("storage failure wrote %d response bytes before headers", response.Body.Len())
	}
	if client.reads != 1 {
		t.Fatalf("storage Head calls=%d, want 1", client.reads)
	}
	if !client.leaseSeen {
		t.Fatal("object-store Head failed before an acquired playback lease was observable")
	}
	if got := countPlaybackLeases(t, h, fixture.TenantID, mediaID); got != 0 {
		t.Fatalf("playback leases after storage failure=%d, want 0", got)
	}
}

func seedPlaybackLeaseFixture(t *testing.T) (*harness.Harness, harness.Task06Fixture, access) {
	t.Helper()
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
	expiresAt := now.Add(time.Hour)
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: fixture.MediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", SourceExpiryAt: &expiresAt, Status: "RETAINED", CreatedAt: now, UpdatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	metadata := requestctx.Metadata{TenantID: fixture.TenantID}
	return h, fixture, access{metadata: metadata, media: database.MediaObject{ID: fixture.MediaID}, source: database.VideoSource{MediaID: fixture.MediaID}, objectKey: "source.mp4", contentType: "video/mp4"}
}

func countPlaybackLeases(t *testing.T, h *harness.Harness, tenantID, mediaID identity.ID) int64 {
	t.Helper()
	var count int64
	if err := h.WithinTenant(context.Background(), tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoSourceLease{}).Where("tenant_id=? AND source_media_id=? AND purpose='PLAYBACK'", tenantID, mediaID).Count(&count).Error
	}); err != nil {
		t.Fatal(err)
	}
	return count
}

func idPtr(id identity.ID) *identity.ID { return &id }
