//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

type task07VideoReport struct {
	snapshotID, planID, frameID identity.ID
	version                     int
}

// seedTask07VideoReport stores the report of plan generation G<n> whose
// finding D1 cites the plan's own frame.
func seedTask07VideoReport(t *testing.T, h *harness.Harness, seed harness.Task06Fixture, generation int) task07VideoReport {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	digest := strings.Repeat(strconv.Itoa(generation%10), 64)
	r := task07VideoReport{planID: identity.NewID(), frameID: identity.NewID()}
	media, preparationID, submissionID, itemID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	pts := int64(generation) * 10_000_000
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", seed.TenantID, seed.InspectionID).Update("media_kind", "VIDEO").Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: media, TenantID: seed.TenantID, ResponsibilityID: seed.ResponsibilityID, ObjectKey: "vv-task07-history/" + media.String(), ContentType: "video/mp4", SHA256: digest, SizeBytes: 10, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "vv-task07-history-" + media.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: seed.TenantID, DraftID: seed.DraftID, ResponsibilityID: seed.ResponsibilityID, VersionNumber: generation, Complete: true, Payload: json.RawMessage(`{}`), PayloadDigest: digest, SubmittedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: seed.TenantID, MediaID: media, SubmissionID: &submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &now, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: r.frameID, TenantID: seed.TenantID, SourceMediaID: media, PreparationID: preparationID, PTSMicros: pts, SHA256: digest, ObjectKey: "vv-task07-history/frame/" + r.frameID.String(), Width: 2, Height: 2, Role: "CURRENT", SizeBytes: 10, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlan{ID: r.planID, TenantID: seed.TenantID, InspectionID: seed.InspectionID, SubmissionID: submissionID, CurrentPreparationID: preparationID, Generation: generation, PlanDigest: digest, Status: "SEALED", SealedAt: &now, NextAttemptAt: now, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlanItem{ID: itemID, TenantID: seed.TenantID, PlanID: r.planID, ComponentID: identity.NewID(), RequirementKey: "video:kitchen", Outcome: "CURRENT_ONLY", InputDigest: digest, OriginFrameIDs: json.RawMessage(`[]`), CurrentFrameIDs: json.RawMessage(`["` + r.frameID.String() + `"]`), CoreGroups: json.RawMessage(`[]`), CurrentGroups: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoPlanItemFrame{ID: identity.NewID(), TenantID: seed.TenantID, PlanItemID: itemID, FrameID: r.frameID, Role: "CURRENT", CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	ms := pts / 1000
	finding := reportcore.Finding{ID: identity.NewID().String(), Category: "CONSERVATION", Title: "Risco D1 na pia (G" + strconv.Itoa(generation) + ")", Description: "Risco visível na pia.", Severity: "LOW", Confidence: 0.9, EvidenceIDs: []string{r.frameID.String()}, Quality: "ADEQUATE", RecommendedAction: "Acompanhar"}
	created, err := generate_snapshot.Create(ctx, h.AdminDB, generate_snapshot.Input{TenantID: seed.TenantID, InspectionID: seed.InspectionID, VideoPlanID: &r.planID, Mode: "CONSOLIDATED", Classification: "ATTENTION", TemplateVersionID: "template-v1", ReferenceVersionID: generate_snapshot.ReferenceFree, PromptDigest: "prompt-v1", Requirements: []reportcore.Requirement{{Key: "video:kitchen", Section: "Vídeo", Label: "Cozinha — Pia", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "COMPLETED"}}, Findings: []reportcore.Finding{finding}, Evidence: []reportcore.Evidence{{ID: r.frameID.String(), RequirementKey: "video:kitchen", Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: r.frameID.String(), SourceMediaID: media.String(), PreparationID: preparationID.String(), PTSMicros: &pts, TimestampMS: &ms, FrameDigest: digest, DisplayDigest: digest, Availability: "AVAILABLE", SourceAvailability: "RETAINED"}}, Context: reportcore.Context{Asset: reportcore.AssetContext{ID: seed.AssetID.String(), Name: "QA", Address: "QA"}, Participant: reportcore.ParticipantContext{ID: seed.ParticipantID.String(), Name: "QA"}, Template: reportcore.TemplateContext{ID: "template-v1", Name: "QA", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: now.Format(time.RFC3339Nano)}}}, now)
	if err != nil {
		t.Fatal(err)
	}
	r.snapshotID, r.version = created.SnapshotID, created.Version
	return r
}

