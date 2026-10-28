//go:build integration

package schedule_video_plan

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	buildrequest "inspection/services/inspection/internal/features/analysis/build_request"
	classify "inspection/services/inspection/internal/features/analysis/classify_inspection"
	processcomparison "inspection/services/inspection/internal/features/analysis/process_comparison"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"

	"gorm.io/gorm"
)

type task04ConditionGateway struct {
	request    llm.StructuredRequest
	categories []string
}

func (g *task04ConditionGateway) CompleteStructured(_ context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
	g.request = request
	findings := make([]map[string]any, 0, len(g.categories))
	for _, category := range g.categories {
		severity, quality := "LOW", "ADEQUATE"
		evidence := []string{}
		for _, image := range request.Images {
			if image.Source == "CURRENT" || category != "EVIDENCE_QUALITY" {
				evidence = append(evidence, image.EvidenceID)
			}
		}
		if category == "EVIDENCE_QUALITY" {
			severity, quality = "NONE", "INSUFFICIENT"
		}
		findings = append(findings, map[string]any{"category": category, "title": "Observação " + category, "description": "Evidência visual observada na pia.", "severity": severity, "confidence": 0.91, "evidenceIds": evidence, "quality": quality, "recommendedAction": "Acompanhar na próxima vistoria"})
	}
	response, err := json.Marshal(map[string]any{"noRelevantChange": false, "findings": findings})
	return llm.StructuredResult{Provider: "test", Model: "fixture", GatewayRequestID: "f6-it177", JSON: response}, err
}

func prepareTask04FrameObjects(t *testing.T, fixture task04PlanFixture) {
	t.Helper()
	data := []byte("synthetic image bytes for request assembly")
	digest := sha256.Sum256(data)
	digestText := hex.EncodeToString(digest[:])
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var frames []database.VideoFrame
		if err := tx.Where("tenant_id=? AND preparation_id IN ?", fixture.tenantID, []identity.ID{fixture.originPreparation, fixture.currentPreparation}).Find(&frames).Error; err != nil {
			return err
		}
		for _, frame := range frames {
			if err := fixture.h.Store.Client.Put(fixture.ctx, fixture.h.Store.Bucket, frame.ObjectKey, bytes.NewReader(data), int64(len(data)), "image/jpeg"); err != nil {
				return err
			}
			if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND id=?", fixture.tenantID, frame.ID).Update("sha256", digestText).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func executeTask04Condition(t *testing.T, fixture task04PlanFixture, categories []string) (*task04ConditionGateway, database.VideoPlan, database.ComparisonJob) {
	t.Helper()
	plan := runTask04Plan(t, fixture)
	prepareTask04FrameObjects(t, fixture)
	var job database.ComparisonJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND video_plan_id=?", fixture.tenantID, plan.ID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	builder, err := buildrequest.Setup(buildrequest.Dependencies{Store: fixture.h.Store})
	if err != nil {
		t.Fatal(err)
	}
	gateway := &task04ConditionGateway{categories: categories}
	processor, err := processcomparison.Setup(processcomparison.Dependencies{Gateway: gateway, BuildRequest: builder, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(processcomparison.Payload{JobID: job.ID})
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return processor(fixture.ctx, tx, payload, fixture.tenantID)
	}); err != nil {
		t.Fatal(err)
	}
	return gateway, plan, job
}

func TestVV_IT_177SealedPlanBuildRequestReachesProcessComparisonImageOnly(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, true)
	gateway, _, _ := executeTask04Condition(t, fixture, []string{"CONSERVATION"})
	if gateway.request.Mode != "COMPARE_ORIGIN_CURRENT" || len(gateway.request.Images) != 2 {
		t.Fatalf("condition processor request mode/images=%s/%d, want COMPARE_ORIGIN_CURRENT/2", gateway.request.Mode, len(gateway.request.Images))
	}
	seen := map[string]int{}
	for _, image := range gateway.request.Images {
		seen[image.Source]++
		if image.DataURL == "" || image.EvidenceID == "" || image.PairID == "" || image.Position == "" {
			t.Fatalf("request contains incomplete image evidence: %+v", image)
		}
	}
	if seen["ORIGIN"] != 1 || seen["CURRENT"] != 1 {
		t.Fatalf("condition request was not image-only origin/current pair: %+v", gateway.request.Images)
	}
}

