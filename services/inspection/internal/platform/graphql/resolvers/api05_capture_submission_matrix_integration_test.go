//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	capturefinalizer "inspection/services/inspection/internal/features/capture/finalize_submission"
	capturesave "inspection/services/inspection/internal/features/capture/save_metadata"
	capturesubmit "inspection/services/inspection/internal/features/capture/submit"
	origincore "inspection/services/inspection/internal/features/origins/core"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	recapturesubmit "inspection/services/inspection/internal/features/recapture/submit"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"
	"inspection/services/inspection/internal/platform/testfailure"

	"gorm.io/gorm"
)

// These are intentionally full GraphQL requests. API05 protects all three
// capture submission mutations with the participant session and CSRF proof.
type api05CaptureOperation struct {
	name, query, payload string
}

func api05CaptureOperations() []api05CaptureOperation {
	return []api05CaptureOperation{
		{
			name:    "saveCaptureMetadata",
			query:   `mutation { saveCaptureMetadata(input:{mediaId:"00000000-0000-4000-8000-000000000001",requirementKey:"video:main",description:"qa",captureSource:"CAMERA",clientMutationId:"vv-it-api05-meta"}) { media { id } clientMutationId } }`,
			payload: `"saveCaptureMetadata":`,
		},
		{
			name:    "submitCapture",
			query:   `mutation { submitCapture(input:{confirmIncomplete:false,clientMutationId:"vv-it-api05-submit"}) { submission { id } clientMutationId } }`,
			payload: `"submitCapture":`,
		},
		{
			name:    "submitRecapture",
			query:   `mutation { submitRecapture(input:{requestId:"00000000-0000-4000-8000-000000000002",confirmIncomplete:false,clientMutationId:"vv-it-api05-recapture"}) { recapture { id } clientMutationId } }`,
			payload: `"submitRecapture":`,
		},
	}
}

type api05CaptureState struct {
	Media, Submissions, Recaptures int64
}

