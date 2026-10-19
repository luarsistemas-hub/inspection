//go:build integration

package schedule_video_plan

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	buildrequest "inspection/services/inspection/internal/features/analysis/build_request"
	processcomparison "inspection/services/inspection/internal/features/analysis/process_comparison"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestVV_IT_213UnfilmedExpectedSinkIsReportedUnobservedNotAbsent(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, false)
	plan := runTask04Plan(t, fixture)
	snapshot := generateTask04GapReport(t, fixture, plan, nil, nil)
	sink := task04Requirement(t, snapshot, "kitchen — sink")
	if sink.AnalysisStatus != "INCONCLUSIVE" || sink.Coverage != generate_snapshot.VideoUnobservedCoverage {
		t.Fatalf("unfilmed sink = %+v, want INCONCLUSIVE with unobserved coverage", sink)
	}
	if len(snapshot.Findings) != 0 {
		t.Fatalf("unfilmed sink produced findings: %+v", snapshot.Findings)
	}
	html := task04SnapshotHTML(t, fixture, plan)
	if !strings.Contains(html, "Não observado no vídeo atual") || strings.Contains(html, "NO_CURRENT_VIEW") || strings.Contains(strings.ToLower(html), "ausente") || strings.Contains(strings.ToLower(html), "removid") {
		t.Fatal("internal report does not label the sink as unobserved, or claims it is absent")
	}
}

func TestVV_IT_214DurationCompliantSourceKeepsOmittedBathroomGap(t *testing.T) {
	fixture := newTask04PlanFixture(t, true, true)
	now := time.Now().UTC()
	// A 900-second source sits exactly at the 15-minute limit.
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.tenantID, MediaID: fixture.currentMediaID(), Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 900_000_000, Width: 1280, Height: 720, FrameRate: 30, ScreeningStatus: "PASSED", Status: "RETAINED", CreatedAt: now, UpdatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	plan := runTask04Plan(t, fixture)
	snapshot := generateTask04GapReport(t, fixture, plan, nil, nil)
	bathroom := task04Requirement(t, snapshot, "bathroom — sink")
	if bathroom.AnalysisStatus != "INCONCLUSIVE" || bathroom.Coverage != generate_snapshot.VideoUnobservedCoverage {
		t.Fatalf("omitted bathroom = %+v, want retained gap", bathroom)
	}
	kitchen := task04Requirement(t, snapshot, "kitchen — sink")
	if kitchen.AnalysisStatus == "INCONCLUSIVE" || kitchen.Coverage == generate_snapshot.VideoUnobservedCoverage {
		t.Fatalf("filmed kitchen was reported as a gap: %+v", kitchen)
	}
}

func TestVV_IT_217InterruptedSnapshotTransactionExposesNoReport(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, false)
	plan := runTask04Plan(t, fixture)
	sqlDB, err := fixture.h.AdminDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	interrupted, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	errInterrupted := errors.New("snapshot transaction interrupted")
	if err := interrupted.Callback().Create().Before("gorm:create").Register("vv_it217:interrupt", func(db *gorm.DB) {
		if strings.HasSuffix((database.ReportSnapshot{}).TableName(), "."+db.Statement.Table) {
			_ = db.AddError(errInterrupted)
		}
	}); err != nil {
		t.Fatal(err)
	}
	requirements := task04VideoRequirements(t, fixture, plan)
	if _, err := generate_snapshot.Create(fixture.ctx, interrupted, task04GapReportInput(fixture, plan, requirements, nil, nil), time.Now()); !errors.Is(err, errInterrupted) {
		t.Fatalf("interrupted snapshot error = %v", err)
	}
	var snapshots int64
	if err := fixture.h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", fixture.tenantID, fixture.inspectionID).Count(&snapshots).Error; err != nil {
		t.Fatal(err)
	}
	if snapshots != 0 {
		t.Fatalf("interrupted transaction exposed %d report snapshots", snapshots)
	}
	snapshot := generateTask04GapReport(t, fixture, plan, nil, nil)
	if sink := task04Requirement(t, snapshot, "kitchen — sink"); sink.AnalysisStatus != "INCONCLUSIVE" {
		t.Fatalf("retried report lost the sink limitation: %+v", sink)
	}
}

