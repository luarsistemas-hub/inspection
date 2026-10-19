//go:build integration

package read_video_evidence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	report "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

type task07DeepLink struct {
	h                            *harness.Harness
	fixture                      harness.Task06Fixture
	frameID, mediaID, snapshotID identity.ID
}

// seedTask07UnpublishedReport stores RPT1 citing frame F11 and source C1
// without any customer publication.
func seedTask07UnpublishedReport(t *testing.T) task07DeepLink {
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
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d := task07DeepLink{h: h, fixture: fixture, frameID: identity.NewID(), mediaID: identity.NewID(), snapshotID: identity.NewID()}
	submissionID, preparationID := identity.NewID(), identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	digest := strings.Repeat("e", 64)
	canonical, err := json.Marshal(report.Snapshot{Evidence: []report.Evidence{{ID: d.frameID.String(), FrameID: d.frameID.String(), SourceMediaID: d.mediaID.String(), PreparationID: preparationID.String(), FrameDigest: digest, DisplayDigest: digest}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		for _, row := range []any{
			&database.MediaObject{ID: d.mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "vv-it-287/" + d.mediaID.String(), ContentType: "video/mp4", SHA256: digest, SizeBytes: 100, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "vv-it-287-" + d.mediaID.String(), AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: now},
			&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: d.mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 1, ScreeningStatus: "APPROVED", Status: "RETAINED", CreatedAt: now, UpdatedAt: now},
			&database.SubmissionVersion{ID: submissionID, TenantID: fixture.TenantID, DraftID: fixture.DraftID, ResponsibilityID: fixture.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: []byte(`{}`), PayloadDigest: digest, SubmittedAt: now},
			&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: d.mediaID, SubmissionID: &submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &now, CreatedAt: now},
			&database.VideoFrame{ID: d.frameID, TenantID: fixture.TenantID, SourceMediaID: d.mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: digest, ObjectKey: "vv-it-287/frame/" + d.frameID.String(), Width: 2, Height: 2, Role: "CURRENT", SizeBytes: 10, CreatedAt: now},
			&database.ReportSnapshot{ID: d.snapshotID, TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, Mode: "CONSOLIDATED", Classification: "ATTENTION", JSONDigest: digest, HTMLDigest: digest, VersionNumber: 1, CanonicalJSON: canonical, HTML: []byte(`"<p></p>"`), CreatedAt: now},
		} {
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return d
}

func (d task07DeepLink) mux(t *testing.T, authenticated bool, metadata requestctx.Metadata, store *readGuardClient) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	if _, err := Setup(mux, Dependencies{DB: d.h.DB, Store: objectstore.Store{Bucket: d.h.Store.Bucket, Client: store}, Authorizer: auth.Authorizer{}, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return metadata, authenticated }}); err != nil {
		t.Fatal(err)
	}
	return mux
}

func (d task07DeepLink) routes() map[string]string {
	return map[string]string{
		"frame":  "/media/video-evidence/" + d.frameID.String() + "?snapshotId=" + d.snapshotID.String(),
		"source": "/media/videos/" + d.mediaID.String() + "/content?snapshotId=" + d.snapshotID.String(),
	}
}

func TestVV_IT_287CustomerDeepLinksToUnpublishedRPT1AreNotFound(t *testing.T) {
	d := seedTask07UnpublishedReport(t)
	customer := requestctx.Metadata{TenantID: d.fixture.TenantID, Principal: requestctx.Principal{IdentityID: d.fixture.IdentityID, MembershipID: d.fixture.MembershipID, TenantID: d.fixture.TenantID, Roles: []string{auth.CustomerViewer}, Scopes: []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: d.fixture.BusinessUnitID}}}}
	store := &readGuardClient{}
	mux := d.mux(t, true, customer, store)
	for name, path := range d.routes() {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusNotFound || response.Body.Len() != 0 {
				t.Fatalf("customer deep link to unpublished RPT1 %s status=%d bytes=%d; want 404 without content", name, response.Code, response.Body.Len())
			}
		})
	}
	if store.reads != 0 {
		t.Fatalf("unpublished deep links made %d object-store reads", store.reads)
	}
}

func TestVV_IT_291SourceURLWithoutSessionIsUnauthorizedWithoutBytes(t *testing.T) {
	d := seedTask07UnpublishedReport(t)
	store := &readGuardClient{}
	mux := d.mux(t, false, requestctx.Metadata{}, store)
	internalPath := "/media/videos/" + d.mediaID.String() + "/content?responsibilityId=" + d.fixture.ResponsibilityID.String()
	for name, path := range map[string]string{"snapshot source": d.routes()["source"], "responsibility source": internalPath} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusUnauthorized || response.Body.Len() != 0 {
				t.Fatalf("sessionless %s status=%d bytes=%d; want 401 without media bytes", name, response.Code, response.Body.Len())
			}
		})
	}
	if store.reads != 0 {
		t.Fatalf("sessionless source requests made %d object-store reads", store.reads)
	}
}
