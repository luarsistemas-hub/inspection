package process_note

import (
	"context"
	"errors"
	"io"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/llm"
)

// DefaultSilenceDetectorTimeout is the bound FFmpegSilenceDetector applies to
// one probe when no other timeout is configured. The worker passes it to the
// detector and to RequiredLease so both read the same value.
const DefaultSilenceDetectorTimeout = 10 * time.Second

const (
	// persistenceTimeout bounds the detached persistence budget. Before
	// transport it bounds each write; after the gateway returns, one shared
	// budget covers every remaining write (stage, ledger finish, complete/fail).
	persistenceTimeout = 3 * time.Second
	// leaseSafetyMargin is the part of the lease left after the attempt deadline,
	// so a result received at the deadline can still be persisted under a valid
	// fence. It must cover the single post-transport persistenceTimeout; the
	// extra covers worker/database clock skew.
	leaseSafetyMargin = persistenceTimeout + 2*time.Second
	// audioDownloadAllowance budgets opening and streaming the private audio object.
	audioDownloadAllowance = 10 * time.Second
)

// RequiredLease returns the minimum lease one attempt needs: silence detector
// timeout + gateway timeout + audio download (10s) + pre-transport
// persistence (3s) + post-deadline margin (5s). With the default 10s detector
// and 45s gateway it is 73s. Non-positive inputs fall back to the defaults.
func RequiredLease(detector, gateway time.Duration) time.Duration {
	return requiredLeaseDuration(detector, gateway)
}

// Job is the immutable processing snapshot claimed under a fencing token.
type Job struct {
	TenantID, NoteID, JobID identity.ID
	InspectionID            identity.ID
	Generation, Attempt     int
	LeaseToken              identity.ID
	LeaseDeadline           time.Time
	// LeaseDuration is the lease granted by the claim; the worker derives its
	// local attempt deadline from it instead of comparing clocks with LeaseDeadline.
	LeaseDuration time.Duration
	Profile       llm.TranscriptionProfile
	ProfileDigest string
	ObjectKey     string
	MIME          string
	DurationMS    int64
	PromptDigest  string
}

// Repository provides atomic claim/fence operations. Complete must append a
// revision and deletion obligation in one transaction, and reject stale tokens.
type Repository interface {
	Claim(context.Context, identity.ID, identity.ID, int) (Job, error)
	StagedResult(context.Context, Job) (string, string, error)
	StageResult(context.Context, Job, string, string) error
	Complete(context.Context, Job, string, string, bool) error
	Fail(context.Context, Job, string, bool) error
}

// AudioStore opens private audio by opaque object key.
type AudioStore interface {
	Open(context.Context, string) (io.ReadSeekCloser, error)
}

// AttemptLedger persists a visible attempt before transport and closes it with
// reported or unknown cost metadata.
type AttemptLedger interface {
	// Start durably records an attempt before provider transport begins.
	Start(context.Context, Job) (string, error)
	// Finish records provider outcome and the source audio duration for the attempt.
	Finish(context.Context, string, llm.StructuredResult, string, int64) error
}

// SilenceDetector is implemented by the audio probe adapter.
type SilenceDetector interface {
	IsSilent(context.Context, io.Reader, string, int64) (bool, error)
}

// Dependencies keep storage, call accounting, and persistence independently testable.
type Dependencies struct {
	Repository Repository
	Audio      AudioStore
	Gateway    llm.TranscriptionGateway
	Ledger     AttemptLedger
	Silence    SilenceDetector
}

