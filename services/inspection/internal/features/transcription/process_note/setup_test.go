package process_note

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/llm"
)

type repositoryFake struct {
	job         Job
	claimErr    error
	completeErr error
	completed   int
	failed      int
	transcript  string
	noSpeech    bool
	stagedCall  string
	stagedText  string
	stageCalls  int
	stageErr    error
	failCode    string
	uncertain   bool
	failCtx     context.Context
	failCtxErr  error
}

func (r *repositoryFake) Claim(_ context.Context, jobID, tenantID identity.ID, generation int) (Job, error) {
	if r.claimErr != nil {
		return Job{}, r.claimErr
	}
	r.job.JobID, r.job.TenantID, r.job.Generation = jobID, tenantID, generation
	return r.job, nil
}
func (r *repositoryFake) Complete(_ context.Context, _ Job, _ string, transcript string, noSpeech bool) error {
	if r.completeErr != nil {
		return r.completeErr
	}
	r.completed++
	r.transcript, r.noSpeech = transcript, noSpeech
	return nil
}
func (r *repositoryFake) StagedResult(context.Context, Job) (string, string, error) {
	return r.stagedCall, r.stagedText, nil
}
func (r *repositoryFake) StageResult(_ context.Context, _ Job, callID, transcript string) error {
	r.stageCalls++
	if r.stageErr != nil {
		return r.stageErr
	}
	r.stagedCall, r.stagedText = callID, transcript
	return nil
}
func (r *repositoryFake) Fail(ctx context.Context, _ Job, code string, uncertain bool) error {
	r.failed++
	r.failCode, r.uncertain = code, uncertain
	r.failCtx = ctx
	r.failCtxErr = ctx.Err()
	return nil
}

type audioFake struct{}

func (audioFake) Open(context.Context, string) (io.ReadSeekCloser, error) {
	return readSeekCloser{bytes.NewReader([]byte("audio"))}, nil
}

type readSeekCloser struct{ *bytes.Reader }

func (readSeekCloser) Close() error { return nil }

type gatewayFake struct {
	calls  int
	result llm.TranscriptionResult
	err    error
}

func (g *gatewayFake) Transcribe(context.Context, llm.TranscriptionRequest) (llm.TranscriptionResult, error) {
	g.calls++
	return g.result, g.err
}

type ledgerFake struct {
	starts, finishes    int
	startErr, finishErr error
	durationMS          int64
	finishCtx           context.Context
	finishOutcome       string
}

func (l *ledgerFake) Start(context.Context, Job) (string, error) {
	l.starts++
	return "call-1", l.startErr
}
func (l *ledgerFake) Finish(ctx context.Context, _ string, _ llm.StructuredResult, outcome string, durationMS int64) error {
	l.finishes++
	l.durationMS = durationMS
	l.finishCtx, l.finishOutcome = ctx, outcome
	return l.finishErr
}

func TestSetupClosesLedgerWhenStagingFails(t *testing.T) {
	jobID, tenantID := identity.NewID(), identity.NewID()
	repo := &repositoryFake{job: Job{LeaseToken: identity.NewID(), ObjectKey: "audio", MIME: "audio/webm", DurationMS: 1000}, stageErr: errors.New("stage failed")}
	ledger := &ledgerFake{}
	if err := Setup(context.Background(), Dependencies{Repository: repo, Audio: audioFake{}, Gateway: &gatewayFake{result: llm.TranscriptionResult{Transcript: "texto"}}, Ledger: ledger}, jobID, tenantID, 1); err == nil {
		t.Fatal("expected stage error")
	}
	if ledger.finishes != 1 || ledger.finishOutcome != "SUCCEEDED" {
		t.Fatalf("ledger was left open after staging failure: %+v", ledger)
	}
}

