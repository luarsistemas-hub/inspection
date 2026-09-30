package lookup_postal_code

import (
	"context"
	"errors"

	"inspection/services/inspection/internal/platform/mediator"
)

type Query struct{ PostalCode, ClientKey string }

func Setup(bus *mediator.Bus, service *Service) error {
	if bus == nil || service == nil {
		return errors.New("slice addresses/lookup_postal_code: missing dependency")
	}
	return bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		query := raw.(Query)
		return service.Lookup(ctx, query.PostalCode, query.ClientKey)
	})
}
