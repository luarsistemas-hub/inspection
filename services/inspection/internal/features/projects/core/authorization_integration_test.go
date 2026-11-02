//go:build integration

package core

import (
	"context"
	"os"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

type projectCallAuthorizationProbe struct {
	resolved []requestctx.Scope
	newWork  []requestctx.Scope
}

func (p *projectCallAuthorizationProbe) Resolve(_ context.Context, _, _ identity.ID, target requestctx.Scope) (auth.ScopeDecision, error) {
	p.resolved = append(p.resolved, target)
	return auth.ScopeDecision{Allowed: true}, nil
}

func (p *projectCallAuthorizationProbe) ResolveNewWork(_ context.Context, _, _ identity.ID, target requestctx.Scope) (auth.ScopeDecision, bool, error) {
	p.newWork = append(p.newWork, target)
	return auth.ScopeDecision{}, false, nil
}

func TestTFE_IT_431StartStageAuthorizesBusinessUnitThenProjectNewWork(t *testing.T) {
	h, fixture, ctx := newProjectAuthorizationFixture(t)
	probe := &projectCallAuthorizationProbe{}
	service := Service{DB: h.DB, Authorizer: auth.Authorizer{Scopes: probe}}
	_, err := service.StartStage(ctx, StartStageInput{TenantID: fixture.TenantID, ProjectID: fixture.ProjectID, StageID: identity.NewID(), ExpectedProjectVersion: 1})
	if err == nil {
		t.Fatal("StartStage should stop when project new-work scope is denied")
	}
	assertProjectCallScopes(t, probe, fixture.BusinessUnitID, fixture.ProjectID)
}

func TestTFE_IT_431AddExceptionalStageAuthorizesBusinessUnitThenProjectNewWork(t *testing.T) {
	h, fixture, ctx := newProjectAuthorizationFixture(t)
	probe := &projectCallAuthorizationProbe{}
	service := Service{DB: h.DB, Authorizer: auth.Authorizer{Scopes: probe}}
	_, err := service.AddExceptionalStage(ctx, ExceptionalStageInput{TenantID: fixture.TenantID, ProjectID: fixture.ProjectID, Key: "extra", Label: "Extra", Reason: "approved exception", ExpectedVersion: 1, IdempotencyKey: "auth-call-test"})
	if err == nil {
		t.Fatal("AddExceptionalStage should stop when project new-work scope is denied")
	}
	assertProjectCallScopes(t, probe, fixture.BusinessUnitID, fixture.ProjectID)
}

func newProjectAuthorizationFixture(t *testing.T) (*harness.Harness, harness.Task06Fixture, context.Context) {
	t.Helper()
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
	ctx = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: fixture.TenantID, Principal: requestctx.Principal{IdentityID: fixture.IdentityID, MembershipID: fixture.MembershipID, TenantID: fixture.TenantID, Roles: []string{auth.Manager}}})
	return h, fixture, ctx
}

func assertProjectCallScopes(t *testing.T, probe *projectCallAuthorizationProbe, businessUnitID, projectID identity.ID) {
	t.Helper()
	if len(probe.resolved) != 2 || probe.resolved[0].Kind != "BUSINESS_UNIT" || probe.resolved[0].ID != businessUnitID || probe.resolved[1].Kind != "BUSINESS_UNIT" || probe.resolved[1].ID != businessUnitID {
		t.Fatalf("regular authorization calls=%+v, want read then mutation BUSINESS_UNIT %s", probe.resolved, businessUnitID)
	}
	if len(probe.newWork) != 1 || probe.newWork[0].Kind != "PROJECT" || probe.newWork[0].ID != projectID {
		t.Fatalf("new-work authorization calls=%+v, want PROJECT %s", probe.newWork, projectID)
	}
}