func TestSetupDeadlineCoversAudioOpenAndLeavesLeaseMargin(t *testing.T) {
	jobID, tenantID := identity.NewID(), identity.NewID()
	// A database deadline from a skewed clock must not influence the local attempt budget.
	skewedDeadline := time.Now().Add(-time.Hour)
	before := time.Now()
	repo := &repositoryFake{job: Job{LeaseToken: identity.NewID(), ObjectKey: "audio", MIME: "audio/webm", DurationMS: 1000, LeaseDeadline: skewedDeadline, LeaseDuration: 20 * time.Second}}
	audio := &contextAudioFake{}
	if err := Setup(context.Background(), Dependencies{Repository: repo, Audio: audio, Gateway: &gatewayFake{}, Ledger: &ledgerFake{}}, jobID, tenantID, 1); err != nil {
		t.Fatal(err)
	}
	after := time.Now()
	deadline, ok := audio.ctx.Deadline()
	if !ok || deadline.Before(before.Add(20*time.Second-leaseSafetyMargin)) || deadline.After(after.Add(20*time.Second-leaseSafetyMargin)) {
		t.Fatalf("audio open deadline=%s, want local start + lease - margin", deadline)
	}
}

func TestSetupStagesProviderResultReceivedAtAttemptDeadline(t *testing.T) {
	jobID, tenantID := identity.NewID(), identity.NewID()
	// The attempt budget is 150ms; persistence then takes longer than the remaining budget.
	repo := &deadlineRepositoryFake{repositoryFake: repositoryFake{job: Job{LeaseToken: identity.NewID(), ObjectKey: "audio", MIME: "audio/webm", DurationMS: 1000, LeaseDuration: leaseSafetyMargin + 150*time.Millisecond}}, latency: 50 * time.Millisecond}
	gateway := &deadlineGateway{before: 10 * time.Millisecond, result: llm.TranscriptionResult{Transcript: "resposta paga no limite"}}
	ledger := &ledgerFake{}
	if err := Setup(context.Background(), Dependencies{Repository: repo, Audio: audioFake{}, Gateway: gateway, Ledger: ledger}, jobID, tenantID, 1); err != nil {
		t.Fatal(err)
	}
	if !gateway.sawDeadline || repo.stageCtxErr != nil || repo.completeCtxErr != nil {
		t.Fatalf("persistence inherited the attempt deadline: sawDeadline=%v stage=%v complete=%v", gateway.sawDeadline, repo.stageCtxErr, repo.completeCtxErr)
	}
	if repo.stageCalls != 1 || repo.completed != 1 || repo.transcript != "resposta paga no limite" || ledger.finishOutcome != "SUCCEEDED" {
		t.Fatalf("paid result was not recorded: repo=%+v ledger=%+v", repo.repositoryFake, ledger)
	}
}

// deadlineRepositoryFake simulates database latency and records whether the
// write context was still usable after it.
type deadlineRepositoryFake struct {
	repositoryFake
	latency                     time.Duration
	stageCtxErr, completeCtxErr error
}

func (r *deadlineRepositoryFake) StageResult(ctx context.Context, job Job, callID, transcript string) error {
	time.Sleep(r.latency)
	if r.stageCtxErr = ctx.Err(); r.stageCtxErr != nil {
		return r.stageCtxErr
	}
	return r.repositoryFake.StageResult(ctx, job, callID, transcript)
}

func (r *deadlineRepositoryFake) Complete(ctx context.Context, job Job, callID, transcript string, noSpeech bool) error {
	time.Sleep(r.latency)
	if r.completeCtxErr = ctx.Err(); r.completeCtxErr != nil {
		return r.completeCtxErr
	}
	return r.repositoryFake.Complete(ctx, job, callID, transcript, noSpeech)
}

// deadlineGateway answers successfully just before the attempt deadline.
type deadlineGateway struct {
	before      time.Duration
	result      llm.TranscriptionResult
	sawDeadline bool
}

func (g *deadlineGateway) Transcribe(ctx context.Context, _ llm.TranscriptionRequest) (llm.TranscriptionResult, error) {
	deadline, ok := ctx.Deadline()
	g.sawDeadline = ok
	if ok {
		time.Sleep(time.Until(deadline.Add(-g.before)))
	}
	return g.result, nil
}

