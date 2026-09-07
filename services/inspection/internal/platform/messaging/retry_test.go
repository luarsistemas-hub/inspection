package messaging

import (
	"errors"
	"testing"
	"time"
)

func TestUT038RetrySchedule(t *testing.T) {
	want := []time.Duration{5 * time.Second, 30 * time.Second, 5 * time.Minute}
	for i, expected := range want {
		got, ok := NextAttempt(i, ErrRetryable)
		if !ok || got != expected {
			t.Fatalf("attempt %d: %v %v", i, got, ok)
		}
	}
	if _, ok := NextAttempt(3, ErrRetryable); ok {
		t.Fatal("exhausted retry must fall back")
	}
}
func TestUT039PermanentAndRetryable(t *testing.T) {
	if _, ok := NextAttempt(0, ErrPermanent); ok {
		t.Fatal("permanent error retried")
	}
	if _, ok := NextAttempt(0, errors.New("dependency")); !ok {
		t.Fatal("dependency error not retried")
	}
}
func TestIT361QueueTopologyContract(t *testing.T) {
	q, err := NewQueueContract("notification.deliver", "notification.*", 12)
	if err != nil || !q.Durable || q.Prefetch != 12 || q.DLQName != "notification.deliver.dlq" || q.RetryNames[2] != "notification.deliver.retry.5m" {
		t.Fatalf("invalid topology: %+v %v", q, err)
	}
}
