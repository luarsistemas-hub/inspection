//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestSealedPreparationEvidenceProjectsFrameSourcePTSAndComponentLinkIT162(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedVideoOriginFixture(ctx, harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	queryCtx := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: fixture.TenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: fixture.TenantID, Roles: []string{auth.TenantAdmin}}})
	query := `query { videoPreparationEvidence(preparationId:"` + fixture.PreparationID.String() + `",first:10) { nodes { id kind frameId sourceMediaId ptsMicros component } } }`
	response := executeTask06GraphQL(t, task06GraphQLServer(h.DB), query, queryCtx)
	if strings.Contains(response, `"errors"`) {
		t.Fatalf("evidence projection failed: %s", response)
	}
	var payload struct {
		Data struct {
			Evidence struct {
				Nodes []struct {
					Kind          string  `json:"kind"`
					FrameID       *string `json:"frameId"`
					SourceMediaID *string `json:"sourceMediaId"`
					PTSMicros     *string `json:"ptsMicros"`
					Component     *string `json:"component"`
				} `json:"nodes"`
			} `json:"videoPreparationEvidence"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil {
		t.Fatal(err)
	}
	var frameFound, componentFound bool
	for _, node := range payload.Data.Evidence.Nodes {
		if node.Kind == "FRAME" && node.FrameID != nil && *node.FrameID == fixture.FrameID.String() {
			frameFound = node.SourceMediaID != nil && *node.SourceMediaID == fixture.MediaID.String() && node.PTSMicros != nil && *node.PTSMicros == "0"
		}
		if node.Kind == "SCOPE" && node.FrameID != nil && *node.FrameID == fixture.FrameID.String() {
			componentFound = node.Component != nil && *node.Component == "sink"
		}
	}
	if !frameFound || !componentFound {
		t.Fatalf("sealed P1 projection lacks frame lineage/component linkage: %+v", payload.Data.Evidence.Nodes)
	}
}
