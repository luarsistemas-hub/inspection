//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	processcomparison "inspection/services/inspection/internal/features/analysis/process_comparison"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	mediacomplete "inspection/services/inspection/internal/features/media/complete_upload"
	mediacore "inspection/services/inspection/internal/features/media/core"
	mediacreate "inspection/services/inspection/internal/features/media/create_upload"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
)

type task07FailingStorageClient struct {
	objectstore.Client
	createCalls int
}

type task07DirectVideoStorageClient struct {
	objectstore.Client
	size          int64
	contentType   string
	createCalls   int
	completeCalls int
}

func (c *task07DirectVideoStorageClient) CreateMultipart(context.Context, string, string, string) (string, error) {
	c.createCalls++
	return fmt.Sprintf("vv-it-upload-%s-%d", identity.NewID(), c.createCalls), nil
}

func (c *task07DirectVideoStorageClient) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	c.completeCalls++
	return nil
}

func (c *task07DirectVideoStorageClient) Head(context.Context, string, string) (objectstore.Head, error) {
	return objectstore.Head{Size: c.size, ContentType: c.contentType}, nil
}

func directIntegratorVideoUpload(t *testing.T, fixture api03FailureFixture, store objectstore.Client, uploadMutationID string) (identity.ID, identity.ID) {
	t.Helper()
	service := mediacore.Service{DB: fixture.harness.DB, Store: objectstore.Store{Bucket: fixture.harness.Store.Bucket, Client: store}, Within: fixture.harness.WithinTenant}
	if err := mediacreate.Setup(mediacreate.Dependencies{Bus: fixture.bus, Service: service}); err != nil {
		t.Fatal(err)
	}
	if err := mediacomplete.Setup(mediacomplete.Dependencies{Bus: fixture.bus, Service: service}); err != nil {
		t.Fatal(err)
	}
	begin := executeCaptureGraphQL(t, fixture.server, `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"vv-it-338-attempt"}) { attempt { id kind acquisitionSource maxDurationSeconds maxBytes status } userErrors { code } } }`, fixture.tenantID, fixture.credentials, true)
	var attemptPayload struct {
		Data struct {
			Result struct {
				Attempt struct {
					ID          string `json:"id"`
					Kind        string `json:"kind"`
					Source      string `json:"acquisitionSource"`
					MaxDuration int    `json:"maxDurationSeconds"`
					MaxBytes    int64  `json:"maxBytes"`
					Status      string `json:"status"`
				} `json:"attempt"`
			} `json:"beginVideoCapture"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(begin), &attemptPayload); err != nil {
		t.Fatalf("decode beginVideoCapture: %s (%v)", begin, err)
	}
	attempt := attemptPayload.Data.Result.Attempt
	if attempt.ID == "" || attempt.Kind != "CURRENT" || attempt.Source != "CAMERA" || attempt.MaxDuration != 900 || attempt.MaxBytes != 1<<30 || attempt.Status != "ACTIVE" {
		t.Fatalf("beginVideoCapture did not freeze VIDEO/CAMERA limits for the active responsibility: %s", begin)
	}
	attemptID, err := identity.ParseID(attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	created := executeCaptureGraphQL(t, fixture.server, `mutation { createMediaUpload(input:{contentType:"video/mp4",sizeBytes:1024,sha256:"`+strings.Repeat("a", 64)+`",attemptId:"`+attemptID.String()+`",clientMutationId:"`+uploadMutationID+`"}) { upload { mediaId uploadId } userErrors { code } } }`, fixture.tenantID, fixture.credentials, true)
	var uploadPayload struct {
		Data struct {
			Result struct {
				Upload struct {
					MediaID  string `json:"mediaId"`
					UploadID string `json:"uploadId"`
				} `json:"upload"`
			} `json:"createMediaUpload"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(created), &uploadPayload); err != nil {
		t.Fatalf("decode createMediaUpload: %s (%v)", created, err)
	}
	upload := uploadPayload.Data.Result.Upload
	mediaID, err := identity.ParseID(upload.MediaID)
	if err != nil || upload.UploadID == "" {
		t.Fatalf("authorized VIDEO upload was not admitted for responsibility %s: %s (%v)", fixture.responsibilityID, created, err)
	}
	complete := executeCaptureGraphQL(t, fixture.server, `mutation { completeMediaUpload(input:{mediaId:"`+mediaID.String()+`",parts:[{partNumber:1,etag:"vv-it-part-1"}],clientMutationId:"vv-it-338-complete"}) { media { id status mediaKind preparationStatus } userErrors { code } } }`, fixture.tenantID, fixture.credentials, true)
	if strings.Contains(complete, `"errors"`) || !strings.Contains(complete, `"status":"RECEIVED"`) || !strings.Contains(complete, `"mediaKind":"VIDEO"`) {
		t.Fatalf("authorized VIDEO upload did not complete as a receipt: %s", complete)
	}
	metadata := executeCaptureGraphQL(t, fixture.server, `mutation { saveCaptureMetadata(input:{mediaId:"`+mediaID.String()+`",requirementKey:"video:main",description:"Vídeo principal",captureSource:"CAMERA",clientMutationId:"vv-it-338-metadata"}) { media { id status mediaKind requirementKey captureSource } userErrors { code } } }`, fixture.tenantID, fixture.credentials, true)
	if strings.Contains(metadata, `"errors"`) || !strings.Contains(metadata, `"requirementKey":"video:main"`) || !strings.Contains(metadata, `"captureSource":"CAMERA"`) {
		t.Fatalf("VIDEO metadata did not bind the received media to the required source and modality: %s", metadata)
	}
	return attemptID, mediaID
}

func directIntegratorSubmit(t *testing.T, fixture api03FailureFixture, mutationID string) string {
	t.Helper()
	return executeCaptureGraphQL(t, fixture.server, `mutation { submitCapture(input:{confirmIncomplete:false,clientMutationId:"`+mutationID+`"}) { submission { id complete preparationPending } userErrors { code } clientMutationId } }`, fixture.tenantID, fixture.credentials, true)
}

func TestVV_IT_338AuthorizedDirectGraphQLAdmitsAndSubmitsVideoCapture(t *testing.T) {
	fixture := newAPI05CaptureFixture(t)
	store := &task07DirectVideoStorageClient{Client: fixture.harness.Store.Client, size: 1024, contentType: "video/mp4"}
	_, mediaID := directIntegratorVideoUpload(t, fixture, store, "vv-it-338-upload")
	submitted := directIntegratorSubmit(t, fixture, "vv-it-338-submit")
	if strings.Contains(submitted, `"errors"`) || !strings.Contains(submitted, `"complete":true`) || !strings.Contains(submitted, `"preparationPending":true`) {
		t.Fatalf("authorized direct GraphQL submit did not accept the VIDEO evidence: %s", submitted)
	}
	var draft database.CaptureDraft
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Take(&draft).Error
	}); err != nil {
		t.Fatal(err)
	}
	var inspection database.Inspection
	var media database.MediaObject
	var jobCount int64
	var responsibility database.Responsibility
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=? AND responsibility_id=?", fixture.tenantID, mediaID, fixture.responsibilityID).Take(&media).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND media_id=?", fixture.tenantID, mediaID).Count(&jobCount).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.responsibilityID).Take(&responsibility).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND id=?", fixture.tenantID, responsibility.InspectionID).Take(&inspection).Error
	}); err != nil {
		t.Fatal(err)
	}
	var asset database.Asset
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", fixture.tenantID, inspection.AssetID).Take(&asset).Error
	}); err != nil {
		t.Fatal(err)
	}
	if draft.MediaKind != "VIDEO" || draft.Status != "SUBMITTED" || responsibility.Status != "SUBMITTED" || inspection.Status != "ANALYZING" || asset.Status != "ACTIVE" || media.VideoAttemptID == nil || media.Status != "RECEIVED" || jobCount != 1 {
		t.Fatalf("direct GraphQL flow bypassed responsibility/asset/modality checks or failed to create one preparation job: draft=%+v responsibility=%+v inspection=%+v media=%+v jobs=%d", draft, responsibility, inspection, media, jobCount)
	}
}

