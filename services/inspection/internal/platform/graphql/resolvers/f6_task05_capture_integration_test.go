//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	capturesubmit "inspection/services/inspection/internal/features/capture/submit"
	mediacore "inspection/services/inspection/internal/features/media/core"
	mediacreate "inspection/services/inspection/internal/features/media/create_upload"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
)

// TestVV_IT_126ConcurrentTabsResumeOneAcceptedMedia verifies the public
// createMediaUpload mutation remains idempotent when two tabs resume together.
func TestVV_IT_126ConcurrentTabsResumeOneAcceptedMedia(t *testing.T) {
	// v1 pinned until template schema 1 is removed
	fixture := newAPI03FailureFixtureWithOptions(t, "beginVideoCapture", harness.Task06FixtureOptions{SchemaVersion: 1, RequirementKey: "room-1"})
	service := mediacore.Service{DB: fixture.harness.DB, Store: fixture.harness.Store}
	if err := mediacreate.Setup(mediacreate.Dependencies{Bus: fixture.bus, Service: service}); err != nil {
		t.Fatal(err)
	}
	started := executeAPI03WithCredentials(t, fixture, `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"vv-it126-attempt"}) { attempt { id } userErrors { code } } }`)
	var begin struct {
		Data struct {
			Result struct {
				Attempt struct {
					ID string `json:"id"`
				} `json:"attempt"`
			} `json:"beginVideoCapture"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(started), &begin); err != nil || begin.Data.Result.Attempt.ID == "" {
		t.Fatalf("beginVideoCapture did not admit A1: %s (%v)", started, err)
	}
	query := `mutation { createMediaUpload(input:{contentType:"video/mp4",sizeBytes:1024,sha256:"` + strings.Repeat("a", 64) + `",attemptId:"` + begin.Data.Result.Attempt.ID + `",clientMutationId:"vv-it126-shared-key"}) { upload { mediaId uploadId } userErrors { code } } }`
	responses := make([]string, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range responses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			responses[i] = executeAPI03WithCredentials(t, fixture, query)
		}(i)
	}
	close(start)
	wg.Wait()
	mediaIDs := make([]string, 2)
	for i, response := range responses {
		var payload struct {
			Data struct {
				Result struct {
					Upload *struct {
						MediaID  string `json:"mediaId"`
						UploadID string `json:"uploadId"`
					} `json:"upload"`
					Errors []struct {
						Code string `json:"code"`
					} `json:"userErrors"`
				} `json:"createMediaUpload"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(response), &payload); err != nil || payload.Data.Result.Upload == nil || len(payload.Data.Result.Errors) != 0 {
			t.Fatalf("tab %d could not resume upload: %s (%v)", i+1, response, err)
		}
		mediaIDs[i] = payload.Data.Result.Upload.MediaID
	}
	if mediaIDs[0] == "" || mediaIDs[0] != mediaIDs[1] {
		t.Fatalf("two tabs received media IDs %q and %q; want one accepted media ID", mediaIDs[0], mediaIDs[1])
	}
	var rows []database.MediaObject
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND responsibility_id=? AND idempotency_key=?", fixture.tenantID, fixture.responsibilityID, "vv-it126-shared-key").Find(&rows).Error
	}); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID.String() != mediaIDs[0] {
		t.Fatalf("shared idempotency key created %d media rows: %+v", len(rows), rows)
	}
}

