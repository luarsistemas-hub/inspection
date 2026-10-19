//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestVV_IT_613PreparationEvidenceCursorRemainsPinnedWhileP2IsCreated(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	p1, err := h.SeedVideoOriginFixture(ctx, harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	baseAt := time.Now().UTC().Truncate(time.Microsecond)
	if err := addTask05EvidenceEntries(ctx, h, p1.TenantID, p1.MediaID, p1.PreparationID, baseAt, 99); err != nil {
		t.Fatal(err)
	}
	queryCtx := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: p1.TenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: p1.TenantID, Roles: []string{auth.TenantAdmin}}})
	server := task06GraphQLServer(h.DB)
	seen := make(map[string]string, 200)
	after := ""
	for page := 0; ; page++ {
		response := executeTask06GraphQL(t, server, fmt.Sprintf(`query { videoPreparationEvidence(preparationId:"%s",first:25,after:%q) { nodes { id kind preparationId } pageInfo { endCursor hasNextPage } } }`, p1.PreparationID, after), queryCtx)
		var decoded struct {
			Data struct {
				Evidence struct {
					Nodes    []struct{ ID, Kind, PreparationID string } `json:"nodes"`
					PageInfo struct {
						EndCursor string `json:"endCursor"`
						HasNext   bool   `json:"hasNextPage"`
					} `json:"pageInfo"`
				} `json:"videoPreparationEvidence"`
			} `json:"data"`
		}
		if strings.Contains(response, `"errors"`) || json.Unmarshal([]byte(response), &decoded) != nil {
			t.Fatalf("P1 page %d failed: %s", page, response)
		}
		for _, item := range decoded.Data.Evidence.Nodes {
			if item.PreparationID != p1.PreparationID.String() {
				t.Fatalf("cursor moved from P1 to P2 at page %d: %+v", page, item)
			}
			if _, duplicate := seen[item.ID]; duplicate {
				t.Fatalf("P1 cursor repeated entry %s", item.ID)
			}
			seen[item.ID] = item.Kind
		}
		if page == 0 {
			if len(decoded.Data.Evidence.Nodes) != 25 || decoded.Data.Evidence.PageInfo.EndCursor == "" || !decoded.Data.Evidence.PageInfo.HasNext {
				t.Fatalf("first page did not return 25 entries and a continuation cursor: %s", response)
			}
			p2ID := identity.NewID()
			if err := h.WithinTenant(ctx, p1.TenantID, func(tx *gorm.DB) error {
				job := database.VideoPreparationJob{ID: p2ID, TenantID: p1.TenantID, MediaID: p1.MediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 2, NextAttemptAt: baseAt, PendingObjectKeys: []byte(`[]`), CreatedAt: baseAt.Add(time.Second)}
				if err := tx.Create(&job).Error; err != nil {
					return err
				}
				return addTask05EvidenceEntriesTx(tx, p1.TenantID, p1.MediaID, p2ID, baseAt.Add(10*time.Minute), 100)
			}); err != nil {
				t.Fatalf("create P2 after P1 first page: %v", err)
			}
		}
		after = decoded.Data.Evidence.PageInfo.EndCursor
		if !decoded.Data.Evidence.PageInfo.HasNext {
			break
		}
		if page >= 8 {
			t.Fatal("P1 pagination did not terminate")
		}
	}
	if len(seen) != 200 {
		t.Fatalf("P1 cursor traversed %d unique scope/frame entries, want exactly 200", len(seen))
	}
	var frames, scopes int
	for _, kind := range seen {
		if kind == "FRAME" {
			frames++
		}
		if kind == "SCOPE" {
			scopes++
		}
	}
	if frames != 100 || scopes != 100 {
		t.Fatalf("P1 traversal returned frames=%d scopes=%d; want 100 each", frames, scopes)
	}
}

func addTask05EvidenceEntries(ctx context.Context, h *harness.Harness, tenantID, mediaID, preparationID identity.ID, at time.Time, additional int) error {
	return h.WithinTenant(ctx, tenantID, func(tx *gorm.DB) error {
		return addTask05EvidenceEntriesTx(tx, tenantID, mediaID, preparationID, at, additional)
	})
}

func addTask05EvidenceEntriesTx(tx *gorm.DB, tenantID, mediaID, preparationID identity.ID, at time.Time, count int, existingFrame ...identity.ID) error {
	for i := 0; i < count; i++ {
		frameID := identity.NewID()
		pts := int64(i+1) * 1_000_000
		frame := database.VideoFrame{ID: frameID, TenantID: tenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: pts, SHA256: strings.Repeat("a", 64), ObjectKey: "vv-it613/" + preparationID.String() + "/" + frameID.String(), Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 1, CreatedAt: at.Add(time.Duration(i+1) * time.Second)}
		if err := tx.Create(&frame).Error; err != nil {
			return err
		}
		scope := database.VideoScopeItem{ID: identity.NewID(), TenantID: tenantID, PreparationID: preparationID, FrameID: frameID, EnvironmentCandidate: "Kitchen", ComponentLabel: fmt.Sprintf("Sink %03d", i), ViewLabel: "Front", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: []byte(`[]`), CreatedAt: at.Add(time.Duration(i+1) * time.Second)}
		if err := tx.Create(&scope).Error; err != nil {
			return err
		}
	}
	return nil
}
