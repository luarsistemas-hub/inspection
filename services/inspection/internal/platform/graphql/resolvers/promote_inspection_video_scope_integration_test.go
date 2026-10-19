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
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	originlist "inspection/services/inspection/internal/features/origins/list_versions"
	originpromotevideo "inspection/services/inspection/internal/features/origins/promote_inspection_video"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

func TestVV_IT_066PromotedKitchenScopeAndLimitedCoverageInOriginVersions(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	attemptID, mediaID, submissionID := identity.NewID(), identity.NewID(), identity.NewID()
	preparationID, frameID, planID := identity.NewID(), identity.NewID(), identity.NewID()
	manifest := strings.Repeat("c", 64)
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Update("media_kind", "VIDEO").Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "it066-attempt", PayloadDigest: manifest, AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "it066/" + mediaID.String(), ContentType: "video/mp4", SHA256: manifest, SizeBytes: 100, Status: "READY", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "it066-media", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: fixture.TenantID, DraftID: fixture.DraftID, ResponsibilityID: fixture.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: json.RawMessage(`{}`), PayloadDigest: manifest, SubmittedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: manifest, ManifestDigest: manifest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &now, CreatedAt: now}).Error; err != nil {
			return err
		}
		days := 30
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &now, RetentionDays: &days, SourceExpiryAt: func() *time.Time { v := now.Add(24 * time.Hour); return &v }(), Status: "RETAINED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: manifest, ObjectKey: "it066/frame/" + frameID.String(), Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 10, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoScopeItem{ID: identity.NewID(), TenantID: fixture.TenantID, PreparationID: preparationID, FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", Landmarks: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlan{ID: planID, TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, SubmissionID: submissionID, CurrentPreparationID: preparationID, Generation: 1, PlanDigest: manifest, Status: "SEALED", SealedAt: &now, CreatedAt: now}).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	bus := mediator.New()
	if err := assetget.Setup(assetget.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	if err := originlist.Setup(originlist.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Within: h.WithinTenant}); err != nil {
		t.Fatal(err)
	}
	if err := originpromotevideo.Setup(originpromotevideo.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: h.DB, Bus: bus}}))
	server.SetErrorPresenter(graph.PresentError)
	operator := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: fixture.TenantID, Principal: requestctx.Principal{IdentityID: fixture.IdentityID, MembershipID: fixture.MembershipID, TenantID: fixture.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "it066"})
	mutation := `mutation { promoteInspectionVideo(input:{inspectionId:"` + fixture.InspectionID.String() + `",clientMutationId:"it066-promote"}) { version { id limitedCoverage } userErrors { code } } }`
	response := executeTask06GraphQL(t, server, mutation, operator)
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"limitedCoverage":true`) || !strings.Contains(response, `"userErrors":[]`) {
		t.Fatalf("promotion did not return limited-coverage signal: %s", response)
	}
	query := `query { originVersions(assetId:"` + fixture.AssetID.String() + `") { nodes { id limitedCoverage } } }`
	response = executeTask06GraphQL(t, server, query, operator)
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"limitedCoverage":true`) {
		t.Fatalf("originVersions did not expose limited-coverage signal: %s", response)
	}
	var scope []database.VideoScopeItem
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND preparation_id=?", fixture.TenantID, preparationID).Order("environment_candidate,component_label").Find(&scope).Error
	}); err != nil {
		t.Fatal(err)
	}
	if len(scope) != 1 || scope[0].EnvironmentCandidate != "kitchen" {
		t.Fatalf("promoted scope should contain kitchen only, got %#v", scope)
	}
}