func TestVV_IT_341DirectUploadAndSubmitReplayK24KeepsOneEvidenceAndSubmission(t *testing.T) {
	fixture := newAPI05CaptureFixture(t)
	store := &task07DirectVideoStorageClient{Client: fixture.harness.Store.Client, size: 1024, contentType: "video/mp4"}
	_, mediaID := directIntegratorVideoUpload(t, fixture, store, "k24")
	// The attempt ID is part of the idempotent payload; resolve it from the
	// persisted media row before replaying the exact mutation.
	var media database.MediaObject
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", fixture.tenantID, mediaID).Take(&media).Error
	}); err != nil {
		t.Fatal(err)
	}
	replayed := executeCaptureGraphQL(t, fixture.server, `mutation { createMediaUpload(input:{contentType:"video/mp4",sizeBytes:1024,sha256:"`+strings.Repeat("a", 64)+`",attemptId:"`+media.VideoAttemptID.String()+`",clientMutationId:"k24"}) { upload { mediaId uploadId } userErrors { code } } }`, fixture.tenantID, fixture.credentials, true)
	if strings.Contains(replayed, `"errors"`) || !strings.Contains(replayed, `"mediaId":"`+mediaID.String()+`"`) {
		t.Fatalf("createMediaUpload replay k24 did not return the original media: %s", replayed)
	}
	firstSubmit := directIntegratorSubmit(t, fixture, "k24")
	// Submit commits and completes the external invitation; the same external
	// read credential remains valid for reconciliation after the lost response.
	secondSubmit := directIntegratorSubmit(t, fixture, "k24")
	var submissionIDs []string
	for _, response := range []string{firstSubmit, secondSubmit} {
		var payload struct {
			Data struct {
				Submit struct {
					Submission struct {
						ID string `json:"id"`
					} `json:"submission"`
				} `json:"submitCapture"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(response), &payload); err != nil || payload.Data.Submit.Submission.ID == "" || strings.Contains(response, `"errors"`) {
			t.Fatalf("submitCapture k24 did not return the committed submission: %s (%v)", response, err)
		}
		submissionIDs = append(submissionIDs, payload.Data.Submit.Submission.ID)
	}
	if submissionIDs[0] != submissionIDs[1] {
		t.Fatalf("submitCapture replay changed submission ID: %s -> %s", submissionIDs[0], submissionIDs[1])
	}
	var mediaCount, submissionCount, preparationCount, snapshotCount int64
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=? AND media_kind='VIDEO'", fixture.tenantID, fixture.responsibilityID).Count(&mediaCount).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.SubmissionVersion{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Count(&submissionCount).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND media_id=?", fixture.tenantID, mediaID).Count(&preparationCount).Error; err != nil {
			return err
		}
		return tx.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=(SELECT inspection_id FROM inspections.responsibilities WHERE tenant_id=? AND id=?)", fixture.tenantID, fixture.tenantID, fixture.responsibilityID).Count(&snapshotCount).Error
	}); err != nil {
		t.Fatal(err)
	}
	if mediaCount != 1 || submissionCount != 1 || preparationCount != 1 || snapshotCount > 1 {
		t.Fatalf("k24 replay duplicated evidence, submission, preparation, or report: media=%d submissions=%d preparations=%d snapshots=%d", mediaCount, submissionCount, preparationCount, snapshotCount)
	}
}

func (c *task07FailingStorageClient) CreateMultipart(context.Context, string, string, string) (string, error) {
	c.createCalls++
	return "", errors.New("storage backend failed qa-secret-123")
}

func TestVV_IT_342IntegratorGraphQLStorageFailureHidesSecret(t *testing.T) {
	fixture := newAPI05CaptureFixture(t)
	now := time.Now().UTC()
	attemptID := identity.NewID()
	if err := fixture.harness.AdminDB.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.tenantID, ResponsibilityID: fixture.responsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "ACTIVE", IdempotencyKey: "vv-it-342-attempt", ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	store := &task07FailingStorageClient{Client: fixture.harness.Store.Client}
	if err := mediacreate.Setup(mediacreate.Dependencies{Bus: fixture.bus, Service: mediacore.Service{DB: fixture.harness.DB, Store: objectstore.Store{Bucket: fixture.harness.Store.Bucket, Client: store}, Within: fixture.harness.WithinTenant}}); err != nil {
		t.Fatal(err)
	}
	query := `mutation { createMediaUpload(input:{contentType:"video/mp4",sizeBytes:1024,sha256:"` + strings.Repeat("a", 64) + `",attemptId:"` + attemptID.String() + `",clientMutationId:"vv-it-342-k21"}) { upload { mediaId } } }`
	response := executeCaptureGraphQL(t, fixture.server, query, fixture.tenantID, fixture.credentials, true)
	if strings.Contains(response, "qa-secret-123") || strings.Contains(response, `"upload":{"mediaId"`) || (!strings.Contains(response, "DEPENDENCY_UNAVAILABLE") && !strings.Contains(response, "INTERNAL")) {
		t.Fatalf("createMediaUpload exposed storage failure or returned success: %s", response)
	}
	if store.createCalls != 1 {
		t.Fatalf("storage CreateMultipart calls=%d; want exactly one injected failure", store.createCalls)
	}
}

func TestVV_IT_351IntegratorRejectsRevokedInvitationAndExpiredSessionBeforeUpload(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		change func(t *testing.T, fixture api03FailureFixture)
		code   string
	}{
		{name: "revoked invitation", code: "SESSION_EXPIRED", change: func(t *testing.T, fixture api03FailureFixture) {
			if err := fixture.harness.AdminDB.Model(&database.Invitation{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.invitationID).Updates(map[string]any{"status": "REVOKED", "revoked_at": time.Now().UTC()}).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{name: "expired external session", code: "SESSION_EXPIRED", change: func(t *testing.T, fixture api03FailureFixture) {
			if err := fixture.harness.AdminDB.Model(&database.ExternalSession{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.sessionID).Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := newAPI05CaptureFixture(t)
			scenario.change(t, fixture)
			store := &task07FailingStorageClient{Client: fixture.harness.Store.Client}
			if err := mediacreate.Setup(mediacreate.Dependencies{Bus: fixture.bus, Service: mediacore.Service{DB: fixture.harness.DB, Store: objectstore.Store{Bucket: fixture.harness.Store.Bucket, Client: store}, Within: fixture.harness.WithinTenant}}); err != nil {
				t.Fatal(err)
			}
			before := api05State(t, fixture)
			attemptID := identity.NewID()
			if err := fixture.harness.AdminDB.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.tenantID, ResponsibilityID: fixture.responsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "ACTIVE", IdempotencyKey: "vv-it-351-" + strings.ReplaceAll(scenario.name, " ", "-"), ExpiresAt: time.Now().UTC().Add(time.Hour), CreatedAt: time.Now().UTC()}).Error; err != nil {
				t.Fatal(err)
			}
			query := `mutation { createMediaUpload(input:{contentType:"video/mp4",sizeBytes:1024,sha256:"` + strings.Repeat("a", 64) + `",attemptId:"` + attemptID.String() + `",clientMutationId:"vv-it-351-` + strings.ReplaceAll(scenario.name, " ", "-") + `"}) { upload { mediaId } } }`
			response := executeCaptureGraphQL(t, fixture.server, query, fixture.tenantID, fixture.credentials, true)
			if !strings.Contains(response, scenario.code) || strings.Contains(response, `"upload":{"mediaId"`) || store.createCalls != 0 {
				t.Fatalf("invalid invitation/session accepted upload or reached storage: response=%s storage_calls=%d", response, store.createCalls)
			}
			assertAPI05StateUnchanged(t, fixture, before)
			var attempts int64
			if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
				return tx.Model(&database.VideoAttempt{}).Where("tenant_id=? AND id=?", fixture.tenantID, attemptID).Count(&attempts).Error
			}); err != nil || attempts != 1 {
				t.Fatalf("fixture attempt count=%d err=%v; upload must not mutate evidence", attempts, err)
			}
		})
	}
}

func TestVV_IT_352RateLimitDoesNotExposeOtherTenantQueueMetadata(t *testing.T) {
	fx := newInspectionModeGraphQLFixture(t, false, catalog.ChecklistOnly)
	if err := fx.h.AdminDB.Exec("INSERT INTO platform.video_admission (id, enabled, version, changed_at, reason, mutation_id, payload_hash) VALUES (1, TRUE, 1, ?, 'IT-352 fixture', 'vv-it-352-admission', '') ON CONFLICT (id) DO UPDATE SET enabled=TRUE", time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	other, err := fx.h.SeedTask06Fixture(context.Background(), harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	markerID, markerKey := identity.NewID(), "vv-it-352-tenant-b-queue-secret"
	if err := fx.h.WithinTenant(context.Background(), other.TenantID, func(tx *gorm.DB) error {
		rows := make([]database.MediaObject, 3)
		for i := range rows {
			rows[i] = database.MediaObject{ID: identity.NewID(), TenantID: other.TenantID, ResponsibilityID: other.ResponsibilityID, ObjectKey: markerKey + "/" + identity.NewID().String(), ContentType: "video/mp4", SHA256: strings.Repeat("b", 64), SizeBytes: 1024, Status: "RECEIVED", MediaKind: "VIDEO", IdempotencyKey: "vv-it-352-tenant-b-" + identity.NewID().String(), RequirementKey: "video:main", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}
		}
		rows[0].ID = markerID
		return tx.Create(&rows).Error
	}); err != nil {
		t.Fatal(err)
	}
	createInspection := `mutation { createInspection(input:{assetId:"` + fx.seed.AssetID.String() + `",participantId:"` + fx.seed.ParticipantID.String() + `",comparisonMode:CHECKLIST_ONLY,dueAt:"` + now.Add(time.Hour).Format(time.RFC3339) + `",deadlineAt:"` + now.Add(24*time.Hour).Format(time.RFC3339) + `",reminderInstants:[],reason:"IT-352",clientMutationId:"vv-it-352-inspection"}) { inspection { id } } }`
	created := executeTask06GraphQL(t, fx.server, createInspection, fx.ctx)
	var createdPayload struct {
		Data struct {
			Create struct {
				Inspection struct {
					ID string `json:"id"`
				} `json:"inspection"`
			} `json:"createInspection"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(created), &createdPayload); err != nil || createdPayload.Data.Create.Inspection.ID == "" || strings.Contains(created, `"errors"`) {
		t.Fatalf("create tenant A inspection: %s (%v)", created, err)
	}
	inspectionID, err := identity.ParseID(createdPayload.Data.Create.Inspection.ID)
	if err != nil {
		t.Fatal(err)
	}
	var responsibilityID identity.ID
	attemptID := identity.NewID()
	if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
		var responsibility database.Responsibility
		if err := tx.Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, inspectionID).Take(&responsibility).Error; err != nil {
			return err
		}
		responsibilityID = responsibility.ID
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fx.seed.TenantID, responsibilityID).Update("media_kind", "VIDEO").Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fx.seed.TenantID, ResponsibilityID: responsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "ACTIVE", IdempotencyKey: "vv-it-352-attempt", ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		rows := make([]database.MediaObject, 100)
		for i := range rows {
			rows[i] = database.MediaObject{ID: identity.NewID(), TenantID: fx.seed.TenantID, ResponsibilityID: responsibilityID, ObjectKey: "vv-it-352-a/" + identity.NewID().String(), ContentType: "video/mp4", SHA256: strings.Repeat("a", 64), SizeBytes: 1024, Status: "RECEIVED", MediaKind: "VIDEO", IdempotencyKey: "vv-it-352-a-" + identity.NewID().String(), RequirementKey: "video:main", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}
		}
		return tx.Create(&rows).Error
	}); err != nil {
		t.Fatal(err)
	}
	pepper := []byte(strings.Repeat("p", 32))
	token, err := security.NewScopedToken(fx.seed.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf := "vv-it-352-csrf"
	hash := security.HashToken(token)
	proof, err := security.CSRFProof(hash, csrf, pepper)
	if err != nil {
		t.Fatal(err)
	}
	invitation := database.Invitation{ID: identity.NewID(), TenantID: fx.seed.TenantID, ResponsibilityID: responsibilityID, TokenHash: hash[:], DeliveryIntents: json.RawMessage(`[]`), Status: "ACTIVE", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := fx.h.AdminDB.Create(&invitation).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.h.AdminDB.Create(&database.ExternalSession{ID: identity.NewID(), TenantID: fx.seed.TenantID, InvitationID: invitation.ID, ResponsibilityID: responsibilityID, SessionDigest: hash[:], CSRFDigest: proof[:], ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := mediacreate.Setup(mediacreate.Dependencies{Bus: bus, Service: mediacore.Service{DB: fx.h.DB, Store: objectstore.Store{Bucket: fx.h.Store.Bucket, Client: fx.h.Store.Client}, Within: fx.h.WithinTenant}}); err != nil {
		t.Fatal(err)
	}
	server := captureGraphQLServer(bus, invitationcore.Service{DB: fx.h.DB, Pepper: pepper})
	query := `mutation { createMediaUpload(input:{contentType:"video/mp4",sizeBytes:1024,sha256:"` + strings.Repeat("c", 64) + `",attemptId:"` + attemptID.String() + `",clientMutationId:"vv-it-352-upload"}) { upload { mediaId } } }`
	response := executeCaptureGraphQL(t, server, query, fx.seed.TenantID, requestctx.ExternalCredentials{SessionToken: token, CSRFToken: csrf}, true)
	if !strings.Contains(response, "RATE_LIMITED") || strings.Contains(response, `"upload":{"mediaId"`) {
		t.Fatalf("100 unfinished tenant A receipts did not rate limit upload: %s", response)
	}
	for _, leaked := range []string{other.TenantID.String(), markerID.String(), markerKey, "vv-it-352-tenant-b-", `"count":3`} {
		if strings.Contains(response, leaked) {
			t.Fatalf("rate limit response exposed tenant B queue metadata %q: %s", leaked, response)
		}
	}
	var aReceipts, bReceipts int64
	if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=?", fx.seed.TenantID, responsibilityID).Count(&aReceipts).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := fx.h.WithinTenant(context.Background(), other.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=? AND media_kind='VIDEO'", other.TenantID, other.ResponsibilityID).Count(&bReceipts).Error
	}); err != nil {
		t.Fatal(err)
	}
	if aReceipts != 100 || bReceipts != 3 {
		t.Fatalf("cross-tenant queue changed: A=%d B=%d; want 100/3", aReceipts, bReceipts)
	}
}

