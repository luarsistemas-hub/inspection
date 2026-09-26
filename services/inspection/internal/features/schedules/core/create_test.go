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
	originAsks int
	originErr  error
	lastQuery  originresolve.Query
}

func newFixedOriginFixture(t *testing.T) (*fixedOriginFixture, *mediator.Bus) {
	t.Helper()
	fixture := &fixedOriginFixture{input: Input{TenantID: identity.NewID(), AssetID: identity.NewID(), ParticipantID: identity.NewID(), TemplateID: identity.NewID(), RRule: "FREQ=MONTHLY", Timezone: "UTC", StartsAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), DeadlineMinutes: 60, IdempotencyKey: "schedule-fixed-origin"}}
	definition, err := json.Marshal(catalog.TemplateDocument{ComparisonMode: catalog.FixedOrigin})
	if err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	for _, registration := range []struct {
		query   any
		handler func(context.Context, any) (any, error)
	}{
		{assetget.Query{}, func(context.Context, any) (any, error) {
			return assetcore.View{Asset: database.Asset{ID: fixture.input.AssetID, BusinessUnitID: identity.NewID(), Status: "ACTIVE"}, Assignments: []database.AssetAssignment{{ParticipantID: fixture.input.ParticipantID, Active: true}}}, nil
		}},
		{participantget.Query{}, func(context.Context, any) (any, error) {
			return participantcore.ParticipantView{Participant: database.Participant{Status: "ACTIVE"}, Selected: []identity.ID{identity.NewID()}}, nil
		}},
		{templateresolve.Query{}, func(context.Context, any) (any, error) {
			return templateresolve.Result{Version: database.TemplateVersion{DefinitionJSON: definition}}, nil
		}},
		{originresolve.Query{}, func(_ context.Context, raw any) (any, error) {
			fixture.originAsks++
			fixture.lastQuery = raw.(originresolve.Query)
			return originresolve.Result{}, fixture.originErr
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

func TestCreateFixedOriginRequiresActiveOriginWithoutPinningIt(t *testing.T) {
	fixture, bus := newFixedOriginFixture(t)
	fixture.originErr = apperror.New(apperror.InvalidState, "referenceVersionId", "active origin is required")
	_, err := (Service{Bus: bus, Authorizer: auth.Authorizer{}}).Create(principalContext(fixture.input.TenantID, auth.TenantAdmin), fixture.input)
	if !errors.Is(err, fixture.originErr) {
		t.Fatalf("Create error = %v, want %v", err, fixture.originErr)
	}
	if fixture.originAsks != 1 || fixture.lastQuery.VersionID != nil || fixture.lastQuery.TemplateID != fixture.input.TemplateID {
		t.Fatalf("origin query = %+v after %d asks", fixture.lastQuery, fixture.originAsks)
	}
}
