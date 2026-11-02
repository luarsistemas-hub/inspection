package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	processnote "inspection/services/inspection/internal/features/transcription/process_note"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/messaging"
)

// runWorkerMainEnv makes the test binary execute the real main() so startup
// failures are observed as a separate process (exit status and stderr).
const runWorkerMainEnv = "INSPECTION_TRANSCRIPTION_WORKER_TEST_RUN_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(runWorkerMainEnv) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestTranscriptionWorkerConfigDefaultsAndValidation(t *testing.T) {
	t.Setenv("INSPECTION_TRANSCRIPTION_PROFILE", "")
	t.Setenv("INSPECTION_TRANSCRIPTION_TIMEOUT", "")
	t.Setenv("INSPECTION_TRANSCRIPTION_LEASE", "")
	got, err := loadTranscriptionWorkerConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.timeout != 45*time.Second || got.lease != 73*time.Second || got.lease != processnote.RequiredLease(processnote.DefaultSilenceDetectorTimeout, got.timeout) {
		t.Fatalf("unexpected defaults: %#v", got)
	}
	t.Setenv("INSPECTION_TRANSCRIPTION_PROFILE", "UNKNOWN")
	if _, err := loadTranscriptionWorkerConfig(); safeTranscriptionCode(err) != codeInvalidConfig {
		t.Fatalf("unknown profile must fail with %s: %v", codeInvalidConfig, err)
	}
	t.Setenv("INSPECTION_TRANSCRIPTION_PROFILE", "GEMINI_2_5_FLASH_LITE")
	if _, err := loadTranscriptionWorkerConfig(); err != nil {
		t.Fatalf("catalog profile was rejected: %v", err)
	}
	t.Setenv("INSPECTION_TRANSCRIPTION_PROFILE", "")
	t.Setenv("INSPECTION_TRANSCRIPTION_TIMEOUT", "0s")
	if _, err := loadTranscriptionWorkerConfig(); safeTranscriptionCode(err) != codeInvalidConfig {
		t.Fatalf("non-positive timeout must fail with %s: %v", codeInvalidConfig, err)
	}
}

func TestTranscriptionWorkerLeaseBounds(t *testing.T) {
	t.Setenv("INSPECTION_TRANSCRIPTION_PROFILE", "")
	cases := []struct {
		timeout, lease string
		valid          bool
	}{
		// lease >= detector 10s + timeout + download 10s + persistence 3s +
		// margin 5s (processnote.RequiredLease) and lease < 5m.
		{"45s", "72s", false},
		{"45s", "73s", true},
		{"45s", "4m59s", true},
		{"45s", "5m", false},
		{"5s", "32s", false},
		{"5s", "33s", true},
		{"2m", "2m27s", false},
		{"2m", "2m28s", true},
		{"4m32s", "4m59s", false},
		{"4m31s", "4m59s", true},
		{"45s", "not-a-duration", false},
	}
	for _, tc := range cases {
		t.Setenv("INSPECTION_TRANSCRIPTION_TIMEOUT", tc.timeout)
		t.Setenv("INSPECTION_TRANSCRIPTION_LEASE", tc.lease)
		_, err := loadTranscriptionWorkerConfig()
		if tc.valid && err != nil {
			t.Errorf("timeout=%s lease=%s rejected: %v", tc.timeout, tc.lease, err)
		}
		if !tc.valid && safeTranscriptionCode(err) != codeInvalidConfig {
			t.Errorf("timeout=%s lease=%s accepted or wrong code: %v", tc.timeout, tc.lease, err)
		}
	}
}

func TestTranscriptionWorkerExitsBeforeDependenciesOnInvalidProfile(t *testing.T) {
	stderr, exitCode := runWorkerProcess(t, "INSPECTION_TRANSCRIPTION_PROFILE=UNKNOWN", "INSPECTION_DATABASE_URL=", "INSPECTION_RABBITMQ_URL=")
	if exitCode != 1 {
		t.Fatalf("exit code=%d, want 1; stderr=%q", exitCode, stderr)
	}
	if want := "inspection-transcription-worker exited: " + codeInvalidConfig; strings.TrimSpace(stderr) != want {
		t.Fatalf("stderr=%q, want %q", stderr, want)
	}
}

// runWorkerProcess re-executes the test binary as the worker main process.
func runWorkerProcess(t *testing.T, env ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	command.Env = append(append(os.Environ(), runWorkerMainEnv+"=1"), env...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	err := command.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stderr.String(), exitErr.ExitCode()
	}
	if err != nil {
		t.Fatalf("worker process did not run: %v", err)
	}
	return stderr.String(), 0
}