func TestVV_IT_344DirectGraphQLRejectsMissingAssetResponsibilityConsentAndReference(t *testing.T) {
	t.Run("asset omitted from schema-required input", func(t *testing.T) {
		fx := newFixedOriginGraphQLFixture(t, false)
		query := `mutation { createInspection(input:{participantId:"` + fx.seed.ParticipantID.String() + `",templateId:"` + fx.templateID.String() + `",comparisonMode:FIXED_ORIGIN,dueAt:"2026-10-17T12:00:00Z",deadlineAt:"2026-10-18T12:00:00Z",reminderInstants:[],reason:"vv-it-344",clientMutationId:"vv-it-344-asset"}) { inspection { id } } }`
		response := executeTask06GraphQL(t, fx.server, query, fx.ctx)
		if !strings.Contains(response, "assetId") || !strings.Contains(response, "errors") || strings.Contains(response, `"inspection":{"id"`) {
			t.Fatalf("createInspection without required asset was not rejected by schema: %s", response)
		}
	})
	t.Run("closed responsibility", func(t *testing.T) {
		assertCurrentOnlyMediaUploadRejected(t, "vv-it-344-closed", "CANCELED", 12, "INVALID_STATE")
	})
	t.Run("video processing consent omitted", func(t *testing.T) {
		fixture := newAPI03FailureFixture(t, "beginVideoCapture")
		if err := fixture.harness.AdminDB.Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Delete(&database.ProcessingAcceptance{}).Error; err != nil {
			t.Fatal(err)
		}
		query := `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"vv-it-344-consent"}) { attempt { id } userErrors { code } } }`
		response := executeCaptureGraphQL(t, fixture.server, query, fixture.tenantID, fixture.credentials, true)
		if !strings.Contains(response, "INVALID_INPUT") || strings.Contains(response, `"attempt":{"id"`) {
			t.Fatalf("beginVideoCapture without processing consent was not rejected: %s", response)
		}
		var attempts int64
		if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
			return tx.Model(&database.VideoAttempt{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Count(&attempts).Error
		}); err != nil || attempts != 0 {
			t.Fatalf("missing consent created %d video attempts (err=%v)", attempts, err)
		}
	})
	t.Run("fixed origin reference omitted", func(t *testing.T) {
		fx := newFixedOriginGraphQLFixture(t, false)
		response := executeFixedOriginEntry(t, fx, "createInspection", "vv-it-344-reference")
		assertFixedOriginInvalidInput(t, response)
		assertFixedOriginNoSideEffects(t, fx, "vv-it-344-reference")
	})
}

