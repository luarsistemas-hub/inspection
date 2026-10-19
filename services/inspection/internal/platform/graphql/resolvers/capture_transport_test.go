package resolvers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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
	mediavideopolicy "inspection/services/inspection/internal/features/media/configure_video_policy"
	mediacore "inspection/services/inspection/internal/features/media/core"
	mediacreate "inspection/services/inspection/internal/features/media/create_upload"
	mediafalsepositive "inspection/services/inspection/internal/features/media/declare_false_positive"
	mediavideoprep "inspection/services/inspection/internal/features/media/manage_video_preparation"
	mediapresign "inspection/services/inspection/internal/features/media/presign_parts"
	originactivate "inspection/services/inspection/internal/features/origins/activate_version"
	origincore "inspection/services/inspection/internal/features/origins/core"
	origininvalidate "inspection/services/inspection/internal/features/origins/invalidate_version"
	origininvite "inspection/services/inspection/internal/features/origins/invite_capture"
	originlist "inspection/services/inspection/internal/features/origins/list_versions"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	recapturerequest "inspection/services/inspection/internal/features/recapture/request"
	recapturesubmit "inspection/services/inspection/internal/features/recapture/submit"
	"inspection/services/inspection/internal/platform/apperror"
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
	if err := bus.RegisterQuery(originlist.VersionDetailsQuery{}, func(context.Context, any) (any, error) {
		return originlist.Result{Details: map[identity.ID]originlist.Details{}}, nil
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

func TestSensitiveFalsePositiveGraphQLTransportIT468ToIT476(t *testing.T) {
	tenantID, responsibilityID := identity.NewID(), identity.NewID()
	invitations, credentials := captureSessionFixture(t, tenantID, responsibilityID)
	mediaID := identity.NewID()
	bus := mediator.New()
	calls := 0
	if err := bus.RegisterCommand(mediafalsepositive.Command{}, func(_ context.Context, raw any) (any, error) {
		calls++
		command := raw.(mediafalsepositive.Command)
		if command.TenantID != tenantID || command.ResponsibilityID != responsibilityID {
			return nil, apperror.New(apperror.NotFound, "mediaId", "media not found")
		}
		if command.MediaID != mediaID {
			return nil, apperror.New(apperror.NotFound, "mediaId", "media not found")
		}
		if strings.TrimSpace(command.Reason) == "" {
			return nil, apperror.New(apperror.InvalidInput, "reason", "false-positive reason is required")
		}
		if command.Reason == "not blocked" {
			return nil, apperror.New(apperror.InvalidState, "mediaId", "media is not blocked by sensitive-content detection")
		}
		if command.Reason == "source absent" {
			return nil, apperror.New(apperror.InvalidState, "mediaId", "video source is unavailable")
		}
		if command.Reason == "adapter unavailable" {
			return nil, apperror.New(apperror.DependencyUnavailable, "media", "store unavailable")
		}
		if command.Reason == "unexpected failure" {
			return nil, errors.New("database password=qa-secret-123")
		}
		return database.MediaObject{ID: mediaID, Status: "SCREENED", Flags: json.RawMessage(`["SENSITIVE_CONTENT_FALSE_POSITIVE"]`)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	server := captureGraphQLServer(bus, invitations)
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	query := `mutation { declareSensitiveDetectionFalsePositive(input:{mediaId:"` + mediaID.String() + `",reason:"QA confirmed wall poster",clientMutationId:"k1"}) { media { id status flags } userErrors { code } } }`
	response := executeCaptureGraphQL(t, server, query, tenantID, credentials, true)
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, "QA") && !strings.Contains(response, "SCREENED") || calls != 1 {
		t.Fatalf("false-positive release response/call count invalid: calls=%d response=%s", calls, response)
	}
	if duplicate := executeCaptureGraphQL(t, server, query, tenantID, credentials, true); !strings.Contains(duplicate, "SCREENED") || calls != 2 {
		t.Fatalf("replayed declaration response/call count invalid: calls=%d response=%s", calls, duplicate)
	}
	unauthenticated := executeCaptureGraphQL(t, server, query, tenantID, credentials, false)
	if !strings.Contains(unauthenticated, "UNAUTHENTICATED") || !strings.Contains(unauthenticated, `"data":null`) {
		t.Fatalf("unauthenticated request returned mutation data: %s", unauthenticated)
	}
	internalRequest := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+quote(query)+`}`))
	internalRequest.Header.Set("Content-Type", "application/json")
	internalCtx := requestctx.WithMetadata(internalRequest.Context(), requestctx.Metadata{TenantID: tenantID, CorrelationID: "it-470", Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Internal: true}})
	internalRequest = internalRequest.WithContext(internalCtx)
	internalResponse := httptest.NewRecorder()
	server.ServeHTTP(internalResponse, internalRequest)
	if !strings.Contains(internalResponse.Body.String(), "FORBIDDEN") || !strings.Contains(internalResponse.Body.String(), `"data":null`) || calls != 2 {
		t.Fatalf("internal actor without management capability was not rejected before mutation: calls=%d response=%s", calls, internalResponse.Body.String())
	}
	invalidProof := credentials
	invalidProof.CSRFToken = "invalid-proof"
	forbidden := executeCaptureGraphQL(t, server, query, tenantID, invalidProof, true)
	if !strings.Contains(forbidden, "FORBIDDEN") || !strings.Contains(forbidden, `"data":null`) {
		t.Fatalf("authenticated request without valid capture proof was not forbidden: %s", forbidden)
	}
	for _, tc := range []struct{ reason, code string }{
		{"", "INVALID_INPUT"}, {"not blocked", "INVALID_STATE"}, {"source absent", "INVALID_STATE"}, {"adapter unavailable", "DEPENDENCY_UNAVAILABLE"}, {"unexpected failure", "INTERNAL"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			mutation := `mutation { declareSensitiveDetectionFalsePositive(input:{mediaId:"` + mediaID.String() + `",reason:"` + tc.reason + `",clientMutationId:"k2"}) { media { id } } }`
			result := executeCaptureGraphQL(t, server, mutation, tenantID, credentials, true)
			if !strings.Contains(result, tc.code) || !strings.Contains(result, `"data":null`) {
				t.Fatalf("expected %s with null result: %s", tc.code, result)
			}
			if tc.code == "INTERNAL" && strings.Contains(result, "qa-secret-123") {
				t.Fatalf("internal database error leaked: %s", result)
			}
		})
	}
	foreign := `mutation { declareSensitiveDetectionFalsePositive(input:{mediaId:"` + identity.NewID().String() + `",reason:"QA confirmed wall poster",clientMutationId:"k3"}) { media { id } } }`
	if result := executeCaptureGraphQL(t, server, foreign, tenantID, credentials, true); !strings.Contains(result, "NOT_FOUND") || !strings.Contains(result, `"data":null`) {
		t.Fatalf("foreign media was not hidden: %s", result)
	}
	if strings.Contains(logs.String(), "qa-secret-123") || !strings.Contains(logs.String(), "INTERNAL") || !strings.Contains(logs.String(), "task-5") {
		t.Fatalf("internal error log exposed details or omitted safe fields: %s", logs.String())
	}
}

