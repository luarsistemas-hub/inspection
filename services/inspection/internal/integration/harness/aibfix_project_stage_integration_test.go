//go:build integration

package harness

import (
	"context"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	inspectioncore "inspection/services/inspection/internal/features/inspections/core"
	projectcore "inspection/services/inspection/internal/features/projects/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AIBFIX-M3: classifying an inspection completes its project stage in the same
// transaction (never a SKIPPED one), which lets the project close and starts its
// retention clock.
func TestAIBFIX_M3ClassifyingTheInspectionCompletesTheProjectStageAndAllowsClosing(t *testing.T) {
	e := newAIPEnv(t)
	f := e.f
	e.must(e.h.AdminDB.Exec("UPDATE projects.project_stages SET status='IN_PROGRESS' WHERE tenant_id=? AND id=?", f.TenantID, f.StageID).Error)
	e.must(e.schedule(f.DraftID))
	e.must(e.process(e.jobFor(e.key), aipOK(aipResult(nil)), 0))

	principal := requestctx.Principal{IdentityID: f.IdentityID, MembershipID: f.MembershipID, TenantID: f.TenantID, Roles: []string{auth.TenantAdmin}}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: f.TenantID, Principal: principal, CorrelationID: "aibfix-m3"})
	service := projectcore.Service{DB: e.h.DB, Authorizer: auth.Authorizer{}}
	project, err := service.Get(ctx, f.TenantID, f.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Close(ctx, projectcore.TransitionInput{TenantID: f.TenantID, ProjectID: f.ProjectID, ExpectedVersion: project.Project.Version}); err == nil || !strings.Contains(err.Error(), "concluídas ou dispensadas") {
		t.Fatalf("closing with an in-progress stage: err=%v, want the Portuguese terminal-decision refusal", err)
	}

	e.must(e.classify(false))

	var stage database.ProjectStage
	e.must(e.h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, f.StageID).Take(&stage).Error)
	if stage.Status != "COMPLETED" || stage.Version != 2 {
		t.Fatalf("stage after the inspection completed = %s v%d, want COMPLETED v2", stage.Status, stage.Version)
	}
	var transitions []database.StageTransition
	e.must(e.h.AdminDB.Where("tenant_id=? AND project_id=? AND stage_id=?", f.TenantID, f.ProjectID, f.StageID).Find(&transitions).Error)
	if len(transitions) != 1 || transitions[0].FromState != "IN_PROGRESS" || transitions[0].ToState != "COMPLETED" || transitions[0].ActorID != f.TenantID {
		t.Fatalf("stage transitions = %+v, want one IN_PROGRESS -> COMPLETED by the system actor", transitions)
	}
	wantEvent := identity.ID(uuid.NewSHA1(uuid.Nil, []byte("project-stage-completed:"+f.TenantID.String()+":"+f.StageID.String())))
	var outbox []database.OutboxIntent
	e.must(e.h.AdminDB.Where("tenant_id=? AND type='project.stage_changed.v1'", f.TenantID).Find(&outbox).Error)
	if len(outbox) != 1 || outbox[0].ID != wantEvent {
		t.Fatalf("stage events = %+v, want the deterministic completion event", outbox)
	}
	var inspection database.Inspection
	e.must(e.h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, f.InspectionID).Take(&inspection).Error)
	if inspection.Status != "COMPLETED" {
		t.Fatalf("inspection status = %s", inspection.Status)
	}

	closed, err := service.Close(ctx, projectcore.TransitionInput{TenantID: f.TenantID, ProjectID: f.ProjectID, ExpectedVersion: project.Project.Version + 1})
	if err != nil {
		t.Fatalf("closing after the stage completed: %v", err)
	}
	var row database.Project
	e.must(e.h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, f.ProjectID).Take(&row).Error)
	if closed.Project.Status != "CLOSED" || row.ClosedAt == nil {
		t.Fatalf("closed project = %s, closed_at=%v; closing must start the retention clock", closed.Project.Status, row.ClosedAt)
	}
	reopened, err := service.Reopen(ctx, projectcore.TransitionInput{TenantID: f.TenantID, ProjectID: f.ProjectID, ExpectedVersion: closed.Project.Version, Reason: "ajuste solicitado"})
	if err != nil {
		t.Fatal(err)
	}
	var afterReopen database.Project
	e.must(e.h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, f.ProjectID).Take(&afterReopen).Error)
	if reopened.Project.Status != "ACTIVE" || afterReopen.ClosedAt != nil {
		t.Fatalf("reopened project = %s, closed_at=%v; reopening must leave retention", reopened.Project.Status, afterReopen.ClosedAt)
	}
}