func TestVV_IT_346TenantBMetadataCannotAssociateTenantAUpload(t *testing.T) {
	fixture := newAPI05CaptureFixture(t)
	owner, err := fixture.harness.SeedVideoOriginFixture(context.Background(), harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	var before int64
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Count(&before).Error
	}); err != nil {
		t.Fatal(err)
	}
	response := executeCaptureGraphQL(t, fixture.server, `mutation { saveCaptureMetadata(input:{mediaId:"`+owner.MediaID.String()+`",requirementKey:"video:main",description:"Foreign source",captureSource:"CAMERA",clientMutationId:"vv-it-346-foreign-media"}) { media { id } userErrors { code } } }`, fixture.tenantID, fixture.credentials, true)
	if !strings.Contains(response, "NOT_FOUND") || strings.Contains(response, `"media":{"id"`) {
		t.Fatalf("tenant B associated tenant A media through saveCaptureMetadata: %s", response)
	}
	var after, ownerMediaCount int64
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Count(&after).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.WithinTenant(context.Background(), owner.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.MediaObject{}).Where("tenant_id=? AND id=? AND responsibility_id=?", owner.TenantID, owner.MediaID, owner.ResponsibilityID).Count(&ownerMediaCount).Error
	}); err != nil {
		t.Fatal(err)
	}
	if after != before || ownerMediaCount != 1 {
		t.Fatalf("cross-tenant metadata attempt changed associations: tenantB media %d -> %d; tenantA media rows=%d", before, after, ownerMediaCount)
	}
}

