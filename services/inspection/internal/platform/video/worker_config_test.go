package video

import (
	"testing"
	"time"
)

func TestDefaultWorkerConfigIsSingleThreadedAndBounded(t *testing.T) {
	c, err := LoadWorkerConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.Concurrency != 1 || c.DecoderThreads != 1 || c.MaxBytes != 1<<30 || c.ScratchBytes != 4<<30 || c.RequiredFreeBytes != 8<<30 {
		t.Fatalf("unexpected worker budget: %+v", c)
	}
}

func TestWorkerConfigAcceptsRequiredSinglePermitProfileUT075(t *testing.T) {
	t.Setenv("INSPECTION_VIDEO_CONCURRENCY", "1")
	t.Setenv("INSPECTION_VIDEO_DECODER_THREADS", "1")
	t.Setenv("INSPECTION_VIDEO_MAX_BYTES", "1073741824")
	c, err := LoadWorkerConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.Concurrency != 1 || c.DecoderThreads != 1 || c.MaxBytes != 1073741824 {
		t.Fatalf("unexpected worker profile: %+v", c)
	}
}

func TestScratchAdmissionRejectsOneByteBelowFloorUT079(t *testing.T) {
	if HasRequiredScratchSpace((8<<30)-1, 8<<30) {
		t.Fatal("worker admitted work one byte below scratch requirement")
	}
	if !HasRequiredScratchSpace(8<<30, 8<<30) {
		t.Fatal("worker rejected work at the scratch requirement")
	}
}

func TestWorkerConfigRejectsInvalidBounds(t *testing.T) {
	base := WorkerConfig{Concurrency: 1, DecoderThreads: 1, MaxBytes: 1 << 30, ScratchBytes: 4 << 30, RequiredFreeBytes: 8 << 30, StepTimeout: 2 * time.Hour, LeaseDuration: 120 * time.Second, HeartbeatInterval: 30 * time.Second, PollInterval: 2 * time.Second, ShutdownTimeout: 30 * time.Second}
	for _, mutate := range []func(*WorkerConfig){func(c *WorkerConfig) { c.Concurrency = 0 }, func(c *WorkerConfig) { c.DecoderThreads = -1 }, func(c *WorkerConfig) { c.ScratchBytes = -1 }, func(c *WorkerConfig) { c.RequiredFreeBytes = 1 }, func(c *WorkerConfig) { c.MaxBytes = 0 }, func(c *WorkerConfig) { c.MaxBytes = 1<<30 + 1 }} {
		c := base
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Fatalf("accepted invalid config: %+v", c)
		}
	}
}

func TestWorkerConfigRejectsInvalidConcurrencyOrScratchEnvironmentUT076(t *testing.T) {
	for _, tc := range []struct{ key, value string }{{"INSPECTION_VIDEO_CONCURRENCY", "0"}, {"INSPECTION_VIDEO_CONCURRENCY", "-1"}, {"INSPECTION_VIDEO_SCRATCH_BYTES", "-1"}} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := LoadWorkerConfig(); err == nil {
				t.Fatalf("accepted %s=%s", tc.key, tc.value)
			}
		})
	}
}

func TestWorkerConfigParsesLimitsFromEnvironment(t *testing.T) {
	t.Setenv("INSPECTION_VIDEO_MAX_BYTES", "1073741824")
	c, err := LoadWorkerConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxBytes != 1073741824 {
		t.Fatalf("got %d", c.MaxBytes)
	}
	t.Setenv("INSPECTION_VIDEO_MAX_BYTES", "-1")
	if _, err := LoadWorkerConfig(); err == nil {
		t.Fatal("expected invalid max bytes")
	}
}
