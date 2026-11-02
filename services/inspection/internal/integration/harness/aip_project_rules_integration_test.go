//go:build integration

package harness

import (
	"context"
	"fmt"
	"sync"
	"testing"

	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	projectcore "inspection/services/inspection/internal/features/projects/core"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

// projectRulesFixture is a tenant with an active asset and the production
// project service running on the runtime role (forced RLS).
type projectRulesFixture struct {
	h       *Harness
	fixture Task06Fixture
	service projectcore.Service
	admin   context.Context
}

func newProjectRulesFixture(t *testing.T) projectRulesFixture {
	t.Helper()
	ctx := context.Background()
	h, err := NewFromEnv(ctx)
	if err != nil {
		t.Skipf("integration dependencies unavailable: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, Task06FixtureOptions{ComparisonMode: "CURRENT_ONLY"})
	if err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := assetget.Setup(assetget.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	return projectRulesFixture{h: h, fixture: fixture, service: projectcore.Service{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}, admin: projectActor(fixture, auth.TenantAdmin)}
}

func projectActor(fixture Task06Fixture, role string) context.Context {
	return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fixture.TenantID, CorrelationID: "aip-project-rules", Principal: requestctx.Principal{IdentityID: fixture.IdentityID, TenantID: fixture.TenantID, Roles: []string{role}, ProductEntitlements: []string{auth.DashboardProduct}}})
}

func (p projectRulesFixture) create(t *testing.T, key string, ordered bool, stages ...string) projectcore.View {
	t.Helper()
	planned := make([]projectcore.PlannedStageInput, 0, len(stages))
	for _, name := range stages {
		planned = append(planned, projectcore.PlannedStageInput{Key: name, Label: "Etapa " + name})
	}
	view, err := p.service.Create(p.admin, projectcore.CreateInput{TenantID: p.fixture.TenantID, AssetID: p.fixture.AssetID, Name: "Projeto " + key, OrderedStages: ordered, Stages: planned, IdempotencyKey: "aip-" + key})
	if err != nil {
		t.Fatalf("create project %s: %v", key, err)
	}
	return view
}

func (p projectRulesFixture) start(t *testing.T, view projectcore.View, index int) (projectcore.View, error) {
	t.Helper()
	stage := view.Stages[index]
	return p.service.StartStage(p.admin, projectcore.StartStageInput{TenantID: p.fixture.TenantID, ProjectID: view.Project.ID, StageID: stage.ID, ExpectedProjectVersion: view.Project.Version, ExpectedStageVersion: stage.Version})
}

func (p projectRulesFixture) reload(t *testing.T, view projectcore.View) projectcore.View {
	t.Helper()
	current, err := p.service.Get(p.admin, p.fixture.TenantID, view.Project.ID)
	if err != nil {
		t.Fatal(err)
	}
	return current
}

func wantCode(t *testing.T, label string, err error, code apperror.Code) {
	t.Helper()
	if !apperror.Is(err, code) {
		t.Fatalf("%s: error = %v, want %s", label, err, code)
	}
}

// wantDenied accepts the two stable ways of refusing a caller: FORBIDDEN for a
// role that may not administer, NOT_FOUND where the role has no grant over the
// project and its existence stays hidden.
func wantDenied(t *testing.T, label string, err error) {
	t.Helper()
	if !apperror.Is(err, apperror.Forbidden) && !apperror.Is(err, apperror.NotFound) {
		t.Fatalf("%s: error = %v, want FORBIDDEN or NOT_FOUND", label, err)
	}
}

// IT-328 (construction): a later stage cannot start before its predecessor
// has a terminal decision when the project orders its stages; a project
// without that rule (cleaning) starts stages in any order.
func TestAIP_IT328OrderedStagesEnforceTheConfiguredPredecessorRule(t *testing.T) {
	p := newProjectRulesFixture(t)
	ordered := p.create(t, "ordered", true, "fundacao", "estrutura", "acabamento")
	if ordered.Stages[0].Status != projectcore.StageAvailable || ordered.Stages[1].Status != projectcore.StagePlanned {
		t.Fatalf("initial stage states = %s/%s, want AVAILABLE/PLANNED", ordered.Stages[0].Status, ordered.Stages[1].Status)
	}
	_, err := p.start(t, ordered, 1)
	wantCode(t, "start before the predecessor", err, apperror.InvalidState)
	ordered = p.reload(t, ordered)
	if ordered.Stages[1].Status != projectcore.StagePlanned {
		t.Fatalf("a refused start changed the stage to %s", ordered.Stages[1].Status)
	}
	skipped, err := p.service.SkipStage(p.admin, projectcore.TransitionInput{TenantID: p.fixture.TenantID, ProjectID: ordered.Project.ID, StageID: &ordered.Stages[0].ID, ExpectedVersion: ordered.Project.Version, Reason: "Etapa dispensada"})
	if err != nil {
		t.Fatalf("skip the predecessor: %v", err)
	}
	started, err := p.start(t, skipped, 1)
	if err != nil || started.Stages[1].Status != projectcore.StageInProgress {
		t.Fatalf("start after the predecessor decision: status=%v err=%v", started.Stages[1].Status, err)
	}
	// Starting the same stage again is a no-op, not a second start.
	if again, err := p.start(t, started, 1); err != nil || again.Project.Version != started.Project.Version {
		t.Fatalf("repeated start changed the project: version %d -> %d err=%v", started.Project.Version, again.Project.Version, err)
	}

	free := p.create(t, "free-order", false, "area-1", "area-2", "area-3")
	if started, err := p.start(t, free, 2); err != nil || started.Stages[2].Status != projectcore.StageInProgress {
		t.Fatalf("a project without the ordering rule refused the last stage: %v", err)
	}
}

// IT-325 (construction): an exceptional stage and the start of a planned stage
// racing on one project version leave one winner and a deterministic,
// gap-free stage order.
func TestAIP_IT325ExceptionalAndPlannedStartsRaceDeterministically(t *testing.T) {
	p := newProjectRulesFixture(t)
	project := p.create(t, "race", false, "planned-1", "planned-2")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := p.start(t, project, 1)
		results <- err
	}()
	go func() {
		defer wg.Done()
		_, err := p.service.AddExceptionalStage(p.admin, projectcore.ExceptionalStageInput{TenantID: p.fixture.TenantID, ProjectID: project.Project.ID, Key: "excecao", Label: "Etapa excepcional", Reason: "Retrabalho aprovado", ExpectedVersion: project.Project.Version, IdempotencyKey: "aip-exceptional"})
		results <- err
	}()
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			wins++
		case apperror.Is(err, apperror.Conflict):
			conflicts++
		default:
			t.Fatalf("unexpected race error: %v", err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("race outcome wins=%d conflicts=%d, want exactly one winner", wins, conflicts)
	}
	current := p.reload(t, project)
	if current.Project.Version != project.Project.Version+1 {
		t.Fatalf("project version = %d, want exactly one increment from %d", current.Project.Version, project.Project.Version)
	}
	for index, stage := range current.Stages {
		if stage.Position != index+1 {
			t.Fatalf("stage positions are not contiguous: %+v", current.Stages)
		}
	}
	// The loser retries on the new version and both effects end up applied in order.
	if _, err := p.service.AddExceptionalStage(p.admin, projectcore.ExceptionalStageInput{TenantID: p.fixture.TenantID, ProjectID: project.Project.ID, Key: "excecao", Label: "Etapa excepcional", Reason: "Retrabalho aprovado", ExpectedVersion: current.Project.Version, IdempotencyKey: "aip-exceptional"}); err != nil && !apperror.Is(err, apperror.Conflict) {
		t.Fatalf("retry: %v", err)
	}
	final := p.reload(t, project)
	exceptional := 0
	for index, stage := range final.Stages {
		if stage.Position != index+1 {
			t.Fatalf("final stage positions are not contiguous: %+v", final.Stages)
		}
		if stage.Kind == "EXCEPTIONAL" {
			exceptional++
		}
	}
	if exceptional > 1 {
		t.Fatalf("one idempotent exceptional stage created %d stages", exceptional)
	}
}

// IT-323 and IT-330 (construction): a project accepts exactly the platform
// stage capacity, the next stage is refused without disturbing the accepted
// history, and the full timeline stays navigable in order.
func TestAIP_IT323IT330StageCapacityAndLongTimelineStayNavigable(t *testing.T) {
	p := newProjectRulesFixture(t)
	keys := make([]string, 0, catalog.MaxStages)
	for i := 1; i <= catalog.MaxStages; i++ {
		keys = append(keys, fmt.Sprintf("e%03d", i))
	}
	overflow := make([]projectcore.PlannedStageInput, 0, catalog.MaxStages+1)
	for _, key := range append(append([]string(nil), keys...), "extra") {
		overflow = append(overflow, projectcore.PlannedStageInput{Key: key, Label: "Etapa " + key})
	}
	_, err := p.service.Create(p.admin, projectcore.CreateInput{TenantID: p.fixture.TenantID, AssetID: p.fixture.AssetID, Name: "Projeto acima do limite", Stages: overflow, IdempotencyKey: "aip-over-capacity"})
	wantCode(t, "project above the stage capacity", err, apperror.InvalidInput)
	var rejected int64
	if err := p.h.AdminDB.Model(&database.Project{}).Where("tenant_id=? AND idempotency_key=?", p.fixture.TenantID, "aip-over-capacity").Count(&rejected).Error; err != nil || rejected != 0 {
		t.Fatalf("a rejected project left %d rows (err=%v)", rejected, err)
	}

	project := p.create(t, "full", false, keys...)
	if len(project.Stages) != catalog.MaxStages || len(project.Transitions) != catalog.MaxStages {
		t.Fatalf("full project has %d stages and %d transitions, want %d each", len(project.Stages), len(project.Transitions), catalog.MaxStages)
	}
	_, err = p.service.AddExceptionalStage(p.admin, projectcore.ExceptionalStageInput{TenantID: p.fixture.TenantID, ProjectID: project.Project.ID, Key: "alem-do-limite", Label: "Além do limite", Reason: "Teste de capacidade", ExpectedVersion: project.Project.Version, IdempotencyKey: "aip-over-capacity-exceptional"})
	wantCode(t, "exceptional stage above the capacity", err, apperror.InvalidState)
	after := p.reload(t, project)
	if len(after.Stages) != catalog.MaxStages || after.Project.Version != project.Project.Version {
		t.Fatalf("a refused stage changed the project: stages=%d version %d -> %d", len(after.Stages), project.Project.Version, after.Project.Version)
	}
	for index, stage := range after.Stages {
		if stage.Key != keys[index] || stage.Position != index+1 {
			t.Fatalf("stage %d = %s at %d, want %s at %d", index, stage.Key, stage.Position, keys[index], index+1)
		}
	}
	page, err := p.service.List(p.admin, p.fixture.TenantID, 100, "")
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("project listing: %d items err=%v", len(page.Items), err)
	}
}