func TestVV_IT_350ComparisonRequestBeforeVideoBankReadyStaysPendingWithoutDispatch(t *testing.T) {
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
	now := time.Now().UTC()
	planID, submissionID, preparationID, itemID, componentID, jobID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	group := 0
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: seed.TenantID, MediaID: seed.MediaID, Status: "PROCESSING", Phase: "SCREENING", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, ProfileDigest: strings.Repeat("e", 64), PendingObjectKeys: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: seed.TenantID, DraftID: seed.DraftID, ResponsibilityID: seed.ResponsibilityID, VersionNumber: 70, Payload: json.RawMessage(`{}`), PayloadDigest: strings.Repeat("a", 64), SubmittedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlan{ID: planID, TenantID: seed.TenantID, InspectionID: seed.InspectionID, SubmissionID: submissionID, CurrentPreparationID: preparationID, Generation: 70, PlanDigest: strings.Repeat("b", 64), Status: "BUILDING", NextAttemptAt: now, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlanItem{ID: itemID, TenantID: seed.TenantID, PlanID: planID, ComponentID: componentID, RequirementKey: "video:main", Outcome: "READY", InputDigest: strings.Repeat("c", 64), OriginFrameIDs: json.RawMessage(`[]`), CurrentFrameIDs: json.RawMessage(`[]`), CoreGroups: json.RawMessage(`[]`), CurrentGroups: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.ComparisonJob{ID: jobID, TenantID: seed.TenantID, InspectionID: seed.InspectionID, VideoPlanID: &planID, VideoPlanItemID: &itemID, VideoCoreGroup: &group, RequirementKey: "video:main", ModelAlias: "test-model", InputDigest: strings.Repeat("d", 64), Status: "QUEUED", CreatedAt: now, UpdatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]string{"comparisonJobId": jobID.String(), "videoPlanId": planID.String(), "videoPlanItemId": itemID.String()})
	if err != nil {
		t.Fatal(err)
	}
	envelope := events.RawEnvelope{ID: identity.NewID(), Type: "analysis.video_comparison_requested.v1", SchemaVersion: 1, OccurredAt: now, TenantID: seed.TenantID, AggregateID: jobID, CorrelationID: "vv-it-350-" + identity.NewID().String(), Payload: payload}
	dispatches := 0
	handler := processcomparison.VideoRequestHandler(func(context.Context, *gorm.DB, []byte, identity.ID) error {
		dispatches++
		return nil
	})
	var handlerErr error
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		handlerErr = handler(ctx, tx, envelope)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(handlerErr, processcomparison.ErrVideoPlanPending) || dispatches != 0 {
		t.Fatalf("comparison before READY returned %v and dispatched %d times; want retryable pending and zero model dispatch", handlerErr, dispatches)
	}
	var status string
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.ComparisonJob{}).Select("status").Where("tenant_id=? AND id=?", seed.TenantID, jobID).Scan(&status).Error
	}); err != nil {
		t.Fatal(err)
	}
	if status != "QUEUED" {
		t.Fatalf("comparison status=%s; pending plan must not advance job", status)
	}
}

