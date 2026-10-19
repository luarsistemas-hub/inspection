package video

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// WorkerConfig is the validated admission budget for the dedicated worker.
type WorkerConfig struct {
	Concurrency, DecoderThreads                                                  int
	MaxBytes, ScratchBytes, RequiredFreeBytes                                    int64
	StepTimeout, LeaseDuration, HeartbeatInterval, PollInterval, ShutdownTimeout time.Duration
}

// LoadWorkerConfig reads the worker's bounded runtime settings. Concurrency and
// decoder thread count are fixed to one so auxiliary work cannot overlap decode.
func LoadWorkerConfig() (WorkerConfig, error) {
	c := WorkerConfig{
		Concurrency: 1, DecoderThreads: 1, MaxBytes: 1 << 30,
		ScratchBytes: 4 << 30, RequiredFreeBytes: 8 << 30,
		StepTimeout: 2 * time.Hour, LeaseDuration: 120 * time.Second,
		HeartbeatInterval: 30 * time.Second, PollInterval: 2 * time.Second,
		ShutdownTimeout: 30 * time.Second,
	}
	var err error
	if c.Concurrency, err = envInt("INSPECTION_VIDEO_CONCURRENCY", c.Concurrency); err != nil {
		return WorkerConfig{}, err
	}
	if c.DecoderThreads, err = envInt("INSPECTION_VIDEO_DECODER_THREADS", c.DecoderThreads); err != nil {
		return WorkerConfig{}, err
	}
	if c.MaxBytes, err = envInt64("INSPECTION_VIDEO_MAX_BYTES", c.MaxBytes); err != nil {
		return WorkerConfig{}, err
	}
	if c.ScratchBytes, err = envInt64("INSPECTION_VIDEO_SCRATCH_BYTES", c.ScratchBytes); err != nil {
		return WorkerConfig{}, err
	}
	if c.RequiredFreeBytes, err = envInt64("INSPECTION_VIDEO_REQUIRED_FREE_BYTES", c.RequiredFreeBytes); err != nil {
		return WorkerConfig{}, err
	}
	if c.StepTimeout, err = envDuration("INSPECTION_VIDEO_STEP_TIMEOUT", c.StepTimeout); err != nil {
		return WorkerConfig{}, err
	}
	if c.LeaseDuration, err = envDuration("INSPECTION_VIDEO_LEASE", c.LeaseDuration); err != nil {
		return WorkerConfig{}, err
	}
	if c.HeartbeatInterval, err = envDuration("INSPECTION_VIDEO_HEARTBEAT", c.HeartbeatInterval); err != nil {
		return WorkerConfig{}, err
	}
	if c.PollInterval, err = envDuration("INSPECTION_VIDEO_POLL", c.PollInterval); err != nil {
		return WorkerConfig{}, err
	}
	if err := c.Validate(); err != nil {
		return WorkerConfig{}, err
	}
	return c, nil
}

func (c WorkerConfig) Validate() error {
	if c.Concurrency != 1 || c.DecoderThreads != 1 || c.MaxBytes <= 0 || c.MaxBytes > 1<<30 || c.ScratchBytes <= 0 || c.ScratchBytes > 4<<30 || c.RequiredFreeBytes < 8<<30 || c.StepTimeout <= 0 || c.LeaseDuration <= 0 || c.HeartbeatInterval <= 0 || c.HeartbeatInterval >= c.LeaseDuration/2 || c.PollInterval <= 0 || c.ShutdownTimeout <= 0 {
		return fmt.Errorf("video worker: invalid admission configuration")
	}
	return nil
}

func HasRequiredScratchSpace(available, required uint64) bool {
	return required > 0 && available >= required
}

func envInt(key string, fallback int) (int, error) {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("video worker: invalid %s", key)
	}
	return value, nil
}

func envInt64(key string, fallback int64) (int64, error) {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("video worker: invalid %s", key)
	}
	return value, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("video worker: invalid %s", key)
	}
	return value, nil
}
