//go:build integration

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	buildrequest "inspection/services/inspection/internal/features/analysis/build_request"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

const aipFinalAttempt = 3 // analysis.MaxAttempts-1: the last broker delivery before a terminal fallback.

// aipAssertNothingPersisted verifies that a rejected or retried delivery left
// the job pending and wrote no run, finding, usage or completion event.
func aipAssertNothingPersisted(t *testing.T, e *aipEnv, job database.ComparisonJob, completedBefore int64) {
	t.Helper()
	if got := e.job(job.ID); got.Status != "PENDING" {
		t.Fatalf("job %s moved to %s without a terminal delivery", job.RequirementKey, got.Status)
	}
	if runs, findings := len(e.runs(job.ID)), len(e.findings(job.ID)); runs != 0 || findings != 0 {
		t.Fatalf("job %s persisted %d runs and %d findings from a rejected delivery", job.RequirementKey, runs, findings)
	}
	if usage := e.count(&database.UsageRecord{}, "job_id=?", job.ID); usage != 0 {
		t.Fatalf("job %s recorded usage for a rejected delivery", job.RequirementKey)
	}
	if completed := e.outbox("analysis.comparison_completed.v1"); completed != completedBefore {
		t.Fatalf("a rejected delivery emitted a completion event (%d -> %d)", completedBefore, completed)
	}
}

// aipAssertFailedTerminal verifies the bounded, visible terminal failure: one
// FAILED run with no findings and one completion event.
func aipAssertFailedTerminal(t *testing.T, e *aipEnv, job database.ComparisonJob) {
	t.Helper()
	got := e.job(job.ID)
	runs := e.runs(job.ID)
	if got.Status != "FAILED" || got.Attempts != aipFinalAttempt+1 || len(runs) != 1 || runs[0].ValidationOutcome != "FAILED" || len(e.findings(job.ID)) != 0 {
		t.Fatalf("job %s = %s attempts=%d runs=%d, want one FAILED terminal run without findings", job.RequirementKey, got.Status, got.Attempts, len(runs))
	}
	var output struct {
		Findings   []any  `json:"findings"`
		ReasonCode string `json:"reasonCode"`
	}
	if err := json.Unmarshal(runs[0].Output, &output); err != nil || len(output.Findings) != 0 || output.ReasonCode == "" {
		t.Fatalf("terminal output is not an explicit empty failure: %s (%v)", runs[0].Output, err)
	}
	if runs[0].Provider != "" || runs[0].Model != "" {
		t.Fatalf("a failed run must not claim provider facts: %+v", runs[0])
	}
}

