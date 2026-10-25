package generate_snapshot

import (
	"context"
	"strings"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestVideoScopeLabel(t *testing.T) {
	tests := []struct {
		name  string
		scope database.VideoScopeItem
		want  string
	}{
		{name: "component", scope: database.VideoScopeItem{ComponentLabel: "Pia da cozinha"}, want: "Pia da cozinha"},
		{name: "component with environment", scope: database.VideoScopeItem{EnvironmentCandidate: "Cozinha", ComponentLabel: "Pia"}, want: "Cozinha — Pia"},
		{name: "missing label", scope: database.VideoScopeItem{EnvironmentCandidate: "Cozinha"}, want: "Componente sem identificação"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VideoScopeLabel(tt.scope)
			if got != tt.want {
				t.Fatalf("VideoScopeLabel() = %q, want %q", got, tt.want)
			}
			if strings.Contains(got, "video:") {
				t.Fatalf("label exposed internal video requirement key: %q", got)
			}
		})
	}
}

func TestVideoScopePreparationUsesPinnedOriginForExpectedComponents(t *testing.T) {
	originPreparationID, currentPreparationID := identity.NewID(), identity.NewID()
	plan := database.VideoPlan{OriginPreparationID: &originPreparationID, CurrentPreparationID: currentPreparationID}

	if got := VideoScopePreparationID(plan, database.VideoPlanItem{Outcome: "MATCHED"}); got != originPreparationID {
		t.Fatalf("expected component preparation = %s, want pinned origin %s", got, originPreparationID)
	}
	if got := VideoScopePreparationID(plan, database.VideoPlanItem{Outcome: "CURRENT_ONLY"}); got != currentPreparationID {
		t.Fatalf("CURRENT_ONLY preparation = %s, want current %s", got, currentPreparationID)
	}
}

func TestCurrentOnlyVideoScopeResolvesFromItsOwnPreparationUT007(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS media").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE media.video_scope_items (id TEXT, tenant_id TEXT, preparation_id TEXT, frame_id TEXT, environment_candidate TEXT, component_label TEXT, view_label TEXT, visibility TEXT, identity_status TEXT, landmarks BLOB, created_at DATETIME)").Error; err != nil {
		t.Fatal(err)
	}
	tenant, currentPrep, component := identity.NewID(), identity.NewID(), identity.NewID()
	originPrep := identity.NewID()
	if err := db.Exec("INSERT INTO media.video_scope_items (id,tenant_id,preparation_id,component_label) VALUES (?,?,?,?)", component, tenant, currentPrep, "Pia").Error; err != nil {
		t.Fatal(err)
	}
	plan := database.VideoPlan{CurrentPreparationID: currentPrep, OriginPreparationID: &originPrep}
	scope, found, err := videoPlanScope(context.Background(), db, tenant, plan, database.VideoPlanItem{ComponentID: component, Outcome: "CURRENT_ONLY"})
	if err != nil || !found || scope.PreparationID != currentPrep || scope.ComponentLabel != "Pia" {
		t.Fatalf("current-only scope = %+v, found=%v, err=%v", scope, found, err)
	}
}

func TestInconclusiveV2VideoScopeResolvesFromSupplementPreparation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS media").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS analysis").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE analysis.video_plan_preparations (tenant_id TEXT, plan_id TEXT, preparation_id TEXT, role TEXT, requirement_key TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE media.video_scope_items (id TEXT, tenant_id TEXT, preparation_id TEXT, component_label TEXT, environment_candidate TEXT, view_label TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	tenantID, planID := identity.NewID(), identity.NewID()
	mainPreparationID, supplementPreparationID, componentID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec("INSERT INTO analysis.video_plan_preparations (tenant_id,plan_id,preparation_id,role,requirement_key) VALUES (?,?,?,?,?)", tenantID, planID, mainPreparationID, "MAIN", "env:kitchen").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO analysis.video_plan_preparations (tenant_id,plan_id,preparation_id,role,requirement_key) VALUES (?,?,?,?,?)", tenantID, planID, supplementPreparationID, "SUPPLEMENT", "env:bathroom").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO media.video_scope_items (id,tenant_id,preparation_id,component_label,environment_candidate,view_label) VALUES (?,?,?,?,?,?)", componentID, tenantID, supplementPreparationID, "Pia", "Banheiro", "frontal").Error; err != nil {
		t.Fatal(err)
	}
	plan := database.VideoPlan{ID: planID, TenantID: tenantID, CurrentPreparationID: mainPreparationID}
	item := database.VideoPlanItem{ComponentID: componentID, Outcome: "INCONCLUSIVE"}
	scope, found, err := videoPlanScope(context.Background(), db, tenantID, plan, item)
	if err != nil || !found || scope.PreparationID != supplementPreparationID || VideoScopeLabel(scope) != "Banheiro — Pia" {
		t.Fatalf("supplement scope=%+v found=%t err=%v", scope, found, err)
	}
}

