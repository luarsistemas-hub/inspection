package process_note

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"inspection/services/inspection/internal/platform/objectstore"
)

// FFmpegSilenceDetector detects whether any speech-band activity exists in a bounded clip.
type FFmpegSilenceDetector struct {
	Timeout   time.Duration
	Threshold string
	Path      string
	// ProbePath is the ffprobe executable resolved by NewFFmpegSilenceDetector.
	ProbePath string
}

// ConfiguredTimeout returns the effective bound for audio probing.
func (d FFmpegSilenceDetector) ConfiguredTimeout() time.Duration {
	if d.Timeout > 0 {
		return d.Timeout
	}
	return DefaultSilenceDetectorTimeout
}

// ErrSilenceDetectorUnavailable signals that the configured FFmpeg or ffprobe executable is missing.
var ErrSilenceDetectorUnavailable = errors.New("ffmpeg silence detector unavailable")

// ErrSilenceDetectorTimeout identifies a bounded FFmpeg timeout.
var ErrSilenceDetectorTimeout = errors.New("ffmpeg silence detector timeout")

var resolvedFFmpegPath, resolvedFFmpegError = exec.LookPath("ffmpeg")

// NewFFmpegSilenceDetector resolves FFmpeg and ffprobe once and validates detector configuration.
func NewFFmpegSilenceDetector(timeout time.Duration, threshold string) (FFmpegSilenceDetector, error) {
	if resolvedFFmpegError != nil {
		return FFmpegSilenceDetector{}, fmt.Errorf("%w: %v", ErrSilenceDetectorUnavailable, resolvedFFmpegError)
	}
	probePath, err := exec.LookPath("ffprobe")
	if err != nil {
		return FFmpegSilenceDetector{}, fmt.Errorf("%w: %v", ErrSilenceDetectorUnavailable, err)
	}
	if threshold == "" {
		threshold = "-50dB"
	}
	if _, err := parseSilenceThreshold(threshold); err != nil {
		return FFmpegSilenceDetector{}, err
	}
	if timeout <= 0 {
		timeout = DefaultSilenceDetectorTimeout
	}
	return FFmpegSilenceDetector{Path: resolvedFFmpegPath, ProbePath: probePath, Timeout: timeout, Threshold: threshold}, nil
}

