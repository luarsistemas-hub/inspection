//go:build integration

package video

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func requireRealFFmpeg(t *testing.T) {
	t.Helper()
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatalf("real FFmpeg integration requires %s: %v", name, err)
		}
	}
}

func makeRealClip(t *testing.T, name string, args ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "ffmpeg", append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-threads", "1"}, append(args, path)...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate %s: %v: %s", name, err, output)
	}
	return path
}

func TestRealSupportedCodecsAndPresentationTimesIT594(t *testing.T) {
	requireRealFFmpeg(t)
	for _, fixture := range []struct {
		name, codec string
		args        []string
	}{
		{"h264.mp4", "h264", []string{"-f", "lavfi", "-i", "testsrc2=size=96x64:rate=5:duration=1", "-c:v", "libx264", "-pix_fmt", "yuv420p"}},
		{"hevc.mov", "hevc", []string{"-f", "lavfi", "-i", "testsrc2=size=96x64:rate=5:duration=1", "-c:v", "libx265", "-x265-params", "pools=1:frame-threads=1:log-level=error", "-pix_fmt", "yuv420p"}},
		{"vp8.webm", "vp8", []string{"-f", "lavfi", "-i", "testsrc2=size=96x64:rate=5:duration=1", "-c:v", "libvpx", "-b:v", "300k", "-pix_fmt", "yuv420p"}},
		{"vp9.webm", "vp9", []string{"-f", "lavfi", "-i", "testsrc2=size=96x64:rate=5:duration=1", "-c:v", "libvpx-vp9", "-b:v", "300k", "-pix_fmt", "yuv420p"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			path := makeRealClip(t, fixture.name, fixture.args...)
			decoder := FFmpeg{Threads: 1}
			probe, err := decoder.Probe(context.Background(), path, Limits{})
			if err != nil {
				t.Fatal(err)
			}
			if probe.Codec != fixture.codec || probe.Width != 96 || probe.Height != 64 || probe.FrameRate != 5 || probe.Duration != time.Second || probe.VideoTracks != 1 {
				t.Fatalf("unexpected probe metadata: %+v", probe)
			}
			var timestamps []int64
			err = decoder.Decode(context.Background(), path, Limits{}, func(frame Frame) error {
				if frame.Width != 96 || frame.Height != 64 || frame.Pixels.Bounds().Dx() != 96 || frame.Pixels.Bounds().Dy() != 64 {
					t.Errorf("bad decoded dimensions at PTS %d", frame.PTSMicros)
				}
				timestamps = append(timestamps, frame.PTSMicros)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			want := []int64{0, 200000, 400000, 600000, 800000}
			if len(timestamps) != len(want) {
				t.Fatalf("PTS %v, want %v", timestamps, want)
			}
			for i := range want {
				if timestamps[i] != want[i] {
					t.Fatalf("PTS %v, want %v", timestamps, want)
				}
			}
		})
	}
}

func TestRealUnsupportedProfilesIT595(t *testing.T) {
	requireRealFFmpeg(t)
	corrupt := filepath.Join(t.TempDir(), "corrupt.mp4")
	if err := os.WriteFile(corrupt, []byte("not a video"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtures := []struct {
		name string
		path string
		args []string
		want error
	}{
		{name: "corrupt", path: corrupt, want: ErrSourceInvalid},
		{name: "two-tracks.mp4", args: []string{"-f", "lavfi", "-i", "testsrc2=size=96x64:rate=5:duration=1", "-f", "lavfi", "-i", "testsrc2=size=96x64:rate=5:duration=1", "-map", "0:v", "-map", "1:v", "-c:v", "libx264", "-pix_fmt", "yuv420p"}, want: ErrUnsupported},
		{name: "pq.mp4", args: []string{"-f", "lavfi", "-i", "testsrc2=size=96x64:rate=5:duration=1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-color_trc", "smpte2084"}, want: ErrUnsupported},
		{name: "hlg.mp4", args: []string{"-f", "lavfi", "-i", "testsrc2=size=96x64:rate=5:duration=1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-color_trc", "arib-std-b67"}, want: ErrUnsupported},
		{name: "61fps.mp4", args: []string{"-f", "lavfi", "-i", "testsrc2=size=96x64:rate=61:duration=1", "-c:v", "libx264", "-pix_fmt", "yuv420p"}, want: ErrUnsupported},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			path := fixture.path
			if path == "" {
				path = makeRealClip(t, fixture.name, fixture.args...)
			}
			if _, err := (FFmpeg{Threads: 1}).Probe(context.Background(), path, Limits{}); !errors.Is(err, fixture.want) {
				t.Fatalf("Probe(%s) = %v, want %v", fixture.name, err, fixture.want)
			}
		})
	}
}
