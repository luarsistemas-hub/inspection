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
	capturebootstrap "inspection/services/inspection/internal/features/capture/bootstrap"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	capturereferences "inspection/services/inspection/internal/features/capture/reference_items"
	"inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/features/media/configure_video_policy"
	origincore "inspection/services/inspection/internal/features/origins/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

func TestExternalCaptureIdentifiesOnlyRequestedAssetAcrossHundredOriginInvites(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	seed, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	var sourceAsset database.Asset
	var templateVersion database.TemplateVersion
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", seed.TenantID, seed.AssetID).Take(&sourceAsset).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND id=?", seed.TenantID, seed.TemplateVersionID).Take(&templateVersion).Error
	}); err != nil {
		t.Fatal(err)
	}
	videoTemplateID, videoVersionID := identity.NewID(), identity.NewID()
	templateVersion.ID, templateVersion.TemplateID = videoVersionID, videoTemplateID
	templateVersion.VersionNumber, templateVersion.IdempotencyKey = 1, "it043-video-template"
	templateVersion.DefinitionJSON = json.RawMessage(`{"schemaVersion":1,"isVideo":true,"defaultComparisonMode":"FIXED_ORIGIN","analysisType":"REAL_ESTATE"}`)
	templateVersion.CreatedBy, templateVersion.PublishedAt = seed.IdentityID, now
	sourceAsset.TemplateID = &videoTemplateID
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		template := database.Template{ID: videoTemplateID, TenantID: seed.TenantID, Key: "it043-video", Name: "IT-043 video", SegmentVersionID: sourceAsset.SegmentVersionID, ActiveVersionID: &videoVersionID, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&template).Error; err != nil {
			return err
		}
		return tx.Create(&templateVersion).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Exec("INSERT INTO platform.video_admission (id, enabled, version, changed_at, reason, mutation_id, payload_hash) VALUES (1, TRUE, 1, ?, ?, ?, '') ON CONFLICT (id) DO UPDATE SET enabled=TRUE", now, "isolated IT-043 fixture", "it043").Error; err != nil {
		t.Fatal(err)
	}
	assets := make([]identity.ID, 100)
	assets[41] = sourceAsset.ID
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		for index := 0; index < 100; index++ {
			if index == 41 {
				continue
			}
			asset := sourceAsset
			asset.ID = identity.NewID()
			asset.Name = fmt.Sprintf("Asset X%d", index+1)
			asset.ExternalKey = fmt.Sprintf("it043-X%d", index+1)
			asset.IdempotencyKey = fmt.Sprintf("it043-X%d", index+1)
			asset.CreatedAt, asset.UpdatedAt = now, now
			if err := tx.Create(&asset).Error; err != nil {
				return err
			}
			assets[index] = asset.ID
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := origincore.Service{DB: h.DB, Within: h.WithinTenant, Now: func() time.Time { return now }}
	type invitedAsset struct {
		assetID, responsibilityID, invitationID identity.ID
		label, token                            string
	}
	invitations := make([]invitedAsset, 100)
	expires := now.Add(time.Hour)
	for index, assetID := range assets {
		label := fmt.Sprintf("Asset X%d", index+1)
		created, err := service.Invite(ctx, origincore.InviteInput{
			TenantID: seed.TenantID, AssetID: assetID, TemplateVersionID: videoVersionID,
			Delivery:     []core.DeliveryIntent{{Channel: "EMAIL", Destination: fmt.Sprintf("x%d@example.test", index+1)}},
			Requirements: []capturecore.Requirement{{Key: "video:main", Label: label, EvidenceKind: "VIDEO", Required: true, MinimumMedia: 1, MaximumMedia: 1}},
			Policy:       json.RawMessage(`{"gpsRequired":false,"allowGallery":true}`), ExpiresAt: expires, IdempotencyKey: fmt.Sprintf("it043-invite-X%d", index+1),
		})
		if err != nil {
			t.Fatalf("create origin invitation X%d: %v", index+1, err)
		}
		invitations[index] = invitedAsset{assetID: assetID, responsibilityID: created.ResponsibilityID, invitationID: created.InvitationID, label: label, token: created.LinkToken}
	}
	x42 := invitations[41]
	if x42.responsibilityID == (identity.ID{}) || x42.token == "" {
		t.Fatalf("X42 invitation missing external session scope: %+v", x42)
	}
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		return tx.Create(&database.ProcessingAcceptance{ID: identity.NewID(), TenantID: seed.TenantID, ResponsibilityID: x42.responsibilityID, DisclosureVersion: core.PrivacyDisclosureVersion, VideoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	pepper := []byte(strings.Repeat("v", 32))
	hash := security.HashToken(x42.token)
	csrf := "it043-csrf"
	proof, err := security.CSRFProof(hash, csrf, pepper)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Create(&database.ExternalSession{ID: identity.NewID(), TenantID: seed.TenantID, InvitationID: identity.Ptr(x42.invitationID), ResponsibilityID: &x42.responsibilityID, SessionDigest: hash[:], CSRFDigest: proof[:], ExpiresAt: expires, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := capturebootstrap.Setup(capturebootstrap.Dependencies{Bus: bus, Service: capturecore.Service{DB: h.DB}}); err != nil {
		t.Fatal(err)
	}
	if err := capturereferences.Setup(capturereferences.Dependencies{Bus: bus, DB: h.DB, Store: h.Store}); err != nil {
		t.Fatal(err)
	}
	if err := configure_video_policy.Setup(configure_video_policy.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: h.DB, Bus: bus, Invitations: core.Service{DB: h.DB, Pepper: pepper}}}))
	server.SetErrorPresenter(graph.PresentError)
	query := `query { externalCapture { responsibilityId kind mediaKind status requirements { key label } } }`
	response := executeCaptureGraphQL(t, server, query, seed.TenantID, requestctx.ExternalCredentials{SessionToken: x42.token, CSRFToken: csrf}, true)
	var payload struct {
		Data struct {
			Capture struct {
				ResponsibilityID string `json:"responsibilityId"`
				Kind             string `json:"kind"`
				MediaKind        string `json:"mediaKind"`
				Requirements     []struct {
					Label string `json:"label"`
				} `json:"requirements"`
			} `json:"externalCapture"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil || len(payload.Errors) != 0 {
		t.Fatalf("externalCapture X42 failed: response=%s err=%v", response, err)
	}
	capture := payload.Data.Capture
	if capture.ResponsibilityID != x42.responsibilityID.String() || capture.Kind != "ORIGIN" || capture.MediaKind != "VIDEO" || len(capture.Requirements) != 1 || capture.Requirements[0].Label != "Asset X42" {
		t.Fatalf("externalCapture returned wrong invitation scope: %+v response=%s", capture, response)
	}
	for _, other := range []string{"Asset X41", "Asset X43"} {
		if strings.Contains(response, other) {
			t.Fatalf("externalCapture X42 leaked neighboring asset %s: %s", other, response)
		}
	}
	var originCount, invitationCount int64
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.Origin{}).Where("tenant_id=? AND asset_id IN ?", seed.TenantID, assets).Count(&originCount).Error; err != nil {
			return err
		}
		return tx.Model(&database.Invitation{}).Where("tenant_id=? AND responsibility_id IN ?", seed.TenantID, func() []identity.ID {
			ids := make([]identity.ID, len(invitations))
			for i, invite := range invitations {
				ids[i] = invite.responsibilityID
			}
			return ids
		}()).Count(&invitationCount).Error
	}); err != nil {
		t.Fatal(err)
	}
	if originCount != 100 || invitationCount != 100 {
		t.Fatalf("scale fixture created origins=%d invitations=%d, want 100 each", originCount, invitationCount)
	}
}
