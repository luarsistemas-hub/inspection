package harness

import (
	"context"
	"fmt"
	"time"
)

// Eventually polls condition until it succeeds, fails, or the context ends.
// A condition error is returned immediately because it normally indicates a
// malformed fixture or an invariant violation, not eventual consistency.
func (h *Harness) Eventually(ctx context.Context, condition func() (bool, error)) error {
	if condition == nil {
		return fmt.Errorf("integration harness: nil wait condition")
	}
	interval := 100 * time.Millisecond
	if h != nil && h.PollInterval > 0 {
		interval = h.PollInterval
	}
	for {
		ready, err := condition()
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return fmt.Errorf("integration harness wait: %w", ctx.Err())
		case <-timer.C:
		}
	}
}