func TestVV_IT_192CurrentOnlyFrameReachesProcessComparisonWithoutOrigin(t *testing.T) {
	fixture := newTask04PlanFixtureWithOriginStatus(t, false, true, "READY", true)
	plan := runTask04Plan(t, fixture)
	var item database.VideoPlanItem
	if err := fixture.h.AdminDB.Where("tenant_id=? AND plan_id=? AND component_id=?", fixture.tenantID, plan.ID, fixture.currentKitchenScope).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.Outcome != "CURRENT_ONLY" {
		t.Fatalf("plan item outcome=%s, want CURRENT_ONLY", item.Outcome)
	}
	prepareTask04FrameObjects(t, fixture)
	var job database.ComparisonJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND video_plan_id=? AND video_plan_item_id=?", fixture.tenantID, plan.ID, item.ID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	builder, err := buildrequest.Setup(buildrequest.Dependencies{Store: fixture.h.Store})
	if err != nil {
		t.Fatal(err)
	}
	requestSeen := false
	processor, err := processcomparison.Setup(processcomparison.Dependencies{
		BuildRequest: builder,
		Gateway: currentOnlyTask04Gateway{onRequest: func(request llm.StructuredRequest) {
			requestSeen = true
			if request.Mode != "CURRENT_ONLY" || len(request.Images) != 1 || request.Images[0].EvidenceID != fixture.currentFrame.String() || request.Images[0].Source != "CURRENT" {
				t.Errorf("current-only request = %+v", request)
			}
		}},
		Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(processcomparison.Payload{JobID: job.ID})
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error { return processor(fixture.ctx, tx, payload, fixture.tenantID) }); err != nil {
		t.Fatal(err)
	}
	if !requestSeen {
		t.Fatal("process_comparison did not dispatch current-only frame F11")
	}
}

func TestVV_IT_201OriginActivationDuringCurrentOnlyAnalysisKeepsRequestMode(t *testing.T) {
	fixture := newTask04PlanFixtureWithOriginStatus(t, false, true, "READY", true)
	plan := runTask04Plan(t, fixture)
	if plan.ReferenceVersionID != nil {
		t.Fatalf("V2 plan unexpectedly pinned origin %s", *plan.ReferenceVersionID)
	}
	now := time.Now().UTC()
	originID, versionID := identity.NewID(), identity.NewID()
	if err := fixture.h.AdminDB.Create(&database.Origin{ID: originID, TenantID: fixture.tenantID, AssetID: fixture.assetID, ActiveVersionID: &versionID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.AdminDB.Create(&database.OriginVersion{ID: versionID, TenantID: fixture.tenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: &fixture.responsibilityID, Status: "ACTIVE", MediaKind: "VIDEO", VideoPreparationID: &fixture.currentPreparation, ScopeManifestDigest: "current-only", IdempotencyKey: "it201-activation-" + versionID.String(), CreatedAt: now, SubmittedAt: &now, ActivatedAt: &now}).Error; err != nil {
		t.Fatal(err)
	}
	prepareTask04FrameObjects(t, fixture)
	var job database.ComparisonJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND video_plan_id=?", fixture.tenantID, plan.ID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	builder, err := buildrequest.Setup(buildrequest.Dependencies{Store: fixture.h.Store})
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	processor, err := processcomparison.Setup(processcomparison.Dependencies{BuildRequest: builder, Gateway: currentOnlyTask04Gateway{onRequest: func(request llm.StructuredRequest) {
		seen = true
		if request.Mode != "CURRENT_ONLY" || len(request.Images) != 1 || request.Images[0].Source != "CURRENT" {
			t.Errorf("request after O1 activation = mode %s, images %+v", request.Mode, request.Images)
		}
	}}, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(processcomparison.Payload{JobID: job.ID})
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error { return processor(fixture.ctx, tx, payload, fixture.tenantID) }); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("current-only comparison was not dispatched after O1 activation")
	}
}

func TestVV_IT_203CompletedCurrentOnlyJobReplayKeepsOneDefectInReport(t *testing.T) {
	fixture := newTask04PlanFixtureWithOriginStatus(t, false, true, "READY", true)
	plan := runTask04Plan(t, fixture)
	prepareTask04FrameObjects(t, fixture)
	var job database.ComparisonJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND video_plan_id=?", fixture.tenantID, plan.ID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	builder, err := buildrequest.Setup(buildrequest.Dependencies{Store: fixture.h.Store})
	if err != nil {
		t.Fatal(err)
	}
	gateway := singleDefectTask04Gateway{}
	processor, err := processcomparison.Setup(processcomparison.Dependencies{BuildRequest: builder, Gateway: gateway, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(processcomparison.Payload{JobID: job.ID})
	for i := 0; i < 2; i++ {
		if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error { return processor(fixture.ctx, tx, payload, fixture.tenantID) }); err != nil {
			t.Fatalf("delivery %d: %v", i+1, err)
		}
	}
	var records []database.FindingRecord
	if err := fixture.h.AdminDB.Where("tenant_id=? AND analysis_run_id IN (SELECT id FROM analysis.analysis_runs WHERE job_id=?)", fixture.tenantID, job.ID).Find(&records).Error; err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Title != "Scratch D1" {
		t.Fatalf("completed job replay produced %d findings, want one Scratch D1: %+v", len(records), records)
	}
	var evidence []string
	if err := json.Unmarshal(records[0].Evidence, &evidence); err != nil {
		t.Fatal(err)
	}
	finding := reportcore.Finding{Category: records[0].Category, Title: records[0].Title, Description: records[0].Description, Severity: records[0].Severity, Quality: records[0].Quality, RecommendedAction: records[0].RecommendedAction, Confidence: records[0].Confidence, EvidenceIDs: evidence}
	ctx := reportcore.Context{Asset: reportcore.AssetContext{ID: fixture.assetID.String(), Name: "QA asset", ExternalKey: "F6", Address: "Local"}, Participant: reportcore.ParticipantContext{ID: "qa", Name: "QA"}, Template: reportcore.TemplateContext{ID: "qa-template", Name: "QA template", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: time.Now().UTC().Format(time.RFC3339)}}
	if _, err := generate_snapshot.Create(fixture.ctx, fixture.h.AdminDB, generate_snapshot.Input{TenantID: fixture.tenantID, InspectionID: fixture.inspectionID, VideoPlanID: &plan.ID, Mode: "CONSOLIDATED", Classification: "ATTENTION", TemplateVersionID: "qa-template-v1", ReferenceVersionID: "current-only", PromptDigest: "qa-prompt", Context: ctx, Findings: []reportcore.Finding{finding}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	var snapshot database.ReportSnapshot
	if err := fixture.h.AdminDB.Where("tenant_id=? AND inspection_id=? AND video_plan_id=?", fixture.tenantID, fixture.inspectionID, plan.ID).Take(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	var decoded reportcore.Snapshot
	if err := json.Unmarshal(snapshot.CanonicalJSON, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Findings) != 1 || decoded.Findings[0].Title != "Scratch D1" || len(decoded.Findings[0].EvidenceIDs) != 1 {
		t.Fatalf("snapshot does not contain exactly one supported D1: %+v", decoded.Findings)
	}
}

func TestVV_IT_212ObstructedCurrentViewRejectsUnsupportedRemovedFinding(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, true)
	plan := runTask04Plan(t, fixture)
	prepareTask04FrameObjects(t, fixture)
	var item database.VideoPlanItem
	if err := fixture.h.AdminDB.Where("tenant_id=? AND plan_id=? AND component_id=?", fixture.tenantID, plan.ID, fixture.kitchenID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND preparation_id=? AND frame_id=?", fixture.tenantID, fixture.currentPreparation, fixture.currentFrame).Update("visibility", "OBSTRUCTED").Error
	}); err != nil {
		t.Fatal(err)
	}
	var job database.ComparisonJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND video_plan_item_id=?", fixture.tenantID, item.ID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	builder, err := buildrequest.Setup(buildrequest.Dependencies{Store: fixture.h.Store})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := processcomparison.Setup(processcomparison.Dependencies{BuildRequest: builder, Gateway: removedClaimTask04Gateway{}, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(processcomparison.Payload{JobID: job.ID})
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error { return processor(fixture.ctx, tx, payload, fixture.tenantID) }); err != nil {
		t.Fatal(err)
	}
	var after database.ComparisonJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, job.ID).Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	var findings int64
	if err := fixture.h.AdminDB.Model(&database.FindingRecord{}).Where("tenant_id=? AND analysis_run_id IN (SELECT id FROM analysis.analysis_runs WHERE job_id=?)", fixture.tenantID, job.ID).Count(&findings).Error; err != nil {
		t.Fatal(err)
	}
	if after.Status != "FAILED" || findings != 0 {
		t.Fatalf("unsupported removal persisted: job=%s findings=%d", after.Status, findings)
	}
}

type removedClaimTask04Gateway struct{}

func (removedClaimTask04Gateway) CompleteStructured(_ context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
	ids := make([]string, 0, len(request.Images))
	for _, image := range request.Images {
		ids = append(ids, image.EvidenceID)
	}
	response, err := json.Marshal(map[string]any{"noRelevantChange": false, "findings": []any{map[string]any{"category": "INVENTORY", "title": "Item REMOVED", "description": "O item não está mais visível.", "severity": "LOW", "confidence": 0.95, "evidenceIds": ids, "quality": "ADEQUATE", "recommendedAction": "Verificar"}}})
	return llm.StructuredResult{Provider: "test", Model: "fixture", GatewayRequestID: "f6-it212", JSON: response}, err
}

type singleDefectTask04Gateway struct{}

func (singleDefectTask04Gateway) CompleteStructured(_ context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
	if request.Mode != "CURRENT_ONLY" || len(request.Images) != 1 || request.Images[0].Source != "CURRENT" {
		return llm.StructuredResult{}, fmt.Errorf("unexpected current-only request: %+v", request)
	}
	response, err := json.Marshal(map[string]any{"noRelevantChange": nil, "findings": []any{map[string]any{"category": "CONSERVATION", "title": "Scratch D1", "description": "Risco visível no acabamento da pia.", "severity": "MEDIUM", "confidence": 0.95, "evidenceIds": []string{request.Images[0].EvidenceID}, "quality": "ADEQUATE", "recommendedAction": "Acompanhar na próxima vistoria"}}})
	return llm.StructuredResult{Provider: "test", Model: "fixture", GatewayRequestID: "f6-it203", JSON: response}, err
}

func TestVV_IT_198CurrentOnlyWithoutUsableFramesProducesInconclusiveReport(t *testing.T) {
	fixture := newTask04PlanFixtureWithOriginStatus(t, false, true, "READY", true)
	// The current preparation has a discovered frame, but its view is
	// obstructed and therefore unusable for a condition conclusion.
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.currentKitchenScope).Update("visibility", "OBSTRUCTED").Error
	}); err != nil {
		t.Fatal(err)
	}
	plan := runTask04Plan(t, fixture)
	var items []database.VideoPlanItem
	if err := fixture.h.AdminDB.Where("tenant_id=? AND plan_id=?", fixture.tenantID, plan.ID).Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Outcome != "INCONCLUSIVE" {
			t.Fatalf("item %s outcome=%s, want INCONCLUSIVE", item.ComponentID, item.Outcome)
		}
	}
	if len(items) == 0 {
		t.Fatal("current-only plan has no reportable items")
	}
	var jobs []database.ComparisonJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND video_plan_id=?", fixture.tenantID, plan.ID).Find(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	processor, err := processcomparison.Setup(processcomparison.Dependencies{Gateway: &task04ConditionGateway{categories: []string{"CONSERVATION"}}, BuildRequest: func(context.Context, *gorm.DB, database.ComparisonJob, processcomparison.Payload) (llm.StructuredRequest, error) {
		return llm.StructuredRequest{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		payload, _ := json.Marshal(processcomparison.Payload{JobID: job.ID})
		if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error { return processor(fixture.ctx, tx, payload, fixture.tenantID) }); err != nil {
			t.Fatal(err)
		}
	}
	var records int64
	if err := fixture.h.AdminDB.Model(&database.FindingRecord{}).Joins("JOIN analysis.analysis_runs ON analysis.analysis_runs.id = analysis.findings.analysis_run_id").Where("analysis.analysis_runs.tenant_id=? AND analysis.analysis_runs.job_id IN ?", fixture.tenantID, jobIDs(jobs)).Count(&records).Error; err != nil {
		t.Fatal(err)
	}
	if records != 0 {
		t.Fatalf("current-only empty view produced %d clean-condition findings", records)
	}
	ctx := reportcore.Context{Asset: reportcore.AssetContext{ID: fixture.assetID.String(), Name: "QA asset", ExternalKey: "F6", Address: "Local"}, Participant: reportcore.ParticipantContext{ID: "qa", Name: "QA"}, Template: reportcore.TemplateContext{ID: "qa-template", Name: "QA template", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: time.Now().UTC().Format(time.RFC3339)}}
	if _, err := generate_snapshot.Create(fixture.ctx, fixture.h.AdminDB, generate_snapshot.Input{TenantID: fixture.tenantID, InspectionID: fixture.inspectionID, VideoPlanID: &plan.ID, Mode: "CONSOLIDATED", Classification: "ATTENTION", TemplateVersionID: "qa-template-v1", ReferenceVersionID: "current-only", PromptDigest: "qa-prompt", Context: ctx, Requirements: []reportcore.Requirement{{Key: "video:current", Section: "Vídeo", Label: "Condição atual", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "INCONCLUSIVE"}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	var snapshot database.ReportSnapshot
	if err := fixture.h.AdminDB.Where("tenant_id=? AND inspection_id=? AND video_plan_id=?", fixture.tenantID, fixture.inspectionID, plan.ID).Take(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	var decoded reportcore.Snapshot
	if err := json.Unmarshal(snapshot.CanonicalJSON, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Findings) != 0 || len(decoded.Requirements) == 0 {
		t.Fatalf("inconclusive snapshot has clean findings or no requirements: %+v", decoded)
	}
}

func jobIDs(jobs []database.ComparisonJob) []identity.ID {
	ids := make([]identity.ID, 0, len(jobs))
	for _, job := range jobs {
		ids = append(ids, job.ID)
	}
	return ids
}

type currentOnlyTask04Gateway struct{ onRequest func(llm.StructuredRequest) }

func (g currentOnlyTask04Gateway) CompleteStructured(_ context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
	g.onRequest(request)
	response, err := json.Marshal(map[string]any{"noRelevantChange": nil, "findings": []any{}})
	return llm.StructuredResult{Provider: "test", Model: "fixture", GatewayRequestID: "f6-it192", JSON: response}, err
}

func TestVV_IT_179VideoConditionCategoriesSurviveClassificationAndSnapshot(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, true)
	categories := []string{"CONSERVATION", "INVENTORY", "CLEANLINESS", "OBSTRUCTION", "EVIDENCE_QUALITY"}
	_, plan, job := executeTask04Condition(t, fixture, categories)
	classifyHandler, err := classify.Setup(classify.Dependencies{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	classificationPayload, _ := json.Marshal(map[string]any{"inspectionId": fixture.inspectionID})
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return classifyHandler(fixture.ctx, tx, events.RawEnvelope{ID: identity.NewID(), Type: "analysis.comparison_completed.v1", TenantID: fixture.tenantID, CorrelationID: "f6-it179-" + fixture.inspectionID.String(), Payload: classificationPayload})
	}); err != nil {
		t.Fatal(err)
	}
	var records []database.FindingRecord
	if err := fixture.h.AdminDB.Where("tenant_id=? AND analysis_run_id IN (SELECT id FROM analysis.analysis_runs WHERE job_id=?)", fixture.tenantID, job.ID).Find(&records).Error; err != nil {
		t.Fatal(err)
	}
	findings := make([]reportcore.Finding, 0, len(records))
	for _, record := range records {
		var evidence []string
		if err := json.Unmarshal(record.Evidence, &evidence); err != nil {
			t.Fatal(err)
		}
		findings = append(findings, reportcore.Finding{Category: record.Category, Title: record.Title, Description: record.Description, Severity: record.Severity, Quality: record.Quality, RecommendedAction: record.RecommendedAction, Confidence: record.Confidence, EvidenceIDs: evidence})
	}
	if len(findings) != len(categories) {
		t.Fatalf("process_comparison persisted %d findings, want %d", len(findings), len(categories))
	}
	var inspection database.Inspection
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.inspectionID).Take(&inspection).Error; err != nil {
		t.Fatal(err)
	}
	var classification database.ClassificationRun
	if err := fixture.h.AdminDB.Where("tenant_id=? AND inspection_id=? AND video_plan_id=?", fixture.tenantID, fixture.inspectionID, plan.ID).Take(&classification).Error; err != nil {
		t.Fatal(err)
	}
	ctx := reportcore.Context{Asset: reportcore.AssetContext{ID: fixture.assetID.String(), Name: "QA asset", ExternalKey: "F6", Address: "Local"}, Participant: reportcore.ParticipantContext{ID: "qa", Name: "QA"}, Template: reportcore.TemplateContext{ID: "qa-template", Name: "QA template", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: time.Now().UTC().Format(time.RFC3339)}}
	_, err = generate_snapshot.Create(fixture.ctx, fixture.h.AdminDB, generate_snapshot.Input{TenantID: fixture.tenantID, InspectionID: inspection.ID, VideoPlanID: &plan.ID, Mode: "HISTORICAL", Classification: classification.Classification, TemplateVersionID: "qa-template-v1", ReferenceVersionID: fixture.originVersionID.String(), PromptDigest: "qa-prompt", Context: ctx, Findings: findings}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var snapshot database.ReportSnapshot
	if err := fixture.h.AdminDB.Where("tenant_id=? AND inspection_id=? AND video_plan_id=?", fixture.tenantID, inspection.ID, plan.ID).Take(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	var decoded reportcore.Snapshot
	if err := json.Unmarshal(snapshot.CanonicalJSON, &decoded); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, finding := range decoded.Findings {
		got[finding.Category] = true
	}
	for _, category := range categories {
		if !got[category] {
			t.Errorf("report snapshot lost category %s: %+v", category, decoded.Findings)
		}
	}
}
