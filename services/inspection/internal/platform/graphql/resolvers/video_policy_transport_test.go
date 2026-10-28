package resolvers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	beginvideocapture "inspection/services/inspection/internal/features/capture/begin_video_capture"
	mediacomplete "inspection/services/inspection/internal/features/media/complete_upload"
	mediavideopolicy "inspection/services/inspection/internal/features/media/configure_video_policy"
	mediacore "inspection/services/inspection/internal/features/media/core"
	mediacreate "inspection/services/inspection/internal/features/media/create_upload"
	mediapresign "inspection/services/inspection/internal/features/media/presign_parts"
	mediareconcile "inspection/services/inspection/internal/features/media/reconcile_upload"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestVideoPolicyGraphQLTransport(t *testing.T) {
	tenantID := [16]byte{1}
	policyVersion := int64(1)
	tenantEnabled := true
	bus := mediator.New()
	if err := bus.RegisterQuery(mediavideopolicy.Query{}, func(_ context.Context, raw any) (any, error) {
		query := raw.(mediavideopolicy.Query)
		if query.TenantID != tenantID {
			return nil, apperror.New(apperror.NotFound, "tenant", "tenant not found")
		}
		return mediavideopolicy.Defaults(query.TenantID), nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterCommand(mediavideopolicy.Command{}, func(_ context.Context, raw any) (any, error) {
		command := raw.(mediavideopolicy.Command)
		if err := mediavideopolicy.Validate(command.MaxDurationMinutes, command.OriginRetentionDays, command.CurrentRetentionDays); err != nil {
			return nil, err
		}
		supersededDays := 365
		if command.SupersededBaselineRetentionDays != nil {
			supersededDays = *command.SupersededBaselineRetentionDays
			if err := mediavideopolicy.ValidateSupersededBaselineRetention(supersededDays); err != nil {
				return nil, err
			}
		}
		if !tenantEnabled {
			return nil, apperror.New(apperror.InvalidState, "tenant", "inactive tenant cannot change video policy")
		}
		if command.ExpectedVersion != policyVersion {
			return nil, apperror.New(apperror.Conflict, "expectedVersion", "video policy has changed")
		}
		if command.MaxDurationMinutes > mediavideopolicy.MaxDurationMinutes {
			return nil, apperror.New(apperror.InvalidInput, "maxDurationMinutes", "duration must be between 1 and 60 minutes")
		}
		policyVersion++
		return database.VideoPolicy{TenantID: command.TenantID, MaxDurationMinutes: command.MaxDurationMinutes, OriginRetentionDays: command.OriginRetentionDays, CurrentRetentionDays: command.CurrentRetentionDays, SupersededBaselineRetentionDays: supersededDays, Version: policyVersion}, nil
	}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{Bus: bus}}))
	server.SetErrorPresenter(graph.PresentError)

	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, CorrelationID: "video-policy-test"})
	response := executeVideoPolicyGraphQL(t, server, `query { videoPolicy { maxDurationMinutes originRetentionDays currentRetentionDays supersededBaselineRetentionDays version } }`, ctx)
	for _, expected := range []string{`"maxDurationMinutes":15`, `"originRetentionDays":30`, `"currentRetentionDays":30`, `"supersededBaselineRetentionDays":365`, `"version":1`} {
		if !strings.Contains(response, expected) {
			t.Fatalf("default policy response missing %s: %s", expected, response)
		}
	}

	response = executeVideoPolicyGraphQL(t, server, `mutation { configureVideoPolicy(input:{maxDurationMinutes:10,originRetentionDays:40,currentRetentionDays:7,supersededBaselineRetentionDays:730,expectedVersion:1,clientMutationId:"k1"}) { policy { maxDurationMinutes originRetentionDays currentRetentionDays supersededBaselineRetentionDays version } clientMutationId userErrors { field message } } }`, ctx)
	for _, expected := range []string{`"maxDurationMinutes":10`, `"originRetentionDays":40`, `"currentRetentionDays":7`, `"supersededBaselineRetentionDays":730`, `"version":2`, `"clientMutationId":"k1"`} {
		if !strings.Contains(response, expected) {
			t.Fatalf("configured policy response missing %s: %s", expected, response)
		}
	}

	unauthenticated := executeVideoPolicyGraphQL(t, server, `query { videoPolicy { version } }`, context.Background())
	if !strings.Contains(unauthenticated, "UNAUTHENTICATED") || strings.Contains(unauthenticated, `"videoPolicy"`) {
		t.Fatalf("unauthenticated query was not rejected: %s", unauthenticated)
	}

	invalid := executeVideoPolicyGraphQL(t, server, `mutation { configureVideoPolicy(input:{maxDurationMinutes:61,originRetentionDays:40,currentRetentionDays:7,expectedVersion:1,clientMutationId:"bad"}) { policy { version } } }`, ctx)
	if !strings.Contains(invalid, "INVALID_INPUT") || strings.Contains(invalid, `"policy"`) {
		t.Fatalf("invalid policy returned success-shaped data: %s", invalid)
	}

	for _, tc := range []struct {
		name, input, code string
	}{
		{"stale version", `maxDurationMinutes:10,originRetentionDays:40,currentRetentionDays:7,expectedVersion:1,clientMutationId:"stale"`, "CONFLICT"},
		{"unsupported retention", `maxDurationMinutes:10,originRetentionDays:36501,currentRetentionDays:7,expectedVersion:2,clientMutationId:"retention"`, "INVALID_INPUT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := executeVideoPolicyGraphQL(t, server, `mutation { configureVideoPolicy(input:{`+tc.input+`}) { policy { version } } }`, ctx)
			if !strings.Contains(response, tc.code) || strings.Contains(response, `"policy":{`) {
				t.Fatalf("rejected policy update returned success data: %s", response)
			}
		})
	}
	tenantEnabled = false
	disabled := executeVideoPolicyGraphQL(t, server, `mutation { configureVideoPolicy(input:{maxDurationMinutes:10,originRetentionDays:40,currentRetentionDays:7,expectedVersion:2,clientMutationId:"disabled"}) { policy { version } } }`, ctx)
	if !strings.Contains(disabled, "INVALID_STATE") || strings.Contains(disabled, `"policy":{`) {
		t.Fatalf("inactive tenant policy update returned success data: %s", disabled)
	}

	injectedTenant := executeVideoPolicyGraphQL(t, server, `query { videoPolicy(tenantId:"00000000-0000-0000-0000-000000000002") { version } }`, ctx)
	if !strings.Contains(injectedTenant, "Unknown argument") || !strings.Contains(injectedTenant, `"data":null`) || strings.Contains(injectedTenant, `"code":"INTERNAL"`) {
		t.Fatalf("tenant selector was not rejected by schema validation: %s", injectedTenant)
	}
}

