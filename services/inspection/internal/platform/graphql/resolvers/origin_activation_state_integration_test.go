//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	originactivate "inspection/services/inspection/internal/features/origins/activate_version"
	origincore "inspection/services/inspection/internal/features/origins/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

func TestActivateOriginDuringExtractionReturnsInvalidStateIT041(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	mediaID, originID, versionID, preparationID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fixture.TenantID, fixture.ResponsibilityID).Updates(map[string]any{"kind": "ORIGIN", "media_kind": "VIDEO", "status": "SUBMITTED"}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "it041/source.mp4", ContentType: "video/mp4", SHA256: strings.Repeat("a", 64), SizeBytes: 100, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Origin{ID: originID, TenantID: fixture.TenantID, AssetID: fixture.AssetID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.OriginVersion{ID: versionID, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: fixture.ResponsibilityID, Status: "DRAFT", MediaKind: "VIDEO", IdempotencyKey: "it041-" + versionID.String(), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, Status: "PROCESSING", Phase: "EXTRACTING", PreparationVersion: 1, Generation: 1, Attempts: 1, NextAttemptAt: now, PendingObjectKeys: json.RawMessage(`[]`), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := originactivate.Setup(originactivate.Dependencies{DB: h.DB, Bus: bus, Service: origincore.Service{DB: h.DB, Within: h.WithinTenant}, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: h.DB, Bus: bus}}))
	server.SetErrorPresenter(graph.PresentError)
	operator := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: fixture.TenantID, Principal: requestctx.Principal{IdentityID: fixture.IdentityID, MembershipID: fixture.MembershipID, TenantID: fixture.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "it041"})
	mutation := `mutation { activateOriginVersion(input:{versionId:"` + versionID.String() + `",clientMutationId:"it041"}) { version { id status } userErrors { code } } }`
	response := executeTask06GraphQL(t, server, mutation, operator)
	if !strings.Contains(response, `"code":"INVALID_STATE"`) || !strings.Contains(response, `"field":"media"`) || strings.Contains(response, `"status":"ACTIVE"`) {
		t.Fatalf("activation during EXTRACTING returned unexpected GraphQL result: %s", response)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var got database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, versionID).Take(&got).Error; err != nil {
			return err
		}
		var origin database.Origin
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, originID).Take(&origin).Error; err != nil {
			return err
		}
		if got.Status != "DRAFT" || origin.ActiveVersionID != nil {
			t.Errorf("EXTRACTING origin changed after rejected activation: version=%s active=%v", got.Status, origin.ActiveVersionID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
