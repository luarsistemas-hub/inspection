package main

import (
	"context"

	"inspection/services/inspection/internal/platform/messaging"
)

type videoPermit interface {
	WithPermit(context.Context, func(context.Context) (bool, error)) (bool, error)
}

type permittedDelivery struct {
	permit videoPermit
	inner  messaging.DeliveryHandler
}

func (d permittedDelivery) Process(ctx context.Context, body []byte, generation int) (bool, error) {
	return d.permit.WithPermit(ctx, func(ctx context.Context) (bool, error) {
		return d.inner.Process(ctx, body, generation)
	})
}