func TestVideoPlanRequirementsKeepGapsUnobservedInsteadOfPending(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, statement := range []string{
		"ATTACH DATABASE ':memory:' AS analysis",
		"ATTACH DATABASE ':memory:' AS media",
		"CREATE TABLE analysis.video_plan_items (id TEXT PRIMARY KEY, tenant_id TEXT, plan_id TEXT, component_id TEXT, carried_result_job_id TEXT, requirement_key TEXT, outcome TEXT, reason TEXT, input_digest TEXT, origin_frame_ids BLOB, current_frame_ids BLOB, core_groups BLOB, current_groups BLOB, created_at DATETIME)",
		"CREATE TABLE media.video_scope_items (id TEXT PRIMARY KEY, tenant_id TEXT, preparation_id TEXT, frame_id TEXT, environment_candidate TEXT, component_label TEXT, view_label TEXT, visibility TEXT, identity_status TEXT, landmarks BLOB, created_at DATETIME)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, originPreparationID := identity.NewID(), identity.NewID()
	plan := database.VideoPlan{ID: identity.NewID(), TenantID: tenantID, OriginPreparationID: &originPreparationID, CurrentPreparationID: identity.NewID()}
	components := []struct {
		key, outcome, reason, environment, label string
	}{
		{"video:a-matched", "MATCHED", "", "Cozinha", "Bancada"},
		{"video:b-sink", "NO_CURRENT_VIEW", "NO_CURRENT_VIEW", "Banheiro", "Pia"},
		{"video:c-budget", "TECHNICAL_FAILURE", "MATCH_BUDGET_EXHAUSTED", "Sala", "Janela"},
	}
	for _, component := range components {
		componentID := identity.NewID()
		if err := db.Exec("INSERT INTO analysis.video_plan_items (id,tenant_id,plan_id,component_id,requirement_key,outcome,reason,input_digest,origin_frame_ids,current_frame_ids,core_groups,current_groups,created_at) VALUES (?,?,?,?,?,?,?,?,X'5B5D',X'5B5D',X'5B5D',X'5B5D',CURRENT_TIMESTAMP)", identity.NewID(), tenantID, plan.ID, componentID, component.key, component.outcome, component.reason, "digest").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO media.video_scope_items (id,tenant_id,preparation_id,frame_id,environment_candidate,component_label,view_label,visibility,identity_status,landmarks,created_at) VALUES (?,?,?,?,?,?,'aproximada','VISIBLE','RESOLVED',X'5B5D',CURRENT_TIMESTAMP)", componentID, tenantID, originPreparationID, identity.NewID(), component.environment, component.label).Error; err != nil {
			t.Fatal(err)
		}
	}

	requirements, gaps, err := VideoPlanRequirements(context.Background(), db, tenantID, plan, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(requirements) != 3 || len(gaps) != 2 {
		t.Fatalf("requirements=%+v gaps=%+v", requirements, gaps)
	}
	if got := requirements[0]; got.AnalysisMode != "COMPARE_ORIGIN_CURRENT" || got.AnalysisStatus != "PENDING" || got.Label != "Cozinha — Bancada" {
		t.Fatalf("matched requirement = %+v", got)
	}
	if got := requirements[1]; got.AnalysisMode != "CURRENT_ONLY" || got.AnalysisStatus != "INCONCLUSIVE" || got.Label != "Banheiro — Pia" || got.Coverage != VideoUnobservedCoverage {
		t.Fatalf("sink gap requirement = %+v", got)
	}
	if got := requirements[1].Instructions; got != "Para recaptura: filme Banheiro — Pia de perto, com boa luz e sem obstrução, mostrando a vista aproximada." {
		t.Fatalf("sink gap guidance = %q", got)
	}
	if got := requirements[2]; got.AnalysisStatus != "FAILED" || got.Coverage != VideoUnprocessedCoverage || got.Instructions != "" {
		t.Fatalf("technical failure requirement = %+v", got)
	}
	for _, requirement := range requirements {
		if strings.Contains(requirement.Coverage, "NO_CURRENT_VIEW") || strings.Contains(requirement.Coverage, "MATCH_BUDGET") || strings.Contains(strings.ToLower(requirement.Coverage), "ausente") {
			t.Fatalf("coverage exposes a raw code or claims absence: %q", requirement.Coverage)
		}
	}
	if _, ok := gaps["video:b-sink"]; !ok {
		t.Fatalf("sink gap key missing from gap set: %+v", gaps)
	}
}

func TestB10EnvironmentVideoPlanItemsGroupByRequirementKey(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS analysis").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS media").Error; err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE analysis.video_plan_preparations (tenant_id TEXT, plan_id TEXT, preparation_id TEXT, role TEXT, requirement_key TEXT)",
		"CREATE TABLE analysis.video_plan_items (id TEXT PRIMARY KEY, tenant_id TEXT, plan_id TEXT, component_id TEXT, carried_result_job_id TEXT, requirement_key TEXT, outcome TEXT, reason TEXT, input_digest TEXT, origin_frame_ids BLOB, current_frame_ids BLOB, core_groups BLOB, current_groups BLOB, created_at DATETIME)",
		"CREATE TABLE media.video_scope_items (id TEXT PRIMARY KEY, tenant_id TEXT, preparation_id TEXT, frame_id TEXT, environment_candidate TEXT, component_label TEXT, view_label TEXT, visibility TEXT, identity_status TEXT, landmarks BLOB, created_at DATETIME)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, currentPreparationID := identity.NewID(), identity.NewID()
	plan := database.VideoPlan{ID: identity.NewID(), TenantID: tenantID, CurrentPreparationID: currentPreparationID}
	for _, component := range []string{"Pia", "Bancada"} {
		id := identity.NewID()
		if err := db.Exec("INSERT INTO analysis.video_plan_items (id,tenant_id,plan_id,component_id,requirement_key,outcome,reason,input_digest,origin_frame_ids,current_frame_ids,core_groups,current_groups,created_at) VALUES (?,?,?,?,?,'CURRENT_ONLY','','digest',X'5B5D',X'5B5D',X'5B5D',X'5B5D',CURRENT_TIMESTAMP)", identity.NewID(), tenantID, plan.ID, id, "env:room").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO media.video_scope_items (id,tenant_id,preparation_id,frame_id,environment_candidate,component_label,view_label,visibility,identity_status,landmarks,created_at) VALUES (?,?,?,?,?,?,'wide','VISIBLE','RESOLVED',X'5B5D',CURRENT_TIMESTAMP)", id, tenantID, currentPreparationID, identity.NewID(), "Kitchen", component).Error; err != nil {
			t.Fatal(err)
		}
	}
	got, gaps, err := VideoPlanRequirements(context.Background(), db, tenantID, plan, false)
	if err != nil || len(got) != 1 || got[0].Key != "env:room" || !strings.Contains(got[0].Label, "Pia") || !strings.Contains(got[0].Label, "Bancada") || len(gaps) != 0 {
		t.Fatalf("environment requirements were not grouped: requirements=%+v gaps=%v err=%v", got, gaps, err)
	}
}

