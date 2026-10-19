//go:build integration

package schedule_video_plan

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	buildrequest "inspection/services/inspection/internal/features/analysis/build_request"
	processcomparison "inspection/services/inspection/internal/features/analysis/process_comparison"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"

	"gorm.io/gorm"
)

// task07NoChangeGateway reports a comparable pair with no relevant change.
type task07NoChangeGateway struct{}

func (task07NoChangeGateway) CompleteStructured(context.Context, llm.StructuredRequest) (llm.StructuredResult, error) {
	return llm.StructuredResult{Provider: "test", Model: "fixture", GatewayRequestID: "f6-task07-no-change", JSON: json.RawMessage(`{"noRelevantChange":true,"findings":[]}`)}, nil
}

// task07CurrentConditionGateway describes the observed current condition only.
type task07CurrentConditionGateway struct{}

func (task07CurrentConditionGateway) CompleteStructured(_ context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
	evidence := []string{}
	for _, image := range request.Images {
		evidence = append(evidence, image.EvidenceID)
	}
	response, err := json.Marshal(map[string]any{"noRelevantChange": nil, "findings": []map[string]any{{"category": "CONSERVATION", "title": "Risco na pia", "description": "Risco visível na pia da cozinha.", "severity": "LOW", "confidence": 0.9, "evidenceIds": evidence, "quality": "ADEQUATE", "recommendedAction": "Acompanhar na próxima vistoria"}}})
	return llm.StructuredResult{Provider: "test", Model: "fixture", GatewayRequestID: "f6-task07-current", JSON: response}, err
}

// processTask07PlanJobs runs every open comparison job of the plan through
// the production builder and processor.
func processTask07PlanJobs(t *testing.T, fixture task04PlanFixture, plan database.VideoPlan, gateway llm.Gateway) {
	t.Helper()
	prepareTask04FrameObjects(t, fixture)
	builder, err := buildrequest.Setup(buildrequest.Dependencies{Store: fixture.h.Store})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := processcomparison.Setup(processcomparison.Dependencies{Gateway: gateway, BuildRequest: builder, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	var jobs []database.ComparisonJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND video_plan_id=?", fixture.tenantID, plan.ID).Order("id").Find(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if len(jobs) == 0 {
		t.Fatal("plan has no comparison jobs")
	}
	for _, job := range jobs {
		payload, _ := json.Marshal(processcomparison.Payload{JobID: job.ID})
		if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error { return processor(fixture.ctx, tx, payload, fixture.tenantID) }); err != nil {
			t.Fatal(err)
		}
	}
}