type task07ReportView struct {
	ID       string `json:"id"`
	Version  int    `json:"version"`
	Findings []struct {
		Title       string   `json:"title"`
		EvidenceIDs []string `json:"evidenceIds"`
	} `json:"findings"`
	Evidence struct {
		Nodes []struct {
			FrameID string `json:"frameId"`
			PlanID  string `json:"planId"`
		} `json:"nodes"`
	} `json:"evidence"`
}

func readTask07Report(t *testing.T, fx api11Fixture, version string) task07ReportView {
	t.Helper()
	argument := ""
	if version != "" {
		argument = ",version:" + version
	}
	query := `query { report(inspectionId:"` + fx.seed.InspectionID.String() + `"` + argument + `) { id version findings { title evidenceIds } evidence(first:25) { nodes { frameId planId } } } }`
	response := executeTask06GraphQL(t, fx.server, query, fx.ctx)
	var payload struct {
		Data struct {
			Report *task07ReportView `json:"report"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil || payload.Data.Report == nil || strings.Contains(response, `"errors"`) {
		t.Fatalf("report version %q: %s (%v)", version, response, err)
	}
	return *payload.Data.Report
}

func assertTask07ReportUses(t *testing.T, view task07ReportView, report task07VideoReport, generation int) {
	t.Helper()
	if view.Version != report.version || len(view.Evidence.Nodes) != 1 || view.Evidence.Nodes[0].FrameID != report.frameID.String() || view.Evidence.Nodes[0].PlanID != report.planID.String() {
		t.Fatalf("report view=%+v; want version %d with G%d frame %s", view, report.version, generation, report.frameID)
	}
	if len(view.Findings) != 1 || len(view.Findings[0].EvidenceIDs) != 1 || view.Findings[0].EvidenceIDs[0] != report.frameID.String() || !strings.Contains(view.Findings[0].Title, "(G"+strconv.Itoa(generation)+")") {
		t.Fatalf("report findings=%+v; want D1 citing G%d frame %s", view.Findings, generation, report.frameID)
	}
}

func TestVV_IT_292SupersededRPT1KeepsItsLabelAndFramesAfterRPT2(t *testing.T) {
	fx := newAPI11Fixture(t)
	rpt1 := seedTask07VideoReport(t, fx.h, fx.seed, 2)
	rpt2 := seedTask07VideoReport(t, fx.h, fx.seed, 3)
	if rpt2.version != rpt1.version+1 || rpt1.frameID == rpt2.frameID {
		t.Fatalf("RPT1 v%d frame %s, RPT2 v%d frame %s; want consecutive versions with distinct frames", rpt1.version, rpt1.frameID, rpt2.version, rpt2.frameID)
	}
	assertTask07ReportUses(t, readTask07Report(t, fx, strconv.Itoa(rpt1.version)), rpt1, 2)
	assertTask07ReportUses(t, readTask07Report(t, fx, strconv.Itoa(rpt2.version)), rpt2, 3)
	assertTask07ReportUses(t, readTask07Report(t, fx, ""), rpt2, 3)
}

func TestVV_IT_290HistoricalFindingD1KeepsOriginalFrameIDsAfterG2(t *testing.T) {
	fx := newAPI11Fixture(t)
	rpt1 := seedTask07VideoReport(t, fx.h, fx.seed, 2)
	seedTask07VideoReport(t, fx.h, fx.seed, 3)
	for read := 0; read < 3; read++ {
		assertTask07ReportUses(t, readTask07Report(t, fx, strconv.Itoa(rpt1.version)), rpt1, 2)
	}
}

func TestVV_IT_221TwoHundredGapsAreAllExposedWithoutSatisfactorySummary(t *testing.T) {
	fx := newAPI11Fixture(t)
	base := seedTask07VideoReport(t, fx.h, fx.seed, 2)
	requirements := make([]reportcore.Requirement, 200)
	for i := range requirements {
		label := "Cômodo " + strconv.Itoa(i+1) + " — Parede"
		requirements[i] = reportcore.Requirement{Key: "video:gap-" + strconv.Itoa(i), Section: "Vídeo", Label: label, Coverage: generate_snapshot.VideoUnobservedCoverage, Instructions: generate_snapshot.VideoRecaptureGuidance(label, "wide"), AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "INCONCLUSIVE"}
	}
	now := time.Now().UTC()
	created, err := generate_snapshot.Create(context.Background(), fx.h.AdminDB, generate_snapshot.Input{TenantID: fx.seed.TenantID, InspectionID: fx.seed.InspectionID, VideoPlanID: &base.planID, Mode: "CONSOLIDATED", Classification: "ATTENTION", TemplateVersionID: "template-v1", ReferenceVersionID: identity.NewID().String(), PromptDigest: "prompt-v1", Requirements: requirements, Context: reportcore.Context{Asset: reportcore.AssetContext{ID: fx.seed.AssetID.String(), Name: "QA", Address: "QA"}, Participant: reportcore.ParticipantContext{ID: fx.seed.ParticipantID.String(), Name: "QA"}, Template: reportcore.TemplateContext{ID: "template-v1", Name: "QA", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: now.Format(time.RFC3339Nano)}}}, now)
	if err != nil {
		t.Fatal(err)
	}
	query := `query { report(inspectionId:"` + fx.seed.InspectionID.String() + `",version:` + strconv.Itoa(created.Version) + `) { version html requirements { key label coverage instructions analysisStatus } } }`
	response := executeTask06GraphQL(t, fx.server, query, fx.ctx)
	var payload struct {
		Data struct {
			Report struct {
				Version      int    `json:"version"`
				HTML         string `json:"html"`
				Requirements []struct {
					Key, Label, Coverage, Instructions, AnalysisStatus string
				} `json:"requirements"`
			} `json:"report"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil || strings.Contains(response, `"errors"`) {
		t.Fatalf("report with 200 gaps: %.400s (%v)", response, err)
	}
	seen := map[string]bool{}
	for _, requirement := range payload.Data.Report.Requirements {
		if requirement.AnalysisStatus != "INCONCLUSIVE" || requirement.Coverage != generate_snapshot.VideoUnobservedCoverage || !strings.Contains(requirement.Instructions, requirement.Label) {
			t.Fatalf("gap %+v lost its status, coverage or named instruction", requirement)
		}
		seen[requirement.Key] = true
	}
	if len(seen) != 200 {
		t.Fatalf("report exposes %d distinct gaps, want 200", len(seen))
	}
	html := payload.Data.Report.HTML
	if strings.Count(html, generate_snapshot.VideoUnobservedCoverage) != 200 || !strings.Contains(html, "Cômodo 200 — Parede") || !strings.Contains(html, generate_snapshot.VideoNoComparativeConclusion) {
		t.Fatal("exported report HTML does not carry all 200 gaps and the no-conclusion statement")
	}
	if lower := strings.ToLower(html); strings.Contains(lower, "satisfatório") || strings.Contains(lower, "bom estado") || strings.Contains(lower, "escopo observado") {
		t.Fatal("200-gap report presents a satisfactory or observed-property summary")
	}
}

func TestVV_IT_254VideoFindingProjectionNamesRoleFrameTimestampAndLimitation(t *testing.T) {
	fx := newAPI11Fixture(t)
	rpt := seedTask07VideoReport(t, fx.h, fx.seed, 2)
	query := `query { report(inspectionId:"` + fx.seed.InspectionID.String() + `",version:` + strconv.Itoa(rpt.version) + `) { findings { title evidenceIds } requirements { key coverage analysisMode } evidence(first:25) { nodes { id requirementKey role evidenceKind frameId timestampMs ptsMicros sourceAvailability } } } }`
	response := executeTask06GraphQL(t, fx.server, query, fx.ctx)
	var payload struct {
		Data struct {
			Report struct {
				Findings []struct {
					EvidenceIDs []string `json:"evidenceIds"`
				} `json:"findings"`
				Requirements []struct {
					Key, Coverage, AnalysisMode string
				} `json:"requirements"`
				Evidence struct {
					Nodes []struct {
						ID, RequirementKey, Role, EvidenceKind, FrameID, PTSMicros, SourceAvailability string
						TimestampMS                                                                    *int `json:"timestampMs"`
					} `json:"nodes"`
				} `json:"evidence"`
			} `json:"report"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil || strings.Contains(response, `"errors"`) {
		t.Fatalf("video report projection: %s (%v)", response, err)
	}
	report := payload.Data.Report
	if len(report.Findings) != 1 || len(report.Findings[0].EvidenceIDs) != 1 {
		t.Fatalf("findings=%+v; want D1 with one supporting frame", report.Findings)
	}
	cited := report.Findings[0].EvidenceIDs[0]
	var found bool
	for _, node := range report.Evidence.Nodes {
		if node.ID != cited {
			continue
		}
		found = true
		if node.Role != "CURRENT" || node.EvidenceKind != "VIDEO_FRAME" || node.FrameID != rpt.frameID.String() || node.TimestampMS == nil || *node.TimestampMS != 20_000 || node.PTSMicros != "20000000" || node.SourceAvailability == "" {
			t.Fatalf("finding evidence projection=%+v; want CURRENT frame %s at 20000 ms with source availability", node, rpt.frameID)
		}
		for _, requirement := range report.Requirements {
			if requirement.Key == node.RequirementKey && requirement.AnalysisMode != "CURRENT_ONLY" {
				t.Fatalf("finding requirement %+v lost its analysis limitation", requirement)
			}
		}
	}
	if !found {
		t.Fatalf("finding evidence %s is not projected in report evidence", cited)
	}
	var stored database.ReportSnapshot
	if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, rpt.snapshotID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot reportcore.Snapshot
	if err := json.Unmarshal(stored.CanonicalJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	// The report-level limitation travels with the projection.
	if len(snapshot.ScopeStatements) == 0 || snapshot.ScopeStatements[0] != generate_snapshot.VideoNoReferenceStatement {
		t.Fatalf("snapshot scope statements=%q; want the current-only limitation", snapshot.ScopeStatements)
	}
}

func TestVV_IT_260KilledPDFWorkerOnRPT2KeepsRPT1Available(t *testing.T) {
	fx := newAPI11Fixture(t)
	publishAPI13PDF(t, fx, "PDF")
	rpt2 := seedTask07VideoReport(t, fx.h, fx.seed, 2)
	// The PDF worker died midway through RPT2: leased, partial object, no artifact.
	now := time.Now().UTC()
	token, expired := identity.NewID(), now.Add(-time.Minute)
	if err := fx.h.AdminDB.Create(&database.ReportPDFJob{ID: identity.NewID(), TenantID: fx.seed.TenantID, SnapshotID: rpt2.snapshotID, Audience: "PDF", RendererVersion: 1, Status: "PROCESSING", Attempts: 1, NextAttemptAt: now, LeaseToken: &token, LeaseExpiresAt: &expired, PendingObjectKey: "vv-it-260/partial-" + rpt2.snapshotID.String() + ".pdf", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	rpt1 := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api13Query(fx.snapshotID), fx.ctx, "", "")
	if strings.Contains(rpt1, `"errors"`) || !strings.Contains(rpt1, `"status":"READY"`) || strings.Contains(rpt1, `"url":""`) {
		t.Fatalf("RPT1 download while RPT2 PDF is interrupted = %s", rpt1)
	}
	pending := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api13Query(rpt2.snapshotID), fx.ctx, "", "")
	if strings.Contains(pending, `"errors"`) || strings.Contains(pending, `"status":"READY"`) || !strings.Contains(pending, `"url":""`) || strings.Contains(pending, "partial") {
		t.Fatalf("interrupted RPT2 PDF was exposed as available = %s", pending)
	}
	// RPT1 itself stays readable as its own version.
	view := readTask07Report(t, fx, "1")
	if view.Version != 1 {
		t.Fatalf("RPT1 report version=%d while RPT2 is pending", view.Version)
	}
}
