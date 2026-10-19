//go:build integration

package schedule_video_plan

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	buildplan "inspection/services/inspection/internal/features/analysis/prepare_video_comparisons"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestVV_IT_195CurrentOnlyObstructedSinkHasNamedReportGap(t *testing.T) {
	fixture := newTask04PlanFixtureWithOriginStatus(t, false, true, "READY", true)
	parkTask04Plans(t, fixture)
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.currentKitchenScope).Update("visibility", "OBSCURED").Error; err != nil {
			return err
		}
		return tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.currentFrame).Update("quality", "GOOD").Error
	}); err != nil {
		t.Fatal(err)
	}
	plan := runTask04Plan(t, fixture)
	items := task07PlanItems(t, fixture, plan)
	if len(items) != 1 || items[0].Outcome != buildplan.Inconclusive || items[0].Reason != "OBSTRUCTED_VIEW" {
		t.Fatalf("current-only obstruction plan items = %+v", items)
	}
	snapshot, html := task07WorkerReportWithoutJobs(t, fixture, plan)
	requirement := task07NamedRequirement(t, snapshot.Requirements, "video:"+fixture.currentKitchenScope.String())
	if requirement.AnalysisStatus != "INCONCLUSIVE" || requirement.Label != "kitchen — sink" || requirement.Coverage != "Visão de kitchen — sink obstruída no vídeo atual; isto não indica ausência do item." {
		t.Fatalf("obstructed sink report requirement = %+v", requirement)
	}
	assertTask07HasReportText(t, html, requirement.Coverage, "Para recaptura: filme kitchen — sink sem objetos na frente, mostrando toda a área.")
}

func TestVV_IT_209NamedBlurredObstructedAndAmbiguousReportGaps(t *testing.T) {
	fixture := newTask04PlanFixtureWithOriginStatus(t, true, true, "READY")
	parkTask04Plans(t, fixture)
	bedroomOriginFrame, bedroomOriginScope := identity.NewID(), identity.NewID()
	bedroomCurrentFrame, bedroomCurrentScope := identity.NewID(), identity.NewID()
	bathroomCurrentFrame, bathroomCurrentScope := identity.NewID(), identity.NewID()
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var origin database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.originPreparation).Take(&origin).Error; err != nil {
			return err
		}
		originBedroom := database.VideoFrame{ID: bedroomOriginFrame, TenantID: fixture.tenantID, SourceMediaID: origin.MediaID, PreparationID: fixture.originPreparation, PTSMicros: 2, SHA256: digest, ObjectKey: "vv-it209-origin/" + bedroomOriginFrame.String(), Width: 1, Height: 1, Role: "BASELINE", Quality: "GOOD", SizeBytes: 1, CreatedAt: time.Now().UTC()}
		if err := tx.Create(&originBedroom).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoScopeItem{ID: bedroomOriginScope, TenantID: fixture.tenantID, PreparationID: fixture.originPreparation, FrameID: bedroomOriginFrame, EnvironmentCandidate: "bedroom", ComponentLabel: "window", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: time.Now().UTC()}).Error; err != nil {
			return err
		}
		var current database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.currentPreparation).Take(&current).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.currentFrame).Update("quality", "LOW_QUALITY").Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.bathroomID).Update("component_label", "basin").Error; err != nil {
			return err
		}
		for _, row := range []struct {
			frameID, scopeID                                            identity.ID
			environment, component, visibility, identityStatus, quality string
			pts                                                         int64
		}{{bathroomCurrentFrame, bathroomCurrentScope, "bathroom", "basin", "OBSCURED", "RESOLVED", "GOOD", 8}, {bedroomCurrentFrame, bedroomCurrentScope, "bedroom", "window", "VISIBLE", "AMBIGUOUS", "GOOD", 9}} {
			frame := database.VideoFrame{ID: row.frameID, TenantID: fixture.tenantID, SourceMediaID: current.MediaID, PreparationID: fixture.currentPreparation, PTSMicros: row.pts, SHA256: digest, ObjectKey: "vv-it209-current/" + row.frameID.String(), Width: 1, Height: 1, Role: "CURRENT", Quality: row.quality, SizeBytes: 1, CreatedAt: time.Now().UTC()}
			if err := tx.Create(&frame).Error; err != nil {
				return err
			}
			scope := database.VideoScopeItem{ID: row.scopeID, TenantID: fixture.tenantID, PreparationID: fixture.currentPreparation, FrameID: row.frameID, EnvironmentCandidate: row.environment, ComponentLabel: row.component, ViewLabel: "wide", Visibility: row.visibility, IdentityStatus: row.identityStatus, Landmarks: json.RawMessage(`[]`), CreatedAt: time.Now().UTC()}
			if err := tx.Create(&scope).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	componentByFrame := map[identity.ID]identity.ID{
		fixture.currentFrame: fixture.kitchenID,
		bathroomCurrentFrame: fixture.bathroomID,
		bedroomOriginFrame:   bedroomOriginScope,
		bedroomCurrentFrame:  bedroomOriginScope,
	}
	var originScopes []database.VideoScopeItem
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.originPreparation).Find(&originScopes).Error
	}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range originScopes {
		componentByFrame[scope.FrameID] = scope.ID
	}
	matcher := integrationMatcher{componentByFrame: componentByFrame}
	plan := runTask07PlanWithMatcher(t, fixture, matcher)
	items := task07PlanItems(t, fixture, plan)
	if len(items) != 3 {
		t.Fatalf("expected exactly the three named gaps, got %+v", items)
	}
	snapshot, html := task07WorkerReportWithoutJobs(t, fixture, plan)
	wants := map[identity.ID]struct{ label, coverage, instruction string }{
		fixture.kitchenID:  {"kitchen — sink", "Imagem de kitchen — sink sem nitidez suficiente no vídeo atual para avaliar o item.", "Para recaptura: filme kitchen — sink, mantenha a câmera estável e aproxime-se até a imagem ficar nítida."},
		fixture.bathroomID: {"bathroom — basin", "Visão de bathroom — basin obstruída no vídeo atual; isto não indica ausência do item.", "Para recaptura: filme bathroom — basin sem objetos na frente, mostrando toda a área."},
		bedroomOriginScope: {"bedroom — window", "Não foi possível identificar com segurança qual ambiente corresponde à referência de bedroom — window.", "Para recaptura: filme bedroom — window e mostre a porta ou o corredor de acesso para diferenciar o ambiente."},
	}
	for componentID, want := range wants {
		requirement := task07NamedRequirement(t, snapshot.Requirements, "video:"+componentID.String())
		if requirement.AnalysisStatus != "INCONCLUSIVE" || requirement.Label != want.label || requirement.Coverage != want.coverage || requirement.Instructions != want.instruction {
			t.Errorf("named gap %s = %+v, want label=%q coverage=%q instructions=%q", componentID, requirement, want.label, want.coverage, want.instruction)
		}
		assertTask07HasReportText(t, html, want.label, want.coverage, want.instruction)
	}
}