// AIBFIX-M3: a skipped stage is never overwritten and a stage with another open
// inspection stays open until the last inspection finishes.
func TestAIBFIX_M3SkippedAndSharedStagesAreNotCompletedEarly(t *testing.T) {
	e := newAIPEnv(t)
	f := e.f
	complete := func() bool {
		t.Helper()
		var changed bool
		e.must(e.tenant(func(tx *gorm.DB) error {
			var inspection database.Inspection
			if err := tx.Where("tenant_id=? AND id=?", f.TenantID, f.InspectionID).Take(&inspection).Error; err != nil {
				return err
			}
			var err error
			changed, err = projectcore.CompleteStageOfInspection(context.Background(), tx, inspection, inspection.UpdatedAt, "aibfix-m3-complete")
			return err
		}))
		return changed
	}
	setStage := func(status string) {
		t.Helper()
		e.must(e.h.AdminDB.Exec("UPDATE projects.project_stages SET status=? WHERE tenant_id=? AND id=?", status, f.TenantID, f.StageID).Error)
	}
	setStage("SKIPPED")
	if complete() {
		t.Fatal("a SKIPPED stage was overwritten")
	}
	setStage("AVAILABLE")
	e.must(e.h.AdminDB.Exec("UPDATE inspections.inspections SET status='COMPLETED' WHERE tenant_id=? AND id=?", f.TenantID, f.InspectionID).Error)
	var template database.Inspection
	e.must(e.h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, f.InspectionID).Take(&template).Error)
	other := template
	other.ID, other.SourceKey, other.Status = identity.NewID(), "aibfix-other-"+identity.NewID().String(), "ANALYZING"
	e.must(e.h.AdminDB.Create(&other).Error)
	if complete() {
		t.Fatal("the stage completed while another inspection of it was still open")
	}
	e.must(e.h.AdminDB.Exec("UPDATE inspections.inspections SET status='COMPLETED' WHERE tenant_id=? AND id=?", f.TenantID, other.ID).Error)
	if !complete() {
		t.Fatal("the stage did not complete after its last inspection finished")
	}
	if complete() {
		t.Fatal("a completed stage changed again")
	}
	var count int64
	e.must(e.h.AdminDB.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='project.stage_changed.v1'", f.TenantID).Count(&count).Error)
	if count != 1 {
		t.Fatalf("stage completion events = %d, want exactly one", count)
	}
}

func aibfixSetStatus(e *aipEnv, id identity.ID, status string) {
	e.t.Helper()
	e.must(e.h.AdminDB.Exec("UPDATE inspections.inspections SET status=? WHERE tenant_id=? AND id=?", status, e.f.TenantID, id).Error)
}

// aibfixSecondInspection adds a second inspection to the fixture's stage.
func aibfixSecondInspection(e *aipEnv, status string) identity.ID {
	e.t.Helper()
	var template database.Inspection
	e.must(e.h.AdminDB.Where("tenant_id=? AND id=?", e.f.TenantID, e.f.InspectionID).Take(&template).Error)
	other := template
	other.ID, other.SourceKey, other.Status, other.EvidenceCount = identity.NewID(), "aibfix-second-"+identity.NewID().String(), status, 0
	e.must(e.h.AdminDB.Create(&other).Error)
	aibfixResponsibility(e, other.ID)
	return other.ID
}

func aibfixResponsibility(e *aipEnv, inspectionID identity.ID) {
	e.t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	e.must(e.h.AdminDB.Create(&database.Responsibility{ID: identity.NewID(), TenantID: e.f.TenantID, InspectionID: inspectionID, ParticipantID: e.f.ParticipantID, Status: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now}).Error)
	var policy database.PolicySnapshot
	e.must(e.h.AdminDB.Where("tenant_id=? AND inspection_id=?", e.f.TenantID, e.f.InspectionID).Take(&policy).Error)
	policy.ID, policy.InspectionID = identity.NewID(), inspectionID
	e.must(e.h.AdminDB.Create(&policy).Error)
}