func TestSetupCompletesStagedResultBeforeOpeningAudio(t *testing.T) {
	jobID, tenantID := identity.NewID(), identity.NewID()
	repo := &repositoryFake{job: Job{LeaseToken: identity.NewID(), ObjectKey: "audio", MIME: "audio/webm", DurationMS: 30000, Attempt: 3}, stagedCall: "call-prior", stagedText: "resultado já pago"}
	audio := &failingAudioFake{err: errors.New("object store offline")}
	gateway, ledger := &gatewayFake{}, &ledgerFake{}
	silence := &countingSilenceFake{err: ErrSilenceDetectorUnavailable}
	if err := Setup(context.Background(), Dependencies{Repository: repo, Audio: audio, Gateway: gateway, Ledger: ledger, Silence: silence}, jobID, tenantID, 1); err != nil {
		t.Fatal(err)
	}
	if audio.opens != 0 || silence.calls != 0 || gateway.calls != 0 || ledger.starts != 0 {
		t.Fatalf("staged recovery touched audio or provider: opens=%d probes=%d calls=%d starts=%d", audio.opens, silence.calls, gateway.calls, ledger.starts)
	}
	if repo.failed != 0 || repo.completed != 1 || repo.transcript != "resultado já pago" {
		t.Fatalf("staged result was not completed: failed=%d code=%q completed=%d text=%q", repo.failed, repo.failCode, repo.completed, repo.transcript)
	}
}

type failingAudioFake struct {
	err   error
	opens int
}

func (f *failingAudioFake) Open(context.Context, string) (io.ReadSeekCloser, error) {
	f.opens++
	return nil, f.err
}

type countingSilenceFake struct {
	err   error
	calls int
}

func (f *countingSilenceFake) IsSilent(context.Context, io.Reader, string, int64) (bool, error) {
	f.calls++
	return false, f.err
}

type contextAudioFake struct{ ctx context.Context }

func (f *contextAudioFake) Open(ctx context.Context, _ string) (io.ReadSeekCloser, error) {
	f.ctx = ctx
	return readSeekCloser{bytes.NewReader([]byte("audio"))}, nil
}

