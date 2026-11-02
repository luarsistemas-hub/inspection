//go:build integration

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	classifyinspection "inspection/services/inspection/internal/features/analysis/classify_inspection"
	processcomparison "inspection/services/inspection/internal/features/analysis/process_comparison"
	requestcomparisons "inspection/services/inspection/internal/features/analysis/request_comparisons"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

// aipEnv is one isolated tenant with a submitted PHOTO inspection, used by the
// autonomous-inspection-platform task 06 analysis, classification and report
// cases. Every handler is invoked through its public Setup, inside a tenant
// transaction with the runtime (RLS) role, exactly as the worker does.
type aipEnv struct {
	t   *testing.T
	h   *Harness
	ctx context.Context
	f   Task06Fixture
	// key is the requirement answered by the fixture draft.
	key string
}

func newAIPEnv(t *testing.T) *aipEnv {
	t.Helper()
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("integration database URLs are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	h, err := NewFromEnv(ctx)
	if err != nil {
		t.Fatalf("open integration harness: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f, err := h.SeedTask06Fixture(ctx, Task06FixtureOptions{ComparisonMode: "CURRENT_ONLY"})
	if err != nil {
		t.Fatal(err)
	}
	return &aipEnv{t: t, h: h, ctx: ctx, f: f, key: "env:new:" + f.EnvironmentIdentityID.String()}
}

// another seeds a second, unrelated tenant on the same harness.
func (e *aipEnv) another() Task06Fixture {
	e.t.Helper()
	f, err := e.h.SeedTask06Fixture(e.ctx, Task06FixtureOptions{ComparisonMode: "CURRENT_ONLY"})
	if err != nil {
		e.t.Fatal(err)
	}
	return f
}

func (e *aipEnv) tenant(fn func(tx *gorm.DB) error) error {
	return e.h.WithinTenant(e.ctx, e.f.TenantID, fn)
}

func (e *aipEnv) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

// addAnswer adds one more submitted requirement answer to the fixture draft.
// With a nil mediaIDs value the answer declares an impossibility instead.
func (e *aipEnv) addAnswer(key string, withMedia bool, flags ...string) {
	e.t.Helper()
	media, _ := json.Marshal([]identity.ID{})
	reason := "Área inacessível"
	if withMedia {
		media, _ = json.Marshal([]identity.ID{e.f.MediaID})
		reason = ""
	}
	if flags == nil {
		flags = []string{}
	}
	flagJSON, _ := json.Marshal(flags)
	now := time.Now().UTC().Truncate(time.Microsecond)
	e.must(e.tenant(func(tx *gorm.DB) error {
		return tx.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: e.f.TenantID, DraftID: e.f.DraftID, RequirementKey: key, MediaIDs: media, Flags: flagJSON, ImpossibilityReason: reason, Version: 1, CreatedAt: now, UpdatedAt: now}).Error
	}))
}

// addRecapture records a recapture responsibility with a newer draft that
// answers one requirement with fresh evidence, like a permitted PHOTO recapture.
func (e *aipEnv) addRecapture(key string) identity.ID {
	e.t.Helper()
	responsibilityID, draftID, mediaID := identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	media, _ := json.Marshal([]identity.ID{mediaID})
	e.must(e.tenant(func(tx *gorm.DB) error {
		var original database.MediaObject
		if err := tx.Where("tenant_id=? AND id=?", e.f.TenantID, e.f.MediaID).Take(&original).Error; err != nil {
			return err
		}
		replacement := original
		replacement.ID, replacement.ResponsibilityID, replacement.CreatedAt = mediaID, responsibilityID, now
		replacement.ObjectKey, replacement.IdempotencyKey, replacement.RequirementKey = original.ObjectKey+"/recapture-"+mediaID.String(), "aip-recapture-"+mediaID.String(), key
		replacement.ReplacesMediaID = &original.ID
		if err := tx.Create(&database.Responsibility{ID: responsibilityID, TenantID: e.f.TenantID, InspectionID: e.f.InspectionID, ParticipantID: e.f.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.CaptureDraft{ID: draftID, TenantID: e.f.TenantID, ResponsibilityID: responsibilityID, Kind: "RECAPTURE", MediaKind: "PHOTO", TemplateVersionID: e.f.TemplateVersionID, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[]`), Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&replacement).Error; err != nil {
			return err
		}
		return tx.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: e.f.TenantID, DraftID: draftID, RequirementKey: key, MediaIDs: media, Flags: json.RawMessage(`[]`), Version: 1, CreatedAt: now, UpdatedAt: now}).Error
	}))
	return draftID
}

// schedule runs request_comparisons for one capture.submitted-style event.
// A zero draftID mirrors the recapture.completed.v1 path, which selects the
// newest draft of the inspection.
func (e *aipEnv) schedule(draftID identity.ID) error {
	e.t.Helper()
	handler, err := requestcomparisons.Setup(requestcomparisons.Dependencies{Now: func() time.Time { return time.Now().UTC() }})
	e.must(err)
	body := map[string]any{"inspectionId": e.f.InspectionID, "kind": "INSPECTION"}
	if draftID != (identity.ID{}) {
		body["draftId"] = draftID
	}
	payload, _ := json.Marshal(body)
	return e.tenant(func(tx *gorm.DB) error {
		return handler(e.ctx, tx, rawPhotoSubmission(e.f.TenantID, e.f.InspectionID, payload))
	})
}

func (e *aipEnv) jobs() []database.ComparisonJob {
	e.t.Helper()
	var jobs []database.ComparisonJob
	e.must(e.tenant(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND inspection_id=?", e.f.TenantID, e.f.InspectionID).Order("requirement_key ASC, created_at ASC").Find(&jobs).Error
	}))
	return jobs
}

func (e *aipEnv) jobFor(key string) database.ComparisonJob {
	e.t.Helper()
	var latest database.ComparisonJob
	for _, job := range e.jobs() {
		if job.RequirementKey == key && !job.CreatedAt.Before(latest.CreatedAt) {
			latest = job
		}
	}
	if latest.ID == (identity.ID{}) {
		e.t.Fatalf("no comparison job for requirement %q", key)
	}
	return latest
}

func (e *aipEnv) job(id identity.ID) database.ComparisonJob {
	e.t.Helper()
	var job database.ComparisonJob
	e.must(e.tenant(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", e.f.TenantID, id).Take(&job).Error
	}))
	return job
}

// aipRequestBuilder returns the request builder used by the PHOTO cases. The
// requirement key is carried in the prompt so scripted gateways can route.
type aipRequestBuilder func(job database.ComparisonJob) llm.StructuredRequest

func aipCurrentOnly(job database.ComparisonJob) llm.StructuredRequest {
	return llm.StructuredRequest{ModelAlias: "inspection-vision", PromptDigest: "aip-prompt-digest", SystemPrompt: "Describe only visible evidence.", UserPrompt: "requirement=" + job.RequirementKey, JSONSchema: json.RawMessage(`{"type":"object"}`), MinimumConfidenceBPS: 8500, Mode: "CURRENT_ONLY", Images: []llm.NormalizedImage{{EvidenceID: "PHOTO-1", Source: "CURRENT", Digest: "fixture-digest", DataURL: "data:image/png;base64,AAAA"}}}
}

func aipComparative(job database.ComparisonJob) llm.StructuredRequest {
	request := aipCurrentOnly(job)
	request.Mode = "COMPARE_ORIGIN_CURRENT"
	request.Images = []llm.NormalizedImage{
		{EvidenceID: "PHOTO-0", Source: "ORIGIN", PairID: "pair-1", Position: "ORIGIN", Digest: "origin-digest", DataURL: "data:image/png;base64,AAAA", VisibilityKnown: true, Visible: true},
		{EvidenceID: "PHOTO-1", Source: "CURRENT", PairID: "pair-1", Position: "CURRENT_1", Digest: "fixture-digest", DataURL: "data:image/png;base64,AAAA", VisibilityKnown: true, Visible: true},
	}
	return request
}

// process invokes the process_comparison consumer for one job. attempt is the
// broker delivery attempt (zero based): attempts before the last one leave a
// failing job retryable, the last one makes the failure terminal.
func (e *aipEnv) process(job database.ComparisonJob, gateway llm.Gateway, attempt int) error {
	return e.processWith(job, gateway, attempt, aipCurrentOnly)
}

func (e *aipEnv) processWith(job database.ComparisonJob, gateway llm.Gateway, attempt int, build aipRequestBuilder) error {
	return e.processInTx(job, gateway, attempt, build, nil)
}

// processInTx also lets a case fail the surrounding transaction after the
// handler succeeded, which models a worker that dies before commit.
func (e *aipEnv) processInTx(job database.ComparisonJob, gateway llm.Gateway, attempt int, build aipRequestBuilder, afterHandler func() error) error {
	handler, err := processHandlerWith(gateway, func(_ context.Context, _ *gorm.DB, job database.ComparisonJob, _ processcomparison.Payload) (llm.StructuredRequest, error) {
		return build(job), nil
	})
	e.must(err)
	payload, _ := json.Marshal(processcomparison.Payload{JobID: job.ID})
	ctx := messaging.WithAttempt(e.ctx, attempt)
	return e.h.WithinTenant(ctx, e.f.TenantID, func(tx *gorm.DB) error {
		if err := handler(ctx, tx, payload, e.f.TenantID); err != nil {
			return err
		}
		if afterHandler != nil {
			return afterHandler()
		}
		return nil
	})
}

// classify invokes classify_inspection for the fixture inspection.
func (e *aipEnv) classify(force bool) error {
	e.t.Helper()
	handler, err := classifyinspection.Setup(classifyinspection.Dependencies{Now: time.Now})
	e.must(err)
	payload, _ := json.Marshal(map[string]any{"inspectionId": e.f.InspectionID, "force": force})
	envelope := events.RawEnvelope{ID: identity.NewID(), Type: "analysis.comparison_completed.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: e.f.TenantID, AggregateID: e.f.InspectionID, CorrelationID: "aip-classify-" + e.f.InspectionID.String(), Payload: payload}
	return e.tenant(func(tx *gorm.DB) error { return handler(e.ctx, tx, envelope) })
}

func (e *aipEnv) classifications() []database.ClassificationRun {
	e.t.Helper()
	var rows []database.ClassificationRun
	e.must(e.tenant(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND inspection_id=?", e.f.TenantID, e.f.InspectionID).Order("created_at ASC, id ASC").Find(&rows).Error
	}))
	return rows
}

func (e *aipEnv) latestClassification() (string, []string) {
	e.t.Helper()
	rows := e.classifications()
	if len(rows) == 0 {
		e.t.Fatal("no classification was published")
	}
	last := rows[len(rows)-1]
	var reasons []string
	e.must(json.Unmarshal(last.ReasonCodes, &reasons))
	return last.Classification, reasons
}

func (e *aipEnv) runs(jobID identity.ID) []database.AnalysisRun {
	e.t.Helper()
	var rows []database.AnalysisRun
	e.must(e.tenant(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND job_id=?", e.f.TenantID, jobID).Order("created_at ASC").Find(&rows).Error
	}))
	return rows
}

func (e *aipEnv) findings(jobID identity.ID) []database.FindingRecord {
	e.t.Helper()
	var rows []database.FindingRecord
	e.must(e.tenant(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND comparison_job_id=?", e.f.TenantID, jobID).Order("created_at ASC, id ASC").Find(&rows).Error
	}))
	return rows
}

// count counts tenant rows of one model, optionally narrowed by a condition.
func (e *aipEnv) count(model any, where string, args ...any) int64 {
	e.t.Helper()
	var total int64
	e.must(e.tenant(func(tx *gorm.DB) error {
		query := tx.Model(model).Where("tenant_id=?", e.f.TenantID)
		if where != "" {
			query = query.Where(where, args...)
		}
		return query.Count(&total).Error
	}))
	return total
}

func (e *aipEnv) outbox(eventType string) int64 {
	return e.count(&database.OutboxIntent{}, "type=?", eventType)
}

// aipFinding builds one provider finding.
func aipFinding(category, severity string, confidence float64, evidence ...string) map[string]any {
	return map[string]any{"category": category, "title": "Observação visual", "description": "Condição visual observada na imagem.", "severity": severity, "confidence": confidence, "evidenceIds": evidence, "quality": "ADEQUATE", "recommendedAction": "Registrar e acompanhar."}
}

// aipResult serializes a provider answer. noChange is nil, true or false.
func aipResult(noChange any, findings ...map[string]any) string {
	if findings == nil {
		findings = []map[string]any{}
	}
	data, err := json.Marshal(map[string]any{"noRelevantChange": noChange, "findings": findings})
	if err != nil {
		panic(err)
	}
	return string(data)
}

// aipStep is one scripted provider reaction.
type aipStep struct {
	JSON string
	Err  error
}

// aipGateway is a scripted llm.Gateway. A route function, when present,
// selects the step by request content; otherwise steps are consumed in order
// and the last one repeats.
type aipGateway struct {
	mu    sync.Mutex
	steps []aipStep
	route func(llm.StructuredRequest) aipStep
	calls int
	enter func()
}

func aipOK(json string) *aipGateway { return &aipGateway{steps: []aipStep{{JSON: json}}} }

func (g *aipGateway) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

func (g *aipGateway) CompleteStructured(_ context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
	if g.enter != nil {
		g.enter()
	}
	g.mu.Lock()
	g.calls++
	call := g.calls
	step := aipStep{JSON: aipResult(nil)}
	if g.route != nil {
		step = g.route(request)
	} else if len(g.steps) > 0 {
		index := call - 1
		if index >= len(g.steps) {
			index = len(g.steps) - 1
		}
		step = g.steps[index]
	}
	g.mu.Unlock()
	if step.Err != nil {
		return llm.StructuredResult{}, step.Err
	}
	input, output, cost := int64(31), int64(7), 0.0123
	return llm.StructuredResult{JSON: []byte(step.JSON), Provider: "litellm-stub", Model: "stub/vision-1", GatewayRequestID: fmt.Sprintf("req-%d", call), InputTokens: &input, OutputTokens: &output, Cost: &cost, Latency: 15 * time.Millisecond, HTTPStatus: 200, TransportDelivered: true}, nil
}

func sortedReasons(reasons []string) []string {
	out := append([]string{}, reasons...)
	sort.Strings(out)
	return out
}

type aipHandler = func(context.Context, *gorm.DB, []byte, identity.ID) error

// processHandlerWith builds the process_comparison consumer through its public Setup.
func processHandlerWith(gateway llm.Gateway, build processcomparison.RequestBuilder) (aipHandler, error) {
	return processcomparison.Setup(processcomparison.Dependencies{Gateway: gateway, BuildRequest: build, Now: time.Now, Mode: "mock"})
}

func processHandler(gateway llm.Gateway) (aipHandler, error) {
	return processHandlerWith(gateway, func(_ context.Context, _ *gorm.DB, job database.ComparisonJob, _ processcomparison.Payload) (llm.StructuredRequest, error) {
		return aipCurrentOnly(job), nil
	})
}

// classifyInTx runs classification and then lets a case abort the transaction,
// which models a worker that stops before committing its decision.
func (e *aipEnv) classifyInTx(force bool, afterHandler func() error) error {
	e.t.Helper()
	handler, err := classifyinspection.Setup(classifyinspection.Dependencies{Now: time.Now})
	e.must(err)
	payload, _ := json.Marshal(map[string]any{"inspectionId": e.f.InspectionID, "force": force})
	envelope := events.RawEnvelope{ID: identity.NewID(), Type: "analysis.comparison_completed.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: e.f.TenantID, AggregateID: e.f.InspectionID, CorrelationID: "aip-classify-" + e.f.InspectionID.String(), Payload: payload}
	return e.tenant(func(tx *gorm.DB) error {
		if err := handler(e.ctx, tx, envelope); err != nil {
			return err
		}
		return afterHandler()
	})
}