func TestCreateMediaUploadGraphQLTransportReturnsInvalidInputAndNullResult(t *testing.T) {
	tenantID, responsibilityID := identity.NewID(), identity.NewID()
	invitations, credentials := captureSessionFixture(t, tenantID, responsibilityID)
	bus := mediator.New()
	if err := bus.RegisterCommand(mediacreate.Command{}, func(context.Context, any) (any, error) {
		return nil, apperror.New(apperror.InvalidInput, "contentType", "photo uploads are unavailable for video captures")
	}); err != nil {
		t.Fatal(err)
	}
	query := `mutation { createMediaUpload(input:{contentType:"image/jpeg",sizeBytes:12,sha256:"` + strings.Repeat("a", 64) + `",clientMutationId:"IT-012"}) { upload { mediaId uploadId } clientMutationId } }`
	response := executeCaptureGraphQL(t, captureGraphQLServer(bus, invitations), query, tenantID, credentials, true)
	if !strings.Contains(response, "INVALID_INPUT") || (!strings.Contains(response, `"createMediaUpload":null`) && !strings.Contains(response, `"data":null`)) {
		t.Fatalf("expected stable INVALID_INPUT error and null mutation result, got %s", response)
	}
}

func TestVideoPolicyGraphQLHidesUnexpectedAdapterErrors(t *testing.T) {
	bus := mediator.New()
	if err := bus.RegisterQuery(mediavideopolicy.Query{}, func(context.Context, any) (any, error) {
		return nil, fmt.Errorf("database unavailable: qa-secret-123")
	}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{Bus: bus}}))
	server.SetErrorPresenter(graph.PresentError)
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: [16]byte{1}, CorrelationID: "video-policy-test"})
	response := executeVideoPolicyGraphQL(t, server, `query { videoPolicy { version } }`, ctx)
	if !strings.Contains(response, "INTERNAL") || strings.Contains(response, "qa-secret-123") || strings.Contains(response, `"videoPolicy"`) {
		t.Fatalf("unexpected adapter error was exposed or returned data: %s", response)
	}
}

func TestVideoPolicyGraphQLMapsDependencyAndUnexpectedFailuresForBothOperations(t *testing.T) {
	for _, failure := range []struct {
		name string
		err  error
		code string
	}{
		{"unavailable", apperror.New(apperror.DependencyUnavailable, "policy", "policy database unavailable"), "DEPENDENCY_UNAVAILABLE"},
		{"unexpected", fmt.Errorf("database failed: qa-secret-123"), "INTERNAL"},
	} {
		t.Run(failure.name, func(t *testing.T) {
			bus := mediator.New()
			if err := bus.RegisterQuery(mediavideopolicy.Query{}, func(context.Context, any) (any, error) { return nil, failure.err }); err != nil {
				t.Fatal(err)
			}
			if err := bus.RegisterCommand(mediavideopolicy.Command{}, func(context.Context, any) (any, error) { return nil, failure.err }); err != nil {
				t.Fatal(err)
			}
			server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{Bus: bus}}))
			server.SetErrorPresenter(graph.PresentError)
			tenantID := identity.NewID()
			ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID}, CorrelationID: "video-policy-failure"})
			for _, operation := range []struct{ name, query, absent string }{
				{"query", `query { videoPolicy { version } }`, `"videoPolicy":{`},
				{"mutation", `mutation { configureVideoPolicy(input:{maxDurationMinutes:10,originRetentionDays:40,currentRetentionDays:7,expectedVersion:1,clientMutationId:"k1"}) { policy { version } } }`, `"policy":{`},
			} {
				response := executeVideoPolicyGraphQL(t, server, operation.query, ctx)
				if !strings.Contains(response, failure.code) || strings.Contains(response, operation.absent) {
					t.Fatalf("%s failure did not return a safe %s response: %s", operation.name, failure.code, response)
				}
				if failure.code == "INTERNAL" && strings.Contains(response, "qa-secret-123") {
					t.Fatalf("%s exposed internal adapter detail: %s", operation.name, response)
				}
			}
		})
	}
}