// TestAIP_IT_241InvalidSeverityConfidenceOrEvidenceIsRejectedNotLooselyParsed
// covers IT-241 of autonomous-inspection-platform.
func TestAIP_IT_241InvalidSeverityConfidenceOrEvidenceIsRejectedNotLooselyParsed(t *testing.T) {
	e := newAIPEnv(t)
	cases := []struct{ name, key, response string }{
		{"unknown severity", "aip-241-a", aipResult(nil, aipFinding("CONSERVATION", "SEVERE", .9, "PHOTO-1"))},
		{"lowercase severity", "aip-241-b", aipResult(nil, aipFinding("CONSERVATION", "high", .9, "PHOTO-1"))},
		{"confidence above one", "aip-241-c", aipResult(nil, aipFinding("CONSERVATION", "LOW", 1.5, "PHOTO-1"))},
		{"negative confidence", "aip-241-d", aipResult(nil, aipFinding("CONSERVATION", "LOW", -.2, "PHOTO-1"))},
		{"confidence below the pinned minimum", "aip-241-e", aipResult(nil, aipFinding("CONSERVATION", "LOW", .5, "PHOTO-1"))},
		{"missing evidence references", "aip-241-f", aipResult(nil, aipFinding("CONSERVATION", "LOW", .9))},
		{"unknown evidence reference", "aip-241-g", aipResult(nil, aipFinding("CONSERVATION", "LOW", .9, "PHOTO-9"))},
		{"unknown field", "aip-241-h", `{"noRelevantChange":null,"findings":[],"coverageStatus":"COMPLETE"}`},
		{"trailing content", "aip-241-i", aipResult(nil) + `{"extra":true}`},
		{"truncated JSON", "aip-241-j", `{"noRelevantChange":null,"findings":[`},
		{"prose instead of JSON", "aip-241-k", `Tudo normal.`},
	}
	for _, tc := range cases {
		e.addAnswer(tc.key, true)
	}
	e.must(e.schedule(e.f.DraftID))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			job := e.jobFor(tc.key)
			gateway := aipOK(tc.response)
			completed := e.outbox("analysis.comparison_completed.v1")
			// Retryable deliveries reject the answer and keep no partial state.
			for attempt := 0; attempt < aipFinalAttempt; attempt++ {
				if err := e.process(job, gateway, attempt); err == nil {
					t.Fatalf("attempt %d accepted an invalid structured answer", attempt)
				}
				aipAssertNothingPersisted(t, e, job, completed)
			}
			// The last delivery turns the failure into one explicit terminal outcome.
			if err := e.process(job, gateway, aipFinalAttempt); err != nil {
				t.Fatalf("terminal delivery failed instead of recording the outcome: %v", err)
			}
			aipAssertFailedTerminal(t, e, job)
			if got := e.outbox("analysis.comparison_completed.v1"); got != completed+1 {
				t.Fatalf("completion events = %d, want %d", got, completed+1)
			}
		})
	}
}

// TestAIP_IT_242NoFindingsAreAcceptedOnlyWithAnExplicitNoRelevantChangeStructure
// covers IT-242.
func TestAIP_IT_242NoFindingsAreAcceptedOnlyWithAnExplicitNoRelevantChangeStructure(t *testing.T) {
	e := newAIPEnv(t)
	type scenario struct {
		name, key, response string
		build               aipRequestBuilder
		want                string
	}
	scenarios := []scenario{
		{"current-only with explicit null", "aip-242-a", aipResult(nil), aipCurrentOnly, "COMPLETED"},
		{"current-only with a boolean", "aip-242-b", aipResult(true), aipCurrentOnly, "FAILED"},
		{"missing noRelevantChange", "aip-242-c", `{"findings":[]}`, aipCurrentOnly, "FAILED"},
		{"comparative explicit no change", "aip-242-d", aipResult(true), aipComparative, "COMPLETED"},
		{"comparative null without findings", "aip-242-e", aipResult(nil), aipComparative, "FAILED"},
		{"comparative change claimed without findings", "aip-242-f", aipResult(false), aipComparative, "FAILED"},
	}
	for _, s := range scenarios {
		e.addAnswer(s.key, true)
	}
	e.must(e.schedule(e.f.DraftID))
	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			job := e.jobFor(s.key)
			attempt := 0
			if s.want == "FAILED" {
				attempt = aipFinalAttempt
			}
			if err := e.processWith(job, aipOK(s.response), attempt, s.build); err != nil {
				t.Fatal(err)
			}
			got := e.job(job.ID)
			if got.Status != s.want {
				t.Fatalf("status = %s, want %s", got.Status, s.want)
			}
			if s.want == "COMPLETED" && (len(e.findings(job.ID)) != 0 || e.runs(job.ID)[0].ValidationOutcome != "ACCEPTED") {
				t.Fatalf("an explicit no-change answer must persist an accepted run without findings")
			}
		})
	}
}

