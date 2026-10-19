// Package video provides a bounded, shell-free FFmpeg adapter for local video files.
package video

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ErrUnsafePath       = errors.New("video: unsafe local path")
	ErrUnsupported      = errors.New("video: unsupported source profile")
	ErrDurationExceeded = errors.New("video: duration exceeds admitted limit")
	ErrSourceInvalid    = errors.New("video: invalid source")
	pathPattern         = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)
)

// Limits bound decoder admission. Zero fields use the package defaults.
type Limits struct {
	MaxBytes            int64
	MaxDuration         time.Duration
	MaxWidth, MaxHeight int
	MaxFPS              float64
	MaxFrameBytes       int64
}

func (l Limits) normalized() Limits {
	if l.MaxBytes <= 0 {
		l.MaxBytes = 1 << 30
	}
	if l.MaxDuration <= 0 {
		l.MaxDuration = 2 * time.Hour
	}
	if l.MaxWidth <= 0 {
		l.MaxWidth = 3840
	}
	if l.MaxHeight <= 0 {
		l.MaxHeight = 3840
	}
	if l.MaxFPS <= 0 {
		l.MaxFPS = 60
	}
	if l.MaxFrameBytes <= 0 {
		l.MaxFrameBytes = 64 << 20
	}
	return l
}

// Probe describes facts measured from the media stream, not upload metadata.
type Probe struct {
	Container, Codec, PixelFormat, ColorTransfer string
	Duration                                     time.Duration
	Width, Height, Rotation                      int
	FrameRate                                    float64
	AudioPresent                                 bool
	VideoTracks                                  int
}

// Frame is one decoded frame in presentation order.
type Frame struct {
	PTSMicros     int64
	Width, Height int
	Pixels        image.Image
}

// Decoder exposes only operations over validated local file paths.
type Decoder interface {
	Probe(context.Context, string, Limits) (Probe, error)
	Decode(context.Context, string, Limits, func(Frame) error) error
	Playback(context.Context, string, string, Limits) error
}

// FFmpeg invokes ffprobe and ffmpeg directly without a shell.
type FFmpeg struct {
	FFmpegPath  string
	FFprobePath string
	Threads     int
}

func (f FFmpeg) binaries() (string, string) {
	ffmpeg, ffprobe := f.FFmpegPath, f.FFprobePath
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	if ffprobe == "" {
		ffprobe = "ffprobe"
	}
	return ffmpeg, ffprobe
}

func (f FFmpeg) threads() string {
	if f.Threads <= 0 {
		return "1"
	}
	return strconv.Itoa(f.Threads)
}

func validatePath(path string) (os.FileInfo, error) {
	if path == "" || strings.Contains(path, "://") || !filepath.IsAbs(path) || filepath.Clean(path) != path || !pathPattern.MatchString(path) {
		return nil, ErrUnsafePath
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: local file unavailable", ErrSourceInvalid)
	}
	if !info.Mode().IsRegular() {
		return nil, ErrUnsafePath
	}
	return info, nil
}

type probeResult struct {
	Streams []struct {
		CodecType     string            `json:"codec_type"`
		CodecName     string            `json:"codec_name"`
		Width         int               `json:"width"`
		Height        int               `json:"height"`
		PixelFormat   string            `json:"pix_fmt"`
		AvgFrameRate  string            `json:"avg_frame_rate"`
		RealFrameRate string            `json:"r_frame_rate"`
		ColorTransfer string            `json:"color_transfer"`
		Duration      string            `json:"duration"`
		Tags          map[string]string `json:"tags"`
		SideData      []struct {
			Rotation int `json:"rotation"`
		} `json:"side_data_list"`
	} `json:"streams"`
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
	} `json:"format"`
}

