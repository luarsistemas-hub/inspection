package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	expireaudio "inspection/services/inspection/internal/features/transcription/expire_audio"
)

func TestDueEvery(t *testing.T) {
	last := time.Date(2026, time.October, 17, 12, 0, 0, 0, time.UTC)
	interval := 5 * time.Minute
	tests := []struct {
		name string
		last time.Time
		now  time.Time
		want bool
	}{
		{name: "first run", now: last, want: true},
		{name: "before interval", last: last, now: last.Add(4*time.Minute + 59*time.Second), want: false},
		{name: "at interval", last: last, now: last.Add(interval), want: true},
		{name: "after delayed interval", last: last, now: last.Add(7 * time.Minute), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dueEvery(tt.last, tt.now, interval); got != tt.want {
				t.Fatalf("dueEvery(%v, %v, %v) = %t, want %t", tt.last, tt.now, interval, got, tt.want)
			}
		})
	}
}

func TestTranscriptionSweepIntervalFromEnv(t *testing.T) {
	tests := []struct {
		value   string
		want    time.Duration
		wantErr bool
	}{
		{value: "", want: defaultTranscriptionSweepInterval},
		{value: " 5m ", want: 5 * time.Minute},
		{value: "1h", want: time.Hour},
		{value: "30s", wantErr: true},
		{value: "2h", wantErr: true},
		{value: "soon", wantErr: true},
	}
	for _, tt := range tests {
		got, err := transcriptionSweepIntervalFromEnv(func(name string) string {
			if name != "INSPECTION_TRANSCRIPTION_SWEEP_INTERVAL" {
				t.Fatalf("unexpected variable %q", name)
			}
			return tt.value
		})
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("value %q: interval=%s err=%v, want %s wantErr=%t", tt.value, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestVideoExpiryIntervalFromEnv(t *testing.T) {
	tests := []struct {
		value   string
		want    time.Duration
		wantErr bool
	}{
		{value: "", want: defaultVideoExpiryInterval},
		{value: " 15s ", want: 15 * time.Second},
		{value: "24h", want: 24 * time.Hour},
		{value: "9s", wantErr: true},
		{value: "25h", wantErr: true},
		{value: "soon", wantErr: true},
	}
	for _, tt := range tests {
		got, err := videoExpiryIntervalFromEnv(func(name string) string {
			if name != "INSPECTION_VIDEO_EXPIRY_INTERVAL" {
				t.Fatalf("unexpected variable %q", name)
			}
			return tt.value
		})
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("value %q: interval=%s err=%v, want %s wantErr=%t", tt.value, got, err, tt.want, tt.wantErr)
		}
	}
}

// blockingSweeper stands in for the voice-note scans: it honours only the
// context it receives, like a database or storage call stuck on a lock.
type blockingSweeper struct {
	mu       sync.Mutex
	passes   int
	tenants  []identity.ID
	runs     int
	blockDue bool
	entered  chan struct{}
}

func (s *blockingSweeper) DueTenants(ctx context.Context) ([]identity.ID, error) {
	s.mu.Lock()
	s.passes++
	s.mu.Unlock()
	if s.entered != nil {
		select {
		case s.entered <- struct{}{}:
		default:
		}
	}
	if s.blockDue {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.tenants, nil
}

func (s *blockingSweeper) RunDue(ctx context.Context, _ identity.ID, _ int) (expireaudio.Counts, error) {
	s.mu.Lock()
	s.runs++
	s.mu.Unlock()
	<-ctx.Done()
	return expireaudio.Counts{}, ctx.Err()
}

func (s *blockingSweeper) snapshot() (passes, runs int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.passes, s.runs
}

func TestRunTranscriptionSweepsBoundsTheWholePass(t *testing.T) {
	tests := map[string]*blockingSweeper{
		"discovery stuck":   {blockDue: true},
		"tenant scan stuck": {tenants: []identity.ID{identity.NewID(), identity.NewID()}},
	}
	for name, sweeper := range tests {
		t.Run(name, func(t *testing.T) {
			started := time.Now()
			runTranscriptionSweeps(context.Background(), sweeper, 80*time.Millisecond)
			if elapsed := time.Since(started); elapsed < 60*time.Millisecond || elapsed > 2*time.Second {
				t.Fatalf("pass lasted %s, want it cut at the timeout", elapsed)
			}
			if _, runs := sweeper.snapshot(); runs > 1 {
				t.Fatalf("pass kept scanning tenants after the timeout: runs=%d", runs)
			}
		})
	}
}

func TestRunTranscriptionSweepLoopRepeatsAndStopsOnShutdown(t *testing.T) {
	sweeper := &blockingSweeper{blockDue: true, entered: make(chan struct{}, 8)}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		runTranscriptionSweepLoop(ctx, sweeper, 50*time.Millisecond)
	}()
	// A pass stuck on its dependency is cancelled at the interval, and the
	// loop then starts the next pass instead of hanging.
	for range 3 {
		select {
		case <-sweeper.entered:
		case <-time.After(3 * time.Second):
			t.Fatal("the loop did not start another pass after a stuck one")
		}
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("the sweep loop outlived its context")
	}
	if passes, _ := sweeper.snapshot(); passes < 3 {
		t.Fatalf("passes=%d, want at least 3", passes)
	}
}
