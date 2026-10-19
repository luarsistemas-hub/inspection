package reanalyze_video_inspection

import (
	"context"
	"errors"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSetupValidatesDependenciesAndRegistersCommand(t *testing.T) {
	db := reanalysisDB(t)
	for _, deps := range []Dependencies{{Bus: mediator.New()}, {DB: db}} {
		if err := Setup(deps); err == nil {
			t.Fatal("Setup succeeded with a missing required dependency")
		}
	}
	bus := mediator.New()
	if err := Setup(Dependencies{DB: db, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	_, err := bus.Send(context.Background(), Command{TenantID: identity.NewID(), InspectionID: identity.NewID(), ExpectedPlanID: identity.NewID(), ClientMutationID: "setup"})
	if err == nil {
		t.Fatal("registered command unexpectedly succeeded without request authorization")
	}
}

func TestReanalysisQueuesNextGenerationAndReplayIsIdempotent(t *testing.T) {
	db := reanalysisDB(t)
	fixture := seedReanalysis(t, db)
	bus := mediator.New()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	if err := Setup(Dependencies{DB: db, Bus: bus, Now: func() time.Time { return now }, Within: sqliteWithin(db)}); err != nil {
		t.Fatal(err)
	}
	ctx := reanalysisContext(fixture)
	command := Command{TenantID: fixture.tenantID, InspectionID: fixture.inspectionID, ExpectedPlanID: fixture.planID, ClientMutationID: "reanalyze-1"}
	firstRaw, err := bus.Send(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	first := firstRaw.(Result)
	if first.Generation != 2 || first.PlanID == fixture.planID {
		t.Fatalf("first result = %+v, want new generation 2", first)
	}
	replayRaw, err := bus.Send(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if replay := replayRaw.(Result); replay != first {
		t.Fatalf("replay = %+v, want original %+v", replay, first)
	}
	var plan database.VideoPlan
	if err := db.Table("analysis.video_plans").Where("id=?", first.PlanID).Take(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if plan.Status != "BUILDING" || plan.SubmissionID != fixture.submissionID || plan.CurrentPreparationID != fixture.preparationID || plan.ReferenceVersionID == nil || *plan.ReferenceVersionID != fixture.referenceID {
		t.Fatalf("queued plan lost pinned inputs or wasn't queued: %+v", plan)
	}
	var auditCount int64
	if err := db.Table("audit.events").Where("action=?", "video.inspection_reanalyzed").Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("audit count = %d, want one", auditCount)
	}
}

func TestReanalysisRejectsStaleAndBuildingPlans(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *gorm.DB, reanalysisFixture)
		stale bool
		want  apperror.Code
	}{
		{name: "stale expected plan", setup: func(t *testing.T, db *gorm.DB, f reanalysisFixture) {}, stale: true, want: apperror.Conflict},
		{name: "plan in progress", setup: func(t *testing.T, db *gorm.DB, f reanalysisFixture) {
			if err := db.Exec(`INSERT INTO analysis.video_plans (id,tenant_id,inspection_id,submission_id,reference_version_id,origin_preparation_id,current_preparation_id,generation,plan_digest,status,next_attempt_at,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, identity.NewID(), f.tenantID, f.inspectionID, f.submissionID, f.referenceID, f.originPreparationID, f.preparationID, 2, "", "BUILDING", time.Now(), time.Now()).Error; err != nil {
				t.Fatal(err)
			}
		}, want: apperror.InvalidState},
		{name: "failed later generation", setup: func(t *testing.T, db *gorm.DB, f reanalysisFixture) {
			if err := db.Exec(`INSERT INTO analysis.video_plans (id,tenant_id,inspection_id,submission_id,reference_version_id,origin_preparation_id,current_preparation_id,generation,plan_digest,status,next_attempt_at,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, identity.NewID(), f.tenantID, f.inspectionID, f.submissionID, f.referenceID, f.originPreparationID, f.preparationID, 2, "", "FAILED", time.Now(), time.Now()).Error; err != nil {
				t.Fatal(err)
			}
		}, want: apperror.InvalidState},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := reanalysisDB(t)
			fixture := seedReanalysis(t, db)
			tc.setup(t, db, fixture)
			bus := mediator.New()
			if err := Setup(Dependencies{DB: db, Bus: bus, Within: sqliteWithin(db)}); err != nil {
				t.Fatal(err)
			}
			expectedPlanID := fixture.planID
			if tc.stale {
				expectedPlanID = identity.NewID()
			}
			command := Command{TenantID: fixture.tenantID, InspectionID: fixture.inspectionID, ExpectedPlanID: expectedPlanID, ClientMutationID: "rejected"}
			_, err := bus.Send(reanalysisContext(fixture), command)
			if code, _, _ := apperror.Public(err); code != tc.want {
				t.Fatalf("error code = %q, want %q (error: %v)", code, tc.want, err)
			}
		})
	}
}

func TestReanalysisRejectsMutationIDReusedForDifferentExpectedPlan(t *testing.T) {
	db := reanalysisDB(t)
	fixture := seedReanalysis(t, db)
	bus := mediator.New()
	if err := Setup(Dependencies{DB: db, Bus: bus, Within: sqliteWithin(db)}); err != nil {
		t.Fatal(err)
	}
	command := Command{TenantID: fixture.tenantID, InspectionID: fixture.inspectionID, ExpectedPlanID: fixture.planID, ClientMutationID: "same-key"}
	if _, err := bus.Send(reanalysisContext(fixture), command); err != nil {
		t.Fatal(err)
	}
	command.ExpectedPlanID = identity.NewID()
	_, err := bus.Send(reanalysisContext(fixture), command)
	if code, _, _ := apperror.Public(err); code != apperror.Conflict {
		t.Fatalf("error code = %q, want CONFLICT (error %v)", code, err)
	}
}

func TestReanalysisRejectsInvalidInputAndPropagatesDependencyFailure(t *testing.T) {
	db := reanalysisDB(t)
	bus := mediator.New()
	if err := Setup(Dependencies{DB: db, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	_, err := bus.Send(context.Background(), Command{TenantID: identity.NewID()})
	if code, _, _ := apperror.Public(err); code != apperror.InvalidInput {
		t.Fatalf("invalid command error code = %q, want INVALID_INPUT", code)
	}

	dependencyFailure := context.DeadlineExceeded
	failingBus := mediator.New()
	if err := Setup(Dependencies{DB: db, Bus: failingBus, Within: func(context.Context, identity.ID, func(*gorm.DB) error) error { return dependencyFailure }}); err != nil {
		t.Fatal(err)
	}
	command := Command{TenantID: identity.NewID(), InspectionID: identity.NewID(), ExpectedPlanID: identity.NewID(), ClientMutationID: "db-outage"}
	_, err = failingBus.Send(context.Background(), command)
	if !errors.Is(err, dependencyFailure) {
		t.Fatalf("dependency failure = %v, want %v", err, dependencyFailure)
	}
}

type reanalysisFixture struct {
	tenantID, inspectionID, assetID, businessUnitID, planID       identity.ID
	submissionID, preparationID, originPreparationID, referenceID identity.ID
}

func reanalysisDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"inspections", "assets", "analysis", "audit"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`CREATE TABLE assets.assets (id text, tenant_id text, business_unit_id text)`,
		`CREATE TABLE inspections.inspections (id text, tenant_id text, asset_id text, media_kind text, active_video_plan_id text)`,
		`CREATE TABLE analysis.video_plans (id text, tenant_id text, inspection_id text, submission_id text, parent_plan_id text, reference_version_id text, origin_preparation_id text, current_preparation_id text, generation integer, plan_digest text, status text, lease_token text, lease_until datetime, attempts integer, next_attempt_at datetime, error_code text, client_mutation_id text, expected_plan_id text, sealed_at datetime, created_at datetime)`,
		`CREATE TABLE audit.events (id text, tenant_id text, actor_id text, action text, target_type text, target_id text, outcome text, reason text, correlation_id text, occurred_at datetime)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func seedReanalysis(t *testing.T, db *gorm.DB) reanalysisFixture {
	t.Helper()
	f := reanalysisFixture{tenantID: identity.NewID(), inspectionID: identity.NewID(), assetID: identity.NewID(), businessUnitID: identity.NewID(), planID: identity.NewID(), submissionID: identity.NewID(), preparationID: identity.NewID(), originPreparationID: identity.NewID(), referenceID: identity.NewID()}
	for _, row := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO assets.assets (id,tenant_id,business_unit_id) VALUES (?,?,?)`, []any{f.assetID, f.tenantID, f.businessUnitID}},
		{`INSERT INTO inspections.inspections (id,tenant_id,asset_id,media_kind,active_video_plan_id) VALUES (?,?,?,?,?)`, []any{f.inspectionID, f.tenantID, f.assetID, "VIDEO", f.planID}},
		{`INSERT INTO analysis.video_plans (id,tenant_id,inspection_id,submission_id,reference_version_id,origin_preparation_id,current_preparation_id,generation,plan_digest,status,next_attempt_at,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, []any{f.planID, f.tenantID, f.inspectionID, f.submissionID, f.referenceID, f.originPreparationID, f.preparationID, 1, "sealed-digest", "SEALED", time.Now(), time.Now()}},
	} {
		if err := db.Exec(row.sql, row.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func reanalysisContext(f reanalysisFixture) context.Context {
	principal := requestctx.Principal{IdentityID: identity.NewID(), TenantID: f.tenantID, Roles: []string{auth.Manager}, Scopes: []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: f.businessUnitID}}}
	return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: f.tenantID, Principal: principal, CorrelationID: "test-reanalyze"})
}

func sqliteWithin(db *gorm.DB) func(context.Context, identity.ID, func(*gorm.DB) error) error {
	return func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
}