// Probe validates the container and every visual stream before returning metadata.
func (f FFmpeg) Probe(ctx context.Context, path string, limits Limits) (Probe, error) {
	info, err := validatePath(path)
	if err != nil {
		return Probe{}, err
	}
	l := limits.normalized()
	if info.Size() > l.MaxBytes {
		return Probe{}, ErrUnsupported
	}
	_, ffprobe := f.binaries()
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-protocol_whitelist", "file", "-show_streams", "-show_format", "-of", "json", path)
	var stdout, stderr cappedBuffer
	stdout.limit, stderr.limit = 1<<20, 64<<10
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Probe{}, ctx.Err()
		}
		return Probe{}, fmt.Errorf("%w: probe failed", ErrSourceInvalid)
	}
	var raw probeResult
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		return Probe{}, ErrSourceInvalid
	}
	visual := make([]struct {
		CodecType     string            `json:"codec_type"`
		CodecName     string            `json:"codec_name"`
		Width         int               `json:"width"`
		Height        int               `json:"height"`
		PixelFormat   string            `json:"pix_fmt"`
		AvgFrameRate  string            `json:"avg_frame_rate"`
		RealFrameRate string            `json:"r_frame_rate"`
		ColorTransfer string            `json:"color_transfer"`
		Duration      string            `json:"duration"`
		Tags          map[string]string `json:"tags"`
		SideData      []struct {
			Rotation int `json:"rotation"`
		} `json:"side_data_list"`
	}, 0)
	audio := false
	for _, s := range raw.Streams {
		if s.CodecType == "video" {
			visual = append(visual, s)
		}
		if s.CodecType == "audio" {
			audio = true
		}
	}
	if len(visual) != 1 {
		return Probe{}, ErrUnsupported
	}
	s := visual[0]
	container := strings.ToLower(raw.Format.FormatName)
	format := strings.Split(container, ",")[0]
	codecOK := (strings.Contains(container, "mov") || strings.Contains(container, "mp4")) && (s.CodecName == "h264" || s.CodecName == "hevc") || strings.Contains(container, "webm") && (s.CodecName == "vp8" || s.CodecName == "vp9")
	if !codecOK || s.Width <= 0 || s.Height <= 0 || s.Width > l.MaxWidth || s.Height > l.MaxHeight || !strings.Contains(s.PixelFormat, "420") {
		return Probe{}, ErrUnsupported
	}
	transfer := strings.ToLower(s.ColorTransfer)
	if transfer == "smpte2084" || transfer == "arib-std-b67" {
		return Probe{}, ErrUnsupported
	}
	durationValue := raw.Format.Duration
	if s.Duration != "" && s.Duration != "N/A" {
		durationValue = s.Duration
	}
	seconds, err := strconv.ParseFloat(durationValue, 64)
	if err != nil || seconds <= 0 {
		return Probe{}, ErrUnsupported
	}
	if seconds > l.MaxDuration.Seconds() {
		return Probe{}, ErrDurationExceeded
	}
	rate, err := parseRate(s.AvgFrameRate)
	if err != nil || rate <= 0 {
		rate, err = parseRate(s.RealFrameRate)
	}
	if err != nil || rate <= 0 || rate > l.MaxFPS {
		return Probe{}, ErrUnsupported
	}
	rotation := 0
	for _, sd := range s.SideData {
		if sd.Rotation != 0 {
			rotation = ((sd.Rotation % 360) + 360) % 360
		}
	}
	if rotation == 0 && s.Tags != nil {
		rotation, _ = strconv.Atoi(s.Tags["rotate"])
		rotation = ((rotation % 360) + 360) % 360
	}
	return Probe{Container: format, Codec: s.CodecName, PixelFormat: s.PixelFormat, ColorTransfer: transfer, Duration: time.Duration(seconds * float64(time.Second)), Width: s.Width, Height: s.Height, Rotation: rotation, FrameRate: rate, AudioPresent: audio, VideoTracks: len(visual)}, nil
}

// Decode streams one RGB frame at a time. PTS values come from ffprobe and are
// paired with FFmpeg's presentation-order output; no nominal-FPS timestamps are synthesized.
func (f FFmpeg) Decode(ctx context.Context, path string, limits Limits, callback func(Frame) error) error {
	if callback == nil {
		return errors.New("video: frame callback is required")
	}
	probe, err := f.Probe(ctx, path, limits)
	if err != nil {
		return err
	}
	pts, err := f.frameTimestamps(ctx, path, limits)
	if err != nil {
		return err
	}
	frameBytes := int64(probe.Width) * int64(probe.Height) * 3
	if frameBytes <= 0 || frameBytes > limits.normalized().MaxFrameBytes {
		return ErrUnsupported
	}
	ffmpeg, _ := f.binaries()
	cmd := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-v", "error", "-xerror", "-protocol_whitelist", "file,pipe", "-threads", f.threads(), "-noautorotate", "-i", path, "-map", "0:v:0", "-fps_mode", "passthrough", "-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr cappedBuffer
	stderr.limit = 64 << 10
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("video: start decoder: %w", err)
	}
	reader := bufio.NewReaderSize(stdout, 64<<10)
	buffer := make([]byte, int(frameBytes))
	index := 0
	var callbackErr error
	for {
		if ctx.Err() != nil {
			callbackErr = ctx.Err()
			break
		}
		_, readErr := io.ReadFull(reader, buffer)
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			if readErr == io.ErrUnexpectedEOF {
				callbackErr = ErrSourceInvalid
			}
			break
		}
		if readErr != nil {
			callbackErr = readErr
			break
		}
		if index >= len(pts) {
			callbackErr = ErrSourceInvalid
			break
		}
		frame := Frame{PTSMicros: pts[index], Width: probe.Width, Height: probe.Height, Pixels: rgbImage(probe.Width, probe.Height, buffer)}
		if err := callback(frame); err != nil {
			callbackErr = err
			break
		}
		index++
	}
	if callbackErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if callbackErr != nil {
		return callbackErr
	}
	if waitErr != nil {
		return fmt.Errorf("%w: decode failed", ErrSourceInvalid)
	}
	if index != len(pts) {
		return ErrSourceInvalid
	}
	return nil
}