// TestAIP_IT_243ResponseOrEvidenceBeyondLimitsBecomesAVisibleBoundedFailure
// covers IT-243.
func TestAIP_IT_243ResponseOrEvidenceBeyondLimitsBecomesAVisibleBoundedFailure(t *testing.T) {
	e := newAIPEnv(t)
	many := func(count int, evidence ...string) []map[string]any {
		findings := make([]map[string]any, 0, count)
		for i := 0; i < count; i++ {
			findings = append(findings, aipFinding("CONSERVATION", "LOW", .9, evidence...))
		}
		return findings
	}
	ids := func(count int) []string {
		out := make([]string, 0, count)
		for i := 1; i <= count; i++ {
			out = append(out, "PHOTO-"+intString(i))
		}
		return out
	}
	imagesFor := func(count int) aipRequestBuilder {
		return func(job database.ComparisonJob) llm.StructuredRequest {
			request := aipCurrentOnly(job)
			request.Images = nil
			for _, id := range ids(count) {
				request.Images = append(request.Images, llm.NormalizedImage{EvidenceID: id, Source: "CURRENT", Digest: "d-" + id, DataURL: "data:image/png;base64,AAAA"})
			}
			return request
		}
	}
	scenarios := []struct {
		name, key, response string
		build               aipRequestBuilder
		want                string
		findings            int
	}{
		{"one hundred findings are the accepted limit", "aip-243-a", aipResult(nil, many(100, "PHOTO-1")...), aipCurrentOnly, "COMPLETED", 100},
		{"more than one hundred findings", "aip-243-b", aipResult(nil, many(101, "PHOTO-1")...), aipCurrentOnly, "FAILED", 0},
		{"fifty evidence references are the accepted limit", "aip-243-c", aipResult(nil, many(1, ids(50)...)...), imagesFor(50), "COMPLETED", 1},
		{"more than fifty evidence references", "aip-243-d", aipResult(nil, many(1, ids(51)...)...), imagesFor(51), "FAILED", 0},
	}
	for _, s := range scenarios {
		e.addAnswer(s.key, true)
	}
	e.must(e.schedule(e.f.DraftID))
	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			job := e.jobFor(s.key)
			attempt := 0
			if s.want == "FAILED" {
				// Within the retry budget the oversized answer is refused, nothing is split or kept.
				if err := e.processWith(job, aipOK(s.response), 0, s.build); err == nil {
					t.Fatal("oversized answer was accepted on a retryable delivery")
				}
				aipAssertNothingPersisted(t, e, job, e.outbox("analysis.comparison_completed.v1"))
				attempt = aipFinalAttempt
			}
			e.must(e.processWith(job, aipOK(s.response), attempt, s.build))
			if got := e.job(job.ID); got.Status != s.want || len(e.findings(job.ID)) != s.findings {
				t.Fatalf("status=%s findings=%d, want %s/%d", got.Status, len(e.findings(job.ID)), s.want, s.findings)
			}
			if s.want == "FAILED" {
				aipAssertFailedTerminal(t, e, job)
			}
		})
	}
	// The visible failure reaches classification as a reason, never as conformity.
	e.must(e.processWith(e.jobFor(e.key), aipOK(aipResult(nil)), 0, aipCurrentOnly))
	e.must(e.classify(false))
	class, reasons := e.latestClassification()
	if class != "ATTENTION" || !contains(reasons, "ANALYSIS_FAILED") {
		t.Fatalf("classification = %s %v, want ATTENTION with ANALYSIS_FAILED", class, reasons)
	}
}