func task07PlanItems(t *testing.T, fixture task04PlanFixture, plan database.VideoPlan) []database.VideoPlanItem {
	t.Helper()
	var items []database.VideoPlanItem
	if err := fixture.h.AdminDB.Where("tenant_id=? AND plan_id=?", fixture.tenantID, plan.ID).Order("requirement_key").Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	return items
}

func runTask07PlanWithMatcher(t *testing.T, fixture task04PlanFixture, matcher integrationMatcher) database.VideoPlan {
	t.Helper()
	scheduler := newTask04Scheduler(t, fixture, matcher, matcher)
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return scheduler.Handle(fixture.ctx, tx, fixture.submittedEvent(t))
	}); err != nil {
		t.Fatal(err)
	}
	var plan database.VideoPlan
	for range 5 {
		worked, err := scheduler.RunDue(fixture.ctx)
		if err != nil || !worked {
			t.Fatalf("RunDue = %t, %v", worked, err)
		}
		if err := fixture.h.AdminDB.Where("tenant_id=? AND submission_id=?", fixture.tenantID, fixture.submissionID).Take(&plan).Error; err != nil {
			t.Fatal(err)
		}
		if plan.Attempts > 0 || plan.Status == "SEALED" {
			return plan
		}
	}
	return plan
}

func task07NamedRequirement(t *testing.T, requirements []reportcore.Requirement, key string) reportcore.Requirement {
	t.Helper()
	for _, requirement := range requirements {
		if requirement.Key == key {
			return requirement
		}
	}
	t.Fatalf("report requirement %q was not found in %+v", key, requirements)
	return reportcore.Requirement{}
}

func assertTask07HasReportText(t *testing.T, html string, expected ...string) {
	t.Helper()
	for _, text := range expected {
		if !strings.Contains(html, text) {
			t.Errorf("HTML report does not contain %q", text)
		}
	}
}
