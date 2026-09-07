package messaging

import (
	"errors"
	"time"
)

var (
	ErrPermanent = errors.New("permanent message failure")
	ErrRetryable = errors.New("retryable message failure")
)

var RetryDelays = [...]time.Duration{0, 5 * time.Second, 30 * time.Second, 5 * time.Minute}

// MaxDeliveryAttempts bounds channel-level notification retries independently
// from the broker delivery attempt count.
const MaxDeliveryAttempts = 4

func NextAttempt(attempt int, err error) (time.Duration, bool) {
	if err == nil || errors.Is(err, ErrPermanent) || attempt < 0 || attempt+1 >= len(RetryDelays) {
		return 0, false
	}
	return RetryDelays[attempt+1], true
}

type QueueContract struct {
	Name       string
	RoutingKey string
	Prefetch   int
	Durable    bool
	RetryNames [3]string
	DLQName    string
}

func NewQueueContract(name, routingKey string, prefetch int) (QueueContract, error) {
	if name == "" || routingKey == "" || prefetch < 1 || prefetch > 1000 {
		return QueueContract{}, errors.New("messaging: invalid queue contract")
	}
	return QueueContract{Name: name, RoutingKey: routingKey, Prefetch: prefetch, Durable: true,
		RetryNames: [3]string{name + ".retry.5s", name + ".retry.30s", name + ".retry.5m"}, DLQName: name + ".dlq"}, nil
}