func intString(value int) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestAIP_IT_244AnalysisIsNotReachableOrVisibleAcrossTenants covers IT-244.
// Analysis has no user-facing surface of its own: it is produced by the worker
// and read through tenant-scoped rows, so another tenant can neither run nor
// observe it.
func TestAIP_IT_244AnalysisIsNotReachableOrVisibleAcrossTenants(t *testing.T) {
	e := newAIPEnv(t)
	other := e.another()
	e.must(e.schedule(e.f.DraftID))
	job := e.jobFor(e.key)
	e.must(e.process(job, aipOK(aipResult(nil, aipFinding("CONSERVATION", "MEDIUM", .9, "PHOTO-1"))), 0))
	e.must(e.classify(false))

	// Another tenant cannot trigger processing of this job.
	handlerErr := e.h.WithinTenant(e.ctx, other.TenantID, func(tx *gorm.DB) error {
		payload, _ := json.Marshal(map[string]any{"jobId": job.ID})
		return errors.Join(runProcessAs(e, tx, other.TenantID, payload))
	})
	if handlerErr == nil || errors.Is(handlerErr, messaging.ErrPermanent) {
		t.Fatalf("foreign tenant processing returned %v, want a not-found failure", handlerErr)
	}
	// ... nor observe any row of it through the runtime role.
	for name, model := range map[string]any{"jobs": &database.ComparisonJob{}, "runs": &database.AnalysisRun{}, "findings": &database.FindingRecord{}, "classifications": &database.ClassificationRun{}, "usage": &database.UsageRecord{}} {
		var visible int64
		e.must(e.h.WithinTenant(e.ctx, other.TenantID, func(tx *gorm.DB) error {
			return tx.Model(model).Where("created_at > ?", time.Now().Add(-time.Hour)).Where(inspectionColumn(name)+"=? OR tenant_id=?", e.f.InspectionID, e.f.TenantID).Count(&visible).Error
		}))
		if visible != 0 {
			t.Fatalf("tenant B can read %d %s of tenant A", visible, name)
		}
	}
	// Row-level security also rejects a write that names the foreign tenant.
	err := e.h.WithinTenant(e.ctx, other.TenantID, func(tx *gorm.DB) error {
		return tx.Create(&database.ClassificationRun{ID: identity.NewID(), TenantID: e.f.TenantID, InspectionID: e.f.InspectionID, PromptSnapshotID: e.f.PromptSnapshotID, Classification: "NORMAL", ReasonCodes: json.RawMessage(`[]`), CreatedAt: time.Now().UTC()}).Error
	})
	if err == nil {
		t.Fatal("tenant B inserted a classification into tenant A")
	}
	if got := e.job(job.ID); got.Status != "COMPLETED" || len(e.classifications()) != 1 {
		t.Fatalf("tenant A state changed: job=%s classifications=%d", got.Status, len(e.classifications()))
	}
}

func inspectionColumn(name string) string {
	switch name {
	case "jobs", "classifications", "usage":
		return "inspection_id"
	default:
		return "tenant_id"
	}
}

func runProcessAs(e *aipEnv, tx *gorm.DB, tenantID identity.ID, payload []byte) error {
	handlerCtx := messaging.WithAttempt(e.ctx, 0)
	handler, err := processHandler(aipOK(aipResult(nil)))
	if err != nil {
		return err
	}
	return handler(handlerCtx, tx, payload, tenantID)
}

// TestAIP_IT_245DuplicateAnalysisStartsKeepOneAcceptedResult covers IT-245,
// including two deliveries of the same job running at the same time.
func TestAIP_IT_245DuplicateAnalysisStartsKeepOneAcceptedResult(t *testing.T) {
	e := newAIPEnv(t)
	e.must(e.schedule(e.f.DraftID))
	e.must(e.schedule(e.f.DraftID)) // a second capture.submitted-style start for the same evidence/profile
	jobs := e.jobs()
	if len(jobs) != 1 {
		t.Fatalf("duplicate starts created %d jobs, want 1", len(jobs))
	}
	if requested := e.outbox("analysis.comparison_requested.v1"); requested != 2 {
		t.Fatalf("expected the two starts to be visible as requests, got %d", requested)
	}
	job := jobs[0]

	// Both deliveries are in flight together; the provider is slow enough for
	// them to overlap unless the job is serialized.
	var entered sync.WaitGroup
	entered.Add(1)
	gateway := &aipGateway{steps: []aipStep{{JSON: aipResult(nil, aipFinding("CONSERVATION", "MEDIUM", .9, "PHOTO-1"))}}}
	gateway.enter = func() { time.Sleep(400 * time.Millisecond) }
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- e.process(job, gateway, 0)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a duplicate delivery failed instead of being absorbed: %v", err)
		}
	}
	if runs := e.runs(job.ID); len(runs) != 1 {
		t.Fatalf("duplicate deliveries produced %d accepted runs, want exactly 1", len(runs))
	}
	if findings := len(e.findings(job.ID)); findings != 1 {
		t.Fatalf("duplicate deliveries produced %d findings, want 1", findings)
	}
	if usage := e.count(&database.UsageRecord{}, "job_id=?", job.ID); usage != 1 {
		t.Fatalf("provider usage was recorded %d times for one analysis", usage)
	}
	if completed := e.outbox("analysis.comparison_completed.v1"); completed != 1 {
		t.Fatalf("completion was announced %d times", completed)
	}
	e.must(e.classify(false))
	if class, reasons := e.latestClassification(); class != "ATTENTION" || !contains(reasons, "OBSERVED_CHANGE") {
		t.Fatalf("the single accepted result did not govern: %s %v", class, reasons)
	}
}

