//go:build integration

package schedule_video_plan

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	buildrequest "inspection/services/inspection/internal/features/analysis/build_request"
	processcomparison "inspection/services/inspection/internal/features/analysis/process_comparison"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"

	"gorm.io/gorm"
)

// scriptedTask04Gateway answers with one finding whose evidence is chosen
// from the request images.
type scriptedTask04Gateway struct {
	calls    int
	finding  func(llm.StructuredRequest) map[string]any
	noChange bool
}

func (g *scriptedTask04Gateway) CompleteStructured(_ context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
	g.calls++
	response, err := json.Marshal(map[string]any{"noRelevantChange": g.noChange, "findings": []map[string]any{g.finding(request)}})
	return llm.StructuredResult{Provider: "test", Model: "fixture", GatewayRequestID: "vv-validation", JSON: response}, err
}

func TestVV_IT_602FindingAcrossDistinctPairsIsRejected(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, true)
	plan := runTask04Plan(t, fixture)
	prepareTask04FrameObjects(t, fixture)
	job := task04PlanJob(t, fixture, plan)
	builder, err := buildrequest.Setup(buildrequest.Dependencies{Store: fixture.h.Store})
	if err != nil {
		t.Fatal(err)
	}
	// A second origin/current pair travels in the same request.
	twoPairs := func(ctx context.Context, tx *gorm.DB, job database.ComparisonJob, payload processcomparison.Payload) (llm.StructuredRequest, error) {
		request, err := builder(ctx, tx, job, payload)
		if err != nil {
			return request, err
		}
		extra := make([]llm.NormalizedImage, 0, len(request.Images))
		for _, image := range request.Images {
			image.EvidenceID, image.PairID = image.EvidenceID+"-B", "component:other"
			extra = append(extra, image)
		}
		request.Images = append(request.Images, extra...)
		return request, nil
	}
	gateway := &scriptedTask04Gateway{finding: func(request llm.StructuredRequest) map[string]any {
		var originA, currentB string
		for _, image := range request.Images {
			if image.Source == "ORIGIN" && image.PairID != "component:other" && originA == "" {
				originA = image.EvidenceID
			}
			if image.Source == "CURRENT" && image.PairID == "component:other" && currentB == "" {
				currentB = image.EvidenceID
			}
		}
		return map[string]any{"category": "CONSERVATION", "title": "Risco na pia", "description": "Risco observado na pia.", "severity": "LOW", "confidence": 0.95, "evidenceIds": []string{originA, currentB}, "quality": "ADEQUATE", "recommendedAction": "Acompanhar na próxima vistoria"}
	}}
	runTask04Validation(t, fixture, job, twoPairs, gateway, false)
	// Control: the same finding inside one pair is accepted.
	control := newTask04PlanFixture(t, false, true)
	controlPlan := runTask04Plan(t, control)
	prepareTask04FrameObjects(t, control)
	controlBuilder, err := buildrequest.Setup(buildrequest.Dependencies{Store: control.h.Store})
	if err != nil {
		t.Fatal(err)
	}
	samePair := &scriptedTask04Gateway{finding: func(request llm.StructuredRequest) map[string]any {
		var origin, current string
		for _, image := range request.Images {
			if image.Source == "ORIGIN" && origin == "" {
				origin = image.EvidenceID
			}
			if image.Source == "CURRENT" && current == "" {
				current = image.EvidenceID
			}
		}
		return map[string]any{"category": "CONSERVATION", "title": "Risco na pia", "description": "Risco observado na pia.", "severity": "LOW", "confidence": 0.95, "evidenceIds": []string{origin, current}, "quality": "ADEQUATE", "recommendedAction": "Acompanhar na próxima vistoria"}
	}}
	runTask04Validation(t, control, task04PlanJob(t, control, controlPlan), controlBuilder, samePair, true)
}

