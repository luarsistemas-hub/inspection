package list_versions

import (
	"context"
	"testing"

	"inspection/libs/identity"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestAuditorListsVersionsButOtherAdminRolesDoNot(t *testing.T) {
	tenant, unit, asset := identity.NewID(), identity.NewID(), identity.NewID()
	bus := mediator.New()
	if err := bus.RegisterQuery(assetget.Query{}, func(context.Context, any) (any, error) {
		return assetcore.View{Asset: database.Asset{ID: asset, TenantID: tenant, BusinessUnitID: unit}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	within := func(context.Context, identity.ID, func(*gorm.DB) error) error { return nil }
	if err := Setup(Dependencies{DB: &gorm.DB{}, Bus: bus, Authorizer: auth.Authorizer{}, Within: within}); err != nil {
		t.Fatal(err)
	}
	ask := func(role string) error {
		ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenant, Roles: []string{role}, Scopes: []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: unit}}}})
		_, err := bus.Ask(ctx, Query{TenantID: tenant, AssetID: asset})
		return err
	}
	if err := ask(auth.Auditor); err != nil {
		t.Fatalf("auditor listing versions = %v", err)
	}
	if err := ask(auth.GovernanceAdmin); !apperror.Is(err, apperror.Forbidden) {
		t.Fatalf("governance admin listing versions = %v, want forbidden", err)
	}
}
