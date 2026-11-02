//go:build integration

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	classifyinspection "inspection/services/inspection/internal/features/analysis/classify_inspection"
	analysis "inspection/services/inspection/internal/features/analysis/core"
	dashboardsummary "inspection/services/inspection/internal/features/dashboard/summary"
	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	handleclassified "inspection/services/inspection/internal/features/reports/handle_classified"
	handlereportready "inspection/services/inspection/internal/features/reports/handle_report_ready"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

// aipProcessAll processes one job per requirement with the given findings.
func aipProcessAll(t *testing.T, e *aipEnv, answers map[string]string) {
	t.Helper()
	for key, response := range answers {
		e.must(e.process(e.jobFor(key), aipOK(response), 0))
	}
}

// TestAIP_IT_251UnknownSeverityOrConfidenceOutsideRangeCannotEnterClassification covers IT-251.
func TestAIP_IT_251UnknownSeverityOrConfidenceOutsideRangeCannotEnterClassification(t *testing.T) {
	e := newAIPEnv(t)
	e.must(e.schedule(e.f.DraftID))
	job := e.jobFor(e.key)
	e.must(e.process(job, aipOK(aipResult(nil)), 0))
	run := e.runs(job.ID)[0]
	insert := func(severity string, confidence float64) error {
		jobID := job.ID
		return e.tenant(func(tx *gorm.DB) error {
			return tx.Create(&database.FindingRecord{ID: identity.NewID(), TenantID: e.f.TenantID, AnalysisRunID: run.ID, ComparisonJobID: &jobID, Category: "CONSERVATION", Title: "t", Description: "d", Severity: severity, Quality: "ADEQUATE", RecommendedAction: "a", Confidence: confidence, Evidence: json.RawMessage(`["PHOTO-1"]`), CreatedAt: time.Now().UTC()}).Error
		})
	}
	for name, attempt := range map[string]error{
		"unknown severity":      insert("SEVERE", .9),
		"confidence above one":  insert("LOW", 1.2),
		"confidence below zero": insert("LOW", -.1),
	} {
		if attempt == nil {
			t.Fatalf("%s reached the findings store", name)
		}
	}
	if err := e.tenant(func(tx *gorm.DB) error {
		return tx.Create(&database.ClassificationRun{ID: identity.NewID(), TenantID: e.f.TenantID, InspectionID: e.f.InspectionID, PromptSnapshotID: e.f.PromptSnapshotID, Classification: "SEVERE", ReasonCodes: json.RawMessage(`[]`), CreatedAt: time.Now().UTC()}).Error
	}); err == nil {
		t.Fatal("a classification outside NORMAL/ATTENTION/CRITICAL was stored")
	}
	if findings := len(e.findings(job.ID)); findings != 0 {
		t.Fatalf("%d invalid findings are visible to classification", findings)
	}
	e.must(e.classify(false))
	if class, _ := e.latestClassification(); class != "NORMAL" {
		t.Fatalf("classification = %s; invalid findings must neither raise nor hide anything", class)
	}
}

