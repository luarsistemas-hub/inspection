//go:build integration

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

func TestVV_IT_601VideoComparisonNeverReachesOrdinaryWorkerHandlers(t *testing.T) {
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
	prefix := "vv-it601-" + identity.NewID().String() + "-"
	contracts := make([]messaging.QueueContract, 0, len(workerQueueDefinitions))
	for _, definition := range workerQueueDefinitions {
		contract, err := messaging.NewQueueContract(prefix+definition[0], definition[1], 1)
		if err != nil {
			t.Fatal(err)
		}
		contracts = append(contracts, contract)
	}
	if err := messaging.DeclareTopology(h.Channel, contracts); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, contract := range contracts {
			for _, name := range append([]string{contract.Name, contract.DLQName}, contract.RetryNames[:]...) {
				_, _ = h.Channel.QueueDelete(name, false, false, false)
			}
		}
	})
	ordinary := ordinaryQueueContracts(contracts)
	var video messaging.QueueContract
	for _, contract := range contracts {
		if contract.RoutingKey == "analysis.video_comparison_requested.v1" {
			video = contract
		}
	}
	if video.Name == "" || len(ordinary) != len(contracts)-1 {
		t.Fatalf("dedicated video queue is not separated: ordinary=%d all=%d", len(ordinary), len(contracts))
	}
	// The ordinary photo worker consumes all of its queues while the video
	// request is delivered.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var decoded []string
	var wg sync.WaitGroup
	for _, contract := range ordinary {
		channel, err := h.Rabbit.Channel()
		if err != nil {
			t.Fatal(err)
		}
		defer channel.Close()
		consumer := messaging.Consumer{DB: h.DB, Registry: events.DefaultRegistry(), Name: contract.Name, Handle: func(_ context.Context, _ *gorm.DB, envelope events.RawEnvelope) error {
			mu.Lock()
			decoded = append(decoded, envelope.Type)
			mu.Unlock()
			return nil
		}}
		wg.Add(1)
		go func(contract messaging.QueueContract) {
			defer wg.Done()
			err := (messaging.RabbitConsumer{Channel: channel, Contract: contract, Handler: consumer, ConsumerName: contract.Name}).Run(ctx)
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("ordinary consumer %s: %v", contract.Name, err)
			}
		}(contract)
	}
	id := identity.NewID()
	envelope := events.Envelope[map[string]any]{ID: id, Type: "analysis.video_comparison_requested.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: identity.NewID(), AggregateID: id, CorrelationID: "vv-it601-" + id.String(), Payload: map[string]any{"comparisonJobId": identity.NewID(), "videoPlanId": identity.NewID(), "videoPlanItemId": identity.NewID()}}
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Publisher.PublishConfirmed(ctx, messaging.Publication{RoutingKey: envelope.Type, Body: body, Mandatory: true, CorrelationID: envelope.CorrelationID}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		stats, err := h.Channel.QueueInspect(video.Name)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Messages == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dedicated video queue messages=%d, want 1", stats.Messages)
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	cancel()
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(decoded) != 0 {
		t.Fatalf("ordinary worker decoded events while only a video request was published: %v", decoded)
	}
	for _, contract := range ordinary {
		stats, err := h.Channel.QueueInspect(contract.Name)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Messages != 0 {
			t.Fatalf("ordinary queue %s received the video request", contract.Name)
		}
	}
}
