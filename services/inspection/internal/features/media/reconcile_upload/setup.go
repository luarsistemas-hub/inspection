// Package reconcile_upload exposes server-confirmed multipart recovery.
package reconcile_upload

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/media/core"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"
)

// Query requests the current durable upload recovery state.
type Query struct {
	TenantID, ResponsibilityID, MediaID identity.ID
}

// Dependencies contains the collaborators required by the recovery slice.
type Dependencies struct {
	Bus     *mediator.Bus
	Service core.Service
}

// Setup registers the recovery query with the application mediator.
func Setup(d Dependencies) error {
	if d.Bus == nil || d.Service.DB == nil || d.Service.Store.Client == nil {
		return fmt.Errorf("slice media/reconcile_upload: missing dependency")
	}
	if _, ok := d.Service.Store.Client.(objectstore.MultipartInspector); !ok {
		return fmt.Errorf("slice media/reconcile_upload: multipart recovery is unavailable")
	}
	return d.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		query := raw.(Query)
		return d.Service.Reconcile(ctx, query.TenantID, query.ResponsibilityID, query.MediaID)
	})
}
