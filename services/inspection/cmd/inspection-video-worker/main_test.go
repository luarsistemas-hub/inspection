package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"

	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

func TestPreparationFailureLogUsesOnlySafeCodeIT611(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	logPreparationFailure(errors.New("object store denied credential=qa-secret-123"))
	value := output.String()
	if !strings.Contains(value, "PREPARATION_FAILED") || strings.Contains(value, "qa-secret-123") || strings.Contains(value, "credential=") {
		t.Fatalf("unsafe preparation failure log: %s", value)
	}
}

func TestVideoConsumerRetainsLegacyInboxIdentity(t *testing.T) {
	legacy, err := messaging.NewQueueContract("inspection-video-comparison", "analysis.video_comparison_requested.v1", 1)
	if err != nil {
		t.Fatal(err)
	}
	consumer := newVideoWorkerConsumer(nil, events.DefaultRegistry(), legacy, func(context.Context, *gorm.DB, events.RawEnvelope) error { return nil }, nil)
	if got := consumer.Name; got != "inspection-video-comparison" {
		t.Fatalf("consumer inbox name = %q; want stable legacy deduplication identity", got)
	}
}
