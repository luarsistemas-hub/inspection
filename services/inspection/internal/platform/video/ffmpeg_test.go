package video

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeMedia(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(path, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func probeScript(t *testing.T, payload string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffprobe")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s' '"+strings.ReplaceAll(payload, "'", "'\\''")+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func probePayload(codec, transfer string, streams int) string {
	video := map[string]any{"codec_type": "video", "codec_name": codec, "width": 1920, "height": 1080, "pix_fmt": "yuv420p", "avg_frame_rate": "30/1", "duration": "1.0", "color_transfer": transfer, "side_data_list": []any{map[string]any{"rotation": 90}}}
	values := []any{video}
	for i := 1; i < streams; i++ {
		values = append(values, map[string]any{"codec_type": "video", "codec_name": codec, "width": 1920, "height": 1080, "pix_fmt": "yuv420p", "avg_frame_rate": "30/1", "duration": "1.0"})
	}
	raw, _ := json.Marshal(map[string]any{"streams": values, "format": map[string]any{"format_name": "mov,mp4,m4a,3gp,3g2,mj2", "duration": "1.0"}})
	return string(raw)
}

func TestProbeMeasuresRotatedSDRMetadata(t *testing.T) {
	path := fakeMedia(t)
	decoder := FFmpeg{FFprobePath: probeScript(t, probePayload("h264", "bt709", 1))}
	probe, err := decoder.Probe(context.Background(), path, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if probe.Duration != time.Second || probe.Width != 1920 || probe.Height != 1080 || probe.Rotation != 90 || probe.VideoTracks != 1 {
		t.Fatalf("unexpected measured metadata: %+v", probe)
	}
}

func TestProbeDistinguishesFrozenDurationBoundaryIT109(t *testing.T) {
	for _, tc := range []struct {
		name, duration string
		wantError      error
	}{
		{name: "exact limit", duration: "900.000"},
		{name: "one millisecond over", duration: "900.001", wantError: ErrDurationExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := strings.ReplaceAll(probePayload("h264", "bt709", 1), "1.0", tc.duration)
			decoder := FFmpeg{FFprobePath: probeScript(t, payload)}
			probe, err := decoder.Probe(context.Background(), fakeMedia(t), Limits{MaxDuration: 900 * time.Second})
			if tc.wantError == nil && (err != nil || probe.Duration != 900*time.Second) {
				t.Fatalf("exact boundary: probe=%+v err=%v", probe, err)
			}
			if tc.wantError != nil && !errors.Is(err, tc.wantError) {
				t.Fatalf("expected %v, got %v", tc.wantError, err)
			}
		})
	}
}

func TestProbeRejectsHDRAndMultipleVisualTracks(t *testing.T) {
	for _, tc := range []struct {
		name, codec, transfer string
		streams               int
	}{{"hdr", "h264", "smpte2084", 1}, {"second-track", "h264", "bt709", 2}, {"codec", "mpeg4", "bt709", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			d := FFmpeg{FFprobePath: probeScript(t, probePayload(tc.codec, tc.transfer, tc.streams))}
			if _, err := d.Probe(context.Background(), fakeMedia(t), Limits{}); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("expected unsupported, got %v", err)
			}
		})
	}
}

func TestProbeRejectsURLAndShellMetacharactersBeforeExecution(t *testing.T) {
	called := filepath.Join(t.TempDir(), "called")
	probe := filepath.Join(t.TempDir(), "ffprobe")
	if err := os.WriteFile(probe, []byte("#!/bin/sh\ntouch "+called+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"https://example.invalid/clip.mp4", "/tmp/clip;touch-owned-file.mp4"} {
		if _, err := (FFmpeg{FFprobePath: probe}).Probe(context.Background(), path, Limits{}); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("%q: expected unsafe path, got %v", path, err)
		}
	}
	if _, err := os.Stat(called); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("probe unexpectedly ran: %v", err)
	}
}

func TestFrameRateParser(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  float64
		fail  bool
	}{{"30000/1001", 29.97002997002997, false}, {"24", 24, false}, {"0/0", 0, true}, {"NaN", 0, true}} {
		got, err := parseRate(tc.input)
		if tc.fail && err == nil {
			t.Fatalf("%s: expected error", tc.input)
		}
		if !tc.fail && (err != nil || fmt.Sprintf("%.6f", got) != fmt.Sprintf("%.6f", tc.want)) {
			t.Fatalf("%s: got %v, %v", tc.input, got, err)
		}
	}
}

func TestDecodeEmitsVFRPresentationTimestampsAndDimensionsUT082(t *testing.T) {
	path := fakeMedia(t)
	probePath := filepath.Join(t.TempDir(), "ffprobe")
	payload := probePayload("h264", "bt709", 1)
	payload = strings.ReplaceAll(payload, `"width":1920`, `"width":2`)
	payload = strings.ReplaceAll(payload, `"height":1080`, `"height":2`)
	probeScriptBody := "#!/bin/sh\ncase \"$*\" in *show_entries*) printf '0.000000\\n0.040000\\n0.105000\\n' ;; *) printf '%s' '" + payload + "' ;; esac\n"
	if err := os.WriteFile(probePath, []byte(probeScriptBody), 0o700); err != nil {
		t.Fatal(err)
	}
	ffmpegPath := filepath.Join(t.TempDir(), "ffmpeg")
	frame := `\001\002\003\004\005\006\007\010\011\012\013\014`
	decoderBody := "#!/bin/sh\nprintf '" + frame + frame + frame + "'\n"
	if err := os.WriteFile(ffmpegPath, []byte(decoderBody), 0o700); err != nil {
		t.Fatal(err)
	}
	var got []int64
	err := (FFmpeg{FFmpegPath: ffmpegPath, FFprobePath: probePath}).Decode(context.Background(), path, Limits{}, func(frame Frame) error {
		if frame.Width != 2 || frame.Height != 2 || frame.Pixels.Bounds().Dx() != 2 || frame.Pixels.Bounds().Dy() != 2 {
			t.Fatalf("unexpected decoded dimensions: %+v", frame)
		}
		got = append(got, frame.PTSMicros)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{0, 40_000, 105_000}
	if len(got) != len(want) {
		t.Fatalf("got timestamps %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got timestamps %v, want %v", got, want)
		}
	}
}

func TestDecodeStopsWhenCallbackCancels(t *testing.T) {
	path := fakeMedia(t)
	probePath := filepath.Join(t.TempDir(), "ffprobe")
	payload := probePayload("h264", "bt709", 1)
	payload = strings.ReplaceAll(payload, `"width":1920`, `"width":2`)
	payload = strings.ReplaceAll(payload, `"height":1080`, `"height":2`)
	if err := os.WriteFile(probePath, []byte("#!/bin/sh\ncase \"$*\" in *show_entries*) printf '0.000000\\n' ;; *) printf '%s' '"+payload+"' ;; esac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ffmpegPath := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(ffmpegPath, []byte("#!/bin/sh\nprintf '\\001\\002\\003\\004\\005\\006\\007\\010\\011\\012\\013\\014'\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := (FFmpeg{FFmpegPath: ffmpegPath, FFprobePath: probePath}).Decode(ctx, path, Limits{}, func(Frame) error { return context.Canceled })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