func TestVideoGapGuidanceNamesSpecificVisualLimitations(t *testing.T) {
	label := "Quarto — Pia"
	tests := []struct {
		reason, coverage, instruction string
	}{
		{"OBSTRUCTED_VIEW", "Visão de Quarto — Pia obstruída no vídeo atual; isto não indica ausência do item.", "Para recaptura: filme Quarto — Pia sem objetos na frente, mostrando toda a área."},
		{"BLURRED_VIEW", "Imagem de Quarto — Pia sem nitidez suficiente no vídeo atual para avaliar o item.", "Para recaptura: filme Quarto — Pia, mantenha a câmera estável e aproxime-se até a imagem ficar nítida."},
		{"AMBIGUOUS_IDENTITY", "Não foi possível identificar com segurança qual ambiente corresponde à referência de Quarto — Pia.", "Para recaptura: filme Quarto — Pia e mostre a porta ou o corredor de acesso para diferenciar o ambiente."},
	}
	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			coverage, instruction := VideoGapGuidance(tt.reason, label, "wide")
			if coverage != tt.coverage || instruction != tt.instruction {
				t.Fatalf("VideoGapGuidance(%q) = (%q, %q), want (%q, %q)", tt.reason, coverage, instruction, tt.coverage, tt.instruction)
			}
		})
	}
	coverage, instruction := VideoGapGuidance("NO_CURRENT_VIEW", label, "wide")
	if coverage != VideoUnobservedCoverage || instruction != "Para recaptura: filme Quarto — Pia de perto, com boa luz e sem obstrução, mostrando a vista wide." {
		t.Fatalf("generic gap wording changed: (%q, %q)", coverage, instruction)
	}
}
