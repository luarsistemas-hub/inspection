package process_note

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/observability"
)

type callLedgerFake struct {
	start  llm.CallStart
	finish llm.CallFinish
	ctx    context.Context
}

func (f *callLedgerFake) Start(ctx context.Context, start llm.CallStart) error {
	f.start, f.ctx = start, ctx
	return nil
}
func (f *callLedgerFake) Finish(ctx context.Context, finish llm.CallFinish) error {
	f.finish, f.ctx = finish, ctx
	return nil
}

func TestAttemptLedgerAdapterPersistsTranscriptionMetadata(t *testing.T) {
	fake := &callLedgerFake{}
	adapter := &AttemptLedgerAdapter{Ledger: fake, Mode: "live"}
	job := Job{TenantID: identity.NewID(), NoteID: identity.NewID(), JobID: identity.NewID(), InspectionID: identity.NewID(), Generation: 2, Attempt: 1, DurationMS: 4200, PromptDigest: "prompt-digest", Profile: llm.TranscriptionProfiles["GEMINI_2_5_FLASH_LITE"]}
	callID, err := adapter.Start(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if fake.start.ComparisonMode != "TRANSCRIPTION" || fake.start.AudioDurationMS != job.DurationMS || fake.start.ModelAlias != job.Profile.ModelAlias {
		t.Fatalf("unexpected transcription start metadata: %+v", fake.start)
	}
	if got := observability.CorrelationFromContext(fake.ctx); got.TenantID != job.TenantID.String() {
		t.Fatalf("tenant correlation was not attached: %+v", got)
	}
	reportedCost := "0.000200"
	if err := adapter.Finish(context.Background(), callID, llm.StructuredResult{Provider: "gemini", Model: "flash-lite", ReportedCostDecimal: &reportedCost, CostSource: "header", TransportDelivered: true}, "UNCERTAIN", job.DurationMS); err != nil {
		t.Fatal(err)
	}
	if fake.finish.TechnicalOutcome != "UNCERTAIN" || !fake.finish.TransportDelivered || fake.finish.ReportedCostDecimal == nil || *fake.finish.ReportedCostDecimal != reportedCost || fake.finish.CostSource != "header" {
		t.Fatalf("uncertain provider outcome was not recorded: %+v", fake.finish)
	}
	if got := observability.CorrelationFromContext(fake.ctx); got.TenantID != job.TenantID.String() || got.CallID != callID {
		t.Fatalf("finish lost call correlation: %+v", got)
	}
	if fake.finish.AudioDurationMS == nil || *fake.finish.AudioDurationMS != job.DurationMS || fake.finish.AudioTokenUnit != "MILLISECONDS" {
		t.Fatalf("audio duration metadata missing: %+v", fake.finish)
	}
}

func TestFailureBackoffAndSafeCodes(t *testing.T) {
	if got := backoff(1); got <= 0 || backoff(2) <= got {
		t.Fatalf("retry backoff should increase: first=%s second=%s", got, backoff(2))
	}
	for _, code := range []string{"AUDIO_INVALID", "TRANSCRIPTION_FAILED", "USAGE_UNCERTAIN"} {
		if !validSafeFailureCode(code) {
			t.Errorf("valid safe failure code rejected: %s", code)
		}
	}
	for _, code := range []string{"provider timeout", "", strings.Repeat("A", 65)} {
		if validSafeFailureCode(code) {
			t.Errorf("unsafe failure code accepted: %q", code)
		}
	}
}

func TestSilenceDetectorFailsInternallyWhenFFmpegIsMissing(t *testing.T) {
	dir := t.TempDir()
	previous := os.Getenv("PATH")
	if err := os.Setenv("PATH", dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Setenv("PATH", previous) })
	_, err := (FFmpegSilenceDetector{Path: filepath.Join(dir, "missing-ffmpeg")}).IsSilent(context.Background(), strings.NewReader("audio"), "audio/webm", 1000)
	if !errors.Is(err, ErrSilenceDetectorUnavailable) {
		t.Fatalf("missing ffmpeg error=%v, want ErrSilenceDetectorUnavailable", err)
	}
}