// AIBFIX-M4: two inspections of one stage that finish at the same time still
// complete the stage: the project and stage locks serialize the completions, so
// the later one sees the earlier one committed instead of both seeing the other open.
func TestAIBFIXM4ConcurrentInspectionsOfOneStageCompleteIt(t *testing.T) {
	e := newAIPEnv(t)
	f := e.f
	e.must(e.h.AdminDB.Exec("UPDATE projects.project_stages SET status='IN_PROGRESS' WHERE tenant_id=? AND id=?", f.TenantID, f.StageID).Error)
	second := aibfixSecondInspection(e, "ANALYZING")
	aibfixSetStatus(e, f.InspectionID, "ANALYZING")

	ids := []identity.ID{f.InspectionID, second}
	ready := make(chan struct{}, len(ids))
	start := make(chan struct{})
	results := make(chan error, len(ids))
	for _, id := range ids {
		go func(id identity.ID) {
			results <- e.tenant(func(tx *gorm.DB) error {
				if err := tx.Exec("UPDATE inspections.inspections SET status='COMPLETED' WHERE tenant_id=? AND id=?", f.TenantID, id).Error; err != nil {
					return err
				}
				var inspection database.Inspection
				if err := tx.Where("tenant_id=? AND id=?", f.TenantID, id).Take(&inspection).Error; err != nil {
					return err
				}
				ready <- struct{}{}
				<-start
				_, err := projectcore.CompleteStageOfInspection(context.Background(), tx, inspection, inspection.UpdatedAt, "aibfix-m4")
				// Hold the transaction open before committing so the two
				// completions genuinely overlap: without the project and stage
				// locks neither would see the other's committed completion.
				time.Sleep(300 * time.Millisecond)
				return err
			})
		}(id)
	}
	for range ids {
		<-ready
	}
	close(start)
	for range ids {
		e.must(<-results)
	}
	var stage database.ProjectStage
	e.must(e.h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, f.StageID).Take(&stage).Error)
	if stage.Status != "COMPLETED" {
		t.Fatalf("stage after two concurrent completions = %s, want COMPLETED", stage.Status)
	}
	var transitions, events int64
	e.must(e.h.AdminDB.Model(&database.StageTransition{}).Where("tenant_id=? AND stage_id=?", f.TenantID, f.StageID).Count(&transitions).Error)
	e.must(e.h.AdminDB.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='project.stage_changed.v1'", f.TenantID).Count(&events).Error)
	if transitions != 1 || events != 1 {
		t.Fatalf("transitions=%d events=%d, want exactly one of each", transitions, events)
	}
}

// AIBFIX-M5: an ACTIVE schedule of the stage keeps it open (it will still create
// inspections); canceling the schedule lets the next terminal inspection complete it.
func TestAIBFIXM5ActiveStageScheduleKeepsTheStageOpen(t *testing.T) {
	e := newAIPEnv(t)
	f := e.f
	e.must(e.h.AdminDB.Exec("UPDATE projects.project_stages SET status='IN_PROGRESS' WHERE tenant_id=? AND id=?", f.TenantID, f.StageID).Error)
	aibfixSetStatus(e, f.InspectionID, "COMPLETED")
	scheduleID := identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	e.must(e.h.AdminDB.Create(&database.Schedule{ID: scheduleID, TenantID: f.TenantID, BusinessUnitID: f.BusinessUnitID, AssetID: f.AssetID, ParticipantID: f.ParticipantID, TemplateID: f.TemplateID, ProjectID: &f.ProjectID, StageID: &f.StageID, ComparisonMode: "CHECKLIST_ONLY", RRule: "FREQ=DAILY", Timezone: "UTC", StartsAt: now, NextDueAt: now.Add(24 * time.Hour), DeadlineMinutes: 60, ReminderOffsets: []byte(`[]`), Status: "ACTIVE", Version: 1, IdempotencyKey: "aibfix-m5-" + scheduleID.String(), CreatedAt: now, UpdatedAt: now}).Error)
	complete := func() bool {
		t.Helper()
		var changed bool
		e.must(e.tenant(func(tx *gorm.DB) error {
			var inspection database.Inspection
			if err := tx.Where("tenant_id=? AND id=?", f.TenantID, f.InspectionID).Take(&inspection).Error; err != nil {
				return err
			}
			var err error
			changed, err = projectcore.CompleteStageOfInspection(context.Background(), tx, inspection, now, "aibfix-m5")
			return err
		}))
		return changed
	}
	if complete() {
		t.Fatal("the stage completed while one of its schedules was still ACTIVE")
	}
	e.must(e.h.AdminDB.Exec("UPDATE schedules.schedules SET status='CANCELED' WHERE tenant_id=? AND id=?", f.TenantID, scheduleID).Error)
	if !complete() {
		t.Fatal("the stage did not complete after its schedule was canceled")
	}
}