func TestSetupKeepsStagedTranscriptWhenLedgerFinishFails(t *testing.T) {
	jobID, tenantID := identity.NewID(), identity.NewID()
	repo := &repositoryFake{job: Job{LeaseToken: identity.NewID(), ObjectKey: "audio", MIME: "audio/webm", DurationMS: 7400, Profile: llm.TranscriptionProfiles["GEMINI_2_5_FLASH_LITE"], PromptDigest: "p"}}
	gateway := &gatewayFake{result: llm.TranscriptionResult{Transcript: "texto recuperável"}}
	ledger := &ledgerFake{finishErr: errors.New("ledger offline")}
	err := Setup(context.Background(), Dependencies{Repository: repo, Audio: audioFake{}, Gateway: gateway, Ledger: ledger}, jobID, tenantID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if repo.completed != 1 || repo.transcript != "texto recuperável" || repo.stageCalls != 1 || gateway.calls != 1 || ledger.durationMS != 7400 {
		t.Fatalf("response was not retained: repo=%+v gateway=%d ledger=%+v", repo, gateway.calls, ledger)
	}
}

func TestSetupRecoversStagedTranscriptWithoutCallingGateway(t *testing.T) {
	jobID, tenantID := identity.NewID(), identity.NewID()
	repo := &repositoryFake{job: Job{LeaseToken: identity.NewID(), ObjectKey: "audio", MIME: "audio/webm", DurationMS: 30000}, stagedCall: "call-prior", stagedText: "texto já recebido"}
	gateway, ledger := &gatewayFake{}, &ledgerFake{}
	if err := Setup(context.Background(), Dependencies{Repository: repo, Audio: audioFake{}, Gateway: gateway, Ledger: ledger}, jobID, tenantID, 1); err != nil {
		t.Fatal(err)
	}
	if gateway.calls != 0 || ledger.starts != 0 || repo.transcript != "texto já recebido" {
		t.Fatalf("staged result was not recovered: calls=%d starts=%d result=%q", gateway.calls, ledger.starts, repo.transcript)
	}
}

func TestSetupRetainsStagedResultWhenCompletionFailsTransiently(t *testing.T) {
	jobID, tenantID := identity.NewID(), identity.NewID()
	repo := &repositoryFake{job: Job{LeaseToken: identity.NewID(), ObjectKey: "audio", MIME: "audio/webm", DurationMS: 30000}, completeErr: errors.New("database temporarily unavailable")}
	gateway := &gatewayFake{result: llm.TranscriptionResult{Transcript: "não perder"}}
	ledger := &ledgerFake{}
	deps := Dependencies{Repository: repo, Audio: audioFake{}, Gateway: gateway, Ledger: ledger}
	if err := Setup(context.Background(), deps, jobID, tenantID, 1); err == nil {
		t.Fatal("expected transient completion failure")
	}
	repo.completeErr = nil
	if err := Setup(context.Background(), deps, jobID, tenantID, 1); err != nil {
		t.Fatal(err)
	}
	if gateway.calls != 1 || repo.transcript != "não perder" {
		t.Fatalf("staged response lost or retranscribed: calls=%d text=%q", gateway.calls, repo.transcript)
	}
}

func TestSetupRejectsOversizedTranscriptTerminally(t *testing.T) {
	jobID, tenantID := identity.NewID(), identity.NewID()
	repo := &repositoryFake{job: Job{LeaseToken: identity.NewID(), ObjectKey: "audio", MIME: "audio/webm", DurationMS: 30000}}
	gateway := &gatewayFake{result: llm.TranscriptionResult{Transcript: strings.Repeat("á", 12001)}}
	ledger := &ledgerFake{}
	if err := Setup(context.Background(), Dependencies{Repository: repo, Audio: audioFake{}, Gateway: gateway, Ledger: ledger}, jobID, tenantID, 1); err != nil {
		t.Fatal(err)
	}
	if repo.failCode != "TRANSCRIPT_TOO_LONG" || repo.uncertain || repo.completed != 0 {
		t.Fatalf("oversized transcript state code=%q uncertain=%v complete=%d", repo.failCode, repo.uncertain, repo.completed)
	}
}

func TestSetupClassifiesDefinitiveHTTPAndUncertainTransportFailures(t *testing.T) {
	for _, tc := range []struct {
		name, code    string
		status        int
		wantUncertain bool
	}{
		{name: "rate limit definite", code: string(llm.CodeRateLimit), status: 429},
		{name: "server error definite", code: string(llm.CodeProviderHTTP), status: 503},
		{name: "timeout uncertain", code: string(llm.CodeTimeout), wantUncertain: true},
		{name: "cancellation uncertain", code: string(llm.CodeCancelled), wantUncertain: true},
		{name: "pre-send transport is retryable", code: string(llm.CodeTransport)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &repositoryFake{job: Job{LeaseToken: identity.NewID(), ObjectKey: "audio", MIME: "audio/webm", DurationMS: 30000}}
			gateway := &gatewayFake{result: llm.TranscriptionResult{Usage: llm.StructuredResult{HTTPStatus: tc.status, TransportDelivered: tc.wantUncertain}}, err: llm.NewError(llm.ErrorCode(tc.code), tc.status, errors.New("transport"))}
			ledger := &ledgerFake{}
			if err := Setup(context.Background(), Dependencies{Repository: repo, Audio: audioFake{}, Gateway: gateway, Ledger: ledger}, identity.NewID(), identity.NewID(), 1); err != nil {
				t.Fatal(err)
			}
			if repo.uncertain != tc.wantUncertain || repo.failed != 1 {
				t.Fatalf("uncertain=%v failed=%d want uncertain=%v", repo.uncertain, repo.failed, tc.wantUncertain)
			}
		})
	}
}

