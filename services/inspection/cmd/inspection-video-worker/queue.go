package main

import (
	"inspection/services/inspection/internal/platform/messaging"

	amqp "github.com/rabbitmq/amqp091-go"
)

// declareVideoQueueMigration routes new events to the canonical queue while
// retaining the old durable queue and its accepted messages for draining.
func declareVideoQueueMigration(current, legacy *amqp.Channel, canonical, old messaging.QueueContract) error {
	if err := messaging.DeclareTopology(current, []messaging.QueueContract{canonical}); err != nil {
		return err
	}
	if err := messaging.DeclareTopology(legacy, []messaging.QueueContract{old}); err != nil {
		return err
	}
	return legacy.QueueUnbind(old.Name, old.RoutingKey, messaging.Exchange, nil)
}
