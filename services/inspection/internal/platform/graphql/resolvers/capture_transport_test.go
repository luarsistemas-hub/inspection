package resolvers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	capturebootstrap "inspection/services/inspection/internal/features/capture/bootstrap"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	capturedeclare "inspection/services/inspection/internal/features/capture/declare_impossibility"
	capturesave "inspection/services/inspection/internal/features/capture/save_metadata"
	capturesubmit "inspection/services/inspection/internal/features/capture/submit"
	acceptprocessing "inspection/services/inspection/internal/features/invitations/accept_processing"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	mediacomplete "inspection/services/inspection/internal/features/media/complete_upload"
	mediacore "inspection/services/inspection/internal/features/media/core"
	mediacreate "inspection/services/inspection/internal/features/media/create_upload"
	mediafalsepositive "inspection/services/inspection/internal/features/media/declare_false_positive"
	mediapresign "inspection/services/inspection/internal/features/media/presign_parts"
	originactivate "inspection/services/inspection/internal/features/origins/activate_version"
	origincore "inspection/services/inspection/internal/features/origins/core"
	origininvalidate "inspection/services/inspection/internal/features/origins/invalidate_version"
	origininvite "inspection/services/inspection/internal/features/origins/invite_capture"
	originlist "inspection/services/inspection/internal/features/origins/list_versions"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	recapturerequest "inspection/services/inspection/internal/features/recapture/request"
	recapturesubmit "inspection/services/inspection/internal/features/recapture/submit"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTask5GraphQLTransportIT411ToIT412IT473ToIT478IT509ToIT526(t *testing.T) {
	tenantID, responsibilityID := identity.NewID(), identity.NewID()
	ids := make([]identity.ID, 8)
	for index := range ids {
		ids[index] = identity.NewID()
	}
	bus := mediator.New()
	register := func(message any, result any) {
		t.Helper()
		if err := bus.RegisterCommand(message, func(context.Context, any) (any, error) { return result, nil }); err != nil {
			t.Fatal(err)
		}
	}
	register(origininvite.Command{}, origincore.Invitation{InvitationID: ids[0], VersionID: ids[1]})
	register(originactivate.Command{}, database.OriginVersion{ID: ids[1], OriginID: ids[2], VersionNumber: 1, Status: "ACTIVE"})
	register(origininvalidate.Command{}, database.OriginVersion{ID: ids[1], OriginID: ids[2], VersionNumber: 1, Status: "INVALIDATED"})
	register(acceptprocessing.Command{}, acceptprocessing.Result{Status: "ACCEPTED"})
	register(mediacreate.Command{}, mediacore.Upload{MediaID: ids[3], UploadID: "upload", ExpiresAt: time.Now().Add(time.Hour)})
	register(mediapresign.Command{}, []objectstore.PresignedPart{{PartNumber: 1, URL: "https://signed.invalid", ExpiresAt: time.Now().Add(time.Minute)}})
	register(mediacomplete.Command{}, database.MediaObject{ID: ids[3], Status: "VERIFIED", Flags: json.RawMessage(`[]`)})
	register(capturesave.Command{}, database.MediaObject{ID: ids[3], Status: "READY", RequirementKey: "overview", Flags: json.RawMessage(`[]`)})
	register(capturedeclare.Command{}, true)
	register(capturesubmit.Command{}, database.SubmissionVersion{ID: ids[4], Complete: true, SubmittedAt: time.Now()})
	register(recapturerequest.Command{}, recapturecore.Result{RequestID: ids[5], ResponsibilityID: ids[6], Status: "REQUESTED"})
	register(recapturesubmit.Command{}, recapturecore.Result{RequestID: ids[5], ResponsibilityID: responsibilityID, Status: "SUBMITTED"})
	register(mediafalsepositive.Command{}, database.MediaObject{ID: ids[3], Status: "READY", Flags: json.RawMessage(`["SENSITIVE_CONTENT_FALSE_POSITIVE"]`)})
	if err := bus.RegisterQuery(assetget.Query{}, func(context.Context, any) (any, error) { return assetcore.View{}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(originlist.Query{}, func(context.Context, any) (any, error) {
		return originlist.Result{Versions: []database.OriginVersion{{ID: ids[1], OriginID: ids[2], VersionNumber: 1, Status: "ACTIVE"}}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(capturebootstrap.Query{}, func(context.Context, any) (any, error) {
		return capturecore.Bootstrap{Draft: database.CaptureDraft{Kind: "INSPECTION", Status: "OPEN", PolicyPayload: json.RawMessage(`{}`)}}, nil
	}); err != nil {
		t.Fatal(err)
	}

	invitationService, credentials := captureSessionFixture(t, tenantID, responsibilityID)
	server := captureGraphQLServer(bus, invitationService)
	validID, mediaID, versionID, inspectionID, requestID := ids[7].String(), ids[3].String(), ids[1].String(), identity.NewID().String(), ids[5].String()
	cases := []struct {
		name, failureName, query, expected string
	}{
		{"IT-411 origin versions", "IT-412 origin versions safe failure", `query { originVersions(assetId:"` + validID + `") { nodes { status } pageInfo { hasNextPage } } }`, "ACTIVE"},
		{"IT-473 invite origin", "IT-474 invite origin safe failure", `mutation { inviteOriginCapture(input:{assetId:"` + validID + `",participantId:"` + validID + `",expiresAt:"2030-01-01T00:00:00Z",clientMutationId:"c"}) { status } }`, "SENT"},
		{"IT-475 activate origin", "IT-476 activate origin safe failure", `mutation { activateOriginVersion(input:{versionId:"` + versionID + `",clientMutationId:"c"}) { version { status } } }`, "ACTIVE"},
		{"IT-477 invalidate origin", "IT-478 invalidate origin safe failure", `mutation { invalidateOriginVersion(input:{versionId:"` + versionID + `",clientMutationId:"c"}) { version { status } } }`, "INVALIDATED"},
		{"accept processing", "", `mutation { acceptProcessing(input:{disclosureVersion:"v1",photoProcessing:true,aiAnalysis:true,gpsUse:true,clientMutationId:"c"}) { status } }`, "ACCEPTED"},
		{"IT-509 create media", "IT-510 create media safe failure", `mutation { createMediaUpload(input:{contentType:"image/jpeg",sizeBytes:3,sha256:"` + strings.Repeat("a", 64) + `",clientMutationId:"c"}) { upload { uploadId } } }`, "upload"},
		{"IT-511 presign media", "IT-512 presign media safe failure", `mutation { presignMediaParts(input:{mediaId:"` + mediaID + `",partNumbers:[1],clientMutationId:"c"}) { parts { partNumber } } }`, "partNumber"},
		{"IT-513 complete media", "IT-514 complete media safe failure", `mutation { completeMediaUpload(input:{mediaId:"` + mediaID + `",parts:[{partNumber:1,etag:"e"}],clientMutationId:"c"}) { media { status } } }`, "VERIFIED"},
		{"IT-515 save metadata", "IT-516 save metadata safe failure", `mutation { saveCaptureMetadata(input:{mediaId:"` + mediaID + `",requirementKey:"overview",description:"safe",captureSource:"CAMERA",clientMutationId:"c"}) { media { status } } }`, "READY"},
		{"IT-517 impossibility", "IT-518 impossibility safe failure", `mutation { declareCaptureImpossibility(input:{requirementKey:"overview",reason:"blocked",clientMutationId:"c"}) { status } }`, "SAVED"},
		{"IT-519 submit capture", "IT-520 submit capture safe failure", `mutation { submitCapture(input:{confirmIncomplete:false,clientMutationId:"c"}) { submission { complete } } }`, "true"},
		{"IT-521 request recapture", "IT-522 request recapture safe failure", `mutation { requestRecapture(input:{inspectionId:"` + inspectionID + `",items:[{requirementKey:"overview",reason:"blurred"}],deadlineAt:"2030-01-01T00:00:00Z",clientMutationId:"c"}) { recapture { status } } }`, "REQUESTED"},
		{"IT-523 submit recapture", "IT-524 submit recapture safe failure", `mutation { submitRecapture(input:{requestId:"` + requestID + `",confirmIncomplete:false,clientMutationId:"c"}) { recapture { status } } }`, "SUBMITTED"},
		{"IT-525 false positive", "IT-526 false positive safe failure", `mutation { declareSensitiveDetectionFalsePositive(input:{mediaId:"` + mediaID + `",reason:"verified by participant",clientMutationId:"c"}) { media { flags } } }`, "SENSITIVE_CONTENT_FALSE_POSITIVE"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := executeCaptureGraphQL(t, server, testCase.query, tenantID, credentials, true)
			if strings.Contains(response, `"errors"`) || !strings.Contains(response, testCase.expected) {
				t.Fatalf("unexpected response: %s", response)
			}
		})
	}

	for _, testCase := range cases {
		if testCase.failureName == "" {
			continue
		}
		t.Run(testCase.failureName, func(t *testing.T) {
			response := executeCaptureGraphQL(t, server, testCase.query, tenantID, credentials, false)
			if !strings.Contains(response, "UNAUTHENTICATED") || strings.Contains(response, "https://signed.invalid") {
				t.Fatalf("unsafe failure response: %s", response)
			}
		})
	}
}

func captureSessionFixture(t *testing.T, tenantID, responsibilityID identity.ID) (invitationcore.Service, requestctx.ExternalCredentials) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS invitations").Error; err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&database.Invitation{}, &database.ExternalSession{}, &database.ProcessingAcceptance{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	pepper := bytes.Repeat([]byte{7}, 32)
	token, err := security.NewScopedToken(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf := "csrf-proof"
	hash := security.HashToken(token)
	proof, err := security.CSRFProof(hash, csrf, pepper)
	if err != nil {
		t.Fatal(err)
	}
	row := database.ExternalSession{ID: identity.NewID(), TenantID: tenantID, InvitationID: identity.NewID(), ResponsibilityID: responsibilityID, SessionDigest: hash[:], CSRFDigest: proof[:], ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	invitation := database.Invitation{ID: row.InvitationID, TenantID: tenantID, ResponsibilityID: responsibilityID, TokenHash: hash[:], DeliveryIntents: json.RawMessage(`[]`), Status: "ACTIVE", ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt}
	if err := db.Create(&invitation).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	acceptance := database.ProcessingAcceptance{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, DisclosureVersion: invitationcore.PrivacyDisclosureVersion, PhotoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: time.Now()}
	if err := db.Create(&acceptance).Error; err != nil {
		t.Fatal(err)
	}
	within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return fn(db.WithContext(ctx))
	}
	return invitationcore.Service{DB: db, Pepper: pepper, Within: within}, requestctx.ExternalCredentials{SessionToken: token, CSRFToken: csrf}
}

func captureGraphQLServer(bus *mediator.Bus, invitations invitationcore.Service) *handler.Server {
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{Bus: bus, Invitations: invitations}}))
	server.SetErrorPresenter(graph.PresentError)
	return server
}

func executeCaptureGraphQL(t *testing.T, server http.Handler, query string, tenantID identity.ID, credentials requestctx.ExternalCredentials, authenticated bool) string {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+quote(query)+`}`))
	req.Header.Set("Content-Type", "application/json")
	if authenticated {
		ctx := requestctx.WithMetadata(req.Context(), requestctx.Metadata{TenantID: tenantID, CorrelationID: "task-5", Principal: requestctx.Principal{Issuer: "test", Subject: "operator"}})
		ctx = requestctx.WithExternalCredentials(ctx, credentials)
		req = req.WithContext(ctx)
	}
	server.ServeHTTP(w, req)
	return w.Body.String()
}