// TestAIP_IT_246ProviderTimeoutOrWorkerRestartRetriesWithinBoundsUntilTerminal
// covers IT-246.
func TestAIP_IT_246ProviderTimeoutOrWorkerRestartRetriesWithinBoundsUntilTerminal(t *testing.T) {
	timeout := llm.NewError(llm.CodeTimeout, 0, context.DeadlineExceeded)
	t.Run("timeouts resume and reach success", func(t *testing.T) {
		e := newAIPEnv(t)
		e.must(e.schedule(e.f.DraftID))
		job := e.jobFor(e.key)
		gateway := &aipGateway{steps: []aipStep{{Err: timeout}, {Err: timeout}, {JSON: aipResult(nil)}}}
		for attempt := 0; attempt < 2; attempt++ {
			if err := e.process(job, gateway, attempt); err == nil {
				t.Fatalf("attempt %d swallowed a provider timeout", attempt)
			}
			aipAssertNothingPersisted(t, e, job, 0)
		}
		e.must(e.process(job, gateway, 2))
		got := e.job(job.ID)
		if got.Status != "COMPLETED" || got.Attempts != 3 || gateway.callCount() != 3 {
			t.Fatalf("job=%s attempts=%d calls=%d, want COMPLETED after the third delivery", got.Status, got.Attempts, gateway.callCount())
		}
	})
	t.Run("a worker that dies before commit leaves no trace and the redelivery completes once", func(t *testing.T) {
		e := newAIPEnv(t)
		e.must(e.schedule(e.f.DraftID))
		job := e.jobFor(e.key)
		gateway := aipOK(aipResult(nil, aipFinding("CONSERVATION", "LOW", .9, "PHOTO-1")))
		crash := errors.New("worker killed before commit")
		if err := e.processInTx(job, gateway, 0, aipCurrentOnly, func() error { return crash }); !errors.Is(err, crash) {
			t.Fatalf("expected the simulated crash, got %v", err)
		}
		aipAssertNothingPersisted(t, e, job, 0)
		if e.count(&database.UsageDailySummary{}, "") != 0 {
			t.Fatal("daily usage was committed by a crashed delivery")
		}
		e.must(e.process(job, gateway, 1))
		if got := e.job(job.ID); got.Status != "COMPLETED" || len(e.runs(job.ID)) != 1 || len(e.findings(job.ID)) != 1 {
			t.Fatalf("redelivery did not complete exactly once: %s runs=%d findings=%d", got.Status, len(e.runs(job.ID)), len(e.findings(job.ID)))
		}
	})
	t.Run("exhaustion is bounded and terminal", func(t *testing.T) {
		e := newAIPEnv(t)
		e.must(e.schedule(e.f.DraftID))
		job := e.jobFor(e.key)
		gateway := &aipGateway{steps: []aipStep{{Err: timeout}}}
		for attempt := 0; attempt < aipFinalAttempt; attempt++ {
			if err := e.process(job, gateway, attempt); err == nil {
				t.Fatalf("attempt %d did not request a retry", attempt)
			}
		}
		e.must(e.process(job, gateway, aipFinalAttempt))
		aipAssertFailedTerminal(t, e, job)
		if gateway.callCount() != aipFinalAttempt+1 {
			t.Fatalf("provider was called %d times, want exactly %d", gateway.callCount(), aipFinalAttempt+1)
		}
		var output struct {
			ReasonCode string `json:"reasonCode"`
		}
		_ = json.Unmarshal(e.runs(job.ID)[0].Output, &output)
		if output.ReasonCode != string(llm.CodeTimeout) {
			t.Fatalf("terminal reason = %q, want the provider timeout code", output.ReasonCode)
		}
		e.must(e.classify(false))
		if class, reasons := e.latestClassification(); class != "ATTENTION" || !contains(reasons, "ANALYSIS_FAILED") {
			t.Fatalf("classification = %s %v", class, reasons)
		}
	})
}

