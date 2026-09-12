package dispatch_outbox

import (
	"context"
	"errors"
	"time"

	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/observability"

	"gorm.io/gorm"
)

type Dependencies struct {
	DB        *gorm.DB
	Publisher messaging.Publisher
	BatchSize int
	Interval  time.Duration
	Metrics   *observability.Metrics
}

func Setup(deps Dependencies) (func(context.Context) error, error) {
	if deps.DB == nil || deps.Publisher == nil {
		return nil, errors.New("dispatch outbox: missing dependency")
	}
	if deps.Interval <= 0 {
		deps.Interval = 250 * time.Millisecond
	}
	dispatcher := messaging.Dispatcher{DB: deps.DB, Publisher: deps.Publisher, BatchSize: deps.BatchSize, Metrics: deps.Metrics}
	return func(ctx context.Context) error {
		ticker := time.NewTicker(deps.Interval)
		defer ticker.Stop()
		for {
			if _, err := dispatcher.Dispatch(ctx); err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
		}
	}, nil
}