// AIBFIX-M5: when the last open inspection of a stage ends by cancellation the
// stage completes (another inspection was completed); canceling the only
// inspection of a stage never completes a stage nobody performed.
func TestAIBFIXM5CancelingTheLastOpenInspectionCompletesTheStage(t *testing.T) {
	e := newAIPEnv(t)
	f := e.f
	e.must(e.h.AdminDB.Exec("UPDATE projects.project_stages SET status='IN_PROGRESS' WHERE tenant_id=? AND id=?", f.TenantID, f.StageID).Error)
	aibfixSetStatus(e, f.InspectionID, "COMPLETED")
	open := aibfixSecondInspection(e, "INVITED")
	principal := requestctx.Principal{IdentityID: f.IdentityID, MembershipID: f.MembershipID, TenantID: f.TenantID, Roles: []string{auth.TenantAdmin}, Audience: "inspection-dashboard", Product: auth.DashboardProduct, ProductEntitlements: []string{auth.DashboardProduct}}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: f.TenantID, Principal: principal, CorrelationID: "aibfix-m5-cancel"})
	service := inspectioncore.Service{DB: e.h.DB, Authorizer: auth.Authorizer{}}
	stageStatus := func(id identity.ID) string {
		var stage database.ProjectStage
		e.must(e.h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, id).Take(&stage).Error)
		return stage.Status
	}
	view, err := service.Get(ctx, f.TenantID, open)
	e.must(err)
	if stageStatus(f.StageID) != "IN_PROGRESS" {
		t.Fatal("precondition: the stage must still be open")
	}
	if _, err := service.Cancel(ctx, f.TenantID, open, view.Inspection.Version); err != nil {
		t.Fatal(err)
	}
	if got := stageStatus(f.StageID); got != "COMPLETED" {
		t.Fatalf("stage after canceling its last open inspection = %s, want COMPLETED", got)
	}

	// A stage whose only inspection is canceled stays open.
	lonely := identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	e.must(e.h.AdminDB.Create(&database.ProjectStage{ID: lonely, TenantID: f.TenantID, ProjectID: f.ProjectID, Key: "lonely", Label: "Lonely", Kind: "INSPECTION", Position: 2, Status: "AVAILABLE", Requirements: []byte(`[]`), EffectiveReference: []byte(`{}`), Version: 1, CreatedAt: now, UpdatedAt: now}).Error)
	var template database.Inspection
	e.must(e.h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, f.InspectionID).Take(&template).Error)
	only := template
	only.ID, only.SourceKey, only.Status, only.EvidenceCount, only.StageID = identity.NewID(), "aibfix-lonely-"+identity.NewID().String(), "INVITED", 0, &lonely
	e.must(e.h.AdminDB.Create(&only).Error)
	aibfixResponsibility(e, only.ID)
	view, err = service.Get(ctx, f.TenantID, only.ID)
	e.must(err)
	if _, err := service.Cancel(ctx, f.TenantID, only.ID, view.Inspection.Version); err != nil {
		t.Fatal(err)
	}
	if got := stageStatus(lonely); got != "AVAILABLE" {
		t.Fatalf("stage whose only inspection was canceled = %s, want it untouched", got)
	}
}