func TestTranscriptionWorkerSafeExitCodes(t *testing.T) {
	brokerLoss := errors.New("rabbit consumer: delivery stream closed unexpectedly")
	err := consumeUntilStopped(context.Background(), func(context.Context) error { return brokerLoss })
	if safeTranscriptionCode(err) != codeBrokerUnavailable || !errors.Is(err, brokerLoss) {
		t.Fatalf("broker loss must exit with %s: %v", codeBrokerUnavailable, err)
	}
	if strings.Contains(strings.ToLower(safeTranscriptionCode(err)), "readiness") {
		t.Fatalf("broker loss code must not mention readiness: %s", safeTranscriptionCode(err))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := consumeUntilStopped(ctx, func(ctx context.Context) error { return ctx.Err() }); !errors.Is(err, context.Canceled) || safeTranscriptionCode(err) == codeBrokerUnavailable {
		t.Fatalf("shutdown must not be reported as broker loss: %v", err)
	}
	if got := safeTranscriptionCode(errors.New("dial tcp: secret-host:5432")); got != codeStartupFailed {
		t.Fatalf("uncoded startup error must map to %s, got %s", codeStartupFailed, got)
	}
}

func TestTranscriptionWorkerRetriesActiveLease(t *testing.T) {
	tenant, note, job := identity.NewID(), identity.NewID(), identity.NewID()
	body, err := json.Marshal(events.Envelope[events.TranscriptionRequestedPayload]{ID: identity.NewID(), Type: "transcription.requested.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: tenant, CorrelationID: note.String(), Payload: events.TranscriptionRequestedPayload{NoteID: note, JobID: job, Generation: 1}})
	if err != nil {
		t.Fatal(err)
	}
	repository := &claimErrorRepository{err: processnote.ErrLeaseActive}
	processor := transcriptionJobProcessor{dependencies: processnote.Dependencies{Repository: repository, Audio: inertAudio{}, Gateway: inertGateway{}, Ledger: inertLedger{}}}
	if _, err := processor.Process(context.Background(), body, 0); !errors.Is(err, messaging.ErrLeaseBusy) {
		t.Fatalf("active lease must enter the five-minute retry queue: %v", err)
	}
	if repository.claims != 1 {
		t.Fatalf("expected one durable claim, got %d", repository.claims)
	}
}

func TestTranscriptionWorkerAcknowledgesUnclaimableJobsAfterConsumerCancellation(t *testing.T) {
	tenant, note, job := identity.NewID(), identity.NewID(), identity.NewID()
	body, err := json.Marshal(events.Envelope[events.TranscriptionRequestedPayload]{ID: identity.NewID(), Type: "transcription.requested.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: tenant, CorrelationID: note.String(), Payload: events.TranscriptionRequestedPayload{NoteID: note, JobID: job, Generation: 1}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, claimErr := range []error{processnote.ErrNotClaimable, processnote.ErrStaleLease} {
		repository := &claimErrorRepository{err: claimErr}
		processor := transcriptionJobProcessor{dependencies: processnote.Dependencies{Repository: repository, Audio: inertAudio{}, Gateway: inertGateway{}, Ledger: inertLedger{}}}
		if ack, err := processor.Process(ctx, body, 0); err != nil || !ack {
			t.Fatalf("%v delivery should be acknowledged: ack=%v err=%v", claimErr, ack, err)
		}
		if repository.ctxErr != nil {
			t.Fatalf("claim received cancelled context: %v", repository.ctxErr)
		}
		if repository.failures != 0 {
			t.Fatalf("%v must not fail the job, got %d Fail calls", claimErr, repository.failures)
		}
	}
}

func TestTranscriptionWorkerRejectsMalformedEnvelope(t *testing.T) {
	processor := transcriptionJobProcessor{}
	if _, err := processor.Process(context.Background(), []byte(`{"type":"transcription.requested.v1"}`), 0); err == nil {
		t.Fatal("malformed message must be rejected")
	}
}

type claimErrorRepository struct {
	err      error
	claims   int
	ctxErr   error
	failures int
}

func (r *claimErrorRepository) Claim(ctx context.Context, _ identity.ID, _ identity.ID, _ int) (processnote.Job, error) {
	r.claims++
	r.ctxErr = ctx.Err()
	return processnote.Job{}, r.err
}
func (*claimErrorRepository) StagedResult(context.Context, processnote.Job) (string, string, error) {
	return "", "", nil
}
func (*claimErrorRepository) StageResult(context.Context, processnote.Job, string, string) error {
	return nil
}
func (*claimErrorRepository) Complete(context.Context, processnote.Job, string, string, bool) error {
	return nil
}
func (r *claimErrorRepository) Fail(context.Context, processnote.Job, string, bool) error {
	r.failures++
	return nil
}

type inertAudio struct{}

func (inertAudio) Open(context.Context, string) (io.ReadSeekCloser, error) {
	return nil, errors.New("unexpected audio open")
}

type inertGateway struct{}

func (inertGateway) Transcribe(context.Context, llm.TranscriptionRequest) (llm.TranscriptionResult, error) {
	return llm.TranscriptionResult{}, errors.New("unexpected gateway call")
}

type inertLedger struct{}

func (inertLedger) Start(context.Context, processnote.Job) (string, error) {
	return "", errors.New("unexpected ledger call")
}
func (inertLedger) Finish(context.Context, string, llm.StructuredResult, string, int64) error {
	return nil
}