// IT-329 (construction) and IT-339 (cleaning): a closed project accepts no new
// work, and reopening needs an audited reason before it accepts work again.
func TestAIP_IT329IT339ClosedProjectsBlockNewWorkUntilAuditedReopening(t *testing.T) {
	p := newProjectRulesFixture(t)
	project := p.create(t, "closing", false, "unica")
	skipped, err := p.service.SkipStage(p.admin, projectcore.TransitionInput{TenantID: p.fixture.TenantID, ProjectID: project.Project.ID, StageID: &project.Stages[0].ID, ExpectedVersion: project.Project.Version, Reason: "Sem trabalho previsto"})
	if err != nil {
		t.Fatal(err)
	}
	closed, err := p.service.Close(p.admin, projectcore.TransitionInput{TenantID: p.fixture.TenantID, ProjectID: project.Project.ID, ExpectedVersion: skipped.Project.Version})
	if err != nil || closed.Project.Status != projectcore.ProjectClosed {
		t.Fatalf("close: status=%s err=%v", closed.Project.Status, err)
	}
	association := func() error {
		return p.h.WithinTenant(context.Background(), p.fixture.TenantID, func(tx *gorm.DB) error {
			return projectcore.ValidateAssociation(tx, p.fixture.TenantID, p.fixture.AssetID, &project.Project.ID, nil)
		})
	}
	wantCode(t, "capture for a closed project", association(), apperror.InvalidState)
	_, err = p.service.AddExceptionalStage(p.admin, projectcore.ExceptionalStageInput{TenantID: p.fixture.TenantID, ProjectID: project.Project.ID, Key: "nova", Label: "Nova etapa", Reason: "Pedido do cliente", ExpectedVersion: closed.Project.Version, IdempotencyKey: "aip-closed-stage"})
	wantCode(t, "new stage on a closed project", err, apperror.InvalidState)
	_, err = p.service.Reopen(p.admin, projectcore.TransitionInput{TenantID: p.fixture.TenantID, ProjectID: project.Project.ID, ExpectedVersion: closed.Project.Version})
	wantCode(t, "reopening without a reason", err, apperror.InvalidInput)
	if stillClosed := p.reload(t, project); stillClosed.Project.Status != projectcore.ProjectClosed {
		t.Fatalf("a refused reopening changed the status to %s", stillClosed.Project.Status)
	}
	reopened, err := p.service.Reopen(p.admin, projectcore.TransitionInput{TenantID: p.fixture.TenantID, ProjectID: project.Project.ID, ExpectedVersion: closed.Project.Version, Reason: "Garantia do serviço"})
	if err != nil || reopened.Project.Status != projectcore.ProjectActive {
		t.Fatalf("reopen: status=%s err=%v", reopened.Project.Status, err)
	}
	if err := association(); err != nil {
		t.Fatalf("capture after the audited reopening: %v", err)
	}
	var audited database.StageTransition
	if err := p.h.AdminDB.Where("tenant_id=? AND project_id=? AND stage_id IS NULL AND to_state=?", p.fixture.TenantID, project.Project.ID, projectcore.ProjectActive).Order("occurred_at DESC").Take(&audited).Error; err != nil || audited.Reason != "Garantia do serviço" {
		t.Fatalf("reopening is not audited with its reason: %+v err=%v", audited, err)
	}
}