// TestAIP_IT_252InspectionWithoutAnalyzableEvidenceIsNeverNormal covers IT-252.
func TestAIP_IT_252InspectionWithoutAnalyzableEvidenceIsNeverNormal(t *testing.T) {
	t.Run("declared impossibility", func(t *testing.T) {
		e := newAIPEnv(t)
		e.addAnswer("aip-252-a", false)
		e.must(e.schedule(e.f.DraftID))
		if got := e.jobFor("aip-252-a"); got.Status != "INCONCLUSIVE" {
			t.Fatalf("an answer without evidence created a %s job, want a terminal INCONCLUSIVE one", got.Status)
		}
		e.must(e.process(e.jobFor(e.key), aipOK(aipResult(nil)), 0))
		e.must(e.classify(false))
		class, reasons := e.latestClassification()
		if class != "ATTENTION" || !contains(reasons, "INCONCLUSIVE") || !contains(reasons, "MISSING_EVIDENCE") {
			t.Fatalf("classification = %s %v", class, reasons)
		}
	})
	t.Run("completed job without any analysis run", func(t *testing.T) {
		e := newAIPEnv(t)
		e.must(e.schedule(e.f.DraftID))
		e.must(e.h.AdminDB.Model(&database.ComparisonJob{}).Where("tenant_id=? AND inspection_id=?", e.f.TenantID, e.f.InspectionID).Update("status", "COMPLETED").Error)
		e.must(e.classify(false))
		if class, reasons := e.latestClassification(); class != "ATTENTION" || !contains(reasons, "INCONCLUSIVE") {
			t.Fatalf("classification = %s %v", class, reasons)
		}
	})
	t.Run("every analysis failed", func(t *testing.T) {
		e := newAIPEnv(t)
		e.must(e.schedule(e.f.DraftID))
		e.must(e.process(e.jobFor(e.key), aipOK(`not json`), aipFinalAttempt))
		e.must(e.classify(false))
		if class, reasons := e.latestClassification(); class != "ATTENTION" || !contains(reasons, "ANALYSIS_FAILED") {
			t.Fatalf("classification = %s %v", class, reasons)
		}
	})
	t.Run("nothing to classify is an error, not a NORMAL classification", func(t *testing.T) {
		e := newAIPEnv(t)
		if err := e.classify(false); !errors.Is(err, messaging.ErrPermanent) {
			t.Fatalf("classifying an inspection without comparisons returned %v, want a permanent refusal", err)
		}
		if rows := e.classifications(); len(rows) != 0 {
			t.Fatalf("an inspection without evidence was classified %s", rows[0].Classification)
		}
	})
}

// TestAIP_IT_253VeryLargeFindingSetStillLetsEveryCriticalAndFlagAffectTheOutcome covers IT-253.
func TestAIP_IT_253VeryLargeFindingSetStillLetsEveryCriticalAndFlagAffectTheOutcome(t *testing.T) {
	e := newAIPEnv(t)
	findings := make([]map[string]any, 0, 100)
	for i := 0; i < 99; i++ {
		findings = append(findings, aipFinding("CONSERVATION", "LOW", .9, "PHOTO-1"))
	}
	findings = append(findings, aipFinding("OBSTRUCTION", "CRITICAL", .99, "PHOTO-1")) // last of the accepted maximum
	e.addAnswer("aip-253-large", true)
	e.addAnswer("aip-253-flagged", true, "GPS_MISSING")
	e.must(e.schedule(e.f.DraftID))
	aipProcessAll(t, e, map[string]string{"aip-253-large": aipResult(nil, findings...), "aip-253-flagged": aipResult(nil), e.key: aipResult(nil)})
	if got := len(e.findings(e.jobFor("aip-253-large").ID)); got != 100 {
		t.Fatalf("%d of 100 accepted findings were stored", got)
	}
	e.must(e.classify(false))
	class, reasons := e.latestClassification()
	for _, want := range []string{"CRITICAL_FINDING", "OBSERVED_CHANGE", "EVIDENCE_FLAG"} {
		if !contains(reasons, want) {
			t.Fatalf("outcome %s lost reason %s: %v", class, want, reasons)
		}
	}
	if class != "CRITICAL" {
		t.Fatalf("classification = %s, want CRITICAL: the last accepted finding was critical", class)
	}
}

// TestAIP_IT_254NoManualMutationCanChangeAClassification covers IT-254.
func TestAIP_IT_254NoManualMutationCanChangeAClassification(t *testing.T) {
	schema, err := os.ReadFile("../../../schema.graphqls")
	if err != nil {
		t.Fatalf("read GraphQL schema: %v", err)
	}
	block := regexp.MustCompile(`(?s)type Mutation \{(.*?)\n\}`).FindStringSubmatch(string(schema))
	if len(block) != 2 {
		t.Fatal("the Mutation type was not found in the schema")
	}
	for _, line := range strings.Split(block[1], "\n") {
		name := strings.TrimSpace(strings.SplitN(strings.TrimSpace(line), "(", 2)[0])
		if strings.Contains(strings.ToLower(line), "classification") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			t.Fatalf("mutation %q lets a caller write a classification: %s", name, strings.TrimSpace(line))
		}
	}
	e := newAIPEnv(t)
	e.must(e.schedule(e.f.DraftID))
	e.must(e.process(e.jobFor(e.key), aipOK(aipResult(nil, aipFinding("CONSERVATION", "CRITICAL", .95, "PHOTO-1"))), 0))
	e.must(e.classify(false))
	row := e.classifications()[0]
	for _, statement := range []string{
		"UPDATE analysis.classification_runs SET classification='NORMAL' WHERE id=?",
		"UPDATE analysis.classification_runs SET reason_codes='[]' WHERE id=?",
		"DELETE FROM analysis.classification_runs WHERE id=?",
	} {
		err := e.tenant(func(tx *gorm.DB) error { return tx.Exec(statement, row.ID).Error })
		if err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Fatalf("the runtime role changed a classification with %q: %v", statement, err)
		}
	}
	if class, _ := e.latestClassification(); class != "CRITICAL" {
		t.Fatalf("classification became %s", class)
	}
}