func TestFFmpegSilenceDetectorClassifiesGeneratedClips(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is unavailable")
	}
	detector, err := NewFFmpegSilenceDetector(10*time.Second, "-50dB")
	if errors.Is(err, ErrSilenceDetectorUnavailable) {
		t.Skip("ffmpeg or ffprobe is unavailable")
	}
	if err != nil {
		t.Fatal(err)
	}
	detector.Path = ffmpeg
	for _, tc := range []struct {
		name       string
		inputs     []string
		filter     string
		wantSilent bool
	}{
		{name: "half-second speech in ten seconds", inputs: []string{"sine=frequency=440:duration=0.5", "anullsrc=r=48000:cl=mono:d=9.5"}, filter: "[0:a][1:a]concat=n=2:v=0:a=1[a]", wantSilent: false},
		{name: "one-second speech in sixty seconds", inputs: []string{"sine=frequency=440:duration=1", "anullsrc=r=48000:cl=mono:d=59"}, filter: "[0:a][1:a]concat=n=2:v=0:a=1[a]", wantSilent: false},
		{name: "pure silence", inputs: []string{"anullsrc=r=48000:cl=mono:d=3"}, wantSilent: true},
		{name: "low background noise", inputs: []string{"anoisesrc=color=white:amplitude=0.001:duration=3"}, wantSilent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"-nostdin", "-hide_banner"}
			for _, source := range tc.inputs {
				args = append(args, "-f", "lavfi", "-i", source)
			}
			if tc.filter != "" {
				args = append(args, "-filter_complex", tc.filter, "-map", "[a]")
			}
			args = append(args, "-c:a", "libopus", "-f", "webm", "pipe:1")
			cmd := exec.Command(ffmpeg, args...)
			data, err := cmd.Output()
			if err != nil {
				t.Fatalf("generate fixture: %v", err)
			}
			// Deliberately pass an unrelated client duration: the detector must probe the bytes.
			got, err := detector.IsSilent(context.Background(), strings.NewReader(string(data)), "audio/webm", 1)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.wantSilent {
				t.Fatalf("IsSilent=%v want %v", got, tc.wantSilent)
			}
		})
	}
}

func TestSilenceDetectorTimeoutAndThresholdValidation(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	detector := FFmpegSilenceDetector{Path: script, Timeout: time.Millisecond, Threshold: "-50dB"}
	_, err := detector.IsSilent(context.Background(), strings.NewReader("audio"), "audio/webm", 1000)
	if !errors.Is(err, ErrSilenceDetectorTimeout) {
		t.Fatalf("timeout error=%v", err)
	}
	if _, err := NewFFmpegSilenceDetector(time.Second, "loud"); err == nil {
		t.Fatal("invalid threshold accepted")
	}
}

func TestSilentFractionIncludesTrailingSilence(t *testing.T) {
	log := "silence_start: 1.000\nsilence_end: 3.000 | silence_duration: 2.000\n"
	if got := silentDuration(log, 3*time.Second); got < 2*time.Second {
		t.Fatalf("duration=%s, want trailing silence included", got)
	}
}