// TestAIP_IT_247ReplayedCompletedRequestAddsNoFindingsToClassification covers IT-247.
func TestAIP_IT_247ReplayedCompletedRequestAddsNoFindingsToClassification(t *testing.T) {
	e := newAIPEnv(t)
	e.must(e.schedule(e.f.DraftID))
	job := e.jobFor(e.key)
	gateway := aipOK(aipResult(nil, aipFinding("CONSERVATION", "HIGH", .95, "PHOTO-1")))
	e.must(e.process(job, gateway, 0))
	e.must(e.classify(false))
	firstClass, firstReasons := e.latestClassification()
	for replay := 0; replay < 3; replay++ {
		e.must(e.process(job, gateway, replay))
		e.must(e.classify(false))
	}
	if gateway.callCount() != 1 {
		t.Fatalf("a completed request called the provider again (%d calls)", gateway.callCount())
	}
	if runs, findings, usage := len(e.runs(job.ID)), len(e.findings(job.ID)), e.count(&database.UsageRecord{}, "job_id=?", job.ID); runs != 1 || findings != 1 || usage != 1 {
		t.Fatalf("replay duplicated state: runs=%d findings=%d usage=%d", runs, findings, usage)
	}
	if rows := e.classifications(); len(rows) != 1 {
		t.Fatalf("replay produced %d classification runs", len(rows))
	}
	// An explicit forced reclassification recomputes from the same single set.
	e.must(e.classify(true))
	class, reasons := e.latestClassification()
	if class != firstClass || strings.Join(reasons, ",") != strings.Join(firstReasons, ",") {
		t.Fatalf("forced replay changed the outcome: %s %v -> %s %v", firstClass, firstReasons, class, reasons)
	}
}