func api05State(t *testing.T, fixture api03FailureFixture) api05CaptureState {
	t.Helper()
	var state api05CaptureState
	err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Count(&state.Media).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.SubmissionVersion{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Count(&state.Submissions).Error; err != nil {
			return err
		}
		return tx.Model(&database.RecaptureRequest{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Count(&state.Recaptures).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func assertAPI05StateUnchanged(t *testing.T, fixture api03FailureFixture, before api05CaptureState) {
	t.Helper()
	if after := api05State(t, fixture); after != before {
		t.Fatalf("protected capture state advanced: before=%+v after=%+v", before, after)
	}
}

func TestVV_IT_425API05CaptureMutationsRequireParticipantSession(t *testing.T) {
	for _, operation := range api05CaptureOperations() {
		t.Run(operation.name, func(t *testing.T) {
			fixture := newAPI03FailureFixture(t, operation.name)
			response := executeAPI03WithoutCredentials(t, fixture, operation.query)
			assertAPI03Rejected(t, response, "UNAUTHENTICATED", operation.payload)
		})
	}
}

func TestVV_IT_426API05CaptureMutationsRejectActorWithoutParticipantCapability(t *testing.T) {
	for _, operation := range api05CaptureOperations() {
		t.Run(operation.name, func(t *testing.T) {
			fixture := newAPI03FailureFixture(t, operation.name)
			before := api05State(t, fixture)
			response := executeAPI03AsInternalPrincipal(t, fixture, operation.query)
			assertAPI03Rejected(t, response, "FORBIDDEN", operation.payload)
			assertAPI05StateUnchanged(t, fixture, before)
		})
	}
}

func TestVV_IT_435API05CaptureMutationsRejectExpiredSession(t *testing.T) {
	for _, operation := range api05CaptureOperations() {
		t.Run(operation.name, func(t *testing.T) {
			fixture := newAPI03FailureFixture(t, operation.name)
			before := api05State(t, fixture)
			if err := fixture.harness.AdminDB.Model(&database.ExternalSession{}).
				Where("tenant_id=? AND id=?", fixture.tenantID, fixture.sessionID).
				Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
				t.Fatal(err)
			}
			response := executeAPI03WithCredentials(t, fixture, operation.query)
			assertAPI03Rejected(t, response, "SESSION_EXPIRED", operation.payload)
			assertAPI05StateUnchanged(t, fixture, before)
		})
	}
}

func TestVV_IT_436API05CaptureMutationsRejectInvalidCSRFBeforeMutation(t *testing.T) {
	for _, operation := range api05CaptureOperations() {
		for _, csrf := range []string{"", "wrong-api05-csrf"} {
			t.Run(fmt.Sprintf("%s/csrf-present=%t", operation.name, csrf != ""), func(t *testing.T) {
				fixture := newAPI03FailureFixture(t, operation.name)
				before := api05State(t, fixture)
				credentials := fixture.credentials
				credentials.CSRFToken = csrf
				response := executeAPI03WithCredentialsValue(t, fixture, operation.query, credentials)
				if strings.Contains(response, operation.payload) || !strings.Contains(response, "errors") || strings.Contains(response, "INTERNAL") || strings.Contains(response, "DEPENDENCY_UNAVAILABLE") {
					t.Fatalf("%s passed invalid CSRF or entered protected work: %s", operation.name, response)
				}
				assertAPI05StateUnchanged(t, fixture, before)
			})
		}
	}
}

func newAPI05CaptureFixture(t *testing.T) api03FailureFixture {
	t.Helper()
	fixture := newAPI03FailureFixture(t, "submitCapture")
	// The Task06 seed stores the template document; an open video draft carries
	// the frozen requirement list that bootstrap and capture validation read.
	if err := fixture.harness.AdminDB.Model(&database.CaptureDraft{}).
		Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).
		Update("requirements", []byte(`[{"key":"video:main","label":"Vídeo principal","evidenceKind":"VIDEO","required":true,"minimumCount":1,"maximumCount":1}]`)).Error; err != nil {
		t.Fatal(err)
	}
	var responsibility database.Responsibility
	if err := fixture.harness.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.responsibilityID).Take(&responsibility).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.AdminDB.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.tenantID, responsibility.InspectionID).Update("status", "IN_PROGRESS").Error; err != nil {
		t.Fatal(err)
	}
	capture := capturecore.Service{DB: fixture.harness.DB}
	finalizer, err := capturefinalizer.Setup(capturefinalizer.Dependencies{Origins: origincore.Service{DB: fixture.harness.DB}, Recapture: recapturecore.Service{DB: fixture.harness.DB}})
	if err != nil {
		t.Fatal(err)
	}
	capture.Finalizer = finalizer
	if err := capturesave.Setup(capturesave.Dependencies{Bus: fixture.bus, Service: capture}); err != nil {
		t.Fatal(err)
	}
	if err := capturesubmit.Setup(capturesubmit.Dependencies{Bus: fixture.bus, Service: capture}); err != nil {
		t.Fatal(err)
	}
	if err := recapturesubmit.Setup(recapturesubmit.Dependencies{DB: fixture.harness.DB, Bus: fixture.bus, Capture: capture}); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func executeAPI05InjectedFailure(t *testing.T, fixture api03FailureFixture, operation, query, failure string) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+quote(query)+`}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(testfailure.HeaderOperation, operation)
	request.Header.Set(testfailure.HeaderFailure, failure)
	request = request.WithContext(requestctx.WithExternalCredentials(request.Context(), fixture.credentials))
	request = testfailure.WithRequest(request, "test")
	response := httptest.NewRecorder()
	fixture.server.ServeHTTP(response, request)
	return response.Body.String()
}

func seedAPI05Receipt(t *testing.T, fixture api03FailureFixture, operation string, mediaID, requestID identity.ID) {
	t.Helper()
	now := time.Now().UTC()
	attemptID := identity.NewID()
	err := fixture.harness.AdminDB.Transaction(func(tx *gorm.DB) error {
		if operation == "submitRecapture" {
			if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Update("kind", "RECAPTURE").Error; err != nil {
				return err
			}
			if err := tx.Create(&database.RecaptureRequest{ID: requestID, TenantID: fixture.tenantID, InspectionID: identity.NewID(), ResponsibilityID: fixture.responsibilityID, Status: "REQUESTED", DeadlineAt: now.Add(time.Hour), IdempotencyKey: "vv-it-api05-request", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
		}
		if operation == "saveCaptureMetadata" || operation == "submitCapture" {
			if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.tenantID, ResponsibilityID: fixture.responsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1024, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "vv-it-api05-attempt", PayloadDigest: strings.Repeat("a", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
				return err
			}
			return tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.tenantID, ResponsibilityID: fixture.responsibilityID, ObjectKey: "vv-it-api05/" + mediaID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("b", 64), SizeBytes: 10, Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "vv-it-api05-media", AttentionItems: []byte("[]"), Flags: []byte("[]"), CreatedAt: now}).Error
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func seedAPI05VideoMedia(t *testing.T, fixture api03FailureFixture, mediaID identity.ID, requirementKey string) {
	t.Helper()
	now := time.Now().UTC()
	attemptID := identity.NewID()
	err := fixture.harness.AdminDB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.tenantID, ResponsibilityID: fixture.responsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1024, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "vv-it-api05-attempt-" + mediaID.String(), PayloadDigest: strings.Repeat("a", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.tenantID, ResponsibilityID: fixture.responsibilityID, ObjectKey: "vv-it-api05/" + mediaID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("b", 64), SizeBytes: 10, Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: requirementKey, IdempotencyKey: "vv-it-api05-media-" + mediaID.String(), AttentionItems: []byte("[]"), Flags: []byte("[]"), CreatedAt: now}).Error
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestVV_IT_428API05CaptureDatabaseAdapterUnavailable(t *testing.T) {
	for _, operation := range api05CaptureOperations() {
		t.Run(operation.name, func(t *testing.T) {
			fixture := newAPI05CaptureFixture(t)
			mediaID, requestID := identity.NewID(), identity.NewID()
			operation.query = strings.ReplaceAll(operation.query, "00000000-0000-4000-8000-000000000001", mediaID.String())
			operation.query = strings.ReplaceAll(operation.query, "00000000-0000-4000-8000-000000000002", requestID.String())
			seedAPI05Receipt(t, fixture, operation.name, mediaID, requestID)
			before := api05State(t, fixture)
			response := executeAPI05InjectedFailure(t, fixture, operation.name, operation.query, "dependency")
			assertAPI03Rejected(t, response, "DEPENDENCY_UNAVAILABLE", operation.payload)
			assertAPI05StateUnchanged(t, fixture, before)
		})
	}
}

func TestVV_IT_429API05CaptureInternalAdapterErrorHidesSecret(t *testing.T) {
	for _, operation := range api05CaptureOperations() {
		t.Run(operation.name, func(t *testing.T) {
			fixture := newAPI05CaptureFixture(t)
			mediaID, requestID := identity.NewID(), identity.NewID()
			operation.query = strings.ReplaceAll(operation.query, "00000000-0000-4000-8000-000000000001", mediaID.String())
			operation.query = strings.ReplaceAll(operation.query, "00000000-0000-4000-8000-000000000002", requestID.String())
			seedAPI05Receipt(t, fixture, operation.name, mediaID, requestID)
			response := executeAPI05InjectedFailure(t, fixture, operation.name, operation.query, "internal")
			assertAPI03Rejected(t, response, "INTERNAL", operation.payload)
			if strings.Contains(response, testfailure.Secret) {
				t.Fatalf("internal adapter error leaked secret: %s", response)
			}
		})
	}
}

func TestVV_IT_431SubmitCaptureWithoutVideoReceiptIsInvalidState(t *testing.T) {
	fixture := newAPI05CaptureFixture(t)
	operation := api05CaptureOperations()[1]
	before := api05State(t, fixture)
	response := executeAPI03WithCredentials(t, fixture, operation.query)
	assertAPI03Rejected(t, response, "INVALID_STATE", operation.payload)
	assertAPI05StateUnchanged(t, fixture, before)
}

func seedAPI05RecaptureRequest(t *testing.T, fixture api03FailureFixture, deadline time.Time) identity.ID {
	t.Helper()
	requestID := identity.NewID()
	err := fixture.harness.AdminDB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.CaptureDraft{}).
			Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).
			Updates(map[string]any{"kind": "RECAPTURE", "requirements": []byte(`[{"key":"video:supplement","label":"Complemento","evidenceKind":"VIDEO","required":true,"minimumCount":1,"maximumCount":1}]`)}).Error; err != nil {
			return err
		}
		var responsibility database.Responsibility
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.responsibilityID).Take(&responsibility).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		return tx.Create(&database.RecaptureRequest{ID: requestID, TenantID: fixture.tenantID, InspectionID: responsibility.InspectionID, ResponsibilityID: fixture.responsibilityID, Status: "REQUESTED", DeadlineAt: deadline, IdempotencyKey: "vv-it-api05-" + requestID.String(), CreatedAt: now, UpdatedAt: now}).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	return requestID
}

func TestVV_IT_432SubmitRecaptureRejectsPassedDeadline(t *testing.T) {
	fixture := newAPI05CaptureFixture(t)
	requestID := seedAPI05RecaptureRequest(t, fixture, time.Now().UTC().Add(-time.Second))
	mediaID := identity.NewID()
	seedAPI05VideoMedia(t, fixture, mediaID, "video:supplement")
	query := `mutation { submitRecapture(input:{requestId:"` + requestID.String() + `",confirmIncomplete:false,clientMutationId:"vv-it-432-deadline"}) { recapture { id status } clientMutationId } }`
	before := api05State(t, fixture)
	response := executeAPI03WithCredentials(t, fixture, query)
	assertAPI03Rejected(t, response, "INVALID_STATE", `"submitRecapture":`)
	assertAPI05StateUnchanged(t, fixture, before)
	var request database.RecaptureRequest
	if err := fixture.harness.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, requestID).Take(&request).Error; err != nil {
		t.Fatal(err)
	}
	if request.Status != "REQUESTED" {
		t.Fatalf("expired recapture status=%q, want REQUESTED", request.Status)
	}
}

func TestVV_IT_433ImpossibleOnlySupplementCannotBeSubmitted(t *testing.T) {
	fixture := newAPI05CaptureFixture(t)
	requestID := seedAPI05RecaptureRequest(t, fixture, time.Now().UTC().Add(time.Hour))
	var draft database.CaptureDraft
	if err := fixture.harness.AdminDB.Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.AdminDB.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: fixture.tenantID, DraftID: draft.ID, RequirementKey: "video:supplement", MediaIDs: []byte(`[]`), Flags: []byte(`[]`), ImpossibilityReason: "O acesso ao cômodo estava bloqueado", Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	query := `mutation { submitRecapture(input:{requestId:"` + requestID.String() + `",confirmIncomplete:true,clientMutationId:"vv-it-433-impossible"}) { recapture { id status } clientMutationId } }`
	before := api05State(t, fixture)
	response := executeAPI03WithCredentials(t, fixture, query)
	assertAPI03Rejected(t, response, "INVALID_STATE", `"submitRecapture":`)
	assertAPI05StateUnchanged(t, fixture, before)
	var request database.RecaptureRequest
	if err := fixture.harness.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, requestID).Take(&request).Error; err != nil {
		t.Fatal(err)
	}
	if request.Status != "REQUESTED" {
		t.Fatalf("impossibility-only recapture status=%q, want REQUESTED", request.Status)
	}
}

func TestVV_IT_434CompetingSubmitCaptureGeneration(t *testing.T) {
	fixture := newAPI05CaptureFixture(t)
	mediaID := identity.NewID()
	seedAPI05VideoMedia(t, fixture, mediaID, "video:main")
	query := `mutation { submitCapture(input:{confirmIncomplete:false,clientMutationId:"vv-it-434-race"}) { submission { id } clientMutationId } }`
	start := make(chan struct{})
	responses := make(chan string, 2)
	for range 2 {
		go func() {
			<-start
			responses <- executeAPI03WithCredentials(t, fixture, query)
		}()
	}
	close(start)
	first, second := <-responses, <-responses
	parse := func(body string) (id, code string) {
		var payload struct {
			Data struct {
				Submit struct {
					Submission *struct {
						ID string `json:"id"`
					} `json:"submission"`
				} `json:"submitCapture"`
			} `json:"data"`
			Errors []struct {
				Extensions struct {
					Code string `json:"code"`
				} `json:"extensions"`
			} `json:"errors"`
		}
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatalf("invalid GraphQL response: %s (%v)", body, err)
		}
		if payload.Data.Submit.Submission != nil {
			id = payload.Data.Submit.Submission.ID
		}
		if len(payload.Errors) > 0 {
			code = payload.Errors[0].Extensions.Code
		}
		return id, code
	}
	firstID, firstCode := parse(first)
	secondID, secondCode := parse(second)
	t.Logf("concurrent submit outcomes: first=(id:%s code:%s), second=(id:%s code:%s)", firstID, firstCode, secondID, secondCode)
	if firstID != "" && secondID != "" {
		if firstID != secondID {
			t.Fatalf("concurrent replays returned different immutable submissions: %s and %s", firstID, secondID)
		}
	} else if !(firstID != "" && secondCode == "CONFLICT" || secondID != "" && firstCode == "CONFLICT") {
		t.Fatalf("race must yield one success and either CONFLICT or the same immutable replay; first=%s second=%s", first, second)
	}
	var submissions int64
	if err := fixture.harness.AdminDB.Model(&database.SubmissionVersion{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Count(&submissions).Error; err != nil {
		t.Fatal(err)
	}
	if submissions != 1 {
		t.Fatalf("concurrent submit created %d submission versions, want one", submissions)
	}
}

func TestVV_IT_424VideoCaptureAndRecaptureSubmissionJourney(t *testing.T) {
	fixture := newAPI05CaptureFixture(t)
	mainID := identity.NewID()
	seedAPI05VideoMedia(t, fixture, mainID, "video:main")
	mainMetadata := `mutation { saveCaptureMetadata(input:{mediaId:"` + mainID.String() + `",requirementKey:"video:main",description:"C1",captureSource:"CAMERA",clientMutationId:"vv-it-424-main-meta"}) { media { id } clientMutationId } }`
	metadataResponse := executeAPI03WithCredentials(t, fixture, mainMetadata)
	if !strings.Contains(metadataResponse, `"clientMutationId":"vv-it-424-main-meta"`) || !strings.Contains(metadataResponse, mainID.String()) {
		t.Fatalf("C1 metadata association failed: %s", metadataResponse)
	}
	mainSubmit := `mutation { submitCapture(input:{confirmIncomplete:false,clientMutationId:"vv-it-424-main-submit"}) { submission { id preparationPending } clientMutationId } }`
	decodeSubmission := func(body string) (string, bool) {
		t.Helper()
		var payload struct {
			Data struct {
				Submit struct {
					Submission *struct {
						ID      string `json:"id"`
						Pending bool   `json:"preparationPending"`
					} `json:"submission"`
					MutationID string `json:"clientMutationId"`
				} `json:"submitCapture"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(body), &payload); err != nil || payload.Data.Submit.Submission == nil || payload.Data.Submit.MutationID != "vv-it-424-main-submit" {
			t.Fatalf("submission payload missing mutation ID/outcome: %s (%v)", body, err)
		}
		return payload.Data.Submit.Submission.ID, payload.Data.Submit.Submission.Pending
	}
	mainResult, pending := decodeSubmission(executeAPI03WithCredentials(t, fixture, mainSubmit))
	if !pending {
		t.Fatal("initial C1 submission should report preparationPending=true")
	}
	replayID, replayPending := decodeSubmission(executeAPI03WithCredentials(t, fixture, mainSubmit))
	if replayID != mainResult || !replayPending {
		t.Fatalf("replay changed immutable submission outcome: id=%s pending=%t; want id=%s pending=true", replayID, replayPending, mainResult)
	}

	var original database.Responsibility
	if err := fixture.harness.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.responsibilityID).Take(&original).Error; err != nil {
		t.Fatal(err)
	}
	var draft database.CaptureDraft
	if err := fixture.harness.AdminDB.Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	newResponsibilityID, newDraftID, requestID := identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	if err := fixture.harness.AdminDB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&database.Responsibility{ID: newResponsibilityID, TenantID: fixture.tenantID, InspectionID: original.InspectionID, ParticipantID: original.ParticipantID, Status: "IN_PROGRESS", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.CaptureDraft{ID: newDraftID, TenantID: fixture.tenantID, ResponsibilityID: newResponsibilityID, Kind: "RECAPTURE", MediaKind: "VIDEO", TemplateVersionID: draft.TemplateVersionID, ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[{"key":"video:supplement","label":"Complemento","evidenceKind":"VIDEO","required":true,"minimumCount":1,"maximumCount":1}]`), Status: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ProcessingAcceptance{ID: identity.NewID(), TenantID: fixture.tenantID, ResponsibilityID: newResponsibilityID, DisclosureVersion: "video-v1", PhotoProcessing: false, VideoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.RecaptureRequest{ID: requestID, TenantID: fixture.tenantID, InspectionID: original.InspectionID, ResponsibilityID: newResponsibilityID, Status: "REQUESTED", DeadlineAt: now.Add(time.Hour), IdempotencyKey: "vv-it-424-request", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	pepper := []byte(strings.Repeat("p", 32))
	token, err := security.NewScopedToken(fixture.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf := "vv-it-424-rc-csrf"
	hash := security.HashToken(token)
	proof, err := security.CSRFProof(hash, csrf, pepper)
	if err != nil {
		t.Fatal(err)
	}
	invitationID := identity.NewID()
	if err := fixture.harness.AdminDB.Create(&database.Invitation{ID: invitationID, TenantID: fixture.tenantID, ResponsibilityID: newResponsibilityID, TokenHash: hash[:], DeliveryIntents: []byte(`[]`), Status: "ACTIVE", ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.AdminDB.Create(&database.ExternalSession{ID: identity.NewID(), TenantID: fixture.tenantID, InvitationID: identity.Ptr(invitationID), ResponsibilityID: &newResponsibilityID, SessionDigest: hash[:], CSRFDigest: proof[:], ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	fixture.credentials = requestctx.ExternalCredentials{SessionToken: token, CSRFToken: csrf}
	supplementID, attemptID := identity.NewID(), identity.NewID()
	if err := fixture.harness.AdminDB.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.tenantID, ResponsibilityID: newResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1024, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "vv-it-424-attempt", PayloadDigest: strings.Repeat("a", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.AdminDB.Create(&database.MediaObject{ID: supplementID, TenantID: fixture.tenantID, ResponsibilityID: newResponsibilityID, ObjectKey: "vv-it-api05/" + supplementID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("b", 64), SizeBytes: 10, Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:supplement", IdempotencyKey: "vv-it-424-media", AttentionItems: []byte("[]"), Flags: []byte("[]"), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.AdminDB.Create(&database.VideoPreparationJob{ID: identity.NewID(), TenantID: fixture.tenantID, MediaID: supplementID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, ManifestDigest: strings.Repeat("c", 64), PendingObjectKeys: []byte(`[]`), FirstReadyAt: &now, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	supplementMetadata := `mutation { saveCaptureMetadata(input:{mediaId:"` + supplementID.String() + `",requirementKey:"video:supplement",description:"RC1",captureSource:"CAMERA",clientMutationId:"vv-it-424-supp-meta"}) { media { id } clientMutationId } }`
	suppMetaResponse := executeAPI03WithCredentials(t, fixture, supplementMetadata)
	if !strings.Contains(suppMetaResponse, `"clientMutationId":"vv-it-424-supp-meta"`) || !strings.Contains(suppMetaResponse, supplementID.String()) {
		t.Fatalf("RC1 supplement association failed: %s", suppMetaResponse)
	}
	submitRecapture := `mutation { submitRecapture(input:{requestId:"` + requestID.String() + `",confirmIncomplete:false,clientMutationId:"vv-it-424-rec-submit"}) { recapture { id status } clientMutationId } }`
	response := executeAPI03WithCredentials(t, fixture, submitRecapture)
	if !strings.Contains(response, `"clientMutationId":"vv-it-424-rec-submit"`) || !strings.Contains(response, `"status":"RECEIVED"`) {
		t.Fatalf("RC1 receipt outcome was not echoed/persisted: %s", response)
	}
	var stored database.RecaptureRequest
	if err := fixture.harness.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, requestID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "RECEIVED" {
		t.Fatalf("persisted recapture status=%s, want RECEIVED", stored.Status)
	}
}

func TestVV_IT_348LostSubmitResponseReconcilesOneCommittedSubmission(t *testing.T) {
	fixture := newAPI05CaptureFixture(t)
	mediaID := identity.NewID()
	seedAPI05VideoMedia(t, fixture, mediaID, "video:main")
	metadata := `mutation { saveCaptureMetadata(input:{mediaId:"` + mediaID.String() + `",requirementKey:"video:main",description:"C1",captureSource:"CAMERA",clientMutationId:"vv-it-348-metadata"}) { media { id } } }`
	if response := executeAPI03WithCredentials(t, fixture, metadata); !strings.Contains(response, mediaID.String()) || strings.Contains(response, `"errors"`) {
		t.Fatalf("C1 metadata association failed: %s", response)
	}
	submit := `mutation { submitCapture(input:{confirmIncomplete:false,clientMutationId:"vv-it-348-k24"}) { submission { id preparationPending } clientMutationId } }`
	// Discard the first response to model a committed request whose network
	// response was lost. The retry must return the committed immutable result.
	_ = executeAPI03WithCredentials(t, fixture, submit)
	replay := executeAPI03WithCredentials(t, fixture, submit)
	var payload struct {
		Data struct {
			Submit struct {
				Submission *struct {
					ID      string `json:"id"`
					Pending bool   `json:"preparationPending"`
				} `json:"submission"`
				MutationID string `json:"clientMutationId"`
			} `json:"submitCapture"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(replay), &payload); err != nil || payload.Data.Submit.Submission == nil || payload.Data.Submit.MutationID != "vv-it-348-k24" || !payload.Data.Submit.Submission.Pending {
		t.Fatalf("replay did not resolve the committed submission: %s (%v)", replay, err)
	}
	var submissions int64
	if err := fixture.harness.AdminDB.Model(&database.SubmissionVersion{}).
		Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).
		Count(&submissions).Error; err != nil {
		t.Fatal(err)
	}
	if submissions != 1 {
		t.Fatalf("lost-response replay created %d submission versions; want exactly one", submissions)
	}
}

