//go:build integration

package main

import (
	"context"
	"os"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/messaging"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestVideoQueueMigrationPreservesAcceptedMessages(t *testing.T) {
	url := os.Getenv("INSPECTION_TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("RabbitMQ test URL is required")
	}
	connection, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	current, err := connection.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	legacy, err := connection.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	name := identity.NewID().String()
	routingKey := "video.queue.migration." + name
	canonical, err := messaging.NewQueueContract("inspection.video.test."+name, routingKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	old, err := messaging.NewQueueContract("inspection-video-comparison.test."+name, routingKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, contract := range []messaging.QueueContract{canonical, old} {
			for _, queue := range append([]string{contract.Name, contract.DLQName}, contract.RetryNames[:]...) {
				_, _ = current.QueueDelete(queue, false, false, false)
			}
		}
	}()
	if err := messaging.DeclareTopology(legacy, []messaging.QueueContract{old}); err != nil {
		t.Fatal(err)
	}
	publisher, err := messaging.NewRabbitPublisher(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.PublishConfirmed(context.Background(), messaging.Publication{RoutingKey: routingKey, Body: []byte("accepted-before-migration"), Mandatory: true}); err != nil {
		t.Fatal(err)
	}
	if queue, err := legacy.QueueInspect(old.Name); err != nil || queue.Messages != 1 {
		t.Fatalf("legacy queue before migration = %+v, %v", queue, err)
	}
	if err := declareVideoQueueMigration(current, legacy, canonical, old); err != nil {
		t.Fatal(err)
	}
	if err := publisher.PublishConfirmed(context.Background(), messaging.Publication{RoutingKey: routingKey, Body: []byte("accepted-after-migration"), Mandatory: true}); err != nil {
		t.Fatal(err)
	}
	if queue, err := legacy.QueueInspect(old.Name); err != nil || queue.Messages != 1 {
		t.Fatalf("legacy queue after migration = %+v, %v; old message must remain and new one must not duplicate", queue, err)
	}
	if queue, err := current.QueueInspect(canonical.Name); err != nil || queue.Messages != 1 {
		t.Fatalf("canonical queue after migration = %+v, %v", queue, err)
	}
	if delivery, ok, err := legacy.Get(old.Name, true); err != nil || !ok || string(delivery.Body) != "accepted-before-migration" {
		t.Fatalf("legacy accepted delivery = %q, %t, %v", delivery.Body, ok, err)
	}
	if delivery, ok, err := current.Get(canonical.Name, true); err != nil || !ok || string(delivery.Body) != "accepted-after-migration" {
		t.Fatalf("canonical delivery = %q, %t, %v", delivery.Body, ok, err)
	}
}