func TestVV_IT_218RetriedGenerationKeepsOneSinkGuidanceEntry(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, false)
	plan := runTask04Plan(t, fixture)
	requirements := task04VideoRequirements(t, fixture, plan)
	first, err := generate_snapshot.Create(fixture.ctx, fixture.h.AdminDB, task04GapReportInput(fixture, plan, requirements, nil, nil), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	second, err := generate_snapshot.Create(fixture.ctx, fixture.h.AdminDB, task04GapReportInput(fixture, plan, task04VideoRequirements(t, fixture, plan), nil, nil), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if first.SnapshotID != second.SnapshotID || second.Version != 1 {
		t.Fatalf("retried generation created another version: first=%+v second=%+v", first, second)
	}
	snapshot := task04StoredSnapshot(t, fixture, plan)
	var guidance []string
	for _, requirement := range snapshot.Requirements {
		if requirement.Instructions != "" {
			guidance = append(guidance, requirement.Instructions)
		}
	}
	if len(guidance) != 1 || !strings.Contains(guidance[0], "kitchen — sink") || !strings.Contains(guidance[0], "vista wide") {
		t.Fatalf("sink guidance = %q, want one entry naming the sink and the requested view", guidance)
	}
}

func TestVV_IT_251CompletedG1JobsAndGapItemsCreateInternalReportWithoutApprovalGate(t *testing.T) {
	fixture := newTask04PlanFixture(t, true, true)
	plan := runTask04Plan(t, fixture)
	prepareTask04FrameObjects(t, fixture)
	var kitchen database.VideoPlanItem
	if err := fixture.h.AdminDB.Where("tenant_id=? AND plan_id=? AND component_id=?", fixture.tenantID, plan.ID, fixture.kitchenID).Take(&kitchen).Error; err != nil {
		t.Fatal(err)
	}
	var job database.ComparisonJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND video_plan_id=? AND video_plan_item_id=?", fixture.tenantID, plan.ID, kitchen.ID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	builder, err := buildrequest.Setup(buildrequest.Dependencies{Store: fixture.h.Store})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := processcomparison.Setup(processcomparison.Dependencies{Gateway: &task04ConditionGateway{categories: []string{"CONSERVATION"}}, BuildRequest: builder, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(processcomparison.Payload{JobID: job.ID})
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error { return processor(fixture.ctx, tx, payload, fixture.tenantID) }); err != nil {
		t.Fatal(err)
	}
	var open int64
	if err := fixture.h.AdminDB.Model(&database.ComparisonJob{}).Where("tenant_id=? AND video_plan_id=? AND status NOT IN ?", fixture.tenantID, plan.ID, []string{"COMPLETED", "INCONCLUSIVE", "FAILED"}).Count(&open).Error; err != nil {
		t.Fatal(err)
	}
	if open != 0 {
		t.Fatalf("G1 still has %d unfinished jobs", open)
	}
	var records []database.FindingRecord
	if err := fixture.h.AdminDB.Where("tenant_id=? AND analysis_run_id IN (SELECT id FROM analysis.analysis_runs WHERE job_id=?)", fixture.tenantID, job.ID).Find(&records).Error; err != nil {
		t.Fatal(err)
	}
	findings := make([]reportcore.Finding, 0, len(records))
	for _, record := range records {
		var evidence []string
		_ = json.Unmarshal(record.Evidence, &evidence)
		findings = append(findings, reportcore.Finding{ID: record.ID.String(), Category: record.Category, Title: record.Title, Description: record.Description, Severity: record.Severity, Confidence: record.Confidence, EvidenceIDs: evidence, Quality: record.Quality, RecommendedAction: record.RecommendedAction})
	}
	snapshot := generateTask04GapReport(t, fixture, plan, findings, nil)
	if len(snapshot.Findings) == 0 || task04Requirement(t, snapshot, "bathroom — sink").AnalysisStatus != "INCONCLUSIVE" {
		t.Fatalf("RPT1 lacks G1 findings or the explicit bathroom gap: %+v", snapshot)
	}
	var row database.ReportSnapshot
	if err := fixture.h.AdminDB.Where("tenant_id=? AND inspection_id=? AND video_plan_id=?", fixture.tenantID, fixture.inspectionID, plan.ID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	var publications int64
	if err := fixture.h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND snapshot_id=?", fixture.tenantID, row.ID).Count(&publications).Error; err != nil {
		t.Fatal(err)
	}
	if row.VersionNumber != 1 || publications != 0 {
		t.Fatalf("RPT1 version=%d publications=%d, want internal version 1", row.VersionNumber, publications)
	}
}

func TestVV_IT_255SnapshotWithFrameOutsidePlanFailsFrameIntegrity(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, true)
	plan := runTask04Plan(t, fixture)
	var member database.VideoFrame
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id IN (SELECT frame_id FROM analysis.video_plan_item_frames WHERE tenant_id=? AND plan_item_id IN (SELECT id FROM analysis.video_plan_items WHERE tenant_id=? AND plan_id=?))", fixture.tenantID, fixture.tenantID, fixture.tenantID, plan.ID).Order("pts_us DESC").First(&member).Error; err != nil {
		t.Fatal(err)
	}
	f99 := database.VideoFrame{ID: identity.NewID(), TenantID: fixture.tenantID, SourceMediaID: member.SourceMediaID, PreparationID: member.PreparationID, PTSMicros: 99_000_000, SHA256: member.SHA256, ObjectKey: "f6-current-frame/F99", Width: 1, Height: 1, Role: "CURRENT", SizeBytes: 1, CreatedAt: time.Now().UTC()}
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error { return tx.Create(&f99).Error }); err != nil {
		t.Fatal(err)
	}
	requirements := task04VideoRequirements(t, fixture, plan)
	_, err := generate_snapshot.Create(fixture.ctx, fixture.h.AdminDB, task04GapReportInput(fixture, plan, requirements, nil, []reportcore.Evidence{task04FrameEvidence(f99)}), time.Now())
	if !errors.Is(err, generate_snapshot.ErrFrameIntegrity) || err.Error() != "FRAME_INTEGRITY_FAILED" {
		t.Fatalf("F99 snapshot error = %v, want FRAME_INTEGRITY_FAILED", err)
	}
	var snapshots int64
	if err := fixture.h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", fixture.tenantID, fixture.inspectionID).Count(&snapshots).Error; err != nil {
		t.Fatal(err)
	}
	if snapshots != 0 {
		t.Fatalf("F99 failure still stored %d snapshots", snapshots)
	}
	snapshot := generateTask04GapReport(t, fixture, plan, nil, []reportcore.Evidence{task04FrameEvidence(member)})
	if len(snapshot.Evidence) != 1 || snapshot.Evidence[0].FrameID != member.ID.String() {
		t.Fatalf("plan member evidence was substituted: %+v", snapshot.Evidence)
	}
}

