package objectstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"time"
)

var errAudioProbeUnavailable = errors.New("audio probe unavailable")

type audioProbeResult struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		Duration  string `json:"duration"`
	} `json:"streams"`
	Format struct {
		Duration  string `json:"duration"`
		StartTime string `json:"start_time"`
	} `json:"format"`
}

type audioPacketResult struct {
	Packets []struct {
		PTS      string `json:"pts_time"`
		Duration string `json:"duration_time"`
	} `json:"packets"`
}

func probeAudioDuration(ctx context.Context, data []byte) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	file, err := os.CreateTemp("", "inspection-audio-*.bin")
	if err != nil {
		return 0, err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return 0, err
	}
	if err := file.Close(); err != nil {
		return 0, err
	}
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=codec_type,duration:format=duration,start_time", "-of", "json", name)
	var output bytes.Buffer
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) {
			return 0, errors.Join(errAudioProbeUnavailable, err)
		}
		return 0, err
	}
	var parsed audioProbeResult
	if err := json.Unmarshal(output.Bytes(), &parsed); err != nil {
		return 0, err
	}
	if len(parsed.Streams) == 0 || parsed.Streams[0].CodecType != "audio" {
		return 0, errors.New("audio stream missing")
	}
	seconds := parsed.Streams[0].Duration
	if seconds == "" || seconds == "N/A" {
		seconds = parsed.Format.Duration
	}
	header, headerErr := strconv.ParseFloat(seconds, 64)
	packets, packetErr := probeAudioPacketDuration(ctx, name, parsed.Format.StartTime)
	if packetErr != nil {
		return 0, packetErr
	}
	if headerErr == nil && header > 0 {
		headerDuration := time.Duration(header * float64(time.Second))
		delta := headerDuration - packets
		if delta < 0 {
			delta = -delta
		}
		if delta > 500*time.Millisecond {
			return 0, errors.New("audio header duration mismatch")
		}
	}
	return packets, nil
}

func probeAudioPacketDuration(ctx context.Context, name, startTime string) (time.Duration, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "a:0", "-show_packets", "-show_entries", "packet=pts_time,duration_time", "-of", "json", name)
	var output bytes.Buffer
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) {
			return 0, errors.Join(errAudioProbeUnavailable, err)
		}
		return 0, err
	}
	var parsed audioPacketResult
	if err := json.Unmarshal(output.Bytes(), &parsed); err != nil {
		return 0, err
	}
	start, startErr := strconv.ParseFloat(startTime, 64)
	if startErr != nil {
		start = 0
	}
	duration, err := packetDurationFromStart(parsed.Packets, start)
	if err != nil {
		return 0, err
	}
	return duration, nil
}

func packetDurationFromStart(packets []struct {
	PTS      string `json:"pts_time"`
	Duration string `json:"duration_time"`
}, start float64) (time.Duration, error) {
	var maxEnd float64
	initialized := false
	for _, packet := range packets {
		pts, ptsErr := strconv.ParseFloat(packet.PTS, 64)
		duration, durationErr := strconv.ParseFloat(packet.Duration, 64)
		if ptsErr != nil || durationErr != nil || duration <= 0 {
			continue
		}
		packetEnd := pts + duration
		if !initialized || packetEnd > maxEnd {
			maxEnd = packetEnd
		}
		initialized = true
	}
	if !initialized || maxEnd-start <= 0 {
		return 0, errors.New("audio duration missing")
	}
	return time.Duration((maxEnd - start) * float64(time.Second)), nil
}