// Setup claims and processes one durable transcription job.
func Setup(ctx context.Context, d Dependencies, jobID, tenantID identity.ID, generation int) error {
	if d.Repository == nil || d.Audio == nil || d.Gateway == nil || d.Ledger == nil ||
		jobID == (identity.ID{}) || tenantID == (identity.ID{}) || generation <= 0 {
		return errors.New("invalid transcription worker dependencies")
	}
	var job Job
	var err error
	requestedLease := requiredLeaseFor(d)
	// Captured before the claim so the local deadline can only be earlier than
	// the database lease, without comparing the worker clock to lease_deadline.
	claimStartedAt := time.Now()
	if repo, ok := d.Repository.(interface {
		ClaimWithLease(context.Context, identity.ID, identity.ID, int, time.Duration) (Job, error)
	}); ok {
		job, err = repo.ClaimWithLease(ctx, jobID, tenantID, generation, requestedLease)
	} else {
		job, err = d.Repository.Claim(ctx, jobID, tenantID, generation)
	}
	if err != nil {
		return err
	}
	if job.LeaseToken == (identity.ID{}) || job.JobID != jobID || job.TenantID != tenantID ||
		job.Generation != generation || job.ObjectKey == "" {
		return errors.New("invalid transcription lease")
	}
	lease := job.LeaseDuration
	if lease <= 0 {
		lease = requestedLease
	}
	attemptCtx, cancel := context.WithDeadline(ctx, claimStartedAt.Add(lease-leaseSafetyMargin))
	defer cancel()

	// A result already paid for and staged by an earlier attempt is completed
	// before any audio I/O, so storage or detector failures cannot discard it.
	if recovered, err := completeStagedResult(attemptCtx, d.Repository, job); recovered || err != nil {
		return err
	}
	if err := attemptCtx.Err(); err != nil {
		return failJob(attemptCtx, d.Repository, job, "TRANSCRIPTION_FAILED", false)
	}
	audio, err := d.Audio.Open(attemptCtx, job.ObjectKey)
	if err != nil {
		return failJob(attemptCtx, d.Repository, job, "AUDIO_UNAVAILABLE", false)
	}
	defer audio.Close()

	if d.Silence != nil {
		silent, probeErr := d.Silence.IsSilent(attemptCtx, audio, job.MIME, job.DurationMS)
		if probeErr != nil {
			code := "AUDIO_INVALID"
			if errors.Is(probeErr, ErrSilenceDetectorUnavailable) {
				code = "SILENCE_DETECTOR_UNAVAILABLE"
			} else if errors.Is(probeErr, context.DeadlineExceeded) || errors.Is(probeErr, ErrSilenceDetectorTimeout) {
				code = "SILENCE_DETECTOR_TIMEOUT"
			}
			return failJob(attemptCtx, d.Repository, job, code, false)
		}
		if silent {
			return completeJob(attemptCtx, d.Repository, job, "", "", true)
		}
		if _, err := audio.Seek(0, io.SeekStart); err != nil {
			return errors.New("audio store must return a seekable reader when silence detection is enabled")
		}
	}

	callID, err := d.Ledger.Start(attemptCtx, job)
	if err != nil {
		// Never call the provider if the pre-transport attempt record failed.
		return failJob(attemptCtx, d.Repository, job, "USAGE_UNAVAILABLE", false)
	}
	result, callErr := d.Gateway.Transcribe(attemptCtx, llm.TranscriptionRequest{
		Profile: job.Profile, Audio: audio, MIME: job.MIME,
		AudioDuration: time.Duration(job.DurationMS) * time.Millisecond,
		PromptDigest:  job.PromptDigest,
	})
	// The provider call is over (and possibly paid). Every remaining write
	// shares one detached budget: it must not inherit the attempt deadline nor
	// the consumer's shutdown, and it must fit inside leaseSafetyMargin so the
	// fence is still valid when a result arrives at the attempt deadline.
	persistCtx, cancelPersist := persistenceContext(attemptCtx)
	defer cancelPersist()
	ledger, repository := d.Ledger, d.Repository
	outcome := "SUCCEEDED"
	if callErr != nil {
		code := llm.CodeOf(callErr)
		if result.Usage.TransportDelivered && (code == string(llm.CodeTimeout) || code == string(llm.CodeCancelled) || code == string(llm.CodeTransport)) {
			outcome = "UNCERTAIN"
		} else {
			outcome = "FAILED"
		}
	}
	if callErr == nil && !result.NoSpeech {
		if len([]rune(result.Transcript)) > 12000 {
			if err := ledger.Finish(persistCtx, callID, result.Usage, "FAILED", job.DurationMS); err != nil {
				_ = ledger.Finish(persistCtx, callID, result.Usage, "UNCERTAIN", job.DurationMS)
			}
			return repository.Fail(persistCtx, job, "TRANSCRIPT_TOO_LONG", false)
		}
		if err := repository.StageResult(persistCtx, job, callID, result.Transcript); err != nil {
			_ = ledger.Finish(persistCtx, callID, result.Usage, "SUCCEEDED", job.DurationMS)
			return err
		}
	}
	if callErr != nil {
		if finishErr := ledger.Finish(persistCtx, callID, result.Usage, outcome, job.DurationMS); finishErr != nil {
			return repository.Fail(persistCtx, job, "USAGE_UNCERTAIN", true)
		}
		code := "TRANSCRIPTION_FAILED"
		uncertain := false
		llmCode := llm.CodeOf(callErr)
		if result.Usage.TransportDelivered && (llmCode == string(llm.CodeTimeout) || llmCode == string(llm.CodeCancelled) || llmCode == string(llm.CodeTransport)) {
			code, uncertain = "TRANSCRIPTION_UNCERTAIN", true
		} else if llmCode == string(llm.CodeTransport) || llmCode == string(llm.CodeTimeout) || llmCode == string(llm.CodeCancelled) {
			code = "TRANSCRIPTION_FAILED"
		} else if (llmCode == string(llm.CodeRateLimit) || llmCode == string(llm.CodeProviderHTTP)) && (result.Usage.HTTPStatus == 429 || result.Usage.HTTPStatus >= 500) {
			code, uncertain = "TRANSCRIPTION_FAILED", false
		} else if llmCode == string(llm.CodeAuthentication) || llmCode == string(llm.CodeInvalidInput) || llmCode == string(llm.CodeMalformedResponse) || llmCode == string(llm.CodeRateLimit) || llmCode == string(llm.CodeProviderHTTP) {
			code = "TRANSCRIPTION_REJECTED"
		}
		return repository.Fail(persistCtx, job, code, uncertain)
	}
	if finishErr := ledger.Finish(persistCtx, callID, result.Usage, outcome, job.DurationMS); finishErr != nil {
		// The result is durable before accounting; a second finish records uncertain cost when possible.
		_ = ledger.Finish(persistCtx, callID, result.Usage, "UNCERTAIN", job.DurationMS)
	}
	return repository.Complete(persistCtx, job, callID, result.Transcript, result.NoSpeech)
}