// IT-324 (construction) and IT-334 (cleaning): participants and read-only
// roles cannot administer projects, and another tenant's projects do not exist
// for them.
func TestAIP_IT324IT334ProjectAdministrationIsInternalAndTenantScoped(t *testing.T) {
	p := newProjectRulesFixture(t)
	project := p.create(t, "restricted", true, "primeira", "segunda")
	for _, role := range []string{auth.Viewer, auth.Employee, auth.CustomerViewer, "CONSTRUCTION_RESPONSIBLE", "CLEANING_EXECUTOR"} {
		actor := projectActor(p.fixture, role)
		t.Run(role, func(t *testing.T) {
			_, err := p.service.StartStage(actor, projectcore.StartStageInput{TenantID: p.fixture.TenantID, ProjectID: project.Project.ID, StageID: project.Stages[0].ID, ExpectedProjectVersion: project.Project.Version, ExpectedStageVersion: project.Stages[0].Version})
			wantDenied(t, "start", err)
			_, err = p.service.AddExceptionalStage(actor, projectcore.ExceptionalStageInput{TenantID: p.fixture.TenantID, ProjectID: project.Project.ID, Key: "x", Label: "x", Reason: "x", ExpectedVersion: project.Project.Version, IdempotencyKey: "aip-forbidden-" + role})
			wantDenied(t, "exceptional stage", err)
			_, err = p.service.Close(actor, projectcore.TransitionInput{TenantID: p.fixture.TenantID, ProjectID: project.Project.ID, ExpectedVersion: project.Project.Version})
			wantDenied(t, "close", err)
		})
	}
	if current := p.reload(t, project); current.Project.Version != project.Project.Version || current.Stages[0].Status != projectcore.StageAvailable {
		t.Fatalf("denied callers changed the project: version %d -> %d", project.Project.Version, current.Project.Version)
	}
	other, err := p.h.SeedTask06Fixture(context.Background(), Task06FixtureOptions{ComparisonMode: "CURRENT_ONLY"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.service.Get(projectActor(other, auth.TenantAdmin), p.fixture.TenantID, project.Project.ID)
	wantCode(t, "another tenant's administrator reading the project", err, apperror.NotFound)
	_, err = p.service.Get(projectActor(other, auth.TenantAdmin), other.TenantID, project.Project.ID)
	wantCode(t, "the project looked up in the caller's tenant", err, apperror.NotFound)
}
