// Package view_baseline returns the baseline and response state seen by a counterparty.
package view_baseline

import (
	"context"
	"errors"

	"inspection/services/inspection/internal/features/counterparty/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Query identifies the invitation bound to the verified external session.
type Query struct{ Owner core.SessionOwner }

// Dependencies are the collaborators required by this operation.
type Dependencies struct {
	Bus     *mediator.Bus
	Service core.Service
}

// Setup validates dependencies and registers the counterparty baseline query.
func Setup(deps Dependencies) error {
	if deps.Bus == nil || deps.Service.DB == nil {
		return errors.New("counterparty view baseline: missing dependency")
	}
	return deps.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		return deps.Service.View(ctx, raw.(Query).Owner)
	})
}
