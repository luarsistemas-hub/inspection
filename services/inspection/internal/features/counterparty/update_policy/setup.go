// Package update_policy changes the tenant contestation deadline used by new invitations.
package update_policy

import (
	"context"
	"errors"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/counterparty/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command sets the contestation deadline in calendar days.
type Command struct {
	TenantID identity.ID
	Days     int
}

// Query reads the tenant contestation deadline without changing it.
type Query struct{ TenantID identity.ID }

// Roles may change the baseline policy.
var Roles = []string{auth.TenantAdmin, auth.InspectionConfigAdmin}

// Dependencies are the collaborators required by this operation.
type Dependencies struct {
	Bus        *mediator.Bus
	Service    core.Service
	Authorizer auth.Authorizer
}

// Setup validates dependencies and registers baseline policy updates.
func Setup(deps Dependencies) error {
	if deps.Bus == nil || deps.Service.DB == nil || deps.Authorizer.Store == nil {
		return errors.New("counterparty update policy: missing dependency")
	}
	if err := deps.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		query := raw.(Query)
		if query.TenantID == (identity.ID{}) {
			return nil, errors.New("counterparty update policy: tenant is required")
		}
		if _, err := deps.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: query.TenantID, Product: auth.AdminProduct, Roles: Roles}); err != nil {
			return nil, err
		}
		return deps.Service.Policy(ctx, query.TenantID)
	}); err != nil {
		return err
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		if _, err := deps.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: command.TenantID, Product: auth.AdminProduct, Roles: Roles, Mutate: true}); err != nil {
			return nil, err
		}
		return deps.Service.UpdatePolicy(ctx, command.TenantID, command.Days)
	})
}
