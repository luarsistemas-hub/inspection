package messaging

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/observability"

	amqp "github.com/rabbitmq/amqp091-go"
)

type DeliveryHandler interface {
	Process(context.Context, []byte, int) (bool, error)
}

type attemptContextKey struct{}

// WithAttempt annotates a delivery context with the broker attempt number.
// Consumers can use it to distinguish a retryable provider failure from the
// exhausted terminal fallback without coupling feature code to AMQP headers.
func WithAttempt(ctx context.Context, attempt int) context.Context {
	return context.WithValue(ctx, attemptContextKey{}, attempt)
}

// Attempt returns the current broker attempt (zero for the first delivery).
func Attempt(ctx context.Context) int {
	value, _ := ctx.Value(attemptContextKey{}).(int)
	return value
}

// HasAttempt reports whether the context was created by a broker consumer.
func HasAttempt(ctx context.Context) bool {
	_, ok := ctx.Value(attemptContextKey{}).(int)
	return ok
}

type RabbitConsumer struct {
	Channel      *amqp.Channel
	Contract     QueueContract
	Handler      DeliveryHandler
	ConsumerName string
	Observer     observability.DeliveryObserver
}

func (c RabbitConsumer) Run(ctx context.Context) error {
	if c.Channel == nil || c.Handler == nil || c.ConsumerName == "" || c.Contract.Name == "" {
		return errors.New("rabbit consumer: missing dependency")
	}
	if err := c.Channel.Qos(c.Contract.Prefetch, 0, false); err != nil {
		return err
	}
	deliveries, err := c.Channel.ConsumeWithContext(ctx, c.Contract.Name, c.ConsumerName, false, false, false, false, nil)
	if err != nil {
		return err
	}
	for delivery := range deliveries {
		started := time.Now()
		attempt := headerInt(delivery.Headers, "attempt")
		generation := headerInt(delivery.Headers, "replay_generation")
		deliveryCtx := WithAttempt(ctx, attempt)
		deliveryCtx = observability.WithExecutionID(deliveryCtx, identity.NewID().String())
		correlation := observability.CorrelationFromContext(deliveryCtx)
		correlation.CorrelationID = delivery.CorrelationId
		correlation.Attempt = attempt
		correlation.ReplayGeneration = generation
		deliveryCtx = observability.WithCorrelation(deliveryCtx, correlation)
		_, handleErr := c.Handler.Process(deliveryCtx, delivery.Body, generation)
		if handleErr == nil {
			if err := delivery.Ack(false); err != nil {
				return err
			}
			continue
		}
		if errors.Is(handleErr, ErrPermanent) || attempt+1 >= len(RetryDelays) {
			if err := c.publishQueue(ctx, c.Contract.DLQName, delivery, attempt+1, "contract_or_exhausted"); err != nil {
				c.observeDelivery(deliveryCtx, delivery, "dlq", "publish_failed", "contract_or_exhausted", attempt, started, err)
				_ = delivery.Nack(false, true)
				continue
			}
			c.observeDelivery(deliveryCtx, delivery, "dlq", "published", "contract_or_exhausted", attempt, started, handleErr)
			_ = delivery.Ack(false)
			continue
		}
		if err := c.publishQueue(ctx, c.Contract.RetryNames[attempt], delivery, attempt+1, "retryable"); err != nil {
			c.observeDelivery(deliveryCtx, delivery, "retry", "publish_failed", "retryable", attempt, started, err)
			_ = delivery.Nack(false, true)
			continue
		}
		c.observeDelivery(deliveryCtx, delivery, "retry", "published", "retryable", attempt, started, handleErr)
		_ = delivery.Ack(false)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (c RabbitConsumer) observeDelivery(ctx context.Context, delivery amqp.Delivery, action, result, reason string, attempt int, started time.Time, err error) {
	if c.Observer == nil {
		return
	}
	c.Observer.AfterDelivery(ctx, observability.DeliveryObservation{ExecutionID: observability.CorrelationFromContext(ctx).ExecutionID, Queue: c.Contract.Name, CorrelationID: delivery.CorrelationId, Action: action, Result: result, Reason: reason, Attempt: attempt, Duration: time.Since(started), Err: err})
}

func (c RabbitConsumer) publishQueue(ctx context.Context, queue string, delivery amqp.Delivery, attempt int, reason string) error {
	headers := delivery.Headers
	if headers == nil {
		headers = amqp.Table{}
	}
	headers["attempt"] = attempt
	headers["failure_reason"] = reason
	return c.Channel.PublishWithContext(ctx, "", queue, false, false, amqp.Publishing{DeliveryMode: amqp.Persistent, ContentType: "application/json", Body: delivery.Body, Headers: headers, CorrelationId: delivery.CorrelationId})
}
func headerInt(headers amqp.Table, key string) int {
	value, ok := headers[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case string:
		n, _ := strconv.Atoi(typed)
		return n
	}
	return 0
}

type HandlerFunc func(context.Context, []byte, int) (bool, error)

func (f HandlerFunc) Process(ctx context.Context, body []byte, generation int) (bool, error) {
	if f == nil {
		return false, fmt.Errorf("nil handler")
	}
	return f(ctx, body, generation)
}