func requiredLeaseFor(deps Dependencies) time.Duration {
	detectorTimeout := DefaultSilenceDetectorTimeout
	if detector, ok := deps.Silence.(interface{ ConfiguredTimeout() time.Duration }); ok {
		detectorTimeout = detector.ConfiguredTimeout()
	}
	gatewayTimeout := llm.DefaultTranscriptionTimeout
	if gateway, ok := deps.Gateway.(interface{ ConfiguredTimeout() time.Duration }); ok {
		gatewayTimeout = gateway.ConfiguredTimeout()
	}
	return requiredLeaseDuration(detectorTimeout, gatewayTimeout)
}

func persistenceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.WithoutCancel(ctx)
	return context.WithTimeout(base, persistenceTimeout)
}

// completeStagedResult completes a previously staged model result, if any.
func completeStagedResult(ctx context.Context, repository Repository, job Job) (bool, error) {
	persistCtx, cancel := persistenceContext(ctx)
	defer cancel()
	callID, transcript, err := repository.StagedResult(persistCtx, job)
	if err != nil || callID == "" {
		return false, err
	}
	return true, repository.Complete(persistCtx, job, callID, transcript, false)
}

func completeJob(ctx context.Context, repository Repository, job Job, callID, transcript string, noSpeech bool) error {
	persistCtx, cancel := persistenceContext(ctx)
	defer cancel()
	return repository.Complete(persistCtx, job, callID, transcript, noSpeech)
}

func failJob(ctx context.Context, repository Repository, job Job, code string, uncertain bool) error {
	persistCtx, cancel := persistenceContext(ctx)
	defer cancel()
	return repository.Fail(persistCtx, job, code, uncertain)
}