func TestVV_IT_621LiabilityClaimFromVideoChangeIsRejected(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, true)
	plan := runTask04Plan(t, fixture)
	prepareTask04FrameObjects(t, fixture)
	job := task04PlanJob(t, fixture, plan)
	builder, err := buildrequest.Setup(buildrequest.Dependencies{Store: fixture.h.Store})
	if err != nil {
		t.Fatal(err)
	}
	gateway := &scriptedTask04Gateway{finding: func(request llm.StructuredRequest) map[string]any {
		evidence := make([]string, 0, len(request.Images))
		for _, image := range request.Images {
			evidence = append(evidence, image.EvidenceID)
		}
		return map[string]any{"category": "CONSERVATION", "title": "Dano na pia", "description": "O locatário tem culpa e deve pagar compensação; liability for the damage and the repair cost is his.", "severity": "HIGH", "confidence": 0.95, "evidenceIds": evidence, "quality": "ADEQUATE", "recommendedAction": "Cobrar o custo do reparo"}
	}}
	runTask04Validation(t, fixture, job, builder, gateway, false)
	// Control: the same evidence with a neutral description is accepted.
	control := newTask04PlanFixture(t, false, true)
	controlPlan := runTask04Plan(t, control)
	prepareTask04FrameObjects(t, control)
	controlBuilder, err := buildrequest.Setup(buildrequest.Dependencies{Store: control.h.Store})
	if err != nil {
		t.Fatal(err)
	}
	neutral := &scriptedTask04Gateway{finding: func(request llm.StructuredRequest) map[string]any {
		evidence := make([]string, 0, len(request.Images))
		for _, image := range request.Images {
			evidence = append(evidence, image.EvidenceID)
		}
		return map[string]any{"category": "CONSERVATION", "title": "Dano na pia", "description": "Dano visível na borda da pia.", "severity": "HIGH", "confidence": 0.95, "evidenceIds": evidence, "quality": "ADEQUATE", "recommendedAction": "Acompanhar na próxima vistoria"}
	}}
	runTask04Validation(t, control, task04PlanJob(t, control, controlPlan), controlBuilder, neutral, true)
}

func task04PlanJob(t *testing.T, fixture task04PlanFixture, plan database.VideoPlan) database.ComparisonJob {
	t.Helper()
	var job database.ComparisonJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND video_plan_id=?", fixture.tenantID, plan.ID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	return job
}

// runTask04Validation processes the job once and requires the provider answer
// to be accepted with one finding, or rejected with none and no completion.
func runTask04Validation(t *testing.T, fixture task04PlanFixture, job database.ComparisonJob, build func(context.Context, *gorm.DB, database.ComparisonJob, processcomparison.Payload) (llm.StructuredRequest, error), gateway *scriptedTask04Gateway, accepted bool) {
	t.Helper()
	processor, err := processcomparison.Setup(processcomparison.Dependencies{Gateway: gateway, BuildRequest: build, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(processcomparison.Payload{JobID: job.ID})
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error { return processor(fixture.ctx, tx, payload, fixture.tenantID) }); err != nil {
		t.Fatal(err)
	}
	var got database.ComparisonJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, job.ID).Take(&got).Error; err != nil {
		t.Fatal(err)
	}
	var findings int64
	if err := fixture.h.AdminDB.Model(&database.FindingRecord{}).Where("tenant_id=? AND analysis_run_id IN (SELECT id FROM analysis.analysis_runs WHERE job_id=?)", fixture.tenantID, job.ID).Count(&findings).Error; err != nil {
		t.Fatal(err)
	}
	if accepted {
		if gateway.calls != 1 || findings != 1 || got.Status != "COMPLETED" {
			t.Fatalf("supported control answer rejected: calls=%d findings=%d status=%s", gateway.calls, findings, got.Status)
		}
		return
	}
	if gateway.calls != 1 || findings != 0 || got.Status == "COMPLETED" {
		t.Fatalf("unsupported provider answer accepted: calls=%d findings=%d status=%s", gateway.calls, findings, got.Status)
	}
}