func task04VideoRequirements(t *testing.T, fixture task04PlanFixture, plan database.VideoPlan) []reportcore.Requirement {
	t.Helper()
	var requirements []reportcore.Requirement
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		var err error
		requirements, _, err = generate_snapshot.VideoPlanRequirements(fixture.ctx, tx, fixture.tenantID, plan, plan.ReferenceVersionID != nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return requirements
}

func task04GapReportInput(fixture task04PlanFixture, plan database.VideoPlan, requirements []reportcore.Requirement, findings []reportcore.Finding, evidence []reportcore.Evidence) generate_snapshot.Input {
	ctx := reportcore.Context{Asset: reportcore.AssetContext{ID: fixture.assetID.String(), Name: "QA asset", ExternalKey: "F6", Address: "Local"}, Participant: reportcore.ParticipantContext{ID: "qa", Name: "QA"}, Template: reportcore.TemplateContext{ID: "qa-template", Name: "QA template", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: "2026-10-18T00:00:00Z"}}
	// The worker records reference-free for inspections without a baseline.
	reference := fixture.originVersionID.String()
	if plan.OriginPreparationID == nil {
		reference = generate_snapshot.ReferenceFree
	}
	return generate_snapshot.Input{TenantID: fixture.tenantID, InspectionID: fixture.inspectionID, VideoPlanID: &plan.ID, Mode: "HISTORICAL", Classification: "ATTENTION", TemplateVersionID: "qa-template-v1", ReferenceVersionID: reference, PromptDigest: "qa-prompt", Context: ctx, Requirements: requirements, Findings: findings, Evidence: evidence}
}

func generateTask04GapReport(t *testing.T, fixture task04PlanFixture, plan database.VideoPlan, findings []reportcore.Finding, evidence []reportcore.Evidence) reportcore.Snapshot {
	t.Helper()
	if _, err := generate_snapshot.Create(fixture.ctx, fixture.h.AdminDB, task04GapReportInput(fixture, plan, task04VideoRequirements(t, fixture, plan), findings, evidence), time.Now()); err != nil {
		t.Fatal(err)
	}
	return task04StoredSnapshot(t, fixture, plan)
}

func task04StoredSnapshot(t *testing.T, fixture task04PlanFixture, plan database.VideoPlan) reportcore.Snapshot {
	t.Helper()
	var row database.ReportSnapshot
	if err := fixture.h.AdminDB.Where("tenant_id=? AND inspection_id=? AND video_plan_id=?", fixture.tenantID, fixture.inspectionID, plan.ID).Order("version_number DESC").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot reportcore.Snapshot
	if err := json.Unmarshal(row.CanonicalJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func task04SnapshotHTML(t *testing.T, fixture task04PlanFixture, plan database.VideoPlan) string {
	t.Helper()
	var row database.ReportSnapshot
	if err := fixture.h.AdminDB.Where("tenant_id=? AND inspection_id=? AND video_plan_id=?", fixture.tenantID, fixture.inspectionID, plan.ID).Order("version_number DESC").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	var html string
	if err := json.Unmarshal(row.HTML, &html); err != nil {
		t.Fatal(err)
	}
	return html
}

func task04Requirement(t *testing.T, snapshot reportcore.Snapshot, label string) reportcore.Requirement {
	t.Helper()
	for _, requirement := range snapshot.Requirements {
		if requirement.Label == label {
			return requirement
		}
	}
	t.Fatalf("report has no requirement labelled %q: %+v", label, snapshot.Requirements)
	return reportcore.Requirement{}
}

func task04FrameEvidence(frame database.VideoFrame) reportcore.Evidence {
	timestamp := frame.PTSMicros / 1000
	return reportcore.Evidence{ID: frame.ID.String(), RequirementKey: "video:frame", Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: frame.ID.String(), SourceMediaID: frame.SourceMediaID.String(), PreparationID: frame.PreparationID.String(), PTSMicros: &frame.PTSMicros, TimestampMS: &timestamp, FrameDigest: frame.SHA256}
}