func TestVV_IT_349CompletedUploadAndRecaptureReplayDoNotCreateExtraWork(t *testing.T) {
	t.Run("completed video upload", func(t *testing.T) {
		fixture := newAPI05CaptureFixture(t)
		store := &task07DirectVideoStorageClient{Client: fixture.harness.Store.Client, size: 1024, contentType: "video/mp4"}
		_, mediaID := directIntegratorVideoUpload(t, fixture, store, "vv-it-349-upload")
		var preparationsBefore int64
		if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
			return tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND media_id=?", fixture.tenantID, mediaID).Count(&preparationsBefore).Error
		}); err != nil {
			t.Fatal(err)
		}
		replay := executeCaptureGraphQL(t, fixture.server, `mutation { completeMediaUpload(input:{mediaId:"`+mediaID.String()+`",parts:[{partNumber:1,etag:"vv-it-part-1"}],clientMutationId:"vv-it-349-complete-replay"}) { media { id status mediaKind } userErrors { code } } }`, fixture.tenantID, fixture.credentials, true)
		if strings.Contains(replay, `"errors"`) || !strings.Contains(replay, `"status":"RECEIVED"`) || !strings.Contains(replay, `"id":"`+mediaID.String()+`"`) {
			t.Fatalf("completed VIDEO upload replay changed the committed receipt: %s", replay)
		}
		var mediaCount, preparationCount int64
		if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
			if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=? AND media_kind='VIDEO'", fixture.tenantID, fixture.responsibilityID).Count(&mediaCount).Error; err != nil {
				return err
			}
			return tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND media_id=?", fixture.tenantID, mediaID).Count(&preparationCount).Error
		}); err != nil {
			t.Fatal(err)
		}
		if mediaCount != 1 || preparationCount != preparationsBefore || preparationsBefore == 0 || store.completeCalls != 1 {
			t.Fatalf("upload replay created duplicate evidence/preparation or repeated storage completion: media=%d preparations=%d remote_completions=%d", mediaCount, preparationCount, store.completeCalls)
		}
	})
	t.Run("recapture mutation", func(t *testing.T) {
		fixture := newAPI10Fixture(t)
		var baselinePreparationJobs int64
		if err := fixture.h.WithinTenant(fixture.ctx, fixture.seed.TenantID, func(tx *gorm.DB) error {
			return tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=?", fixture.seed.TenantID).Count(&baselinePreparationJobs).Error
		}); err != nil {
			t.Fatal(err)
		}
		query := fixture.valid("vv-it-349-recapture")
		first := executeFixedOriginRequest(t, fixture.fixedOriginGraphQLFixture, query, fixture.ctx, "", "")
		second := executeFixedOriginRequest(t, fixture.fixedOriginGraphQLFixture, query, fixture.ctx, "", "")
		var ids []string
		for _, response := range []string{first, second} {
			var payload struct {
				Data struct {
					Result struct {
						Recapture struct {
							ID string `json:"id"`
						} `json:"recapture"`
					} `json:"requestRecapture"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(response), &payload); err != nil || payload.Data.Result.Recapture.ID == "" || strings.Contains(response, `"errors"`) {
				t.Fatalf("requestRecapture replay did not return the committed cycle: %s (%v)", response, err)
			}
			ids = append(ids, payload.Data.Result.Recapture.ID)
		}
		if ids[0] != ids[1] {
			t.Fatalf("requestRecapture replay created a different cycle: %s -> %s", ids[0], ids[1])
		}
		var requests, preparationJobs int64
		if err := fixture.h.WithinTenant(fixture.ctx, fixture.seed.TenantID, func(tx *gorm.DB) error {
			if err := tx.Model(&database.RecaptureRequest{}).Where("tenant_id=? AND inspection_id=?", fixture.seed.TenantID, fixture.seed.InspectionID).Count(&requests).Error; err != nil {
				return err
			}
			return tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=?", fixture.seed.TenantID).Count(&preparationJobs).Error
		}); err != nil {
			t.Fatal(err)
		}
		if requests != 1 || preparationJobs != baselinePreparationJobs {
			t.Fatalf("recapture replay created extra cycle or preparation work: requests=%d jobs=%d", requests, preparationJobs)
		}
	})
}