// aipOutcomes scripts the PHOTO set used by the classification determinism cases.
func aipOutcomes(e *aipEnv) (answers map[string]string, terminal map[string]int, expected analysis.Decision) {
	answers = map[string]string{
		"aip-256-a": aipResult(nil, aipFinding("CONSERVATION", "MEDIUM", .9, "PHOTO-1")),
		"aip-256-b": aipResult(nil, aipFinding("OBSTRUCTION", "CRITICAL", .95, "PHOTO-1")),
		"aip-256-c": `not json`,
		e.key:       aipResult(nil),
	}
	terminal = map[string]int{"aip-256-c": aipFinalAttempt}
	// Independent oracle: jobs are classified in requirement-key order.
	expected = analysis.Classify([]analysis.ComparisonFacts{
		{Terminal: true, Findings: []analysis.Finding{{Severity: "MEDIUM"}}},
		{Terminal: true, Findings: []analysis.Finding{{Severity: "CRITICAL"}}},
		{Terminal: true, TechnicalFailure: true},
		{Terminal: true},
	})
	return answers, terminal, expected
}

func aipRunScenario(t *testing.T, e *aipEnv, concurrent bool) {
	t.Helper()
	answers, terminal, _ := aipOutcomes(e)
	for key := range answers {
		if key != e.key {
			e.addAnswer(key, true)
		}
	}
	e.must(e.schedule(e.f.DraftID))
	var wg sync.WaitGroup
	errs := make(chan error, len(answers))
	for key, response := range answers {
		run := func(key, response string) {
			defer wg.Done()
			errs <- e.process(e.jobFor(key), aipOK(response), terminal[key])
		}
		wg.Add(1)
		if concurrent {
			go run(key, response)
		} else {
			run(key, response)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		e.must(err)
	}
}

// TestAIP_IT_255SimultaneousResultsClassifyFromTheFullCommittedTerminalSet covers IT-255.
func TestAIP_IT_255SimultaneousResultsClassifyFromTheFullCommittedTerminalSet(t *testing.T) {
	e := newAIPEnv(t)
	_, _, expected := aipOutcomes(e)
	answers, terminal, _ := aipOutcomes(e)
	for key := range answers {
		if key != e.key {
			e.addAnswer(key, true)
		}
	}
	e.must(e.schedule(e.f.DraftID))
	// Each completion triggers classification at once, like the worker does when
	// analysis.comparison_completed.v1 arrives; premature attempts are retried.
	var wg sync.WaitGroup
	errs := make(chan error, len(answers))
	for key, response := range answers {
		wg.Add(1)
		go func(key, response string) {
			defer wg.Done()
			if err := e.process(e.jobFor(key), aipOK(response), terminal[key]); err != nil {
				errs <- err
				return
			}
			for try := 0; try < 100; try++ {
				err := e.classify(false)
				if errors.Is(err, classifyinspection.ErrClassificationPending) {
					time.Sleep(30 * time.Millisecond)
					continue
				}
				errs <- err
				return
			}
			errs <- errors.New("classification stayed pending although every job is terminal")
		}(key, response)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		e.must(err)
	}
	rows := e.classifications()
	if len(rows) != 1 {
		t.Fatalf("simultaneous completions produced %d classification runs, want exactly one", len(rows))
	}
	var reasons []string
	e.must(json.Unmarshal(rows[0].ReasonCodes, &reasons))
	if rows[0].Classification != expected.Classification || strings.Join(reasons, ",") != strings.Join(expected.ReasonCodes, ",") {
		t.Fatalf("classification %s %v does not reflect the full terminal set %s %v", rows[0].Classification, reasons, expected.Classification, expected.ReasonCodes)
	}
	if events := e.outbox("inspection.classified.v1"); events != 1 {
		t.Fatalf("inspection.classified.v1 was published %d times", events)
	}
}

// TestAIP_IT_256StoppedClassificationRetriesToTheSameDeterministicResult covers IT-256.
func TestAIP_IT_256StoppedClassificationRetriesToTheSameDeterministicResult(t *testing.T) {
	e := newAIPEnv(t)
	_, _, expected := aipOutcomes(e)
	aipRunScenario(t, e, false)
	crash := errors.New("worker killed before commit")
	if err := e.classifyInTx(false, func() error { return crash }); !errors.Is(err, crash) {
		t.Fatalf("expected the simulated crash, got %v", err)
	}
	if rows, events := len(e.classifications()), e.outbox("inspection.classified.v1"); rows != 0 || events != 0 {
		t.Fatalf("a stopped classification left %d runs and %d events", rows, events)
	}
	if completed := e.count(&database.Inspection{}, "id=? AND status='COMPLETED'", e.f.InspectionID); completed != 0 {
		t.Fatal("the inspection was completed by a rolled back classification")
	}
	e.must(e.classify(false))
	class, reasons := e.latestClassification()
	if class != expected.Classification || strings.Join(reasons, ",") != strings.Join(expected.ReasonCodes, ",") {
		t.Fatalf("retry produced %s %v, want the deterministic %s %v", class, reasons, expected.Classification, expected.ReasonCodes)
	}
}

// TestAIP_IT_257ProcessingTheSameFindingsAgainLeavesClassificationUnchanged covers IT-257.
func TestAIP_IT_257ProcessingTheSameFindingsAgainLeavesClassificationUnchanged(t *testing.T) {
	e := newAIPEnv(t)
	aipRunScenario(t, e, true)
	// Six classifiers race for the same terminal set.
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- e.classify(false) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		e.must(err)
	}
	first := e.classifications()
	if len(first) != 1 || e.outbox("inspection.classified.v1") != 1 {
		t.Fatalf("racing classifiers produced %d runs and %d events", len(first), e.outbox("inspection.classified.v1"))
	}
	for i := 0; i < 3; i++ {
		e.must(e.classify(false))
	}
	if again := e.classifications(); len(again) != 1 || again[0].ID != first[0].ID {
		t.Fatalf("reprocessing the same findings changed classification history: %d runs", len(again))
	}
	e.must(e.classify(true))
	rows := e.classifications()
	last := rows[len(rows)-1]
	if last.Classification != first[0].Classification || string(last.ReasonCodes) != string(first[0].ReasonCodes) {
		t.Fatalf("a forced reprocessing changed the outcome: %s %s -> %s %s", first[0].Classification, first[0].ReasonCodes, last.Classification, last.ReasonCodes)
	}
	if rows[0].ID != first[0].ID || rows[0].Classification != first[0].Classification {
		t.Fatal("the original classification row was altered")
	}
}