// task07WorkerReport mirrors the report handler of the worker: plan
// requirements overlaid with each job's mode, status and change flag, plus
// the persisted findings.
func task07WorkerReport(t *testing.T, fixture task04PlanFixture, plan database.VideoPlan) (reportcore.Snapshot, string) {
	t.Helper()
	requirements := task04VideoRequirements(t, fixture, plan)
	var findings []reportcore.Finding
	var evidence []reportcore.Evidence
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var jobs []database.ComparisonJob
		if err := tx.Where("tenant_id=? AND video_plan_id=?", fixture.tenantID, plan.ID).Order("requirement_key ASC,video_core_group ASC").Find(&jobs).Error; err != nil {
			return err
		}
		for _, job := range jobs {
			// Like reportVideoPlanEvidence: every job contributes its item's frames.
			var memberships []database.VideoPlanItemFrame
			if err := tx.Where("tenant_id=? AND plan_item_id=?", fixture.tenantID, *job.VideoPlanItemID).Order("core_group ASC,role ASC,pair_order ASC").Find(&memberships).Error; err != nil {
				return err
			}
			var planItem database.VideoPlanItem
			if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, *job.VideoPlanItemID).Take(&planItem).Error; err != nil {
				return err
			}
			for _, membership := range memberships {
				var frame database.VideoFrame
				if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, membership.FrameID).Take(&frame).Error; err != nil {
					return err
				}
				role := "CURRENT"
				if membership.Role == "ORIGIN" {
					role = "REFERENCE"
				}
				pts, ms := frame.PTSMicros, frame.PTSMicros/1000
				evidence = append(evidence, reportcore.Evidence{ID: frame.ID.String(), RequirementKey: planItem.RequirementKey, Role: role, EvidenceKind: "VIDEO_FRAME", FrameID: frame.ID.String(), SourceMediaID: frame.SourceMediaID.String(), PreparationID: frame.PreparationID.String(), PTSMicros: &pts, TimestampMS: &ms, FrameDigest: frame.SHA256, CaptureSource: "VIDEO", DisplayDigest: frame.SHA256, Availability: "AVAILABLE"})
			}
		}
		for _, job := range jobs {
			var run database.AnalysisRun
			if err := tx.Where("tenant_id=? AND job_id=?", fixture.tenantID, job.ID).Order("created_at DESC").First(&run).Error; err == gorm.ErrRecordNotFound {
				t.Logf("job %s (%s) status=%s has no analysis run", job.ID, job.RequirementKey, job.Status)
				continue
			} else if err != nil {
				return err
			}
			var output struct {
				NoRelevantChange *bool `json:"noRelevantChange"`
			}
			_ = json.Unmarshal(run.Output, &output)
			var item database.VideoPlanItem
			if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, *job.VideoPlanItemID).Take(&item).Error; err != nil {
				return err
			}
			mode := "CURRENT_ONLY"
			if item.Outcome == "MATCHED" {
				mode = "COMPARE_ORIGIN_CURRENT"
			}
			for i := range requirements {
				if requirements[i].Key == job.RequirementKey {
					requirements[i].AnalysisMode, requirements[i].AnalysisStatus, requirements[i].NoRelevantChange = mode, job.Status, output.NoRelevantChange
				}
			}
			var rows []database.FindingRecord
			if err := tx.Where("tenant_id=? AND analysis_run_id=?", fixture.tenantID, run.ID).Find(&rows).Error; err != nil {
				return err
			}
			for _, row := range rows {
				var evidence []string
				_ = json.Unmarshal(row.Evidence, &evidence)
				findings = append(findings, reportcore.Finding{ID: row.ID.String(), Category: row.Category, Title: row.Title, Description: row.Description, Severity: row.Severity, Confidence: row.Confidence, EvidenceIDs: evidence, Quality: row.Quality, RecommendedAction: row.RecommendedAction})
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := generate_snapshot.Create(fixture.ctx, fixture.h.AdminDB, task04GapReportInput(fixture, plan, requirements, findings, evidence), time.Now()); err != nil {
		t.Fatal(err)
	}
	return task04StoredSnapshot(t, fixture, plan), task04SnapshotHTML(t, fixture, plan)
}

func task07ObservedScope(labels ...string) string {
	return "Conclusões limitadas ao escopo observado: " + strings.Join(labels, "; ") + ". Isto não constitui avaliação do imóvel inteiro."
}

func assertTask07Statements(t *testing.T, snapshot reportcore.Snapshot, html string, want ...string) {
	t.Helper()
	if len(snapshot.ScopeStatements) != len(want) {
		t.Fatalf("scope statements=%q, want %q", snapshot.ScopeStatements, want)
	}
	for i, statement := range want {
		if snapshot.ScopeStatements[i] != statement || !strings.Contains(html, statement) {
			t.Fatalf("scope statement %d=%q (in HTML: %t), want %q", i, snapshot.ScopeStatements[i], strings.Contains(html, statement), statement)
		}
	}
}

func TestVV_IT_207MatchedKitchenFindingAndMissingBathroomGap(t *testing.T) {
	fixture := newTask04PlanFixture(t, true, true)
	plan := runTask04Plan(t, fixture)
	processTask07PlanJobs(t, fixture, plan, &task04ConditionGateway{categories: []string{"CONSERVATION"}})
	snapshot, html := task07WorkerReport(t, fixture, plan)
	kitchen, bathroom := task04Requirement(t, snapshot, "kitchen — sink"), task04Requirement(t, snapshot, "bathroom — sink")
	if kitchen.AnalysisMode != "COMPARE_ORIGIN_CURRENT" || kitchen.AnalysisStatus != "COMPLETED" || len(snapshot.Findings) == 0 {
		t.Fatalf("kitchen=%+v findings=%d; want completed comparison with a finding", kitchen, len(snapshot.Findings))
	}
	if bathroom.AnalysisStatus != "INCONCLUSIVE" || bathroom.Coverage != generate_snapshot.VideoUnobservedCoverage {
		t.Fatalf("bathroom=%+v; want inconclusive visual gap", bathroom)
	}
	assertTask07Statements(t, snapshot, html, task07ObservedScope("kitchen — sink"))
}