func TestVV_IT_430VideoCaptureRejectsPhotoRequirementKey(t *testing.T) {
	fixture := newAPI05CaptureFixture(t)
	mediaID := identity.NewID()
	if err := fixture.harness.AdminDB.Model(&database.CaptureDraft{}).
		Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).
		Update("requirements", []byte(`[{"key":"room","section":"photo","label":"Sala","evidenceKind":"PHOTO","required":true}]`)).Error; err != nil {
		t.Fatal(err)
	}
	seedAPI05VideoMedia(t, fixture, mediaID, "room")
	query := `mutation { saveCaptureMetadata(input:{mediaId:"` + mediaID.String() + `",requirementKey:"room",description:"QA",captureSource:"CAMERA",clientMutationId:"vv-it-430-photo-key"}) { media { id } clientMutationId } }`
	response := executeAPI03WithCredentials(t, fixture, query)
	assertAPI03Rejected(t, response, "INVALID_INPUT", `"saveCaptureMetadata":`)
	var stored database.MediaObject
	if err := fixture.harness.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, mediaID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.CapturedAt != nil || stored.RequirementKey != "room" {
		t.Fatalf("rejected photo requirement was persisted as capture metadata: %+v", stored)
	}
}

func TestVV_IT_427ForeignTenantMediaAssociationIsNotFound(t *testing.T) {
	fixtureA := newAPI05CaptureFixture(t)
	fixtureB := newAPI03FailureFixture(t, "submitCapture")
	foreignMediaID := identity.NewID()
	seedAPI05VideoMedia(t, fixtureB, foreignMediaID, "video:main")
	query := `mutation { saveCaptureMetadata(input:{mediaId:"` + foreignMediaID.String() + `",requirementKey:"video:main",description:"QA",captureSource:"CAMERA",clientMutationId:"vv-it-427-foreign"}) { media { id } clientMutationId } }`
	response := executeAPI03WithCredentials(t, fixtureA, query)
	assertAPI03Rejected(t, response, "NOT_FOUND", `"saveCaptureMetadata":`)
	if strings.Contains(response, fixtureB.tenantID.String()) || strings.Contains(response, foreignMediaID.String()) {
		t.Fatalf("foreign tenant media metadata leaked: %s", response)
	}
	var foreignMedia database.MediaObject
	if err := fixtureB.harness.AdminDB.Where("tenant_id=? AND id=?", fixtureB.tenantID, foreignMediaID).Take(&foreignMedia).Error; err != nil {
		t.Fatal(err)
	}
	if foreignMedia.CapturedAt != nil || foreignMedia.Description != "" {
		t.Fatalf("foreign media was associated under tenant A: %+v", foreignMedia)
	}

	t.Run("submitRecapture foreign request", func(t *testing.T) {
		foreignRequestID := identity.NewID()
		now := time.Now().UTC()
		if err := fixtureB.harness.AdminDB.Create(&database.RecaptureRequest{ID: foreignRequestID, TenantID: fixtureB.tenantID, InspectionID: identity.NewID(), ResponsibilityID: fixtureB.responsibilityID, Status: "REQUESTED", DeadlineAt: now.Add(time.Hour), IdempotencyKey: "vv-it-427-foreign-request", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		before := api05State(t, fixtureA)
		query := `mutation { submitRecapture(input:{requestId:"` + foreignRequestID.String() + `",confirmIncomplete:false,clientMutationId:"vv-it-427-foreign-request"}) { recapture { id status } clientMutationId } }`
		response := executeAPI03WithCredentials(t, fixtureA, query)
		assertAPI03Rejected(t, response, "NOT_FOUND", `"submitRecapture":`)
		if strings.Contains(response, fixtureB.tenantID.String()) || strings.Contains(response, foreignRequestID.String()) {
			t.Fatalf("foreign recapture request leaked: %s", response)
		}
		assertAPI05StateUnchanged(t, fixtureA, before)
		var stored database.RecaptureRequest
		if err := fixtureB.harness.AdminDB.Where("tenant_id=? AND id=?", fixtureB.tenantID, foreignRequestID).Take(&stored).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Status != "REQUESTED" {
			t.Fatalf("foreign recapture request changed under tenant A: %s", stored.Status)
		}
	})

	t.Run("submitCapture scope is actor-derived", func(t *testing.T) {
		before := api05State(t, fixtureA)
		query := `mutation { submitCapture(input:{confirmIncomplete:false,tenantId:"` + fixtureB.tenantID.String() + `",clientMutationId:"vv-it-427-selector"}) { submission { id } clientMutationId } }`
		response := executeAPI03WithCredentials(t, fixtureA, query)
		if !strings.Contains(response, `"errors"`) || !strings.Contains(response, "tenantId") || strings.Contains(response, `"submission":{`) {
			t.Fatalf("injected tenant selector was not rejected by schema validation: %s", response)
		}
		assertAPI05StateUnchanged(t, fixtureA, before)
	})
}
