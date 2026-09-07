package ratelimit

import (
	"context"
	"errors"
	"testing"
	"time"
)

type storeStub struct {
	result Result
	err    error
}

func (s storeStub) Take(context.Context, string, int, time.Duration, time.Time) (Result, error) {
	return s.result, s.err
}
func TestUT020LimitsDoNotFailOpen(t *testing.T) {
	l := Limiter{Store: storeStub{result: Result{Allowed: false, RetryAfter: time.Minute}}}
	got, err := l.Take(context.Background(), "otp", 5, time.Hour)
	if err != nil || got.Allowed {
		t.Fatal("limit bypassed")
	}
	l.Store = storeStub{err: errors.New("down")}
	if _, err = l.Take(context.Background(), "otp", 5, time.Hour); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("outage failed open: %v", err)
	}
}