func TestVV_IT_208UnchangedKitchenConclusionNamesKitchenOnly(t *testing.T) {
	fixture := newTask04PlanFixture(t, true, true)
	plan := runTask04Plan(t, fixture)
	processTask07PlanJobs(t, fixture, plan, task07NoChangeGateway{})
	snapshot, html := task07WorkerReport(t, fixture, plan)
	kitchen := task04Requirement(t, snapshot, "kitchen — sink")
	if kitchen.AnalysisStatus != "COMPLETED" || kitchen.NoRelevantChange == nil || !*kitchen.NoRelevantChange || len(snapshot.Findings) != 0 {
		t.Fatalf("kitchen=%+v findings=%d; want completed pair without relevant change", kitchen, len(snapshot.Findings))
	}
	assertTask07Statements(t, snapshot, html, task07ObservedScope("kitchen — sink"))
	if lower := strings.ToLower(html); strings.Contains(lower, "imóvel satisfatório") || strings.Contains(lower, "imóvel em bom estado") {
		t.Fatal("report claims a whole-property satisfactory status")
	}
}

func testTask07AllComparativeItemsInconclusive(t *testing.T) {
	fixture := newTask04PlanFixture(t, true, true)
	plan := runTask04Plan(t, fixture)
	processTask07PlanJobs(t, fixture, plan, &task04ConditionGateway{categories: []string{"EVIDENCE_QUALITY"}})
	snapshot, html := task07WorkerReport(t, fixture, plan)
	for _, requirement := range snapshot.Requirements {
		if requirement.AnalysisStatus == "COMPLETED" {
			t.Fatalf("requirement %q completed; want every G1 item inconclusive", requirement.Label)
		}
	}
	assertTask07Statements(t, snapshot, html, generate_snapshot.VideoNoComparativeConclusion)
}

func TestVV_IT_210AllG1ItemsInconclusiveHaveNoComparativeConclusion(t *testing.T) {
	testTask07AllComparativeItemsInconclusive(t)
}

func TestVV_IT_256AllPlanItemsInconclusiveReportHasNoComparativeConclusion(t *testing.T) {
	testTask07AllComparativeItemsInconclusive(t)
}

func TestVV_IT_253CurrentOnlyReportSaysNoReferenceComparison(t *testing.T) {
	fixture := newTask04PlanFixtureWithOriginStatus(t, false, true, "READY", true)
	plan := runTask04Plan(t, fixture)
	if plan.OriginPreparationID != nil {
		t.Fatalf("current-only plan pinned origin %s", plan.OriginPreparationID)
	}
	snapshot, html := task07WorkerReportWithoutJobs(t, fixture, plan)
	assertTask07Statements(t, snapshot, html, generate_snapshot.VideoNoReferenceStatement)
}

func TestVV_IT_196KitchenOnlyCurrentVideoConfinesSummaryToKitchen(t *testing.T) {
	fixture := newTask04PlanFixtureWithOriginStatus(t, false, true, "READY", true)
	plan := runTask04Plan(t, fixture)
	processTask07PlanJobs(t, fixture, plan, task07CurrentConditionGateway{})
	snapshot, html := task07WorkerReport(t, fixture, plan)
	for _, requirement := range snapshot.Requirements {
		if requirement.AnalysisMode != "CURRENT_ONLY" {
			t.Fatalf("current-only requirement %+v used comparison mode", requirement)
		}
	}
	assertTask07Statements(t, snapshot, html, generate_snapshot.VideoNoReferenceStatement, task07ObservedScope("kitchen — sink"))
}

// task07WorkerReportWithoutJobs generates the report before any job ran.
func task07WorkerReportWithoutJobs(t *testing.T, fixture task04PlanFixture, plan database.VideoPlan) (reportcore.Snapshot, string) {
	t.Helper()
	return generateTask04GapReport(t, fixture, plan, nil, nil), task04SnapshotHTML(t, fixture, plan)
}

