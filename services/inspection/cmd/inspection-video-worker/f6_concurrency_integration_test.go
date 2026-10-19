//go:build integration

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	preparevideo "inspection/services/inspection/internal/features/media/prepare_video"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/sensitivecontent"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

type idleVideoGateway struct{}

func (idleVideoGateway) CompleteStructured(context.Context, llm.StructuredRequest) (llm.StructuredResult, error) {
	return llm.StructuredResult{}, errors.New("not used")
}

// videoExecutionProbe records how many video executions overlap.
type videoExecutionProbe struct {
	active, peak, runs atomic.Int64
}

func (p *videoExecutionProbe) run(ctx context.Context) (bool, error) {
	current := p.active.Add(1)
	for {
		peak := p.peak.Load()
		if current <= peak || p.peak.CompareAndSwap(peak, current) {
			break
		}
	}
	time.Sleep(time.Millisecond)
	p.active.Add(-1)
	p.runs.Add(1)
	return true, nil
}

func TestVV_IT_608HundredVideoJobsRunOneAtATimeAcrossStages(t *testing.T) {
	for _, key := range []string{"INSPECTION_TEST_DATABASE_URL", "INSPECTION_TEST_RUNTIME_DATABASE_URL", "INSPECTION_TEST_RABBITMQ_URL"} {
		if os.Getenv(key) == "" {
			t.Skip(key + " is required")
		}
	}
	h, err := harness.NewFromEnv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	executor, err := preparevideo.Setup(preparevideo.Dependencies{DB: h.DB, ClaimDB: h.AdminDB, Store: objectstore.VideoStore{Store: h.Store}, Decoder: video.FFmpeg{Threads: 1}, Detector: sensitivecontent.NewDisabledDetector(), Gateway: idleVideoGateway{}, Now: time.Now, TempDir: os.TempDir(), Limits: video.WorkerConfig{Concurrency: 1, DecoderThreads: 1, MaxBytes: 1 << 30, ScratchBytes: 4 << 30, RequiredFreeBytes: 8 << 30, StepTimeout: time.Minute, LeaseDuration: 120 * time.Second, HeartbeatInterval: 30 * time.Second, PollInterval: 2 * time.Second, ShutdownTimeout: 30 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	queueName := "vv-it608-" + identity.NewID().String()
	routingKey := "vv.it608." + identity.NewID().String()
	contract, err := messaging.NewQueueContract(queueName, routingKey, 32)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Declare(contract); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, name := range append([]string{contract.Name, contract.DLQName}, contract.RetryNames[:]...) {
			_, _ = h.Channel.QueueDelete(name, false, false, false)
		}
	})
	probe := &videoExecutionProbe{}
	var dispatched atomic.Int64
	// Condition dispatch: each delivery runs inside the shared permit.
	delivery := messaging.Consumer{DB: h.DB, Registry: events.DefaultRegistry(), Name: queueName, Handle: func(ctx context.Context, _ *gorm.DB, _ events.RawEnvelope) error {
		dispatched.Add(1)
		_, err := probe.run(ctx)
		return err
	}}
	for i := 0; i < 100; i++ {
		id := identity.NewID()
		body, err := json.Marshal(events.Envelope[map[string]any]{ID: id, Type: "analysis.video_comparison_requested.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: identity.NewID(), AggregateID: id, CorrelationID: "vv-it608-" + id.String(), Payload: map[string]any{"comparisonJobId": identity.NewID(), "videoPlanId": identity.NewID(), "videoPlanItemId": identity.NewID()}})
		if err != nil {
			t.Fatal(err)
		}
		if err := h.Publisher.PublishConfirmed(context.Background(), messaging.Publication{RoutingKey: routingKey, Body: body, Mandatory: true}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	// Decode, discovery and matching loops compete for the same permit.
	for _, stage := range []string{"decode", "discovery", "matching"} {
		wg.Add(1)
		go func(stage string) {
			defer wg.Done()
			for ctx.Err() == nil {
				if _, err := executor.WithPermit(ctx, probe.run); err != nil && ctx.Err() == nil {
					t.Errorf("%s stage: %v", stage, err)
					return
				}
			}
		}(stage)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			_, _ = executor.RunDue(ctx)
		}
	}()
	for i := 0; i < 2; i++ {
		channel, err := h.Rabbit.Channel()
		if err != nil {
			t.Fatal(err)
		}
		defer channel.Close()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = (messaging.RabbitConsumer{Channel: channel, Contract: contract, Handler: permittedDelivery{permit: executor, inner: delivery}, ConsumerName: queueName}).Run(ctx)
		}()
	}
	deadline := time.Now().Add(60 * time.Second)
	for dispatched.Load() < 100 {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of 100 video jobs dispatched", dispatched.Load())
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	wg.Wait()
	if probe.peak.Load() != 1 || probe.runs.Load() < 100 {
		t.Fatalf("video executions overlapped: peak=%d runs=%d", probe.peak.Load(), probe.runs.Load())
	}
}