// TestAIP_IT_258PartialResultsDoNotPublishAClassificationBeforeTerminality covers IT-258.
func TestAIP_IT_258PartialResultsDoNotPublishAClassificationBeforeTerminality(t *testing.T) {
	e := newAIPEnv(t)
	e.addAnswer("aip-258-a", true)
	e.addAnswer("aip-258-b", true)
	e.must(e.schedule(e.f.DraftID))
	e.must(e.process(e.jobFor(e.key), aipOK(aipResult(nil)), 0))
	e.must(e.process(e.jobFor("aip-258-a"), aipOK(aipResult(nil, aipFinding("CONSERVATION", "CRITICAL", .95, "PHOTO-1"))), 0))
	// One job is still PENDING: even a critical partial finding is not published.
	if err := e.classify(false); !errors.Is(err, classifyinspection.ErrClassificationPending) {
		t.Fatalf("classification before terminality returned %v, want the retryable pending error", err)
	}
	if rows, events := len(e.classifications()), e.outbox("inspection.classified.v1"); rows != 0 || events != 0 {
		t.Fatalf("a partial set published %d classifications and %d events", rows, events)
	}
	var status string
	e.must(e.tenant(func(tx *gorm.DB) error {
		return tx.Model(&database.Inspection{}).Where("id=?", e.f.InspectionID).Select("status").Scan(&status).Error
	}))
	if status == "COMPLETED" {
		t.Fatal("the inspection was completed before every comparison was terminal")
	}
	e.must(e.process(e.jobFor("aip-258-b"), aipOK(aipResult(nil)), 0))
	e.must(e.classify(false))
	if class, reasons := e.latestClassification(); class != "CRITICAL" || !contains(reasons, "CRITICAL_FINDING") {
		t.Fatalf("final classification = %s %v", class, reasons)
	}
}