var silenceThresholdPattern = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?dB$`)

func parseSilenceThreshold(value string) (float64, error) {
	if !silenceThresholdPattern.MatchString(value) {
		return 0, errors.New("invalid silence threshold")
	}
	n, err := strconv.ParseFloat(strings.TrimSuffix(value, "dB"), 64)
	if err != nil || n > 0 || n < -120 {
		return 0, errors.New("invalid silence threshold")
	}
	return n, nil
}

// IsSilent returns true when at least 90% of the clip is below the configured silence threshold.
func (d FFmpegSilenceDetector) IsSilent(ctx context.Context, reader io.Reader, mime string, _ int64) (bool, error) {
	if reader == nil {
		return false, errors.New("missing audio")
	}
	format := map[string]string{"audio/webm": "webm", "audio/ogg": "ogg", "audio/mp4": "mp4", "audio/mpeg": "mp3"}[strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0]))]
	if format == "" {
		return false, objectstore.ErrInvalid
	}
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = DefaultSilenceDetectorTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	file, err := os.CreateTemp("", "inspection-transcription-*.audio")
	if err != nil {
		return false, err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = io.Copy(file, io.LimitReader(reader, 5*1024*1024+1)); err != nil {
		_ = file.Close()
		return false, err
	}
	if info, statErr := file.Stat(); statErr != nil {
		_ = file.Close()
		return false, statErr
	} else if info.Size() <= 0 || info.Size() > 5*1024*1024 {
		_ = file.Close()
		return false, objectstore.ErrInvalid
	}
	if err = file.Close(); err != nil {
		return false, err
	}
	threshold := d.Threshold
	if threshold == "" {
		threshold = "-50dB"
	}
	if _, err := parseSilenceThreshold(threshold); err != nil {
		return false, err
	}
	path := d.Path
	if path == "" {
		if resolvedFFmpegError != nil {
			return false, fmt.Errorf("%w: %v", ErrSilenceDetectorUnavailable, resolvedFFmpegError)
		}
		path = resolvedFFmpegPath
	}
	if _, err := os.Stat(path); err != nil {
		return false, fmt.Errorf("%w: %v", ErrSilenceDetectorUnavailable, err)
	}
	cmd := exec.CommandContext(ctx, path, "-nostdin", "-hide_banner", "-protocol_whitelist", "file", "-f", format, "-i", name, "-af", "silencedetect=noise="+threshold+":d=0.25", "-f", "null", "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return false, fmt.Errorf("%w: %v", ErrSilenceDetectorUnavailable, err)
		}
		if ctx.Err() != nil {
			return false, fmt.Errorf("%w: %v", ErrSilenceDetectorTimeout, ctx.Err())
		}
		return false, err
	}
	actualDuration, err := probeAudioDuration(ctx, d.ProbePath, format, name)
	if err != nil {
		return false, err
	}
	return silentDuration(stderr.String(), actualDuration) > actualDuration-minimumNonsilentDuration, nil
}

// probeAudioDuration measures the decoded clip length. Missing ffprobe and
// context expiry are infrastructure failures, never invalid audio.
func probeAudioDuration(ctx context.Context, ffprobePath, format, name string) (time.Duration, error) {
	if ffprobePath == "" {
		resolved, err := exec.LookPath("ffprobe")
		if err != nil {
			return 0, fmt.Errorf("%w: %v", ErrSilenceDetectorUnavailable, err)
		}
		ffprobePath = resolved
	}
	if _, err := os.Stat(ffprobePath); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrSilenceDetectorUnavailable, err)
	}
	cmd := exec.CommandContext(ctx, ffprobePath, "-v", "error", "-f", format, "-select_streams", "a:0", "-show_packets", "-show_entries", "packet=pts_time,duration_time", "-of", "csv=p=0", name)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return 0, fmt.Errorf("%w: %v", ErrSilenceDetectorUnavailable, err)
		}
		if ctx.Err() != nil {
			return 0, fmt.Errorf("%w: %v", ErrSilenceDetectorTimeout, ctx.Err())
		}
		return 0, fmt.Errorf("ffprobe audio duration: %w", err)
	}
	var endSeconds float64
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) < 2 {
			continue
		}
		pts, ptsErr := strconv.ParseFloat(strings.TrimSpace(fields[0]), 64)
		packetDuration, durationErr := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
		if ptsErr == nil && durationErr == nil && pts+packetDuration > endSeconds {
			endSeconds = pts + packetDuration
		}
	}
	if endSeconds <= 0 {
		return 0, errors.New("invalid audio duration")
	}
	return time.Duration(endSeconds * float64(time.Second)), nil
}

var silenceStartPattern = regexp.MustCompile(`silence_start:\s*([0-9]+(?:\.[0-9]+)?)`)
var silenceDurationPattern = regexp.MustCompile(`silence_duration:\s*([0-9]+(?:\.[0-9]+)?)`)

// minimumNonsilentDuration preserves any detected speech longer than a short quarter-second burst.
const minimumNonsilentDuration = 250 * time.Millisecond

// silentDuration returns the total detected silence, including an unfinished tail.
func silentDuration(log string, duration time.Duration) time.Duration {
	if duration <= 0 {
		return 0
	}
	var silent time.Duration
	for _, match := range silenceDurationPattern.FindAllStringSubmatch(log, -1) {
		seconds, _ := strconv.ParseFloat(match[1], 64)
		silent += time.Duration(seconds * float64(time.Second))
	}
	starts := silenceStartPattern.FindAllStringSubmatch(log, -1)
	if len(starts) > len(silenceDurationPattern.FindAllStringSubmatch(log, -1)) {
		seconds, _ := strconv.ParseFloat(starts[len(starts)-1][1], 64)
		tail := duration - time.Duration(seconds*float64(time.Second))
		if tail > 0 {
			silent += tail
		}
	}
	if silent > duration {
		silent = duration
	}
	return silent
}
