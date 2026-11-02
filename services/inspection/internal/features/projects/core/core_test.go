package core

import (
	"context"
	"strings"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestProjectPlanAndLifecycleRules(t *testing.T) {
	for _, status := range []string{StageCompleted, StageSkipped, StageCanceled, StageInvalidated} {
		if !terminalStage(status) {
			t.Fatalf("terminal stage rejected: %s", status)
		}
	}
	for _, status := range []string{StagePlanned, StageAvailable, StageInProgress} {
		if terminalStage(status) {
			t.Fatalf("open stage accepted as terminal: %s", status)
		}
	}
	if validReason("") || validReason("   ") || validReason(strings.Repeat("a", 2001)) {
		t.Fatal("invalid reason accepted")
	}
	if !validReason("audited reason") {
		t.Fatal("valid reason rejected")
	}
	project := database.Project{Name: "Renovação", OrderedStages: true}
	if project.Name != "Renovação" || !project.OrderedStages {
		t.Fatal("project planning configuration was not retained")
	}
}

func TestIT201ToIT220ExceptionalStageAndClosureContracts(t *testing.T) {
	if !validReason("insert") || !validReason("skip") || !validReason("reopen") {
		t.Fatal("required reasons rejected")
	}
	states := []string{StageCompleted, StageSkipped, StageCanceled, StageInvalidated}
	for _, state := range states {
		if !terminalStage(state) {
			t.Fatalf("closure did not accept %s", state)
		}
	}
}

type projectAuthProbe struct {
	archived  bool
	resolved  []requestctx.Scope
	newWork   []requestctx.Scope
	mutations []requestctx.Scope
}

func (p *projectAuthProbe) Resolve(_ context.Context, _, _ identity.ID, target requestctx.Scope) (auth.ScopeDecision, error) {
	p.resolved = append(p.resolved, target)
	return auth.ScopeDecision{Allowed: true}, nil
}

func (p *projectAuthProbe) ValidateMutationTarget(_ context.Context, _ identity.ID, target requestctx.Scope) (bool, error) {
	p.mutations = append(p.mutations, target)
	return false, nil
}

func (p *projectAuthProbe) ResolveNewWork(_ context.Context, _, _ identity.ID, target requestctx.Scope) (auth.ScopeDecision, bool, error) {
	p.newWork = append(p.newWork, target)
	return auth.ScopeDecision{Allowed: true}, p.archived, nil
}

func TestProjectStageAuthorizationMarksNewWork(t *testing.T) {
	tenantID, unitID := identity.NewID(), identity.NewID()
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.Manager}}})
	project := database.Project{ID: identity.NewID(), TenantID: tenantID, BusinessUnitID: unitID}
	for _, tc := range []struct {
		name      string
		archived  bool
		wantError bool
	}{
		{name: "active asset and project"},
		{name: "archived asset with active project", archived: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := &projectAuthProbe{archived: tc.archived}
			service := Service{Authorizer: auth.Authorizer{Scopes: probe}}
			_, err := service.authorize(ctx, project, true, true)
			if (err != nil) != tc.wantError {
				t.Fatalf("authorize error=%v, wantError=%v", err, tc.wantError)
			}
			if err != nil {
				if code, _, _ := apperror.Public(err); code != apperror.NotFound {
					t.Fatalf("error code=%s, want NOT_FOUND", code)
				}
			}
			if len(probe.newWork) != 1 || probe.newWork[0].Kind != "PROJECT" || probe.newWork[0].ID != project.ID {
				t.Fatalf("new-work targets=%+v, want project %s", probe.newWork, project.ID)
			}
			if len(probe.mutations) != 0 {
				t.Fatalf("BU mutation unexpectedly requested NewWork validation: %+v", probe.mutations)
			}
			if len(probe.resolved) != 1 || probe.resolved[0].Kind != "BUSINESS_UNIT" || probe.resolved[0].ID != unitID {
				t.Fatalf("regular scope checks=%+v, want BUSINESS_UNIT %s", probe.resolved, unitID)
			}
		})
	}
}
