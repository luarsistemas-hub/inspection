package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrDependencyUnavailable = errors.New("rate limit dependency unavailable")

type Result struct {
	Allowed    bool
	RetryAfter time.Duration
	Remaining  int
}
type Store interface {
	Take(context.Context, string, int, time.Duration, time.Time) (Result, error)
}
type Limiter struct {
	Store Store
	Clock func() time.Time
}

func (l Limiter) Take(ctx context.Context, key string, limit int, window time.Duration) (Result, error) {
	if l.Store == nil || key == "" || limit <= 0 || window <= 0 {
		return Result{}, errors.New("rate limiter: invalid configuration")
	}
	now := time.Now
	if l.Clock != nil {
		now = l.Clock
	}
	result, err := l.Store.Take(ctx, key, limit, window, now())
	if err != nil {
		return Result{}, fmt.Errorf("%w", ErrDependencyUnavailable)
	}
	return result, nil
}

type OTPPolicy struct{ Limiter Limiter }

func (p OTPPolicy) AllowSend(ctx context.Context, invitation string) (Result, error) {
	return p.Limiter.Take(ctx, "otp:send:"+invitation, 5, time.Hour)
}
func (p OTPPolicy) AllowResend(ctx context.Context, invitation string) (Result, error) {
	return p.Limiter.Take(ctx, "otp:resend:"+invitation, 1, 60*time.Second)
}
func (p OTPPolicy) AllowAttempt(ctx context.Context, invitation string) (Result, error) {
	return p.Limiter.Take(ctx, "otp:attempt:"+invitation, 5, 10*time.Minute)
}
