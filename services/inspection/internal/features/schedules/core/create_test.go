package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"inspection/libs/identity"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	originresolve "inspection/services/inspection/internal/features/origins/resolve_reference"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	"inspection/services/inspection/internal/features/templates/catalog"
	templateresolve "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
)

type fixedOriginFixture struct {
	input      Input
	assetUnit  identity.ID
	personUnit identity.ID
	originAsks int
	originErr  error
	lastQuery  originresolve.Query
	resolvedID identity.ID
	video      bool
}

func newFixedOriginFixture(t *testing.T) (*fixedOriginFixture, *mediator.Bus) {
	return newFixedOriginFixtureWithVideo(t, false)
}

func newFixedOriginFixtureWithVideo(t *testing.T, video bool) (*fixedOriginFixture, *mediator.Bus) {
	t.Helper()
	fixture := &fixedOriginFixture{input: Input{TenantID: identity.NewID(), AssetID: identity.NewID(), ParticipantID: identity.NewID(), TemplateID: identity.NewID(), ComparisonMode: catalog.FixedOrigin, RRule: "FREQ=MONTHLY", Timezone: "UTC", StartsAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), DeadlineMinutes: 60, IdempotencyKey: "schedule-fixed-origin"}, assetUnit: identity.NewID(), personUnit: identity.NewID(), resolvedID: identity.NewID(), video: video}
	definition, err := json.Marshal(catalog.TemplateDocument{DefaultComparisonMode: catalog.ChecklistOnly, IsVideo: video})
	if err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	for _, registration := range []struct {
		query   any
		handler func(context.Context, any) (any, error)
	}{
		{assetget.Query{}, func(context.Context, any) (any, error) {
			return assetcore.View{Asset: database.Asset{ID: fixture.input.AssetID, BusinessUnitID: fixture.assetUnit, Status: "ACTIVE"}}, nil
		}},
		{participantget.Query{}, func(context.Context, any) (any, error) {
			return participantcore.ParticipantView{Participant: database.Participant{BusinessUnitID: fixture.personUnit, Status: "ACTIVE"}, Selected: []identity.ID{identity.NewID()}}, nil
		}},
		{templateresolve.Query{}, func(context.Context, any) (any, error) {
			return templateresolve.Result{Version: database.TemplateVersion{DefinitionJSON: definition}}, nil
		}},
		{originresolve.Query{}, func(_ context.Context, raw any) (any, error) {
			fixture.originAsks++
			fixture.lastQuery = raw.(originresolve.Query)
			if fixture.lastQuery.MediaKind == "VIDEO" {
				return nil, apperror.New(apperror.InvalidInput, "referenceVersionId", "reference modality must match the inspection modality")
			}
			return originresolve.Result{Version: database.OriginVersion{ID: fixture.resolvedID, MediaKind: "PHOTO"}}, fixture.originErr
		}},
	} {
		if err := bus.RegisterQuery(registration.query, registration.handler); err != nil {
			t.Fatal(err)
		}
	}
	return fixture, bus
}

func principalContext(tenantID identity.ID, role string) context.Context {
	principal := requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{role}}
	return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: principal})
}

func TestCreateFixedOriginAuthorizesBeforeResolvingOrigin(t *testing.T) {
	fixture, bus := newFixedOriginFixture(t)
	_, err := (Service{Bus: bus, Authorizer: auth.Authorizer{}}).Create(principalContext(fixture.input.TenantID, auth.Viewer), fixture.input)
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.Forbidden {
		t.Fatalf("Create error = %v, want forbidden", err)
	}
	if fixture.originAsks != 0 {
		t.Fatal("origin was resolved before authorization")
	}
}

func TestCreateFixedOriginRequiresActiveOrigin(t *testing.T) {
	fixture, bus := newFixedOriginFixture(t)
	fixture.originErr = apperror.New(apperror.InvalidInput, "referenceVersionId", "active origin is required")
	_, err := (Service{Bus: bus, Authorizer: auth.Authorizer{}}).Create(principalContext(fixture.input.TenantID, auth.TenantAdmin), fixture.input)
	if !errors.Is(err, fixture.originErr) {
		t.Fatalf("Create error = %v, want %v", err, fixture.originErr)
	}
	if fixture.originAsks != 1 || fixture.lastQuery.VersionID != nil || fixture.lastQuery.AssetID != fixture.input.AssetID {
		t.Fatalf("origin query = %+v after %d asks", fixture.lastQuery, fixture.originAsks)
	}
}

func TestResolveFixedOriginValidatesActiveOriginWithTemplateMediaKind(t *testing.T) {
	fixture, bus := newFixedOriginFixture(t)
	if err := resolveFixedOrigin(principalContext(fixture.input.TenantID, auth.TenantAdmin), bus, fixture.input, "PHOTO"); err != nil {
		t.Fatalf("resolveFixedOrigin: %v", err)
	}
	if fixture.originAsks != 1 || fixture.lastQuery.MediaKind != "PHOTO" || fixture.lastQuery.VersionID != nil {
		t.Fatalf("origin query = %+v after %d asks", fixture.lastQuery, fixture.originAsks)
	}
}

func TestResolveFixedOriginValidatesExplicitVersionWithoutChangingMediaKind(t *testing.T) {
	fixture, bus := newFixedOriginFixture(t)
	requestedID := identity.NewID()
	fixture.input.ReferenceVersionID = &requestedID
	if err := resolveFixedOrigin(principalContext(fixture.input.TenantID, auth.TenantAdmin), bus, fixture.input, "PHOTO"); err != nil {
		t.Fatalf("resolveFixedOrigin: %v", err)
	}
	if fixture.originAsks != 1 || fixture.lastQuery.MediaKind != "PHOTO" || fixture.lastQuery.VersionID == nil || *fixture.lastQuery.VersionID != requestedID {
		t.Fatalf("origin query = %+v after %d asks", fixture.lastQuery, fixture.originAsks)
	}
}

func TestCreateFixedOriginRejectsPhotoOriginForVideoTemplate(t *testing.T) {
	fixture, bus := newFixedOriginFixtureWithVideo(t, true)
	_, err := (Service{Bus: bus, Authorizer: auth.Authorizer{}}).Create(principalContext(fixture.input.TenantID, auth.TenantAdmin), fixture.input)
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.InvalidInput {
		t.Fatalf("Create error = %v, want INVALID_INPUT", err)
	}
}

func TestCreateRejectsParticipantOutsideAuthorizedUnits(t *testing.T) {
	fixture, bus := newFixedOriginFixture(t)
	principal := requestctx.Principal{IdentityID: identity.NewID(), TenantID: fixture.input.TenantID, Roles: []string{auth.Manager}, Scopes: []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: fixture.assetUnit}}}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fixture.input.TenantID, Principal: principal})
	_, err := (Service{Bus: bus, Authorizer: auth.Authorizer{}}).Create(ctx, fixture.input)
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.Forbidden {
		t.Fatalf("Create error = %v, want forbidden for a participant from an inaccessible unit", err)
	}
	if fixture.originAsks != 0 {
		t.Fatal("origin was resolved before rejecting the participant's unit scope")
	}
}