func (f FFmpeg) frameTimestamps(ctx context.Context, path string, limits Limits) ([]int64, error) {
	_, ffprobe := f.binaries()
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-protocol_whitelist", "file", "-select_streams", "v:0", "-show_entries", "frame=best_effort_timestamp_time", "-of", "csv=p=0", path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr cappedBuffer
	stderr.limit = 64 << 10
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	result := make([]int64, 0, 1024)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 1<<16)
	var parseErr error
	for scanner.Scan() {
		line := strings.TrimSpace(strings.Split(scanner.Text(), ",")[0])
		if line == "" {
			continue
		}
		if line == "N/A" {
			parseErr = ErrSourceInvalid
			break
		}
		seconds, err := strconv.ParseFloat(line, 64)
		if err != nil || seconds < 0 || math.IsNaN(seconds) {
			parseErr = ErrSourceInvalid
			break
		}
		pts := int64(math.Round(seconds * 1_000_000))
		if len(result) > 0 && pts < result[len(result)-1] {
			parseErr = ErrSourceInvalid
			break
		}
		l := limits.normalized()
		if len(result) >= int(l.MaxDuration.Seconds()*l.MaxFPS) {
			parseErr = ErrResourceLimit
			break
		}
		result = append(result, pts)
	}
	if err := scanner.Err(); err != nil && parseErr == nil {
		parseErr = err
	}
	if parseErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if parseErr != nil {
		return nil, parseErr
	}
	if waitErr != nil || len(result) == 0 {
		return nil, ErrSourceInvalid
	}
	return result, nil
}

// Playback remuxes compatible input, or transcodes to browser-friendly H.264 SDR.
func (f FFmpeg) Playback(ctx context.Context, input, output string, limits Limits) error {
	if _, err := f.Probe(ctx, input, limits); err != nil {
		return err
	}
	if _, err := validateOutputPath(output); err != nil {
		return err
	}
	ffmpeg, _ := f.binaries()
	cmd := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-v", "error", "-xerror", "-protocol_whitelist", "file", "-threads", f.threads(), "-i", input, "-map", "0:v:0", "-map", "0:a?", "-threads", f.threads(), "-c:v", "libx264", "-pix_fmt", "yuv420p", "-movflags", "+faststart", "-c:a", "aac", "-f", "mp4", output)
	var stderr cappedBuffer
	stderr.limit = 64 << 10
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("video: playback transcode failed")
	}
	return nil
}

func validateOutputPath(path string) (os.FileInfo, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || !pathPattern.MatchString(path) {
		return nil, ErrUnsafePath
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil || !parent.IsDir() {
		return nil, ErrUnsafePath
	}
	if _, err := os.Lstat(path); err == nil {
		return nil, ErrUnsafePath
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, ErrUnsafePath
	}
	return nil, nil
}

func parseRate(value string) (float64, error) {
	parts := strings.Split(value, "/")
	if len(parts) == 1 {
		rate, err := strconv.ParseFloat(parts[0], 64)
		if err != nil || rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
			return 0, ErrSourceInvalid
		}
		return rate, nil
	}
	if len(parts) != 2 {
		return 0, ErrSourceInvalid
	}
	numerator, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, err
	}
	denominator, err := strconv.ParseFloat(parts[1], 64)
	if err != nil || denominator == 0 {
		return 0, ErrSourceInvalid
	}
	rate := numerator / denominator
	if rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return 0, ErrSourceInvalid
	}
	return rate, nil
}

func rgbImage(width, height int, raw []byte) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := (y*width + x) * 3
			img.Set(x, y, color.RGBA{R: raw[i], G: raw[i+1], B: raw[i+2], A: 255})
		}
	}
	return img
}

type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.Len()
	if remaining > 0 {
		n := len(p)
		if n > remaining {
			n = remaining
		}
		_, _ = b.Buffer.Write(p[:n])
	}
	return len(p), nil
}