func TestVV_IT_103ThreeRoomSubmissionAdmitsOneContinuousMainVideo(t *testing.T) {
	// v1 pinned until template schema 1 is removed
	fixture := newAPI03FailureFixtureWithOptions(t, "beginVideoCapture", harness.Task06FixtureOptions{SchemaVersion: 1, RequirementKey: "room-1"})
	if err := mediacreate.Setup(mediacreate.Dependencies{Bus: fixture.bus, Service: mediacore.Service{DB: fixture.harness.DB, Store: fixture.harness.Store}}); err != nil {
		t.Fatal(err)
	}
	if err := capturesubmit.Setup(capturesubmit.Dependencies{Bus: fixture.bus, Service: capturecore.Service{DB: fixture.harness.DB, Finalizer: f6SubmissionFinalizer{}}}); err != nil {
		t.Fatal(err)
	}
	started := executeAPI03WithCredentials(t, fixture, `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"vv-it103-attempt"}) { attempt { id } } }`)
	var begin struct {
		Data struct {
			Result struct {
				Attempt struct {
					ID string `json:"id"`
				} `json:"attempt"`
			} `json:"beginVideoCapture"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(started), &begin); err != nil || begin.Data.Result.Attempt.ID == "" {
		t.Fatalf("beginVideoCapture did not admit A1: %s (%v)", started, err)
	}
	create := func(key string) string {
		return `mutation { createMediaUpload(input:{contentType:"video/mp4",sizeBytes:1024,sha256:"` + strings.Repeat("a", 64) + `",attemptId:"` + begin.Data.Result.Attempt.ID + `",clientMutationId:"` + key + `"}) { upload { mediaId uploadId } userErrors { code } } }`
	}
	first := executeAPI03WithCredentials(t, fixture, create("vv-it103-c1-original"))
	if !strings.Contains(first, `"upload":{"mediaId":`) || strings.Contains(first, `"errors"`) {
		t.Fatalf("one continuous C1 video was not admitted: %s", first)
	}
	second := executeAPI03WithCredentials(t, fixture, create("vv-it103-room-two"))
	if !strings.Contains(second, "CONFLICT") || !strings.Contains(second, `"data":null`) {
		t.Fatalf("second per-room video was not rejected with a null GraphQL result: %s", second)
	}
	var media []database.MediaObject
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND responsibility_id=? AND video_attempt_id=?", fixture.tenantID, fixture.responsibilityID, begin.Data.Result.Attempt.ID).Find(&media).Error
	}); err != nil {
		t.Fatal(err)
	}
	if len(media) != 1 || media[0].MediaKind != "VIDEO" || media[0].RequirementKey != "video:main" {
		t.Fatalf("C1 should retain one video:main original, got %+v", media)
	}
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND id=?", fixture.tenantID, media[0].ID).Update("status", "RECEIVED").Error; err != nil {
			return err
		}
		return tx.Model(&database.VideoAttempt{}).Where("tenant_id=? AND id=?", fixture.tenantID, begin.Data.Result.Attempt.ID).Update("status", "RECEIVED").Error
	}); err != nil {
		t.Fatal(err)
	}
	submitted := executeAPI03WithCredentials(t, fixture, `mutation { submitCapture(input:{confirmIncomplete:false,clientMutationId:"vv-it103-submit-c1"}) { submission { id } userErrors { code } } }`)
	if strings.Contains(submitted, `"errors"`) || !strings.Contains(submitted, `"submission":{"id":`) {
		t.Fatalf("three-room C1 submission did not accept its single video:main original: %s", submitted)
	}
}

func TestVV_IT_115ExpiredInvitationDuringRecordingRejectsSubmissionWithoutReceipt(t *testing.T) {
	// v1 pinned until template schema 1 is removed
	fixture := newAPI03FailureFixtureWithOptions(t, "beginVideoCapture", harness.Task06FixtureOptions{SchemaVersion: 1, RequirementKey: "room-1"})
	if err := mediacreate.Setup(mediacreate.Dependencies{Bus: fixture.bus, Service: mediacore.Service{DB: fixture.harness.DB, Store: fixture.harness.Store}}); err != nil {
		t.Fatal(err)
	}
	if err := capturesubmit.Setup(capturesubmit.Dependencies{Bus: fixture.bus, Service: capturecore.Service{DB: fixture.harness.DB, Finalizer: f6SubmissionFinalizer{}}}); err != nil {
		t.Fatal(err)
	}
	started := executeAPI03WithCredentials(t, fixture, `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"vv-it115-attempt"}) { attempt { id } } }`)
	var begin struct {
		Data struct {
			Result struct {
				Attempt struct {
					ID string `json:"id"`
				} `json:"attempt"`
			} `json:"beginVideoCapture"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(started), &begin); err != nil || begin.Data.Result.Attempt.ID == "" {
		t.Fatalf("recording attempt was not admitted before invitation expiry: %s (%v)", started, err)
	}
	upload := executeAPI03WithCredentials(t, fixture, task05CreateVideoUpload(begin.Data.Result.Attempt.ID, "vv-it115-upload"))
	if !strings.Contains(upload, `"upload":{"mediaId":`) {
		t.Fatalf("A1 upload was not persisted while invitation was active: %s", upload)
	}
	if err := fixture.harness.AdminDB.Model(&database.Invitation{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.invitationID).Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	response := executeAPI03WithCredentials(t, fixture, `mutation { submitCapture(input:{confirmIncomplete:false,clientMutationId:"vv-it115-submit"}) { submission { id } userErrors { code } } }`)
	assertAPI03Rejected(t, response, "SESSION_EXPIRED", `"submitCapture":{`)
	var media database.MediaObject
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND responsibility_id=? AND idempotency_key=?", fixture.tenantID, fixture.responsibilityID, "vv-it115-upload").Take(&media).Error
	}); err != nil {
		t.Fatal(err)
	}
	if media.Status != "UPLOADING" {
		t.Fatalf("expired invitation changed the in-progress upload to %s; expected no RECEIVED receipt", media.Status)
	}
}