func TestVideoUploadGraphQLTransportIT410ToIT423(t *testing.T) {
	tenantID, responsibilityID, attemptID, expiredAttemptID, limitedAttemptID, internalErrorAttemptID, unavailableAttemptID, foreignAttemptID, mediaID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	foreignMediaID, internalErrorMediaID, unavailableMediaID := identity.NewID(), identity.NewID(), identity.NewID()
	invitations, credentials := captureSessionFixture(t, tenantID, responsibilityID)
	var acceptance database.ProcessingAcceptance
	if err := invitations.DB.Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).First(&acceptance).Error; err != nil {
		t.Fatal(err)
	}
	if err := invitations.DB.Model(&acceptance).Update("video_processing", true).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := bus.RegisterCommand(mediacreate.Command{}, func(_ context.Context, raw any) (any, error) {
		command := raw.(mediacreate.Command)
		if command.AttemptID != nil && *command.AttemptID == limitedAttemptID {
			return nil, apperror.New(apperror.RateLimited, "upload", "unfinished upload limit reached")
		}
		if command.AttemptID != nil && *command.AttemptID == unavailableAttemptID {
			return nil, apperror.New(apperror.DependencyUnavailable, "upload", "upload storage unavailable")
		}
		if command.AttemptID != nil && *command.AttemptID == internalErrorAttemptID {
			return nil, fmt.Errorf("storage adapter failed: qa-secret-123")
		}
		if command.SizeBytes > 1<<30 || command.SizeBytes < 1 || command.SHA256 == "" || len(command.SHA256) != 64 {
			return nil, apperror.New(apperror.InvalidInput, "upload", "invalid upload declaration")
		}
		if _, err := hex.DecodeString(command.SHA256); err != nil {
			return nil, apperror.New(apperror.InvalidInput, "sha256", "invalid SHA-256 digest")
		}
		if command.AttemptID == nil || *command.AttemptID != attemptID {
			if command.AttemptID != nil && *command.AttemptID == foreignAttemptID {
				return nil, apperror.New(apperror.NotFound, "attemptId", "video attempt not found")
			}
			if command.AttemptID != nil && *command.AttemptID == expiredAttemptID {
				return nil, apperror.New(apperror.InvalidState, "attemptId", "video attempt expired")
			}
			return nil, apperror.New(apperror.InvalidInput, "attemptId", "video attempt is required")
		}
		return mediacore.Upload{MediaID: mediaID, UploadID: "multipart-1", ExpiresAt: time.Now().Add(time.Hour)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterCommand(mediapresign.Command{}, func(_ context.Context, raw any) (any, error) {
		command := raw.(mediapresign.Command)
		if command.MediaID == foreignMediaID {
			return nil, apperror.New(apperror.NotFound, "mediaId", "media not found")
		}
		if command.MediaID == internalErrorMediaID {
			return nil, fmt.Errorf("storage adapter failed: qa-secret-123")
		}
		if command.MediaID == unavailableMediaID {
			return nil, apperror.New(apperror.DependencyUnavailable, "upload", "upload storage unavailable")
		}
		for _, number := range command.Parts {
			if number < 1 || number > 10000 {
				return nil, apperror.New(apperror.InvalidInput, "parts", "invalid multipart part number")
			}
		}
		parts := make([]objectstore.PresignedPart, 0, len(command.Parts))
		for _, number := range command.Parts {
			parts = append(parts, objectstore.PresignedPart{PartNumber: number, URL: "https://signed.invalid/" + fmt.Sprint(number), ExpiresAt: time.Now().Add(time.Minute)})
		}
		return parts, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterCommand(mediacomplete.Command{}, func(_ context.Context, raw any) (any, error) {
		command := raw.(mediacomplete.Command)
		if command.MediaID == foreignMediaID {
			return nil, apperror.New(apperror.NotFound, "mediaId", "media not found")
		}
		if command.MediaID == internalErrorMediaID {
			return nil, fmt.Errorf("storage adapter failed: qa-secret-123")
		}
		if command.MediaID == unavailableMediaID {
			return nil, apperror.New(apperror.DependencyUnavailable, "upload", "upload storage unavailable")
		}
		if len(command.Parts) != 2 {
			return nil, apperror.New(apperror.InvalidInput, "parts", "both multipart parts are required")
		}
		if command.Parts[0].ETag != "etag-1" || command.Parts[1].ETag != "etag-2" {
			return nil, apperror.New(apperror.Conflict, "parts", "completed upload payload changed")
		}
		return database.MediaObject{ID: mediaID, Status: "RECEIVED", MediaKind: "VIDEO", SizeBytes: 10 * 1024 * 1024, Flags: []byte(`[]`)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(mediareconcile.Query{}, func(_ context.Context, raw any) (any, error) {
		query := raw.(mediareconcile.Query)
		if query.MediaID == foreignMediaID {
			return nil, apperror.New(apperror.NotFound, "mediaId", "media not found")
		}
		if query.MediaID == internalErrorMediaID {
			return nil, fmt.Errorf("storage adapter failed: qa-secret-123")
		}
		if query.MediaID == unavailableMediaID {
			return nil, apperror.New(apperror.DependencyUnavailable, "upload", "upload storage unavailable")
		}
		if query.MediaID != mediaID {
			return nil, apperror.New(apperror.NotFound, "mediaId", "media not found")
		}
		return mediacore.Reconciliation{MediaID: mediaID, Status: "RECEIVED_PENDING_VALIDATION", ExpiresAt: time.Now().Add(time.Hour), Received: []objectstore.UploadedPart{{Number: 1, ETag: "etag-1", SizeBytes: 5 * 1024 * 1024}, {Number: 2, ETag: "etag-2", SizeBytes: 5 * 1024 * 1024}}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	server := captureGraphQLServer(bus, invitations)
	attemptText, mediaText := attemptID.String(), mediaID.String()
	expiredAttemptText := expiredAttemptID.String()
	validID := `"` + strings.Repeat("a", 64) + `"`
	queries := []struct{ name, query, expected string }{
		{"create", `mutation { createMediaUpload(input:{contentType:"video/mp4",sizeBytes:10485760,sha256:` + validID + `,attemptId:"` + attemptText + `",clientMutationId:"u1"}) { upload { mediaId uploadId partSizeBytes } clientMutationId } }`, "multipart-1"},
		{"presign", `mutation { presignMediaParts(input:{mediaId:"` + mediaText + `",partNumbers:[1,2],clientMutationId:"u2"}) { parts { partNumber url } } }`, "https://signed.invalid/2"},
		{"complete", `mutation { completeMediaUpload(input:{mediaId:"` + mediaText + `",parts:[{partNumber:1,etag:"etag-1"},{partNumber:2,etag:"etag-2"}],clientMutationId:"u3"}) { media { status mediaKind preparationStatus } } }`, "PENDING_VALIDATION"},
		{"status", `query { videoUploadStatus(mediaId:"` + mediaText + `") { status receivedParts confirmedParts { partNumber etag sizeBytes } missingParts } }`, `"status":"RECEIVED_PENDING_VALIDATION","receivedParts":[1,2],"confirmedParts":[{"partNumber":1,"etag":"etag-1","sizeBytes":5242880},{"partNumber":2,"etag":"etag-2","sizeBytes":5242880}],"missingParts":[]`},
	}
	for _, tc := range queries {
		t.Run(tc.name, func(t *testing.T) {
			response := executeCaptureGraphQL(t, server, tc.query, tenantID, credentials, true)
			if strings.Contains(response, `"errors"`) || !strings.Contains(response, tc.expected) {
				t.Fatalf("unexpected video upload response: %s", response)
			}
		})
	}
	unauthenticated := executeCaptureGraphQL(t, server, queries[0].query, tenantID, requestctx.ExternalCredentials{}, false)
	if !strings.Contains(unauthenticated, "UNAUTHENTICATED") || strings.Contains(unauthenticated, "multipart-1") {
		t.Fatalf("unauthenticated video upload was accepted: %s", unauthenticated)
	}
	for _, query := range queries[1:] {
		response := executeCaptureGraphQL(t, server, query.query, tenantID, requestctx.ExternalCredentials{}, false)
		if !strings.Contains(response, "UNAUTHENTICATED") || strings.Contains(response, "signed.invalid") || strings.Contains(response, "RECEIVED_PENDING_VALIDATION") {
			t.Fatalf("unauthenticated %s operation was accepted: %s", query.name, response)
		}
	}

	for _, tc := range []struct{ name, query, code string }{
		{"oversize", `mutation { createMediaUpload(input:{contentType:"video/mp4",sizeBytes:1073741825,sha256:` + validID + `,attemptId:"` + attemptText + `",clientMutationId:"oversize"}) { upload { mediaId } } }`, "INVALID_INPUT"},
		{"malformed digest", `mutation { createMediaUpload(input:{contentType:"video/mp4",sizeBytes:10485760,sha256:"not-a-digest",attemptId:"` + attemptText + `",clientMutationId:"digest"}) { upload { mediaId } } }`, "INVALID_INPUT"},
		{"expired attempt", `mutation { createMediaUpload(input:{contentType:"video/mp4",sizeBytes:10485760,sha256:` + validID + `,attemptId:"` + expiredAttemptText + `",clientMutationId:"expired"}) { upload { mediaId } } }`, "INVALID_STATE"},
		{"rate limited", `mutation { createMediaUpload(input:{contentType:"video/mp4",sizeBytes:10485760,sha256:` + validID + `,attemptId:"` + limitedAttemptID.String() + `",clientMutationId:"limited"}) { upload { mediaId } } }`, "RATE_LIMITED"},
		{"part zero", `mutation { presignMediaParts(input:{mediaId:"` + mediaText + `",partNumbers:[0],clientMutationId:"part-zero"}) { parts { partNumber } } }`, "INVALID_INPUT"},
		{"replay payload changed", `mutation { completeMediaUpload(input:{mediaId:"` + mediaText + `",parts:[{partNumber:1,etag:"changed"},{partNumber:2,etag:"etag-2"}],clientMutationId:"changed"}) { media { status } } }`, "CONFLICT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := executeCaptureGraphQL(t, server, tc.query, tenantID, credentials, true)
			if !strings.Contains(response, tc.code) || strings.Contains(response, `"upload":{`) || strings.Contains(response, `"parts":[`) || strings.Contains(response, `"media":{`) {
				t.Fatalf("rejected upload operation returned success data: %s", response)
			}
		})
	}

	for _, mediaID := range []identity.ID{foreignMediaID, internalErrorMediaID} {
		query := `query { videoUploadStatus(mediaId:"` + mediaID.String() + `") { status } }`
		response := executeCaptureGraphQL(t, server, query, tenantID, credentials, true)
		if mediaID == foreignMediaID && (!strings.Contains(response, "NOT_FOUND") || strings.Contains(response, mediaID.String())) {
			t.Fatalf("foreign upload metadata was exposed: %s", response)
		}
		if mediaID == internalErrorMediaID && (!strings.Contains(response, "INTERNAL") || strings.Contains(response, "qa-secret-123")) {
			t.Fatalf("unexpected adapter error was exposed: %s", response)
		}
	}
	for _, tc := range []struct{ name, query string }{
		{"create", `mutation { createMediaUpload(input:{contentType:"video/mp4",sizeBytes:10485760,sha256:` + validID + `,attemptId:"` + foreignAttemptID.String() + `",clientMutationId:"foreign-attempt"}) { upload { mediaId } } }`},
		{"presign", `mutation { presignMediaParts(input:{mediaId:"` + foreignMediaID.String() + `",partNumbers:[1],clientMutationId:"foreign-presign"}) { parts { url } } }`},
		{"complete", `mutation { completeMediaUpload(input:{mediaId:"` + foreignMediaID.String() + `",parts:[{partNumber:1,etag:"etag-1"},{partNumber:2,etag:"etag-2"}],clientMutationId:"foreign-complete"}) { media { status } } }`},
		{"status", `query { videoUploadStatus(mediaId:"` + foreignMediaID.String() + `") { status } }`},
	} {
		response := executeCaptureGraphQL(t, server, tc.query, tenantID, credentials, true)
		if !strings.Contains(response, "NOT_FOUND") || strings.Contains(response, foreignMediaID.String()) || strings.Contains(response, `"upload":{`) || strings.Contains(response, `"parts":[`) || strings.Contains(response, `"media":{`) {
			t.Fatalf("tenant B resource was exposed through %s: %s", tc.name, response)
		}
	}
	for _, tc := range []struct{ name, query string }{
		{"create", `mutation { createMediaUpload(input:{contentType:"video/mp4",sizeBytes:10485760,sha256:` + validID + `,attemptId:"` + unavailableAttemptID.String() + `",clientMutationId:"unavailable"}) { upload { mediaId } } }`},
		{"presign", `mutation { presignMediaParts(input:{mediaId:"` + unavailableMediaID.String() + `",partNumbers:[1],clientMutationId:"unavailable"}) { parts { partNumber } } }`},
		{"complete", `mutation { completeMediaUpload(input:{mediaId:"` + unavailableMediaID.String() + `",parts:[{partNumber:1,etag:"etag-1"},{partNumber:2,etag:"etag-2"}],clientMutationId:"unavailable"}) { media { status } } }`},
		{"status", `query { videoUploadStatus(mediaId:"` + unavailableMediaID.String() + `") { status } }`},
	} {
		response := executeCaptureGraphQL(t, server, tc.query, tenantID, credentials, true)
		if !strings.Contains(response, "DEPENDENCY_UNAVAILABLE") || strings.Contains(response, `"upload":{`) || strings.Contains(response, `"parts":[`) || strings.Contains(response, `"media":{`) {
			t.Fatalf("%s operation returned success-shaped data during dependency failure: %s", tc.name, response)
		}
	}
	internalCreate := executeCaptureGraphQL(t, server, `mutation { createMediaUpload(input:{contentType:"video/mp4",sizeBytes:10485760,sha256:`+validID+`,attemptId:"`+internalErrorAttemptID.String()+`",clientMutationId:"internal"}) { upload { mediaId } } }`, tenantID, credentials, true)
	if !strings.Contains(internalCreate, "INTERNAL") || strings.Contains(internalCreate, "qa-secret-123") {
		t.Fatalf("unexpected create upload adapter details leaked: %s", internalCreate)
	}
	for _, query := range []string{
		`mutation { presignMediaParts(input:{mediaId:"` + internalErrorMediaID.String() + `",partNumbers:[1],clientMutationId:"internal"}) { parts { url } } }`,
		`mutation { completeMediaUpload(input:{mediaId:"` + internalErrorMediaID.String() + `",parts:[{partNumber:1,etag:"etag-1"},{partNumber:2,etag:"etag-2"}],clientMutationId:"internal"}) { media { status } } }`,
		`query { videoUploadStatus(mediaId:"` + internalErrorMediaID.String() + `") { status } }`,
	} {
		response := executeCaptureGraphQL(t, server, query, tenantID, credentials, true)
		if !strings.Contains(response, "INTERNAL") || strings.Contains(response, "qa-secret-123") {
			t.Fatalf("unexpected upload adapter details leaked: %s", response)
		}
	}
	for _, csrf := range []string{"", "incorrect-csrf"} {
		badCredentials := credentials
		badCredentials.CSRFToken = csrf
		response := executeCaptureGraphQL(t, server, queries[0].query, tenantID, badCredentials, true)
		if !strings.Contains(response, "FORBIDDEN") || strings.Contains(response, "multipart-1") {
			t.Fatalf("video upload proceeded with invalid CSRF proof %q: %s", csrf, response)
		}
		response = executeCaptureGraphQL(t, server, queries[3].query, tenantID, badCredentials, true)
		if !strings.Contains(response, "FORBIDDEN") || strings.Contains(response, "RECEIVED_PENDING_VALIDATION") {
			t.Fatalf("video status proceeded without the session proof capability %q: %s", csrf, response)
		}
	}
	if err := invitations.DB.Model(&database.ExternalSession{}).Where("tenant_id=?", tenantID).Update("expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	expiredSession := executeCaptureGraphQL(t, server, queries[0].query, tenantID, credentials, true)
	if !strings.Contains(expiredSession, "SESSION_EXPIRED") || strings.Contains(expiredSession, "multipart-1") {
		t.Fatalf("expired external session performed protected upload work: %s", expiredSession)
	}
}

func executeVideoPolicyGraphQL(t *testing.T, server http.Handler, query string, ctx context.Context) string {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+quote(query)+`}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(w, req)
	return w.Body.String()
}

func TestVideoPolicyGraphQLUsesRealTenantSliceAndCapabilities(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"tenancy", "media", "audit"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.Tenant{}, &database.VideoPolicy{}, &database.VideoSource{}, &database.AuditEvent{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	tenantA, tenantB := identity.NewID(), identity.NewID()
	for _, id := range []identity.ID{tenantA, tenantB} {
		if err := db.Create(&database.Tenant{ID: id, TenantID: id, Name: "Video policy tenant", Language: "pt-BR", DefaultTimezone: "UTC", Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	adminID, viewerID, customerViewerID := identity.NewID(), identity.NewID(), identity.NewID()
	store := policyTransportMembershipStore{roles: map[identity.ID]string{adminID: auth.TenantAdmin, viewerID: auth.Viewer, customerViewerID: auth.CustomerViewer}}
	bus := mediator.New()
	if err := mediavideopolicy.Setup(mediavideopolicy.Dependencies{
		DB: db, Bus: bus, Authorizer: auth.Authorizer{Store: store},
		Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
			return fn(db.WithContext(ctx))
		},
	}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{Bus: bus}}))
	server.SetErrorPresenter(graph.PresentError)
	adminCtx := policyTransportContext(tenantA, adminID)
	initial := executeVideoPolicyGraphQL(t, server, `query { videoPolicy { maxDurationMinutes originRetentionDays currentRetentionDays version } }`, adminCtx)
	for _, value := range []string{`"maxDurationMinutes":15`, `"originRetentionDays":30`, `"currentRetentionDays":30`, `"version":1`} {
		if !strings.Contains(initial, value) {
			t.Fatalf("default policy missing %s: %s", value, initial)
		}
	}
	deadline := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	source := database.VideoSource{ID: identity.NewID(), TenantID: tenantA, MediaID: identity.NewID(), SourceExpiryAt: &deadline, Status: "RETAINED"}
	if err := db.Create(&source).Error; err != nil {
		t.Fatal(err)
	}
	invalidRetention := executeVideoPolicyGraphQL(t, server, `mutation { configureVideoPolicy(input:{maxDurationMinutes:10,originRetentionDays:-1,currentRetentionDays:7,expectedVersion:1,clientMutationId:"negative-retention"}) { policy { version } clientMutationId } }`, adminCtx)
	if !strings.Contains(invalidRetention, "INVALID_INPUT") || strings.Contains(invalidRetention, `"policy":{`) {
		t.Fatalf("negative origin retention was not rejected without result data: %s", invalidRetention)
	}
	var unchangedSource database.VideoSource
	if err := db.Where("tenant_id=? AND id=?", tenantA, source.ID).Take(&unchangedSource).Error; err != nil {
		t.Fatal(err)
	}
	if unchangedSource.SourceExpiryAt == nil || !unchangedSource.SourceExpiryAt.Equal(deadline) || unchangedSource.Status != "RETAINED" {
		t.Fatalf("invalid policy update changed source deadline/status: expiry=%v status=%s", unchangedSource.SourceExpiryAt, unchangedSource.Status)
	}
	configured := executeVideoPolicyGraphQL(t, server, `mutation { configureVideoPolicy(input:{maxDurationMinutes:10,originRetentionDays:40,currentRetentionDays:7,expectedVersion:1,clientMutationId:"policy-k1"}) { policy { maxDurationMinutes originRetentionDays currentRetentionDays version } clientMutationId userErrors { code } } }`, adminCtx)
	for _, value := range []string{`"maxDurationMinutes":10`, `"originRetentionDays":40`, `"currentRetentionDays":7`, `"version":2`, `"clientMutationId":"policy-k1"`} {
		if !strings.Contains(configured, value) {
			t.Fatalf("configured policy missing %s: %s", value, configured)
		}
	}
	replay := executeVideoPolicyGraphQL(t, server, `mutation { configureVideoPolicy(input:{maxDurationMinutes:10,originRetentionDays:40,currentRetentionDays:7,expectedVersion:1,clientMutationId:"policy-k1"}) { policy { version } } }`, adminCtx)
	if !strings.Contains(replay, `"version":2`) || strings.Contains(replay, `"errors"`) {
		t.Fatalf("idempotent policy replay changed version or failed: %s", replay)
	}
	conflict := executeVideoPolicyGraphQL(t, server, `mutation { configureVideoPolicy(input:{maxDurationMinutes:11,originRetentionDays:40,currentRetentionDays:7,expectedVersion:1,clientMutationId:"policy-k1"}) { policy { version } } }`, adminCtx)
	if !strings.Contains(conflict, "CONFLICT") || strings.Contains(conflict, `"policy":{`) {
		t.Fatalf("changed replay did not return a payload conflict: %s", conflict)
	}
	zeroVersion := executeVideoPolicyGraphQL(t, server, `mutation { configureVideoPolicy(input:{maxDurationMinutes:11,originRetentionDays:40,currentRetentionDays:7,expectedVersion:0,clientMutationId:"policy-stale-zero"}) { policy { version } } }`, adminCtx)
	if !strings.Contains(zeroVersion, "CONFLICT") || strings.Contains(zeroVersion, `"policy":{`) {
		t.Fatalf("zero expectedVersion did not return a stale-version conflict: %s", zeroVersion)
	}
	viewerUpdate := executeVideoPolicyGraphQL(t, server, `mutation { configureVideoPolicy(input:{maxDurationMinutes:11,originRetentionDays:40,currentRetentionDays:7,expectedVersion:2,clientMutationId:"viewer-update"}) { policy { version } } }`, policyTransportContext(tenantA, viewerID))
	if !strings.Contains(viewerUpdate, "FORBIDDEN") || strings.Contains(viewerUpdate, `"policy":{`) {
		t.Fatalf("viewer changed tenant video policy: %s", viewerUpdate)
	}
	customerViewerRead := executeVideoPolicyGraphQL(t, server, `query { videoPolicy { version } }`, policyTransportContext(tenantA, customerViewerID))
	if !strings.Contains(customerViewerRead, "FORBIDDEN") || strings.Contains(customerViewerRead, `"videoPolicy":{`) {
		t.Fatalf("customer viewer read tenant video policy: %s", customerViewerRead)
	}
	unauthenticated := executeVideoPolicyGraphQL(t, server, `query { videoPolicy { version } }`, context.Background())
	if !strings.Contains(unauthenticated, "UNAUTHENTICATED") || strings.Contains(unauthenticated, `"videoPolicy":{`) {
		t.Fatalf("unauthenticated policy query was accepted: %s", unauthenticated)
	}
	foreignScope := executeVideoPolicyGraphQL(t, server, `query { videoPolicy { maxDurationMinutes } }`, policyTransportContext(tenantB, adminID))
	if !strings.Contains(foreignScope, `"maxDurationMinutes":15`) || strings.Contains(foreignScope, `"maxDurationMinutes":10`) {
		t.Fatalf("policy read escaped the actor tenant scope: %s", foreignScope)
	}
	if err := db.Model(&database.Tenant{}).Where("tenant_id=?", tenantA).Update("status", "INACTIVE").Error; err != nil {
		t.Fatal(err)
	}
	disabled := executeVideoPolicyGraphQL(t, server, `mutation { configureVideoPolicy(input:{maxDurationMinutes:12,originRetentionDays:40,currentRetentionDays:7,expectedVersion:2,clientMutationId:"inactive-tenant"}) { policy { version } } }`, adminCtx)
	if !strings.Contains(disabled, "INVALID_STATE") || strings.Contains(disabled, `"policy":{`) {
		t.Fatalf("inactive tenant policy update was not rejected: %s", disabled)
	}
}

func TestVideoUploadGraphQLUsesRealMediaSlicesAndPersistsReceiptIT624(t *testing.T) {
	tenantID, responsibilityID := identity.NewID(), identity.NewID()
	invitations, credentials := captureSessionFixture(t, tenantID, responsibilityID)
	db := invitations.DB
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.Exec("ATTACH DATABASE ':memory:' AS capture").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS media").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS messaging").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS platform").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS audit").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS inspections").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS templates").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE inspections.responsibilities (id TEXT PRIMARY KEY, tenant_id TEXT, status TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&database.CaptureDraft{}, &database.VideoAttempt{}, &database.VideoAdmission{}, &database.VideoPolicy{}, &database.MediaObject{}, &database.MultipartUpload{}, &database.UploadPart{}, &database.OutboxIntent{}, &database.VideoPreparationJob{}, &database.AuditEvent{}, &database.TemplateVersion{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	var acceptance database.ProcessingAcceptance
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).First(&acceptance).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&acceptance).Update("video_processing", true).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	templateVersionID := identity.NewID()
	if err := db.Create(&database.TemplateVersion{ID: templateVersionID, TenantID: tenantID, TemplateID: identity.NewID(), VersionNumber: 1, SchemaVersion: 1, DefinitionJSON: []byte(`{}`), CanonicalDigest: strings.Repeat("a", 64), Status: "ACTIVE", IdempotencyKey: "video-transport-v1", PublishedAt: now, CreatedBy: identity.NewID()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: templateVersionID, ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{"allowGallery":true}`), Requirements: []byte(`[]`), Status: "OPEN", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.VideoAdmission{ID: 1, Enabled: false, Version: 1, ChangedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	client := &videoReceiptGraphQLClient{size: 10 * 1024 * 1024, contentType: "video/mp4", digest: strings.Repeat("a", 64)}
	within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return fn(db.WithContext(ctx))
	}
	service := mediacore.Service{DB: db, Store: objectstore.Store{Bucket: "private", Client: client}, Now: func() time.Time { return now }, Within: within}
	bus := mediator.New()
	if err := beginvideocapture.Setup(beginvideocapture.Dependencies{DB: db, Bus: bus, Now: func() time.Time { return now }, Within: within}); err != nil {
		t.Fatal(err)
	}
	// CAMERA is a participant's declared acquisition policy. The test deliberately
	// uploads eligible bytes and does not treat that declaration as cryptographic
	// evidence of device, camera, or location provenance.
	createdAttempt, err := bus.Send(context.Background(), beginvideocapture.Command{TenantID: tenantID, ResponsibilityID: responsibilityID, AcquisitionSource: "CAMERA", MutationID: "client-camera-declaration"})
	if err != nil {
		t.Fatalf("camera declaration was rejected by acquisition policy: %v", err)
	}
	attemptID := createdAttempt.(database.VideoAttempt).ID
	for _, setup := range []func() error{
		func() error { return mediacreate.Setup(mediacreate.Dependencies{Bus: bus, Service: service}) },
		func() error { return mediapresign.Setup(mediapresign.Dependencies{Bus: bus, Service: service}) },
		func() error { return mediacomplete.Setup(mediacomplete.Dependencies{Bus: bus, Service: service}) },
		func() error { return mediareconcile.Setup(mediareconcile.Dependencies{Bus: bus, Service: service}) },
	} {
		if err := setup(); err != nil {
			t.Fatal(err)
		}
	}
	server := captureGraphQLServer(bus, invitations)
	validID := `"` + client.digest + `"`
	create := `mutation { createMediaUpload(input:{contentType:"video/mp4",sizeBytes:10485760,sha256:` + validID + `,attemptId:"` + attemptID.String() + `",clientMutationId:"real-video-upload"}) { upload { mediaId uploadId partSizeBytes } } }`
	response := executeCaptureGraphQL(t, server, create, tenantID, credentials, true)
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"partSizeBytes":5242880`) {
		t.Fatalf("real media slice did not admit a two-part upload: %s", response)
	}
	var created struct {
		Data struct {
			Payload struct {
				Upload struct {
					MediaID string `json:"mediaId"`
				} `json:"upload"`
			} `json:"createMediaUpload"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &created); err != nil || created.Data.Payload.Upload.MediaID == "" {
		t.Fatalf("could not decode admitted upload: %s err=%v", response, err)
	}
	mediaID := created.Data.Payload.Upload.MediaID
	for _, query := range []string{
		`mutation { presignMediaParts(input:{mediaId:"` + mediaID + `",partNumbers:[1,2],clientMutationId:"real-presign"}) { parts { partNumber url } } }`,
	} {
		response = executeCaptureGraphQL(t, server, query, tenantID, credentials, true)
		if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"partNumber":2`) {
			t.Fatalf("real media slice did not presign both parts: %s", response)
		}
	}
	complete := `mutation { completeMediaUpload(input:{mediaId:"` + mediaID + `",parts:[{partNumber:1,etag:"etag-1"},{partNumber:2,etag:"etag-2"}],clientMutationId:"real-complete"}) { media { id status mediaKind preparationStatus } } }`
	response = executeCaptureGraphQL(t, server, complete, tenantID, credentials, true)
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"status":"RECEIVED"`) || !strings.Contains(response, `"preparationStatus":"PENDING_VALIDATION"`) {
		t.Fatalf("real media slice did not persist a pending receipt: %s", response)
	}
	status := `query { videoUploadStatus(mediaId:"` + mediaID + `") { status receivedParts confirmedParts { partNumber etag sizeBytes } missingParts } }`
	response = executeCaptureGraphQL(t, server, status, tenantID, credentials, true)
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"status":"RECEIVED"`) || !strings.Contains(response, `"receivedParts":[1,2]`) || !strings.Contains(response, `"confirmedParts":[{"partNumber":1,"etag":"etag-1","sizeBytes":5242880},{"partNumber":2,"etag":"etag-2","sizeBytes":5242880}]`) || !strings.Contains(response, `"missingParts":[]`) {
		t.Fatalf("receipt status did not return confirmed parts: %s", response)
	}
	if client.completions != 1 || client.headCalls != 1 {
		t.Fatalf("unexpected remote multipart operations: %+v", client)
	}
	unfinished := make([]database.MediaObject, 100)
	for i := range unfinished {
		unfinished[i] = database.MediaObject{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, ObjectKey: "tenant/unfinished/" + identity.NewID().String(), ContentType: "video/mp4", SHA256: strings.Repeat("b", 64), SizeBytes: 1024, Status: "UPLOADING", MediaKind: "VIDEO", IdempotencyKey: "unfinished-" + identity.NewID().String(), RequirementKey: "video:main", CreatedAt: now}
	}
	if err := db.Create(&unfinished).Error; err != nil {
		t.Fatal(err)
	}
	secondAttemptID := identity.NewID()
	if err := db.Create(&database.VideoAttempt{ID: secondAttemptID, TenantID: tenantID, ResponsibilityID: responsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: objectstore.MaxVideoBytes, AcquisitionSource: "CAMERA", Status: "ACTIVE", IdempotencyKey: "attempt-two", PayloadDigest: strings.Repeat("c", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	limited := `mutation { createMediaUpload(input:{contentType:"video/mp4",sizeBytes:1024,sha256:"` + strings.Repeat("d", 64) + `",attemptId:"` + secondAttemptID.String() + `",clientMutationId:"over-limit"}) { upload { mediaId } } }`
	response = executeCaptureGraphQL(t, server, limited, tenantID, credentials, true)
	if !strings.Contains(response, "RATE_LIMITED") || strings.Contains(response, `"upload":{`) {
		t.Fatalf("GraphQL accepted a new upload with 100 unfinished receipts: %s", response)
	}
	if err := db.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).Update("status", "SUBMITTED").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.Invitation{}).Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).Update("status", "COMPLETED").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.ExternalSession{}).Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).Update("revoked_at", now).Error; err != nil {
		t.Fatal(err)
	}
	response = executeCaptureGraphQL(t, server, status, tenantID, credentials, true)
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"status":"RECEIVED"`) {
		t.Fatalf("completed capture lost its authoritative receipt: %s", response)
	}
}

func TestOriginVideoAttemptRejectsJPEGMediaUploadGraphQLIT034(t *testing.T) {
	tenantID, responsibilityID := identity.NewID(), identity.NewID()
	invitations, credentials := captureSessionFixture(t, tenantID, responsibilityID)
	db := invitations.DB
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.Exec("ATTACH DATABASE ':memory:' AS capture").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS media").Error; err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&database.CaptureDraft{}, &database.VideoAttempt{}, &database.MediaObject{}, &database.MultipartUpload{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	attemptID := identity.NewID()
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, Kind: "ORIGIN", MediaKind: "VIDEO", TemplateVersionID: identity.NewID(), ReferencePayload: []byte(`{}`), PolicyPayload: []byte(`{}`), Requirements: []byte(`[]`), Status: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.VideoAttempt{ID: attemptID, TenantID: tenantID, ResponsibilityID: responsibilityID, Kind: "ORIGIN", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "FILE", Status: "ACTIVE", IdempotencyKey: "it034-origin-attempt", PayloadDigest: strings.Repeat("a", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return fn(db.WithContext(ctx))
	}
	service := mediacore.Service{DB: db, Store: objectstore.Store{Bucket: "private", Client: &videoReceiptGraphQLClient{}}, Within: within, Now: func() time.Time { return now }}
	bus := mediator.New()
	if err := mediacreate.Setup(mediacreate.Dependencies{Bus: bus, Service: service}); err != nil {
		t.Fatal(err)
	}
	server := captureGraphQLServer(bus, invitations)
	query := `mutation { createMediaUpload(input:{contentType:"image/jpeg",sizeBytes:12,sha256:"` + strings.Repeat("a", 64) + `",attemptId:"` + attemptID.String() + `",clientMutationId:"IT-034"}) { upload { mediaId uploadId } } }`
	response := executeCaptureGraphQL(t, server, query, tenantID, credentials, true)
	if !strings.Contains(response, "INVALID_INPUT") || (!strings.Contains(response, `"createMediaUpload":null`) && !strings.Contains(response, `"data":null`)) {
		t.Fatalf("origin video JPEG upload response=%s, want INVALID_INPUT and null result", response)
	}
	var mediaCount int64
	if err := db.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).Count(&mediaCount).Error; err != nil {
		t.Fatal(err)
	}
	if mediaCount != 0 {
		t.Fatalf("invalid JPEG attempt created %d media rows", mediaCount)
	}
}

type videoReceiptGraphQLClient struct {
	size        int64
	contentType string
	digest      string
	parts       []objectstore.UploadedPart
	completions int
	headCalls   int
}

func (c *videoReceiptGraphQLClient) CreateMultipart(context.Context, string, string, string) (string, error) {
	return "remote-video-upload", nil
}
func (c *videoReceiptGraphQLClient) PresignPart(_ context.Context, _, _, _ string, part int, _ time.Duration) (string, error) {
	return fmt.Sprintf("https://signed.invalid/%d", part), nil
}
func (c *videoReceiptGraphQLClient) CompleteMultipart(_ context.Context, _, _, _ string, parts []objectstore.Part) error {
	c.completions++
	for _, part := range parts {
		partSize := int64(objectstore.PartSizeBytes)
		if part.Number == objectstore.ExpectedParts(c.size) {
			partSize = c.size - int64(part.Number-1)*objectstore.PartSizeBytes
		}
		c.parts = append(c.parts, objectstore.UploadedPart{Number: part.Number, ETag: part.ETag, SizeBytes: partSize})
	}
	return nil
}
func (c *videoReceiptGraphQLClient) AbortMultipart(context.Context, string, string, string) error {
	return nil
}
func (c *videoReceiptGraphQLClient) Head(context.Context, string, string) (objectstore.Head, error) {
	c.headCalls++
	return objectstore.Head{Size: c.size, ContentType: c.contentType, ChecksumSHA256: c.digest}, nil
}
func (c *videoReceiptGraphQLClient) Get(context.Context, string, string) (io.ReadCloser, error) {
	return nil, errors.New("unexpected video read")
}
func (c *videoReceiptGraphQLClient) Put(context.Context, string, string, io.Reader, int64, string) error {
	return errors.New("unexpected video write")
}
func (c *videoReceiptGraphQLClient) ListMultipartParts(context.Context, string, string, string) ([]objectstore.UploadedPart, error) {
	return append([]objectstore.UploadedPart(nil), c.parts...), nil
}

type policyTransportMembershipStore struct{ roles map[identity.ID]string }

func (s policyTransportMembershipStore) Resolve(_ context.Context, tenantID, identityID identity.ID) (requestctx.Principal, error) {
	role := s.roles[identityID]
	if role == "" {
		return requestctx.Principal{}, fmt.Errorf("membership unavailable")
	}
	return requestctx.Principal{IdentityID: identityID, TenantID: tenantID, MembershipID: identityID, Roles: []string{role}}, nil
}

func policyTransportContext(tenantID, identityID identity.ID) context.Context {
	return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identityID, TenantID: tenantID}, CorrelationID: "video-policy-real-slice"})
}