func aipReportHandler(t *testing.T, e *aipEnv) func() []database.ReportSnapshot {
	t.Helper()
	// The worker creates snapshots with its privileged role (BYPASSRLS), so the
	// version lookup sees the whole inspection history.
	createSnapshot, err := generate_snapshot.Setup(generate_snapshot.Dependencies{DB: e.h.AdminDB, Now: time.Now})
	e.must(err)
	handler, err := handleclassified.Setup(handleclassified.Dependencies{Notifications: photoFlowNotifications{}, CreateSnapshot: createSnapshot, Now: time.Now})
	e.must(err)
	return func() []database.ReportSnapshot {
		classified, err := loadOutboxEnvelope(e.ctx, e.h, e.f.TenantID, "inspection.classified.v1")
		e.must(err)
		e.must(e.tenant(func(tx *gorm.DB) error { return handler(e.ctx, tx, classified) }))
		var snapshots []database.ReportSnapshot
		e.must(e.tenant(func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND inspection_id=?", e.f.TenantID, e.f.InspectionID).Order("version_number ASC").Find(&snapshots).Error
		}))
		return snapshots
	}
}

// TestAIP_IT_259RecaptureReclassifiesWithoutEditingThePreviousVersion covers IT-259.
func TestAIP_IT_259RecaptureReclassifiesWithoutEditingThePreviousVersion(t *testing.T) {
	e := newAIPEnv(t)
	report := aipReportHandler(t, e)
	e.must(e.schedule(e.f.DraftID))
	e.must(e.process(e.jobFor(e.key), aipOK(aipResult(nil, aipFinding("CONSERVATION", "MEDIUM", .9, "PHOTO-1"))), 0))
	e.must(e.classify(false))
	first := e.classifications()[0]
	v1 := report()
	if len(v1) != 1 || v1[0].Classification != "ATTENTION" {
		t.Fatalf("first report = %+v", v1)
	}

	e.addRecapture(e.key)
	e.must(e.schedule(identity.ID{}))
	e.must(e.process(e.jobFor(e.key), aipOK(aipResult(nil)), 0))
	e.must(e.classify(false))
	rows := e.classifications()
	if len(rows) != 2 || rows[1].Classification != "NORMAL" || rows[1].ID == first.ID {
		t.Fatalf("recapture did not append a new NORMAL classification: %+v", rows)
	}
	if rows[0].ID != first.ID || rows[0].Classification != "ATTENTION" || string(rows[0].ReasonCodes) != string(first.ReasonCodes) || !rows[0].CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("the previous classification was edited: %+v", rows[0])
	}
	v2 := report()
	if len(v2) != 2 || v2[0].ID != v1[0].ID || v2[0].Classification != "ATTENTION" || v2[0].JSONDigest != v1[0].JSONDigest || v2[1].Classification != "NORMAL" || v2[1].VersionNumber != 2 {
		summary := make([]string, 0, len(v2))
		for _, snapshot := range v2 {
			summary = append(summary, snapshot.ID.String()+" v"+intString(snapshot.VersionNumber)+" "+snapshot.Classification)
		}
		t.Fatalf("report versions after recapture = %v (first report was %s)", summary, v1[0].ID)
	}
	if err := e.h.AdminDB.Exec("UPDATE reports.report_snapshots SET classification='NORMAL' WHERE id=?", v2[0].ID).Error; err == nil {
		t.Fatal("the previous report version accepted an edit")
	}
}

type aipPortfolioItem struct {
	first, second string // severities of the first and, when set, the superseding analysis
	invalidated   bool
}