func task05CreateVideoUpload(attemptID, key string) string {
	return `mutation { createMediaUpload(input:{contentType:"video/mp4",sizeBytes:1024,sha256:"` + strings.Repeat("a", 64) + `",attemptId:"` + attemptID + `",clientMutationId:"` + key + `"}) { upload { mediaId uploadId } userErrors { code } } }`
}

func TestVV_IT_125ReauthenticatedResumeCannotAccessRevokedPersistedUpload(t *testing.T) {
	// v1 pinned until template schema 1 is removed
	fixture := newAPI03FailureFixtureWithOptions(t, "externalCapture", harness.Task06FixtureOptions{SchemaVersion: 1, RequirementKey: "room-1"})
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Update("status", "OPEN").Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := mediacreate.Setup(mediacreate.Dependencies{Bus: fixture.bus, Service: mediacore.Service{DB: fixture.harness.DB, Store: fixture.harness.Store}}); err != nil {
		t.Fatal(err)
	}
	started := executeAPI03WithCredentials(t, fixture, `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"vv-it125-attempt"}) { attempt { id } } }`)
	var begin struct {
		Data struct {
			Result struct {
				Attempt struct {
					ID string `json:"id"`
				} `json:"attempt"`
			} `json:"beginVideoCapture"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(started), &begin); err != nil || begin.Data.Result.Attempt.ID == "" {
		t.Fatalf("recording attempt was not admitted before session expiry: %s (%v)", started, err)
	}
	uploadResponse := executeAPI03WithCredentials(t, fixture, task05CreateVideoUpload(begin.Data.Result.Attempt.ID, "vv-it125-upload"))
	if !strings.Contains(uploadResponse, `"upload":{"mediaId":`) {
		t.Fatalf("A1 upload was not persisted before session expiry: %s", uploadResponse)
	}
	if err := fixture.harness.AdminDB.Model(&database.ExternalSession{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.sessionID).Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.AdminDB.Model(&database.Invitation{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.invitationID).Update("status", "REVOKED").Error; err != nil {
		t.Fatal(err)
	}
	pepper := []byte(strings.Repeat("p", 32))
	token, err := security.NewScopedToken(fixture.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf := "vv-it125-reauthenticated-resume"
	hash := security.HashToken(token)
	proof, err := security.CSRFProof(hash, csrf, pepper)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := fixture.harness.AdminDB.Create(&database.ExternalSession{ID: identity.NewID(), TenantID: fixture.tenantID, InvitationID: identity.Ptr(fixture.invitationID), ResponsibilityID: &fixture.responsibilityID, SessionDigest: hash[:], CSRFDigest: proof[:], ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	response := executeAPI03WithCredentialsValue(t, fixture, `query { externalCapture { responsibilityId status } }`, requestctx.ExternalCredentials{SessionToken: token, CSRFToken: csrf})
	assertAPI03Rejected(t, response, "SESSION_EXPIRED", `"externalCapture":{`)
	var media database.MediaObject
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND responsibility_id=? AND idempotency_key=?", fixture.tenantID, fixture.responsibilityID, "vv-it125-upload").Take(&media).Error
	}); err != nil {
		t.Fatal(err)
	}
	if media.Status != "UPLOADING" {
		t.Fatalf("revoked invitation exposed or completed the saved upload: status=%s", media.Status)
	}
}
