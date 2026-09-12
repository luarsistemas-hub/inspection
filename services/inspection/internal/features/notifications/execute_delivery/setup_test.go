package execute_delivery

import (
	"testing"
	"time"

	"inspection/services/inspection/internal/features/notifications/core"
	"inspection/services/inspection/internal/platform/notifications"
)

func TestUT041UT042RetryScheduleHasFourAttemptBudget(t *testing.T) {
	e := &Executor{maxAttempts: 4, retryDelays: []time.Duration{5 * time.Second, 30 * time.Second, 5 * time.Minute}}
	now := time.Unix(100, 0).UTC()
	for attempt, want := range []time.Duration{5 * time.Second, 30 * time.Second, 5 * time.Minute} {
		state, next, _ := e.outcome(attempt+1, &notifications.DeliveryError{Kind: notifications.ErrorTransient, Code: "unavailable", PreSend: true}, now)
		if state != core.StateQueued || !next.Equal(now.Add(want)) {
			t.Fatalf("attempt %d got %s at %s", attempt+1, state, next)
		}
	}
	state, next, _ := e.outcome(4, &notifications.DeliveryError{Kind: notifications.ErrorTransient, Code: "unavailable", PreSend: true}, now)
	if state != core.StateFailed || !next.IsZero() {
		t.Fatalf("attempt four=%s at %s", state, next)
	}
}

func TestUT043UT044UT045ProviderOutcomeSafety(t *testing.T) {
	e := &Executor{maxAttempts: 4, retryDelays: []time.Duration{5 * time.Second, 30 * time.Second, 5 * time.Minute}}
	now := time.Unix(100, 0).UTC()
	for name, test := range map[string]struct {
		err   error
		state core.State
	}{
		"permanent": {&notifications.DeliveryError{Kind: notifications.ErrorPermanent, Code: "rejected", PreSend: true}, core.StateFailed},
		"pre-send":  {&notifications.DeliveryError{Kind: notifications.ErrorTransient, Code: "unavailable", PreSend: true}, core.StateQueued},
		"uncertain": {&notifications.DeliveryError{Kind: notifications.ErrorUnknown, Code: "interrupted"}, core.StateUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			state, _, _ := e.outcome(1, test.err, now)
			if state != test.state {
				t.Fatalf("got %s", state)
			}
		})
	}
}