// aipSeverityResponse maps a scripted severity to a provider answer.
func aipSeverityResponse(severity string) string {
	if severity == "" || severity == "NONE" {
		return aipResult(nil)
	}
	return aipResult(nil, aipFinding("CONSERVATION", severity, .9, "PHOTO-1"))
}

func aipClassOf(severity string) string {
	switch severity {
	case "CRITICAL":
		return "CRITICAL"
	case "", "NONE":
		return "NORMAL"
	default:
		return "ATTENTION"
	}
}

// TestAIP_IT_260PortfolioCountsEqualTheClassificationOfValidLatestReports covers IT-260.
func TestAIP_IT_260PortfolioCountsEqualTheClassificationOfValidLatestReports(t *testing.T) {
	e := newAIPEnv(t)
	items := []aipPortfolioItem{
		{first: "NONE"}, {first: "NONE"}, {first: "NONE"},
		{first: "LOW"}, {first: "HIGH"}, {first: "MEDIUM"},
		{first: "CRITICAL"}, {first: "CRITICAL"},
		{first: "CRITICAL", second: "NONE"}, // corrected by a recapture: NORMAL now
		{first: "NONE", second: "CRITICAL"}, // worsened by a recapture: CRITICAL now
		{first: "LOW", second: "HIGH"},      // still ATTENTION, other reasons
		{first: "CRITICAL", invalidated: true},
		{first: "NONE", invalidated: true},
	}
	ready, err := handlereportready.Setup(handlereportready.Dependencies{
		InAppNotification: func(context.Context, *gorm.DB, events.RawEnvelope) error { return nil },
		UpsertTriage: func(context.Context, *gorm.DB, identity.ID, identity.ID, database.ClassificationRun, int, time.Time) error {
			return nil
		},
		Now: time.Now,
	})
	e.must(err)
	classify, err := classifyinspection.Setup(classifyinspection.Dependencies{Now: time.Now})
	e.must(err)
	var original database.Inspection
	e.must(e.tenant(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", e.f.TenantID, e.f.InspectionID).Take(&original).Error
	}))
	expected := map[string]int{"NORMAL": 0, "ATTENTION": 0, "CRITICAL": 0}
	invalid := 0
	for index, item := range items {
		inspectionID := identity.NewID()
		step := func(severity string, attempt int) (jobID identity.ID) {
			now := time.Now().UTC().Truncate(time.Microsecond)
			job := database.ComparisonJob{ID: identity.NewID(), TenantID: e.f.TenantID, InspectionID: inspectionID, PromptSnapshotID: e.f.PromptSnapshotID, RequirementKey: "portfolio", ModelAlias: "inspection-vision", PromptDigest: "aip", InputDigest: identity.NewID().String(), Status: "COMPLETED", CreatedAt: now, UpdatedAt: now}
			run := database.AnalysisRun{ID: identity.NewID(), TenantID: e.f.TenantID, JobID: job.ID, PromptSnapshotID: e.f.PromptSnapshotID, Provider: "stub", Model: "stub/vision", GatewayRequestID: "req", PromptDigest: "aip", InputDigest: job.InputDigest, Output: json.RawMessage(aipSeverityResponse(severity)), LatencyMS: 1, ValidationOutcome: "ACCEPTED", CreatedAt: now}
			e.must(e.tenant(func(tx *gorm.DB) error {
				if err := tx.Create(&job).Error; err != nil {
					return err
				}
				if err := tx.Create(&run).Error; err != nil {
					return err
				}
				if aipClassOf(severity) == "NORMAL" {
					return nil
				}
				evidence, _ := json.Marshal([]string{"PHOTO-1"})
				return tx.Create(&database.FindingRecord{ID: identity.NewID(), TenantID: e.f.TenantID, AnalysisRunID: run.ID, ComparisonJobID: &job.ID, Category: "CONSERVATION", Title: "t", Description: "d", Severity: severity, Quality: "ADEQUATE", RecommendedAction: "a", Confidence: .9, Evidence: evidence, CreatedAt: now}).Error
			}))
			return job.ID
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		clone := original
		clone.ID, clone.SourceKey, clone.Status, clone.CreatedAt, clone.UpdatedAt = inspectionID, "aip-portfolio-"+inspectionID.String(), "ANALYZING", now, now
		responsibilityID, draftID := identity.NewID(), identity.NewID()
		media, _ := json.Marshal([]identity.ID{e.f.MediaID})
		e.must(e.tenant(func(tx *gorm.DB) error {
			if err := tx.Create(&clone).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.Responsibility{ID: responsibilityID, TenantID: e.f.TenantID, InspectionID: inspectionID, ParticipantID: e.f.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.CaptureDraft{ID: draftID, TenantID: e.f.TenantID, ResponsibilityID: responsibilityID, Kind: "INSPECTION", MediaKind: "PHOTO", TemplateVersionID: e.f.TemplateVersionID, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[]`), Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			return tx.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: e.f.TenantID, DraftID: draftID, RequirementKey: "portfolio", MediaIDs: media, Flags: json.RawMessage(`[]`), Version: 1, CreatedAt: now, UpdatedAt: now}).Error
		}))
		classifyOnce := func() {
			payload, _ := json.Marshal(map[string]any{"inspectionId": inspectionID})
			envelope := events.RawEnvelope{ID: identity.NewID(), Type: "analysis.comparison_completed.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: e.f.TenantID, AggregateID: inspectionID, CorrelationID: "aip-portfolio", Payload: payload}
			e.must(e.tenant(func(tx *gorm.DB) error { return classify(e.ctx, tx, envelope) }))
		}
		publish := func(version int) {
			snapshotID := identity.NewID()
			markup, _ := json.Marshal("<html></html>")
			var latest database.ClassificationRun
			e.must(e.tenant(func(tx *gorm.DB) error {
				return tx.Where("tenant_id=? AND inspection_id=?", e.f.TenantID, inspectionID).Order("created_at DESC").Take(&latest).Error
			}))
			e.must(e.tenant(func(tx *gorm.DB) error {
				if err := tx.Create(&database.ReportSnapshot{ID: snapshotID, TenantID: e.f.TenantID, InspectionID: inspectionID, Mode: "HISTORICAL", Classification: latest.Classification, JSONDigest: snapshotID.String(), HTMLDigest: snapshotID.String(), VersionNumber: version, CanonicalJSON: json.RawMessage(`{}`), HTML: markup, CreatedAt: time.Now().UTC()}).Error; err != nil {
					return err
				}
				if item.invalidated {
					if err := tx.Model(&database.Inspection{}).Where("id=?", inspectionID).Update("status", "INVALIDATED").Error; err != nil {
						return err
					}
				}
				payload, _ := json.Marshal(map[string]any{"inspectionId": inspectionID, "snapshotId": snapshotID})
				return ready(e.ctx, tx, events.RawEnvelope{ID: identity.NewID(), Type: "report.ready.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: e.f.TenantID, AggregateID: snapshotID, CorrelationID: "aip-portfolio", Payload: payload})
			}))
		}
		step(item.first, 0)
		classifyOnce()
		publish(1)
		final := item.first
		if item.second != "" {
			time.Sleep(5 * time.Millisecond) // the superseding job is strictly newer than the first run
			// The recapture's job replaces the earlier one for the same requirement.
			step(item.second, 0)
			classifyOnce()
			publish(2)
			final = item.second
		}
		if item.invalidated {
			invalid++
		} else {
			expected[aipClassOf(final)]++
		}
		_ = index
	}
	var summary struct{ Total, Critical, Attention, Normal, Pending, Invalidated int }
	e.must(e.tenant(func(tx *gorm.DB) error {
		got, err := dashboardsummary.Query(e.ctx, tx, dashboardsummary.Input{TenantID: e.f.TenantID})
		summary.Total, summary.Critical, summary.Attention, summary.Normal, summary.Pending, summary.Invalidated = got.Total, got.Critical, got.Attention, got.Normal, got.Pending, got.Invalidated
		return err
	}))
	if summary.Normal != expected["NORMAL"] || summary.Attention != expected["ATTENTION"] || summary.Critical != expected["CRITICAL"] || summary.Invalidated != invalid || summary.Total != len(items) {
		t.Fatalf("summary %+v does not equal the latest valid classifications %v (invalidated=%d total=%d)", summary, expected, invalid, len(items))
	}
	if valid := summary.Normal + summary.Attention + summary.Critical; valid != len(items)-invalid {
		t.Fatalf("valid indicators sum to %d, want %d", valid, len(items)-invalid)
	}
}
