//go:build integration

package schedule_video_plan

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
)

// task07ClaimGateway returns one current-only finding with the given title.
type task07ClaimGateway struct{ title string }

func (g task07ClaimGateway) CompleteStructured(_ context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
	evidence := []string{}
	for _, image := range request.Images {
		evidence = append(evidence, image.EvidenceID)
	}
	response, err := json.Marshal(map[string]any{"noRelevantChange": nil, "findings": []map[string]any{{"category": "CONSERVATION", "title": g.title, "description": g.title + " na pia da cozinha.", "severity": "LOW", "confidence": 0.9, "evidenceIds": evidence, "quality": "ADEQUATE", "recommendedAction": "Acompanhar na próxima vistoria"}}})
	return llm.StructuredResult{Provider: "test", Model: "fixture", GatewayRequestID: "f6-task07-claim", JSON: response}, err
}

func TestVV_IT_193CurrentOnlyScratchIsObservedDamageNotNewDamage(t *testing.T) {
	fixture := newTask04PlanFixtureWithOriginStatus(t, false, true, "READY", true)
	plan := runTask04Plan(t, fixture)
	processTask07PlanJobs(t, fixture, plan, task07ClaimGateway{title: "Risco visível"})
	snapshot, html := task07WorkerReport(t, fixture, plan)
	kitchen := task04Requirement(t, snapshot, "kitchen — sink")
	if kitchen.AnalysisMode != "CURRENT_ONLY" || kitchen.AnalysisStatus != "COMPLETED" || kitchen.NoRelevantChange != nil {
		t.Fatalf("kitchen=%+v; want completed current-only observation", kitchen)
	}
	if len(snapshot.Findings) != 1 || snapshot.Findings[0].Title != "Risco visível" || snapshot.Findings[0].Category != "CONSERVATION" {
		t.Fatalf("findings=%+v; want the observed scratch D1", snapshot.Findings)
	}
	lower := strings.ToLower(html)
	for _, claim := range []string{"novo risco", "novo dano", "alteração relevante identificada", "comparação com referência ·"} {
		if strings.Contains(lower, claim) {
			t.Fatalf("current-only report claims %q", claim)
		}
	}
	if len(snapshot.ScopeStatements) == 0 || snapshot.ScopeStatements[0] != generate_snapshot.VideoNoReferenceStatement || !strings.Contains(html, "Análise atual") {
		t.Fatalf("current-only report lacks its observed-condition framing: %q", snapshot.ScopeStatements)
	}
}

func TestVV_IT_194CurrentOnlyHistoricalClaimsAreRejectedByValidator(t *testing.T) {
	for _, claim := range []string{"Torneira removida", "Armário adicionado", "Pia substituída", "Rodapé reparado"} {
		t.Run(claim, func(t *testing.T) {
			fixture := newTask04PlanFixtureWithOriginStatus(t, false, true, "READY", true)
			plan := runTask04Plan(t, fixture)
			processTask07PlanJobs(t, fixture, plan, task07ClaimGateway{title: claim})
			var jobs []database.ComparisonJob
			if err := fixture.h.AdminDB.Where("tenant_id=? AND video_plan_id=?", fixture.tenantID, plan.ID).Find(&jobs).Error; err != nil {
				t.Fatal(err)
			}
			for _, job := range jobs {
				if job.Status == "COMPLETED" {
					t.Fatalf("job %s accepted current-only claim %q", job.ID, claim)
				}
				var findings int64
				if err := fixture.h.AdminDB.Model(&database.FindingRecord{}).Where("tenant_id=? AND analysis_run_id IN (SELECT id FROM analysis.analysis_runs WHERE job_id=?)", fixture.tenantID, job.ID).Count(&findings).Error; err != nil {
					t.Fatal(err)
				}
				if findings != 0 {
					t.Fatalf("claim %q persisted %d findings", claim, findings)
				}
			}
		})
	}
}
