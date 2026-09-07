package messaging

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const Exchange = "inspection.events"

type RabbitPublisher struct {
	Channel  *amqp.Channel
	confirms <-chan amqp.Confirmation
	returns  <-chan amqp.Return
	mu       sync.Mutex
}

func NewRabbitPublisher(channel *amqp.Channel) (*RabbitPublisher, error) {
	if channel == nil {
		return nil, errors.New("rabbit publisher: missing channel")
	}
	if err := channel.Confirm(false); err != nil {
		return nil, fmt.Errorf("rabbit confirms: %w", err)
	}
	// Register each listener once. Registering a new NotifyPublish channel for
	// every message leaks listeners: old buffered channels eventually fill and
	// RabbitMQ can no longer deliver confirmations to the active publisher.
	return &RabbitPublisher{
		Channel:  channel,
		confirms: channel.NotifyPublish(make(chan amqp.Confirmation, 32)),
		returns:  channel.NotifyReturn(make(chan amqp.Return, 32)),
	}, nil
}

func (p *RabbitPublisher) PublishConfirmed(ctx context.Context, publication Publication) error {
	if !publication.Mandatory || publication.RoutingKey == "" || len(publication.Body) == 0 {
		return errors.New("rabbit publisher: invalid publication")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	err := p.Channel.PublishWithContext(ctx, Exchange, publication.RoutingKey, true, false, amqp.Publishing{DeliveryMode: amqp.Persistent, ContentType: "application/json", Body: publication.Body, CorrelationId: publication.CorrelationID, Headers: amqp.Table{"causation_id": publication.CausationID}, Timestamp: time.Now().UTC()})
	if err != nil {
		return fmt.Errorf("rabbit publish: %w", err)
	}
	select {
	case confirm, ok := <-p.confirms:
		if !ok {
			return errors.New("rabbit publish confirmation channel closed")
		}
		if !confirm.Ack {
			return errors.New("rabbit publish not confirmed")
		}
		// RabbitMQ sends basic.return before the corresponding publisher
		// acknowledgement. Drain that buffered return before accepting the ack.
		select {
		case returned := <-p.returns:
			return fmt.Errorf("%w: %s", ErrUnroutable, returned.ReplyText)
		default:
			return nil
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

func DeclareTopology(channel *amqp.Channel, contracts []QueueContract) error {
	if channel == nil {
		return errors.New("rabbit topology: missing channel")
	}
	if err := channel.ExchangeDeclare(Exchange, "topic", true, false, false, false, nil); err != nil {
		return err
	}
	for _, contract := range contracts {
		if !contract.Durable || contract.Prefetch <= 0 {
			return errors.New("rabbit topology: invalid contract")
		}
		if _, err := channel.QueueDeclare(contract.DLQName, true, false, false, false, amqp.Table{"x-queue-type": "quorum"}); err != nil {
			return err
		}
		for i, delay := range RetryDelays[1:] {
			args := amqp.Table{"x-message-ttl": int64(delay / time.Millisecond), "x-dead-letter-exchange": Exchange, "x-dead-letter-routing-key": contract.RoutingKey}
			if _, err := channel.QueueDeclare(contract.RetryNames[i], true, false, false, false, args); err != nil {
				return err
			}
		}
		args := amqp.Table{"x-dead-letter-exchange": "", "x-dead-letter-routing-key": contract.DLQName, "x-queue-type": "quorum"}
		if _, err := channel.QueueDeclare(contract.Name, true, false, false, false, args); err != nil {
			return err
		}
		if err := channel.QueueBind(contract.Name, contract.RoutingKey, Exchange, false, nil); err != nil {
			return err
		}
		if err := channel.Qos(contract.Prefetch, 0, false); err != nil {
			return err
		}
	}
	return nil
}
