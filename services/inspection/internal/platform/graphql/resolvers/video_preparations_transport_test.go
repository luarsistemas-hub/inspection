package resolvers

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	mediafalsepositive "inspection/services/inspection/internal/features/media/declare_false_positive"
	mediareview "inspection/services/inspection/internal/features/media/review_sensitive_media"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
)

func TestVideoPreparationsGraphQLTenantFilterAndPagination(t *testing.T) {
	db := task06ResolverDB(t)
	for _, statement := range []string{
		`CREATE TABLE media.video_preparation_jobs (id blob primary key, tenant_id blob not null, media_id blob not null, status text not null, phase text not null, preparation_version integer not null, attempts integer not null, processed_frames integer not null, error_code text not null, manifest_digest text not null, first_ready_at datetime, created_at datetime not null)`,
		`CREATE TABLE media.media_objects (id blob primary key, tenant_id blob not null, object_key text not null, status text not null)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantA, tenantB := identity.NewID(), identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	jobs := []database.VideoPreparationJob{
		{ID: identity.NewID(), TenantID: tenantA, MediaID: identity.NewID(), Status: "FAILED", Phase: "EXTRACTING", PreparationVersion: 3, Attempts: 2, ProcessedFrames: 10, ErrorCode: "STORE_UNAVAILABLE", CreatedAt: now},
		{ID: identity.NewID(), TenantID: tenantA, MediaID: identity.NewID(), Status: "FAILED", Phase: "FAILED", PreparationVersion: 1, Attempts: 1, ProcessedFrames: 20, ErrorCode: "SOURCE_INVALID", CreatedAt: now.Add(-time.Second)},
		{ID: identity.NewID(), TenantID: tenantB, MediaID: identity.NewID(), Status: "FAILED", Phase: "FAILED", PreparationVersion: 1, Attempts: 9, ErrorCode: "FOREIGN", CreatedAt: now.Add(time.Second)},
	}
	for _, job := range jobs {
		if err := db.Exec(`INSERT INTO media.video_preparation_jobs (id,tenant_id,media_id,status,phase,preparation_version,attempts,processed_frames,error_code,manifest_digest,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, job.ID, job.TenantID, job.MediaID, job.Status, job.Phase, job.PreparationVersion, job.Attempts, job.ProcessedFrames, job.ErrorCode, "digest", job.CreatedAt).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`INSERT INTO media.media_objects (id,tenant_id,object_key,status) VALUES (?,?,?,?)`, job.MediaID, job.TenantID, "source/object", "READY").Error; err != nil {
			t.Fatal(err)
		}
	}
	server := task06GraphQLServer(db)
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantA, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantA, Roles: []string{auth.Manager}}})
	first := executeTask06GraphQL(t, server, `query { videoPreparations(status:["FAILED"],first:1) { nodes { mediaId status stage preparationVersion attempts processedFrames errorCode retryable sourceAvailable } pageInfo { endCursor hasNextPage } } }`, ctx)
	if strings.Contains(first, `"errors"`) || !strings.Contains(first, `"hasNextPage":true`) || !strings.Contains(first, `"errorCode":"STORE_UNAVAILABLE"`) || strings.Contains(first, jobs[2].ID.String()) {
		t.Fatalf("unexpected first page: %s", first)
	}
	var cursor string
	start := strings.Index(first, `"endCursor":"`) + len(`"endCursor":"`)
	if start < len(`"endCursor":"`) {
		t.Fatalf("missing cursor: %s", first)
	}
	end := strings.Index(first[start:], `"`)
	cursor = first[start : start+end]
	next := executeTask06GraphQL(t, server, fmt.Sprintf(`query { videoPreparations(status:["FAILED"],first:1,after:%q) { nodes { mediaId status } pageInfo { hasNextPage } } }`, cursor), ctx)
	if strings.Contains(next, `"errors"`) || !strings.Contains(next, jobs[1].MediaID.String()) || strings.Contains(next, `"hasNextPage":true`) {
		t.Fatalf("unexpected second page: %s", next)
	}
	invalidFilter := executeTask06GraphQL(t, server, `query { videoPreparations(status:["PRIVATE"],first:1) { nodes { mediaId } } }`, ctx)
	if !strings.Contains(invalidFilter, "INVALID_INPUT") {
		t.Fatalf("invalid status filter was accepted: %s", invalidFilter)
	}
	viewer := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantA, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantA, Roles: []string{auth.Viewer}}})
	denied := executeTask06GraphQL(t, server, `query { videoPreparations { nodes { mediaId } } }`, viewer)
	if !strings.Contains(denied, "FORBIDDEN") {
		t.Fatalf("viewer read was not denied: %s", denied)
	}
}