func TestSetupUsesDetachedContextForFailureAfterShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo := &repositoryFake{job: Job{LeaseToken: identity.NewID(), ObjectKey: "audio", MIME: "audio/webm", DurationMS: 1000}}
	gateway := &cancelGateway{cancel: cancel}
	if err := Setup(ctx, Dependencies{Repository: repo, Audio: audioFake{}, Gateway: gateway, Ledger: &ledgerFake{}}, identity.NewID(), identity.NewID(), 1); err != nil {
		t.Fatal(err)
	}
	if repo.failCtx == nil || repo.failCtxErr != nil {
		t.Fatalf("failure persistence inherited shutdown cancellation: %v", repo.failCtx)
	}
}

type cancelGateway struct{ cancel context.CancelFunc }

func (g *cancelGateway) Transcribe(context.Context, llm.TranscriptionRequest) (llm.TranscriptionResult, error) {
	g.cancel()
	return llm.TranscriptionResult{Usage: llm.StructuredResult{TransportDelivered: true}}, llm.NewError(llm.CodeTimeout, 0, errors.New("timeout"))
}

func TestSetupPersistsSingleTranscriptRevisionUT009(t *testing.T) {
	jobID, tenantID, token := identity.NewID(), identity.NewID(), identity.NewID()
	repo := &repositoryFake{job: Job{LeaseToken: token, ObjectKey: "opaque/audio", MIME: "audio/webm", DurationMS: 30_000, Profile: llm.TranscriptionProfiles["GEMINI_2_5_FLASH_LITE"], PromptDigest: "p"}}
	gateway := &gatewayFake{result: llm.TranscriptionResult{Transcript: "cinco pratos"}}
	ledger := &ledgerFake{}
	err := Setup(context.Background(), Dependencies{Repository: repo, Audio: audioFake{}, Gateway: gateway, Ledger: ledger}, jobID, tenantID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if gateway.calls != 1 || ledger.starts != 1 || ledger.finishes != 1 || repo.completed != 1 || repo.transcript != "cinco pratos" {
		t.Fatalf("unexpected processing outcome: calls=%d starts=%d finishes=%d completed=%d transcript=%q", gateway.calls, ledger.starts, ledger.finishes, repo.completed, repo.transcript)
	}
}

func TestSetupRejectsStaleLeaseWithoutPersistingUT010(t *testing.T) {
	jobID, tenantID := identity.NewID(), identity.NewID()
	repo := &repositoryFake{claimErr: errors.New("stale lease")}
	gateway, ledger := &gatewayFake{}, &ledgerFake{}
	err := Setup(context.Background(), Dependencies{Repository: repo, Audio: audioFake{}, Gateway: gateway, Ledger: ledger}, jobID, tenantID, 1)
	if err == nil {
		t.Fatal("expected stale lease failure")
	}
	if gateway.calls != 0 || ledger.starts != 0 || repo.completed != 0 {
		t.Fatalf("stale lease crossed processing boundary: calls=%d starts=%d completed=%d", gateway.calls, ledger.starts, repo.completed)
	}
}

func TestSetupDoesNotCallProviderWhenAttemptLedgerCannotStart(t *testing.T) {
	jobID, tenantID := identity.NewID(), identity.NewID()
	repo := &repositoryFake{job: Job{LeaseToken: identity.NewID(), ObjectKey: "opaque/audio", MIME: "audio/webm", DurationMS: 30_000}}
	gateway, ledger := &gatewayFake{}, &ledgerFake{startErr: errors.New("ledger")}
	if err := Setup(context.Background(), Dependencies{Repository: repo, Audio: audioFake{}, Gateway: gateway, Ledger: ledger}, jobID, tenantID, 1); err != nil {
		t.Fatal(err)
	}
	if gateway.calls != 0 || repo.failed != 1 {
		t.Fatalf("provider must not run without a usage start row: calls=%d failed=%d", gateway.calls, repo.failed)
	}
}

func TestSetupPersistsNoSpeechWithoutCallingGateway(t *testing.T) {
	jobID, tenantID := identity.NewID(), identity.NewID()
	repo := &repositoryFake{job: Job{LeaseToken: identity.NewID(), ObjectKey: "opaque/audio", MIME: "audio/webm", DurationMS: 30_000}}
	gateway, ledger := &gatewayFake{}, &ledgerFake{}
	err := Setup(context.Background(), Dependencies{Repository: repo, Audio: audioFake{}, Gateway: gateway, Ledger: ledger, Silence: silenceFake(true)}, jobID, tenantID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if gateway.calls != 0 || ledger.starts != 0 || !repo.noSpeech || repo.completed != 1 {
		t.Fatalf("silence was not handled locally")
	}
}

type silenceFake bool

func (s silenceFake) IsSilent(context.Context, io.Reader, string, int64) (bool, error) {
	return bool(s), nil
}

// budgetRepositoryFake records the deadline of every post-transport write.
type budgetRepositoryFake struct {
	repositoryFake
	deadlines map[string]time.Time
}

func (r *budgetRepositoryFake) record(name string, ctx context.Context) {
	deadline, _ := ctx.Deadline()
	r.deadlines[name] = deadline
}

func (r *budgetRepositoryFake) StageResult(ctx context.Context, job Job, callID, transcript string) error {
	r.record("stage", ctx)
	return r.repositoryFake.StageResult(ctx, job, callID, transcript)
}

func (r *budgetRepositoryFake) Complete(ctx context.Context, job Job, callID, transcript string, noSpeech bool) error {
	r.record("complete", ctx)
	return r.repositoryFake.Complete(ctx, job, callID, transcript, noSpeech)
}

func (r *budgetRepositoryFake) Fail(ctx context.Context, job Job, code string, uncertain bool) error {
	r.record("fail", ctx)
	return r.repositoryFake.Fail(ctx, job, code, uncertain)
}

// budgetLedgerFake records the deadline of each ledger finish.
type budgetLedgerFake struct {
	ledgerFake
	deadlines []time.Time
}

func (l *budgetLedgerFake) Finish(ctx context.Context, callID string, result llm.StructuredResult, outcome string, durationMS int64) error {
	deadline, _ := ctx.Deadline()
	l.deadlines = append(l.deadlines, deadline)
	return l.ledgerFake.Finish(ctx, callID, result, outcome, durationMS)
}

func TestSetupSharesOnePersistenceBudgetAfterTranscribe(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result llm.TranscriptionResult
		err    error
		writes []string
	}{
		{name: "stage, finish and complete", result: llm.TranscriptionResult{Transcript: "texto pago"}, writes: []string{"stage", "complete"}},
		{name: "finish and fail", result: llm.TranscriptionResult{Usage: llm.StructuredResult{HTTPStatus: 503}}, err: llm.NewError(llm.CodeProviderHTTP, 503, errors.New("unavailable")), writes: []string{"fail"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &budgetRepositoryFake{repositoryFake: repositoryFake{job: Job{LeaseToken: identity.NewID(), ObjectKey: "audio", MIME: "audio/webm", DurationMS: 1000}}, deadlines: map[string]time.Time{}}
			ledger := &budgetLedgerFake{}
			gateway := &gatewayFake{result: tc.result, err: tc.err}
			before := time.Now()
			if err := Setup(context.Background(), Dependencies{Repository: repo, Audio: audioFake{}, Gateway: gateway, Ledger: ledger}, identity.NewID(), identity.NewID(), 1); err != nil {
				t.Fatal(err)
			}
			if len(ledger.deadlines) != 1 || len(repo.deadlines) != len(tc.writes) {
				t.Fatalf("unexpected writes: ledger=%d repo=%v", len(ledger.deadlines), repo.deadlines)
			}
			shared := ledger.deadlines[0]
			if shared.IsZero() || shared.After(before.Add(persistenceTimeout+time.Second)) {
				t.Fatalf("ledger finish deadline=%s, want one bounded persistence budget", shared)
			}
			for _, write := range tc.writes {
				if got := repo.deadlines[write]; !got.Equal(shared) {
					t.Fatalf("%s deadline=%s, want shared budget %s", write, got, shared)
				}
			}
			if leaseSafetyMargin < persistenceTimeout {
				t.Fatalf("lease margin=%s does not cover the post-transport budget=%s", leaseSafetyMargin, persistenceTimeout)
			}
		})
	}
}