// TestAIP_IT_248AnalysisWaitsForMediaVerificationAndSubmission covers IT-248.
func TestAIP_IT_248AnalysisWaitsForMediaVerificationAndSubmission(t *testing.T) {
	e := newAIPEnv(t)
	// Submission is not terminal yet: the draft does not exist, so scheduling
	// is retried rather than completed or discarded as permanent.
	err := e.schedule(identity.NewID())
	if err == nil || errors.Is(err, messaging.ErrPermanent) {
		t.Fatalf("scheduling before the draft exists returned %v, want a retryable error", err)
	}
	if len(e.jobs()) != 0 {
		t.Fatal("jobs were created for an unsubmitted draft")
	}
	e.must(e.schedule(e.f.DraftID))
	job := e.jobFor(e.key)

	// The real request builder needs the verified ANALYSIS derivative.
	build, err := buildrequest.Setup(buildrequest.Dependencies{Store: e.h.Store})
	e.must(err)
	handler, err := processHandlerWith(aipOK(aipResult(nil)), build)
	e.must(err)
	payload, _ := json.Marshal(map[string]any{"jobId": job.ID})
	run := func(attempt int) error {
		ctx := messaging.WithAttempt(e.ctx, attempt)
		return e.h.WithinTenant(ctx, e.f.TenantID, func(tx *gorm.DB) error { return handler(ctx, tx, payload, e.f.TenantID) })
	}
	for attempt := 0; attempt < aipFinalAttempt; attempt++ {
		if err := run(attempt); err == nil {
			t.Fatalf("attempt %d proceeded without verified media", attempt)
		}
		aipAssertNothingPersisted(t, e, job, 0)
	}
	// Verification finishes: the derivative appears and the same job resumes.
	var normalized database.MediaDerivative
	e.must(e.tenant(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND media_id=? AND kind='NORMALIZED'", e.f.TenantID, e.f.MediaID).Take(&normalized).Error
	}))
	body := []byte("\xff\xd8\xff\xe0aip-analysis-derivative")
	key := normalized.ObjectKey + "/analysis"
	e.must(e.h.Store.Client.Put(e.ctx, e.h.Store.Bucket, key, bytes.NewReader(body), int64(len(body)), "image/jpeg"))
	e.must(e.tenant(func(tx *gorm.DB) error {
		return tx.Create(&database.MediaDerivative{ID: identity.NewID(), TenantID: e.f.TenantID, MediaID: e.f.MediaID, ObjectKey: key, Kind: "ANALYSIS", SHA256: normalized.SHA256, ContentType: "image/jpeg", Width: 1, Height: 1, SizeBytes: int64(len(body)), CreatedAt: time.Now().UTC()}).Error
	}))
	if err := run(0); err != nil {
		t.Fatalf("job did not resume after verification: %v", err)
	}
	if got := e.job(job.ID); got.Status != "COMPLETED" || len(e.runs(job.ID)) != 1 {
		t.Fatalf("job=%s runs=%d after prerequisites were met", got.Status, len(e.runs(job.ID)))
	}
}

// TestAIP_IT_249RecaptureCreatesSupersedingAnalysisWithoutEditingPriorResults covers IT-249.
func TestAIP_IT_249RecaptureCreatesSupersedingAnalysisWithoutEditingPriorResults(t *testing.T) {
	e := newAIPEnv(t)
	e.must(e.schedule(e.f.DraftID))
	first := e.jobFor(e.key)
	e.must(e.process(first, aipOK(aipResult(nil, aipFinding("CONSERVATION", "MEDIUM", .9, "PHOTO-1"))), 0))
	priorRuns, priorFindings := e.runs(first.ID), e.findings(first.ID)

	e.addRecapture(e.key)
	e.must(e.schedule(identity.ID{})) // recapture.completed.v1 selects the newest draft
	jobs := e.jobs()
	if len(jobs) != 2 {
		t.Fatalf("recapture created %d jobs, want a superseding second job", len(jobs))
	}
	second := e.jobFor(e.key)
	if second.ID == first.ID || second.InputDigest == first.InputDigest {
		t.Fatalf("superseding job reuses the prior identity: %+v", second)
	}
	e.must(e.process(second, aipOK(aipResult(nil)), 0))

	// Prior results are byte-for-byte unchanged and remain stored.
	afterRuns, afterFindings := e.runs(first.ID), e.findings(first.ID)
	if len(afterRuns) != 1 || len(afterFindings) != 1 || afterRuns[0].ID != priorRuns[0].ID || string(afterRuns[0].Output) != string(priorRuns[0].Output) || afterFindings[0].ID != priorFindings[0].ID || afterFindings[0].Severity != "MEDIUM" {
		t.Fatalf("prior analysis was edited: runs=%+v findings=%+v", afterRuns, afterFindings)
	}
	if got := e.job(first.ID); got.Status != "COMPLETED" {
		t.Fatalf("prior job changed to %s", got.Status)
	}
	// And the platform refuses to edit them even for the privileged owner.
	for _, statement := range []string{
		"UPDATE analysis.analysis_runs SET validation_outcome='EDITED' WHERE id=?",
		"UPDATE analysis.findings SET severity='LOW' WHERE id=?",
	} {
		id := afterRuns[0].ID
		if strings.Contains(statement, "findings") {
			id = afterFindings[0].ID
		}
		if err := e.h.AdminDB.Exec(statement, id).Error; err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Fatalf("prior analysis row accepted an edit: %v", err)
		}
	}
	if len(e.runs(second.ID)) != 1 || len(e.findings(second.ID)) != 0 {
		t.Fatalf("superseding job state is wrong: runs=%d findings=%d", len(e.runs(second.ID)), len(e.findings(second.ID)))
	}
}