func TestInternalReviewerCanDeclareVideoFalsePositive(t *testing.T) {
	db := task06ResolverDB(t)
	if err := db.Exec(`CREATE TABLE media.media_objects (id blob primary key, tenant_id blob not null, responsibility_id blob not null, object_key text not null, content_type text not null, sha256 text not null, size_bytes integer not null, status text not null, media_kind text not null, idempotency_key text not null, requirement_key text not null, description text not null, attention_items blob, capture_source text not null, flags blob, created_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	tenantID, mediaID, responsibilityID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO media.media_objects (id,tenant_id,responsibility_id,object_key,content_type,sha256,size_bytes,status,media_kind,idempotency_key,requirement_key,description,capture_source,flags) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, mediaID, tenantID, responsibilityID, "source/object", "video/mp4", "digest", 10, "BLOCKED", "VIDEO", "k", "video:main", "source", "CAMERA", []byte(`[]`)).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	called := false
	if err := bus.RegisterCommand(mediafalsepositive.Command{}, func(_ context.Context, raw any) (any, error) {
		command := raw.(mediafalsepositive.Command)
		called = true
		if command.TenantID != tenantID || command.ResponsibilityID != responsibilityID || command.MediaID != mediaID {
			t.Fatalf("wrong internally resolved ownership: %+v", command)
		}
		return database.MediaObject{ID: mediaID, TenantID: tenantID, ResponsibilityID: responsibilityID, Status: "READY", Flags: []byte(`[]`)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: db, Bus: bus}}))
	server.SetErrorPresenter(graph.PresentError)
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Internal: true, Roles: []string{auth.Manager}}})
	response := executeTask06GraphQL(t, server, `mutation { declareSensitiveDetectionFalsePositive(input:{mediaId:"`+mediaID.String()+`",reason:"Review cleared the flag",clientMutationId:"internal-review"}) { media { id } userErrors { code } clientMutationId } }`, ctx)
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, mediaID.String()) || !called {
		t.Fatalf("internal review failed: %s called=%v", response, called)
	}
}

func TestSensitiveMediaReviewQueriesRejectNonReviewers(t *testing.T) {
	db := task06ResolverDB(t)
	bus := mediator.New()
	if err := mediareview.Setup(mediareview.Dependencies{DB: db, Store: objectstore.Store{Bucket: "test", Client: reportMediaClient{}}, Bus: bus, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: db, Bus: bus}}))
	server.SetErrorPresenter(graph.PresentError)
	for _, role := range []string{auth.Employee, auth.CustomerViewer, auth.Viewer} {
		t.Run(role, func(t *testing.T) {
			tenantID := identity.NewID()
			ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Internal: true, Roles: []string{role}}})
			response := executeTask06GraphQL(t, server, `query { sensitiveMediaReviews { nodes { mediaId } } }`, ctx)
			if !strings.Contains(response, `"code":"FORBIDDEN"`) {
				t.Fatalf("role %s read privacy queue: %s", role, response)
			}
		})
	}
	tenantID := identity.NewID()
	external := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.Manager}}})
	response := executeTask06GraphQL(t, server, `query { sensitiveMediaReviews { nodes { mediaId } } }`, external)
	if !strings.Contains(response, `"code":"FORBIDDEN"`) {
		t.Fatalf("external participant read privacy queue: %s", response)
	}
}

func TestVideoPreparationErrorCodeIsAllowlisted(t *testing.T) {
	if got := safeVideoPreparationError("STORE_UNAVAILABLE"); got != "STORE_UNAVAILABLE" {
		t.Fatalf("known safe error code changed: %q", got)
	}
	if got := safeVideoPreparationError("NO_FRAMES"); got != "NO_FRAMES" {
		t.Fatalf("no-frame error code changed: %q", got)
	}
	if got := safeVideoPreparationError("QA_SECRET_123"); got != "PREPARATION_FAILED" {
		t.Fatalf("unexpected error text was exposed: %q", got)
	}
}