func TestSensitiveFalsePositiveGraphQLAuditsReleaseOnceIT468(t *testing.T) {
	tenantID, responsibilityID := identity.NewID(), identity.NewID()
	invitations, credentials := captureSessionFixture(t, tenantID, responsibilityID)
	db := invitations.DB
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, schema := range []string{"capture", "media", "audit"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.CaptureDraft{}, &database.MediaObject{}, &database.VideoSource{}, &database.ScreeningRun{}, &database.AuditEvent{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	mediaID := identity.NewID()
	now := time.Now().UTC()
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: identity.NewID(), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "OPEN", Version: 1, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.MediaObject{ID: mediaID, TenantID: tenantID, ResponsibilityID: responsibilityID, ObjectKey: "qa/false-positive/" + mediaID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("a", 64), SizeBytes: 123, Status: "SCREENED", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "false-positive-" + mediaID.String(), AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.VideoSource{ID: identity.NewID(), TenantID: tenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 96, Height: 64, FrameRate: 5, ScreeningStatus: "BLOCKED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.ScreeningRun{ID: identity.NewID(), TenantID: tenantID, MediaID: mediaID, Status: "BLOCKED", ModelDigest: "screening-v1", Regions: []byte(`[]`), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	foreignTenant, foreignResponsibility, foreignMediaID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Create(&database.MediaObject{ID: foreignMediaID, TenantID: foreignTenant, ResponsibilityID: foreignResponsibility, ObjectKey: "qa/foreign/" + foreignMediaID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("b", 64), SizeBytes: 456, Status: "SCREENED", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "foreign-false-positive-" + foreignMediaID.String(), AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	missingSourceID := identity.NewID()
	if err := db.Create(&database.MediaObject{ID: missingSourceID, TenantID: tenantID, ResponsibilityID: responsibilityID, ObjectKey: "qa/missing-source/" + missingSourceID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("c", 64), SizeBytes: 789, Status: "SCREENED", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "missing-source-" + missingSourceID.String(), AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.ScreeningRun{ID: identity.NewID(), TenantID: tenantID, MediaID: missingSourceID, Status: "BLOCKED", ModelDigest: "screening-v1", Regions: []byte(`[]`), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	noRunMediaID := identity.NewID()
	if err := db.Create(&database.MediaObject{ID: noRunMediaID, TenantID: tenantID, ResponsibilityID: responsibilityID, ObjectKey: "qa/no-screening/" + noRunMediaID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("d", 64), SizeBytes: 321, Status: "SCREENED", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "no-screening-" + noRunMediaID.String(), AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	service := mediacore.Service{DB: db, Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return fn(db.WithContext(ctx))
	}}
	within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return fn(db.WithContext(ctx))
	}
	if err := mediafalsepositive.Setup(mediafalsepositive.Dependencies{DB: db, Bus: bus, Service: service, Within: within}); err != nil {
		t.Fatal(err)
	}
	server := captureGraphQLServer(bus, invitations)
	query := `mutation { declareSensitiveDetectionFalsePositive(input:{mediaId:"` + mediaID.String() + `",reason:"QA confirmed wall poster",clientMutationId:"k1"}) { media { id status flags } } }`
	foreignQuery := `mutation { declareSensitiveDetectionFalsePositive(input:{mediaId:"` + foreignMediaID.String() + `",reason:"QA confirmed wall poster",clientMutationId:"foreign"}) { media { id status flags } } }`
	if response := executeCaptureGraphQL(t, server, foreignQuery, tenantID, credentials, true); !strings.Contains(response, "NOT_FOUND") || !strings.Contains(response, `"data":null`) || strings.Contains(response, foreignMediaID.String()) {
		t.Fatalf("cross-tenant media was not hidden: %s", response)
	}
	missingSourceQuery := `mutation { declareSensitiveDetectionFalsePositive(input:{mediaId:"` + missingSourceID.String() + `",reason:"QA confirmed wall poster",clientMutationId:"missing-source"}) { media { id } } }`
	if response := executeCaptureGraphQL(t, server, missingSourceQuery, tenantID, credentials, true); !strings.Contains(response, "INVALID_STATE") || !strings.Contains(response, `"data":null`) {
		t.Fatalf("missing source was not rejected: %s", response)
	}
	noRunQuery := `mutation { declareSensitiveDetectionFalsePositive(input:{mediaId:"` + noRunMediaID.String() + `",reason:"QA confirmed wall poster",clientMutationId:"no-run"}) { media { id } } }`
	if response := executeCaptureGraphQL(t, server, noRunQuery, tenantID, credentials, true); !strings.Contains(response, "INVALID_STATE") || !strings.Contains(response, `"data":null`) {
		t.Fatalf("media without reviewable detection was not rejected: %s", response)
	}
	emptyReasonQuery := `mutation { declareSensitiveDetectionFalsePositive(input:{mediaId:"` + mediaID.String() + `",reason:"",clientMutationId:"empty-reason"}) { media { id } } }`
	if response := executeCaptureGraphQL(t, server, emptyReasonQuery, tenantID, credentials, true); !strings.Contains(response, "INVALID_INPUT") || !strings.Contains(response, `"data":null`) {
		t.Fatalf("empty reason was not rejected: %s", response)
	}
	if err := service.DeclareFalsePositive(context.Background(), tenantID, responsibilityID, mediaID, "QA confirmed wall poster"); err != nil {
		t.Fatalf("domain declaration fixture failed before GraphQL replay: %v", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		response := executeCaptureGraphQL(t, server, query, tenantID, credentials, true)
		if strings.Contains(response, `"errors"`) || !strings.Contains(response, "SCREENED") || !strings.Contains(response, "SENSITIVE_CONTENT_FALSE_POSITIVE") {
			t.Fatalf("attempt %d did not return audited release outcome: %s", attempt+1, response)
		}
	}
	var run database.ScreeningRun
	if err := db.Where("tenant_id=? AND media_id=?", tenantID, mediaID).Take(&run).Error; err != nil || run.Status != "OVERRIDDEN" || run.FalsePositiveReason != "QA confirmed wall poster" {
		t.Fatalf("screening override missing: run=%+v err=%v", run, err)
	}
	var source database.VideoSource
	if err := db.Where("tenant_id=? AND media_id=?", tenantID, mediaID).Take(&source).Error; err != nil || source.ScreeningStatus != "APPROVED" {
		t.Fatalf("source release missing: source=%+v err=%v", source, err)
	}
	var audits int64
	if err := db.Model(&database.AuditEvent{}).Where("tenant_id=? AND target_id=? AND action='media.sensitive_false_positive_declared'", tenantID, mediaID.String()).Count(&audits).Error; err != nil || audits != 1 {
		t.Fatalf("declaration audit count=%d err=%v, want exactly one", audits, err)
	}
}

func TestSensitiveFalsePositiveUnavailableAdapterIT472(t *testing.T) {
	tenantID, responsibilityID, mediaID := identity.NewID(), identity.NewID(), identity.NewID()
	invitations, credentials := captureSessionFixture(t, tenantID, responsibilityID)
	dbFailure := errors.New("database unavailable password=qa-secret-123")
	within := func(context.Context, identity.ID, func(*gorm.DB) error) error { return dbFailure }
	bus := mediator.New()
	service := mediacore.Service{DB: invitations.DB, Within: within}
	if err := mediafalsepositive.Setup(mediafalsepositive.Dependencies{DB: invitations.DB, Bus: bus, Service: service, Within: within}); err != nil {
		t.Fatal(err)
	}
	query := `mutation { declareSensitiveDetectionFalsePositive(input:{mediaId:"` + mediaID.String() + `",reason:"QA confirmed wall poster",clientMutationId:"adapter-down"}) { media { id } } }`
	response := executeCaptureGraphQL(t, captureGraphQLServer(bus, invitations), query, tenantID, credentials, true)
	if !strings.Contains(response, "DEPENDENCY_UNAVAILABLE") || !strings.Contains(response, `"data":null`) || strings.Contains(response, "qa-secret-123") {
		t.Fatalf("unavailable media adapter returned unsafe response: %s", response)
	}
}

func TestExternalCaptureShowsEffectiveVideoLimitBeforeConsent(t *testing.T) {
	tenantID, responsibilityID := identity.NewID(), identity.NewID()
	invitations, credentials := captureSessionFixture(t, tenantID, responsibilityID)
	if err := invitations.DB.Exec(`ATTACH DATABASE ':memory:' AS capture`).Error; err != nil {
		t.Fatal(err)
	}
	if err := invitations.DB.Exec(`CREATE TABLE capture.capture_drafts (id blob primary key,tenant_id blob,responsibility_id blob,media_kind text)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := invitations.DB.Exec(`INSERT INTO capture.capture_drafts (id,tenant_id,responsibility_id,media_kind) VALUES (?,?,?,?)`, identity.NewID(), tenantID, responsibilityID, "VIDEO").Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := bus.RegisterQuery(capturebootstrap.Query{}, func(context.Context, any) (any, error) {
		return capturecore.Bootstrap{Draft: database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: identity.NewID(), Status: "OPEN", ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{"allowGallery":false}`)}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(mediavideopolicy.CaptureQuery{}, func(context.Context, any) (any, error) {
		return database.VideoPolicy{TenantID: tenantID, MaxDurationMinutes: 10, Version: 7}, nil
	}); err != nil {
		t.Fatal(err)
	}
	response := executeCaptureGraphQL(t, captureGraphQLServer(bus, invitations), `query { externalCapture { status mediaKind policy } }`, tenantID, credentials, true)
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"maxDurationSeconds":600`) || !strings.Contains(response, `"ACCEPTANCE_REQUIRED"`) {
		t.Fatalf("effective video policy absent before consent: %s", response)
	}
}

func TestVideoPreparationRemainsReadableAfterSubmission(t *testing.T) {
	tenantID, responsibilityID, mediaID := identity.NewID(), identity.NewID(), identity.NewID()
	invitations, credentials := captureSessionFixture(t, tenantID, responsibilityID)
	db := invitations.DB
	if err := db.Exec(`ATTACH DATABASE ':memory:' AS media`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE media.media_objects (id blob primary key,tenant_id blob,responsibility_id blob,media_kind text)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO media.media_objects (id,tenant_id,responsibility_id,media_kind) VALUES (?,?,?,?)`, mediaID, tenantID, responsibilityID, "VIDEO").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.Invitation{}).Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).Update("status", "COMPLETED").Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.Model(&database.ExternalSession{}).Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).Update("revoked_at", now).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := bus.RegisterQuery(mediavideoprep.CaptureQuery{}, func(context.Context, any) (any, error) {
		return mediavideoprep.Status{MediaID: mediaID, PreparationID: identity.NewID(), Stage: "PROCESSING"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: db, Bus: bus, Invitations: invitations}}))
	server.SetErrorPresenter(graph.PresentError)
	response := executeCaptureGraphQL(t, server, `query { videoPreparation(mediaId:"`+mediaID.String()+`") { stage } }`, tenantID, credentials, true)
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"stage":"PROCESSING"`) {
		t.Fatalf("submitted capture cannot read preparation: %s", response)
	}
	response = executeCaptureGraphQL(t, server, `query { videoPreparation(mediaId:"`+identity.NewID().String()+`") { stage } }`, tenantID, credentials, true)
	if !strings.Contains(response, "NOT_FOUND") {
		t.Fatalf("foreign preparation was exposed: %s", response)
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
	if err := db.Exec("ATTACH DATABASE ':memory:' AS dashboard").Error; err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&database.Invitation{}, &database.ExternalSession{}, &database.ProcessingAcceptance{}, &database.TriageCase{}, &database.TriageCaseEvent{}} {
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
	return captureGraphQLServerWithDB(bus, invitations, nil)
}

// captureGraphQLServerWithDB also gives resolvers the runtime database used by
// video submissions to report a pending preparation.
func captureGraphQLServerWithDB(bus *mediator.Bus, invitations invitationcore.Service, db *gorm.DB) *handler.Server {
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{Bus: bus, Invitations: invitations, DB: db}}))
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
