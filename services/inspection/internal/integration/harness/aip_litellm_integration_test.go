//go:build integration

package harness

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"

	"gorm.io/gorm"
)

// aipLiteLLM is an OpenAI-compatible LiteLLM stub. It records every request
// body and answers from a script, so the real llm.HTTPGateway adapter runs in
// front of the real analysis slices.
type aipLiteLLM struct {
	mu       sync.Mutex
	requests [][]byte
	respond  func(call int, w http.ResponseWriter)
	server   *httptest.Server
}

func newAIPLiteLLM(t *testing.T, respond func(call int, w http.ResponseWriter)) *aipLiteLLM {
	t.Helper()
	stub := &aipLiteLLM{respond: respond}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		stub.mu.Lock()
		stub.requests = append(stub.requests, body)
		call := len(stub.requests)
		stub.mu.Unlock()
		stub.respond(call, w)
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *aipLiteLLM) gateway() llm.HTTPGateway {
	return llm.HTTPGateway{BaseURL: s.server.URL, APIKey: "aip-gateway-key"}
}

func (s *aipLiteLLM) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

// aipCompletion writes a LiteLLM chat completion carrying content.
func aipCompletion(w http.ResponseWriter, id, content string) {
	body, _ := json.Marshal(map[string]any{
		"id": id, "model": "provider/vision-model", "provider": "provider",
		"choices": []any{map[string]any{"message": map[string]any{"content": content}}},
		"usage":   map[string]any{"prompt_tokens": 31, "completion_tokens": 7},
		"cost":    0.0123,
	})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// TestAIP_IT_371ValidLiteLLMOutputPersistsModelPromptDigestUsageCostAndEvidence covers IT-371.
func TestAIP_IT_371ValidLiteLLMOutputPersistsModelPromptDigestUsageCostAndEvidence(t *testing.T) {
	e := newAIPEnv(t)
	answer := aipResult(nil, aipFinding("CONSERVATION", "MEDIUM", .93, "PHOTO-1"))
	stub := newAIPLiteLLM(t, func(call int, w http.ResponseWriter) { aipCompletion(w, "req-371", answer) })
	e.must(e.schedule(e.f.DraftID))
	job := e.jobFor(e.key)
	if err := e.process(job, stub.gateway(), 0); err != nil {
		t.Fatal(err)
	}
	// The provider received the logical alias, the pinned schema and only the authorized image.
	var sent struct {
		Model          string `json:"model"`
		ResponseFormat struct {
			Type       string `json:"type"`
			JSONSchema struct {
				Strict bool `json:"strict"`
			} `json:"json_schema"`
		} `json:"response_format"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if stub.calls() != 1 {
		t.Fatalf("provider called %d times", stub.calls())
	}
	if err := json.Unmarshal(stub.requests[0], &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Model != "inspection-vision" || sent.ResponseFormat.Type != "json_schema" || !sent.ResponseFormat.JSONSchema.Strict || len(sent.Messages) != 2 || !strings.Contains(string(sent.Messages[1].Content), "evidenceId=PHOTO-1; role=CURRENT") || !strings.Contains(string(sent.Messages[1].Content), "data:image/png;base64,") {
		t.Fatalf("unexpected provider request: %s", stub.requests[0])
	}
	if strings.Contains(string(stub.requests[0]), "provider/vision-model") || strings.Contains(string(stub.requests[0]), e.f.TenantID.String()) {
		t.Fatal("the request leaked a provider model or tenant identity")
	}

	got := e.job(job.ID)
	runs := e.runs(job.ID)
	if got.Status != "COMPLETED" || len(runs) != 1 {
		t.Fatalf("job=%s runs=%d", got.Status, len(runs))
	}
	run := runs[0]
	if run.Provider != "provider" || run.Model != "provider/vision-model" || run.GatewayRequestID != "req-371" || run.PromptDigest != job.PromptDigest || run.PromptDigest == "" || run.InputDigest != job.InputDigest || run.PromptSnapshotID != job.PromptSnapshotID || run.ValidationOutcome != "ACCEPTED" || !aipSameJSON(run.Output, answer) {
		t.Fatalf("run does not record the actual provider facts: %+v", run)
	}
	if run.InputTokens == nil || *run.InputTokens != 31 || run.OutputTokens == nil || *run.OutputTokens != 7 || run.Cost == nil || *run.Cost != 0.0123 || run.LatencyMS < 0 {
		t.Fatalf("usage, cost or latency were not persisted: %+v", run)
	}
	var usage []database.UsageRecord
	e.must(e.tenant(func(tx *gorm.DB) error { return tx.Where("job_id=?", job.ID).Find(&usage).Error }))
	if len(usage) != 1 || usage[0].Model != "provider/vision-model" || usage[0].PromptDigest != job.PromptDigest || usage[0].InputTokens == nil || *usage[0].InputTokens != 31 || usage[0].Cost == nil || *usage[0].Cost != 0.0123 || usage[0].InspectionID != e.f.InspectionID {
		t.Fatalf("usage ledger = %+v", usage)
	}
	var daily database.UsageDailySummary
	e.must(e.tenant(func(tx *gorm.DB) error { return tx.Where("tenant_id=?", e.f.TenantID).Take(&daily).Error }))
	if daily.Requests != 1 || daily.InputTokens != 31 || daily.OutputTokens != 7 || daily.Cost != 0.0123 {
		t.Fatalf("daily summary = %+v", daily)
	}
	findings := e.findings(job.ID)
	var evidence []string
	if len(findings) != 1 || json.Unmarshal(findings[0].Evidence, &evidence) != nil || len(evidence) != 1 || evidence[0] != "PHOTO-1" || findings[0].AnalysisRunID != run.ID || findings[0].Severity != "MEDIUM" || findings[0].Confidence != .93 {
		t.Fatalf("findings do not carry their evidence: %+v", findings)
	}
}

// TestAIP_IT_372MalformedLiteLLMOutputRetriesWithoutAPartialFinding covers IT-372.
func TestAIP_IT_372MalformedLiteLLMOutputRetriesWithoutAPartialFinding(t *testing.T) {
	e := newAIPEnv(t)
	valid := aipResult(nil, aipFinding("CLEANLINESS", "LOW", .9, "PHOTO-1"))
	partlyValid := aipResult(nil, aipFinding("CONSERVATION", "HIGH", .9, "PHOTO-1"), aipFinding("CONSERVATION", "SEVERE", .9, "PHOTO-1"))
	stub := newAIPLiteLLM(t, func(call int, w http.ResponseWriter) {
		switch call {
		case 1:
			aipCompletion(w, "req-372-1", `{"noRelevantChange":null,"findings":[{"category":"CONSERVATION","title":`)
		case 2:
			aipCompletion(w, "req-372-2", partlyValid)
		default:
			aipCompletion(w, "req-372-3", valid)
		}
	})
	e.must(e.schedule(e.f.DraftID))
	job := e.jobFor(e.key)
	for attempt := 0; attempt < 2; attempt++ {
		if err := e.process(job, stub.gateway(), attempt); err == nil {
			t.Fatalf("attempt %d accepted malformed provider output", attempt)
		}
		aipAssertNothingPersisted(t, e, job, 0)
	}
	e.must(e.process(job, stub.gateway(), 2))
	if stub.calls() != 3 {
		t.Fatalf("provider was called %d times, want 3", stub.calls())
	}
	findings := e.findings(job.ID)
	if got := e.job(job.ID); got.Status != "COMPLETED" || len(findings) != 1 || findings[0].Category != "CLEANLINESS" || findings[0].Severity != "LOW" {
		t.Fatalf("job=%s findings=%+v; only the valid third answer may produce findings", got.Status, findings)
	}
	if runs := e.runs(job.ID); len(runs) != 1 || runs[0].GatewayRequestID != "req-372-3" {
		t.Fatalf("runs = %+v, want one run from the accepted answer", runs)
	}
}

// TestAIP_IT_373ExhaustedLiteLLMFailurePersistsInconclusiveComparisonAndTerminalReport covers IT-373.
func TestAIP_IT_373ExhaustedLiteLLMFailurePersistsInconclusiveComparisonAndTerminalReport(t *testing.T) {
	status := func(t *testing.T, e *aipEnv, snapshots []database.ReportSnapshot) string {
		t.Helper()
		if len(snapshots) != 1 {
			t.Fatalf("snapshots = %d, want one terminal report", len(snapshots))
		}
		var document reportcore.Snapshot
		e.must(json.Unmarshal(snapshots[0].CanonicalJSON, &document))
		for _, requirement := range document.Requirements {
			if requirement.Key == e.key {
				return requirement.AnalysisStatus
			}
		}
		t.Fatalf("the report does not describe requirement %s: %+v", e.key, document.Requirements)
		return ""
	}
	t.Run("provider failures are exhausted", func(t *testing.T) {
		e := newAIPEnv(t)
		stub := newAIPLiteLLM(t, func(call int, w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) })
		e.must(e.schedule(e.f.DraftID))
		job := e.jobFor(e.key)
		for attempt := 0; attempt < aipFinalAttempt; attempt++ {
			if err := e.process(job, stub.gateway(), attempt); err == nil {
				t.Fatalf("attempt %d swallowed a provider failure", attempt)
			}
		}
		e.must(e.process(job, stub.gateway(), aipFinalAttempt))
		aipAssertFailedTerminal(t, e, job)
		if stub.calls() != aipFinalAttempt+1 {
			t.Fatalf("provider calls = %d, want %d", stub.calls(), aipFinalAttempt+1)
		}
		e.must(e.classify(false))
		class, reasons := e.latestClassification()
		if class != "ATTENTION" || !contains(reasons, "ANALYSIS_FAILED") {
			t.Fatalf("classification = %s %v", class, reasons)
		}
		snapshots := aipReportHandler(t, e)()
		if snapshots[0].Classification != "ATTENTION" || status(t, e, snapshots) != "FAILED" {
			t.Fatalf("terminal report = %s / requirement %s", snapshots[0].Classification, status(t, e, snapshots))
		}
	})
	t.Run("the provider reports insufficient evidence", func(t *testing.T) {
		e := newAIPEnv(t)
		quality := aipResult(nil, map[string]any{"category": "EVIDENCE_QUALITY", "title": "Imagem escura", "description": "A imagem não permite avaliar o ambiente.", "severity": "NONE", "confidence": .95, "evidenceIds": []string{"PHOTO-1"}, "quality": "INSUFFICIENT", "recommendedAction": "Fotografar novamente com mais luz."})
		stub := newAIPLiteLLM(t, func(call int, w http.ResponseWriter) { aipCompletion(w, "req-373", quality) })
		e.must(e.schedule(e.f.DraftID))
		job := e.jobFor(e.key)
		e.must(e.process(job, stub.gateway(), 0))
		if got := e.job(job.ID); got.Status != "INCONCLUSIVE" || e.runs(job.ID)[0].ValidationOutcome != "INCONCLUSIVE" {
			t.Fatalf("job=%s run=%s, want an explicit inconclusive comparison", got.Status, e.runs(job.ID)[0].ValidationOutcome)
		}
		e.must(e.classify(false))
		if class, reasons := e.latestClassification(); class != "ATTENTION" || !contains(reasons, "INCONCLUSIVE") {
			t.Fatalf("classification = %s %v", class, reasons)
		}
		snapshots := aipReportHandler(t, e)()
		if snapshots[0].Classification != "ATTENTION" || status(t, e, snapshots) != "INCONCLUSIVE" {
			t.Fatalf("terminal report = %s / requirement %s", snapshots[0].Classification, status(t, e, snapshots))
		}
	})
}

// aipSameJSON compares JSON documents irrespective of formatting, because the
// jsonb column normalizes whitespace and key order.
func aipSameJSON(stored []byte, expected string) bool {
	var left, right any
	return json.Unmarshal(stored, &left) == nil && json.Unmarshal([]byte(expected), &right) == nil && reflect.DeepEqual(left, right)
}