// TestAIP_IT_250ManyComparisonsStayAttributableAndReachATerminalClassification
// covers IT-250.
func TestAIP_IT_250ManyComparisonsStayAttributableAndReachATerminalClassification(t *testing.T) {
	e := newAIPEnv(t)
	type outcome struct {
		key      string
		response string
		terminal string
		findings int
	}
	var plan []outcome
	for i := 0; i < 12; i++ {
		key := "aip-250-" + intString(100+i)
		switch i % 4 {
		case 0:
			plan = append(plan, outcome{key, aipResult(nil, aipFinding("CONSERVATION", "LOW", .9, "PHOTO-1"), aipFinding("CLEANLINESS", "MEDIUM", .9, "PHOTO-1")), "COMPLETED", 2})
		case 1:
			plan = append(plan, outcome{key, aipResult(nil), "COMPLETED", 0})
		case 2:
			plan = append(plan, outcome{key, `not json`, "FAILED", 0})
		default:
			plan = append(plan, outcome{key, aipResult(nil, map[string]any{"category": "EVIDENCE_QUALITY", "title": "Imagem escura", "description": "A imagem não permite avaliar.", "severity": "NONE", "confidence": .9, "evidenceIds": []string{"PHOTO-1"}, "quality": "INSUFFICIENT", "recommendedAction": "Fotografar novamente."}), "INCONCLUSIVE", 1})
		}
		e.addAnswer(key, true)
	}
	e.must(e.schedule(e.f.DraftID))
	gateway := &aipGateway{route: func(request llm.StructuredRequest) aipStep {
		for _, item := range plan {
			if strings.HasSuffix(request.UserPrompt, "requirement="+item.key) || request.UserPrompt == "requirement="+item.key {
				return aipStep{JSON: item.response}
			}
		}
		return aipStep{JSON: aipResult(nil)}
	}}
	var wg sync.WaitGroup
	errs := make(chan error, len(plan)+1)
	process := func(key string, attempt int) {
		defer wg.Done()
		errs <- e.process(e.jobFor(key), gateway, attempt)
	}
	for _, item := range plan {
		wg.Add(1)
		attempt := 0
		if item.terminal == "FAILED" {
			attempt = aipFinalAttempt
		}
		go process(item.key, attempt)
	}
	wg.Add(1)
	go process(e.key, 0)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent comparison failed: %v", err)
		}
	}
	for _, item := range plan {
		job := e.jobFor(item.key)
		runs, findings := e.runs(job.ID), e.findings(job.ID)
		if job.Status != item.terminal || len(runs) != 1 || len(findings) != item.findings {
			t.Fatalf("%s = %s runs=%d findings=%d, want %s/1/%d", item.key, job.Status, len(runs), len(findings), item.terminal, item.findings)
		}
		for _, finding := range findings {
			if finding.ComparisonJobID == nil || *finding.ComparisonJobID != job.ID || finding.AnalysisRunID != runs[0].ID {
				t.Fatalf("finding %s is not attributable to %s", finding.ID, item.key)
			}
		}
	}
	e.must(e.classify(false))
	class, reasons := e.latestClassification()
	for _, want := range []string{"OBSERVED_CHANGE", "ANALYSIS_FAILED", "INCONCLUSIVE"} {
		if !contains(reasons, want) {
			t.Fatalf("classification %s lacks reason %s: %v", class, want, reasons)
		}
	}
	if class != "ATTENTION" || len(e.classifications()) != 1 {
		t.Fatalf("classification = %s over %d runs, want one ATTENTION", class, len(e.classifications()))
	}
}