func TestVV_IT_101CurrentOnlyThreeRoomsReportDeclaresObservedScopeOnly(t *testing.T) {
	fixture := newTask04PlanFixtureWithOriginStatus(t, false, true, "READY", true)
	// The fixture property has 20 rooms; the current video shows only three.
	var kitchenFrame database.VideoFrame
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.currentFrame).Take(&kitchenFrame).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, room := range [][2]string{{"living room", "window"}, {"bedroom", "wardrobe"}} {
		frame := kitchenFrame
		frame.ID, frame.PTSMicros, frame.ObjectKey, frame.CreatedAt = identity.NewID(), kitchenFrame.PTSMicros+int64(i+1)*10_000_000, "f6-current-frame/"+identity.NewID().String(), now
		if err := fixture.h.AdminDB.Create(&frame).Error; err != nil {
			t.Fatal(err)
		}
		if err := fixture.h.AdminDB.Create(&database.VideoScopeItem{ID: identity.NewID(), TenantID: fixture.tenantID, PreparationID: fixture.currentPreparation, FrameID: frame.ID, EnvironmentCandidate: room[0], ComponentLabel: room[1], ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now.Add(time.Duration(i+1) * time.Second)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	plan := runTask04Plan(t, fixture)
	processTask07PlanJobs(t, fixture, plan, task07CurrentConditionGateway{})
	snapshot, html := task07WorkerReport(t, fixture, plan)
	if len(snapshot.ScopeStatements) != 2 || snapshot.ScopeStatements[0] != generate_snapshot.VideoNoReferenceStatement {
		t.Fatalf("scope statements=%q; want no-reference plus observed scope", snapshot.ScopeStatements)
	}
	observed := snapshot.ScopeStatements[1]
	for _, label := range []string{"kitchen — sink", "living room — window", "bedroom — wardrobe"} {
		if !strings.Contains(observed, label) {
			t.Fatalf("observed scope %q misses %s", observed, label)
		}
	}
	if strings.Count(observed, " — ") != 3 || !strings.Contains(observed, "não constitui avaliação do imóvel inteiro") || !strings.Contains(html, observed) {
		t.Fatalf("observed scope %q does not declare exactly the 3 observed rooms without full coverage", observed)
	}
}

func TestVV_IT_205LaterComparativeV3KeepsV2ReportWithoutReferenceComparison(t *testing.T) {
	fixture := newTask04PlanFixtureWithOriginStatus(t, false, true, "READY", true)
	plan := runTask04Plan(t, fixture)
	processTask07PlanJobs(t, fixture, plan, task07CurrentConditionGateway{})
	v2, _ := task07WorkerReport(t, fixture, plan)
	var before database.ReportSnapshot
	if err := fixture.h.AdminDB.Where("tenant_id=? AND inspection_id=?", fixture.tenantID, fixture.inspectionID).Order("version_number DESC").Take(&before).Error; err != nil {
		t.Fatal(err)
	}
	// Later, the asset gets an active origin and a comparative V3 is created.
	now := time.Now().UTC()
	originID, versionID, v3, responsibility := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var base database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.inspectionID).Take(&base).Error; err != nil {
			return err
		}
		later := base
		later.ID, later.SourceKey, later.Status, later.ActiveVideoPlanID = v3, "vv-it-205-v3-"+v3.String(), "PLANNED", nil
		if err := tx.Create(&later).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Responsibility{ID: responsibility, TenantID: fixture.tenantID, InspectionID: v3, ParticipantID: base.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Origin{ID: originID, TenantID: fixture.tenantID, AssetID: fixture.assetID, ActiveVersionID: &versionID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.OriginVersion{ID: versionID, TenantID: fixture.tenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: responsibility, Status: "ACTIVE", MediaKind: "VIDEO", VideoPreparationID: &fixture.currentPreparation, IdempotencyKey: "vv-it-205-" + versionID.String(), CreatedAt: now, ActivatedAt: &now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.ReferenceSnapshot{ID: identity.NewID(), TenantID: fixture.tenantID, InspectionID: v3, ReferenceVersionID: &versionID, ComparisonMode: "FIXED_ORIGIN", Payload: []byte(`{"comparisonMode":"FIXED_ORIGIN"}`), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	var after database.ReportSnapshot
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, before.ID).Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.JSONDigest != before.JSONDigest || after.HTMLDigest != before.HTMLDigest {
		t.Fatal("V2 report changed after the comparative V3 was created")
	}
	// Regenerating V2 from its own pinned (reference-free) inspection is idempotent.
	replayed, html := task07WorkerReport(t, fixture, plan)
	if len(v2.ScopeStatements) == 0 || v2.ScopeStatements[0] != generate_snapshot.VideoNoReferenceStatement || len(replayed.ScopeStatements) == 0 || replayed.ScopeStatements[0] != generate_snapshot.VideoNoReferenceStatement || !strings.Contains(html, generate_snapshot.VideoNoReferenceStatement) {
		t.Fatalf("historical V2 lost its no-reference statement: before=%q after=%q", v2.ScopeStatements, replayed.ScopeStatements)
	}
	var versions int64
	if err := fixture.h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", fixture.tenantID, fixture.inspectionID).Count(&versions).Error; err != nil {
		t.Fatal(err)
	}
	if versions != 1 {
		t.Fatalf("V2 replay created %d versions, want 1", versions)
	}
}
