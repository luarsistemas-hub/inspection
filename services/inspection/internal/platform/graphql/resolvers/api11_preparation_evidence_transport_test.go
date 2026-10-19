package resolvers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestVideoPreparationEvidenceCursorStaysOnPreparation(t *testing.T) {
	db := task06ResolverDB(t)
	for _, statement := range []string{
		`CREATE TABLE media.video_preparation_jobs (id blob primary key, tenant_id blob, media_id blob)`,
		`CREATE TABLE media.video_frames (id blob primary key, tenant_id blob, source_media_id blob, preparation_id blob, pts_us integer, sha256 text, object_key text, width integer, height integer, role text, quality text, size_bytes integer, created_at datetime)`,
		`CREATE TABLE media.video_scope_items (id blob primary key, tenant_id blob, preparation_id blob, frame_id blob, environment_candidate text, component_label text, view_label text, visibility text, identity_status text, landmarks blob, created_at datetime)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, preparationID := identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO media.video_preparation_jobs (id,tenant_id,media_id) VALUES (?,?,?)`, preparationID, tenantID, identity.NewID()).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		frameID, scopeID, sourceID := identity.NewID(), identity.NewID(), identity.NewID()
		created := time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC)
		if err := db.Exec(`INSERT INTO media.video_frames (id,tenant_id,source_media_id,preparation_id,pts_us,sha256,object_key,role,created_at) VALUES (?,?,?,?,?,?,?,?,?)`, frameID, tenantID, sourceID, preparationID, i*1000, strings.Repeat("a", 64), fmt.Sprintf("frame-%03d.jpg", i), "BASELINE", created).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`INSERT INTO media.video_scope_items (id,tenant_id,preparation_id,frame_id,environment_candidate,component_label,view_label,visibility,identity_status,landmarks,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, scopeID, tenantID, preparationID, frameID, "Cozinha", fmt.Sprintf("Componente %03d", i), "Frontal", "VISIBLE", "RESOLVED", []byte(`{}`), created).Error; err != nil {
			t.Fatal(err)
		}
	}
	server := task06GraphQLServer(db)
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.TenantAdmin}}})
	otherPreparation := identity.NewID()
	if err := db.Exec(`INSERT INTO media.video_preparation_jobs (id,tenant_id,media_id) VALUES (?,?,?)`, otherPreparation, tenantID, identity.NewID()).Error; err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, 200)
	after := ""
	for page := 0; page < 10; page++ {
		query := fmt.Sprintf(`query { videoPreparationEvidence(preparationId:"%s",first:25,after:%q) { nodes { id kind preparationId } pageInfo { endCursor hasNextPage } } }`, preparationID, after)
		response := executeTask06GraphQL(t, server, query, ctx)
		if strings.Contains(response, `"errors"`) {
			t.Fatalf("page %d failed: %s", page, response)
		}
		var decoded struct {
			Data struct {
				Page struct {
					Nodes []struct {
						ID            string `json:"id"`
						PreparationID string `json:"preparationId"`
					} `json:"nodes"`
					Info struct {
						EndCursor string `json:"endCursor"`
						HasNext   bool   `json:"hasNextPage"`
					} `json:"pageInfo"`
				} `json:"videoPreparationEvidence"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(response), &decoded); err != nil {
			t.Fatal(err)
		}
		for _, item := range decoded.Data.Page.Nodes {
			if item.PreparationID != preparationID.String() || seen[item.ID] {
				t.Fatalf("cursor moved preparation or repeated an item: %+v", item)
			}
			seen[item.ID] = true
		}
		after = decoded.Data.Page.Info.EndCursor
		if page == 0 && decoded.Data.Page.Info.EndCursor == "" {
			t.Fatal("first page did not return a cursor")
		}
		if !decoded.Data.Page.Info.HasNext {
			break
		}
	}
	if len(seen) != 200 {
		t.Fatalf("traversed %d unique P1 entries, want 200", len(seen))
	}
	bad := executeTask06GraphQL(t, server, fmt.Sprintf(`query { videoPreparationEvidence(preparationId:"%s",first:25,after:%q) { nodes { id } } }`, otherPreparation, after), ctx)
	if !strings.Contains(bad, "INVALID_INPUT") {
		t.Fatalf("accepted cursor from another preparation: %s", bad)
	}
	unauthenticated := executeTask06GraphQL(t, server, fmt.Sprintf(`query { videoPreparationEvidence(preparationId:"%s") { nodes { id } } }`, preparationID), context.Background())
	if !strings.Contains(unauthenticated, "UNAUTHENTICATED") {
		t.Fatalf("unauthenticated request was accepted: %s", unauthenticated)
	}
	forbiddenCtx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.CustomerViewer}}})
	forbidden := executeTask06GraphQL(t, server, fmt.Sprintf(`query { videoPreparationEvidence(preparationId:"%s") { nodes { id } } }`, preparationID), forbiddenCtx)
	if !strings.Contains(forbidden, "FORBIDDEN") {
		t.Fatalf("unauthorized role was accepted: %s", forbidden)
	}
	foreignCtx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: identity.NewID(), Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: identity.NewID(), Roles: []string{auth.TenantAdmin}}})
	foreign := executeTask06GraphQL(t, server, fmt.Sprintf(`query { videoPreparationEvidence(preparationId:"%s") { nodes { id } } }`, preparationID), foreignCtx)
	if !strings.Contains(foreign, "NOT_FOUND") {
		t.Fatalf("cross-tenant preparation was not hidden: %s", foreign)
	}
	tooMany := executeTask06GraphQL(t, server, fmt.Sprintf(`query { videoPreparationEvidence(preparationId:"%s",first:101) { nodes { id } } }`, preparationID), ctx)
	if !strings.Contains(tooMany, "INVALID_INPUT") {
		t.Fatalf("oversized page was accepted: %s", tooMany)
	}
	dependencyServer := task06GraphQLServer(nil)
	dependency := executeTask06GraphQL(t, dependencyServer, fmt.Sprintf(`query { videoPreparationEvidence(preparationId:"%s") { nodes { id } } }`, preparationID), ctx)
	if !strings.Contains(dependency, "DEPENDENCY_UNAVAILABLE") {
		t.Fatalf("missing database was not reported as unavailable: %s", dependency)
	}
}