func TestDefaultLeaseExceedsFFmpegAndGatewayTimeouts(t *testing.T) {
	if leaseSafetyMargin < persistenceTimeout {
		t.Fatalf("lease margin=%s must cover post-deadline persistence=%s", leaseSafetyMargin, persistenceTimeout)
	}
	want := DefaultSilenceDetectorTimeout + llm.DefaultTranscriptionTimeout + audioDownloadAllowance + persistenceTimeout + leaseSafetyMargin
	if lease := defaultLeaseDuration(); lease != want {
		t.Fatalf("default lease=%s, want sum of every attempt stage %s", lease, want)
	}
	// The exported formula is the single source the worker validates against.
	if got := RequiredLease(DefaultSilenceDetectorTimeout, 45*time.Second); got != 73*time.Second || got != defaultLeaseDuration() {
		t.Fatalf("RequiredLease(default detector, 45s)=%s, want 73s and the repository default", got)
	}
	// 20s detector + 90s gateway + 10s download + 3s persistence + 5s margin.
	if got := requiredLeaseDuration(20*time.Second, 90*time.Second); got != 128*time.Second {
		t.Fatalf("configured timeouts require 128s lease, got %s", got)
	}
	deps := Dependencies{Silence: FFmpegSilenceDetector{Timeout: 20 * time.Second}, Gateway: llm.HTTPTranscriptionGateway{Client: &http.Client{Timeout: 90 * time.Second}}}
	if got := requiredLeaseFor(deps); got != 128*time.Second {
		t.Fatalf("lease did not reflect configured detector and client timeouts: %s", got)
	}
	// The attempt deadline leaves the full post-transport persistence window inside the lease.
	if attempt := requiredLeaseFor(deps) - leaseSafetyMargin; attempt != 20*time.Second+90*time.Second+audioDownloadAllowance+persistenceTimeout {
		t.Fatalf("attempt budget=%s does not cover detector, gateway, download and persistence", attempt)
	}
}

func TestSilenceDetectorConstructorRequiresFFprobe(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := NewFFmpegSilenceDetector(time.Second, "-50dB"); !errors.Is(err, ErrSilenceDetectorUnavailable) {
		t.Fatalf("missing ffprobe error=%v, want ErrSilenceDetectorUnavailable", err)
	}
}

func TestSilenceDetectorClassifiesFFprobeInfrastructureFailures(t *testing.T) {
	dir := t.TempDir()
	ffmpeg := filepath.Join(dir, "ffmpeg")
	slowProbe := filepath.Join(dir, "ffprobe")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(slowProbe, []byte("#!/bin/sh\nexec sleep 2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	missing := FFmpegSilenceDetector{Path: ffmpeg, ProbePath: filepath.Join(dir, "missing-ffprobe"), Timeout: 10 * time.Second, Threshold: "-50dB"}
	if _, err := missing.IsSilent(context.Background(), strings.NewReader("audio"), "audio/webm", 1000); !errors.Is(err, ErrSilenceDetectorUnavailable) {
		t.Fatalf("missing ffprobe error=%v, want ErrSilenceDetectorUnavailable", err)
	}
	slow := FFmpegSilenceDetector{Path: ffmpeg, ProbePath: slowProbe, Timeout: 300 * time.Millisecond, Threshold: "-50dB"}
	if _, err := slow.IsSilent(context.Background(), strings.NewReader("audio"), "audio/webm", 1000); !errors.Is(err, ErrSilenceDetectorTimeout) {
		t.Fatalf("slow ffprobe error=%v, want ErrSilenceDetectorTimeout", err)
	}
}

func TestSetupMapsFFprobeFailuresToDetectorCodes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{err: ErrSilenceDetectorUnavailable, want: "SILENCE_DETECTOR_UNAVAILABLE"},
		{err: ErrSilenceDetectorTimeout, want: "SILENCE_DETECTOR_TIMEOUT"},
	} {
		repo := &repositoryFake{job: Job{LeaseToken: identity.NewID(), ObjectKey: "audio", MIME: "audio/webm", DurationMS: 1000}}
		if err := Setup(context.Background(), Dependencies{Repository: repo, Audio: audioFake{}, Gateway: &gatewayFake{}, Ledger: &ledgerFake{}, Silence: &countingSilenceFake{err: tc.err}}, identity.NewID(), identity.NewID(), 1); err != nil {
			t.Fatal(err)
		}
		if repo.failCode != tc.want {
			t.Fatalf("detector error %v mapped to %q, want %q", tc.err, repo.failCode, tc.want)
		}
	}
}
