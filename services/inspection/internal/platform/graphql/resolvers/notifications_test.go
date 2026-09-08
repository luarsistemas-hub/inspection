package resolvers

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestNotificationsAllowTenantAdmin(t *testing.T) {
	tenantID := identity.NewID()
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{
		TenantID: tenantID,
		Principal: requestctx.Principal{
			IdentityID: identity.NewID(),
			TenantID:   tenantID,
			Roles:      []string{auth.TenantAdmin},
		},
	})

	result, err := (&queryResolver{Resolver: &Resolver{}}).MyNotifications(ctx, nil, nil, nil)
	if err != nil {
		t.Fatalf("tenant admin notifications denied: %v", err)
	}
	if result.UnreadCount != 0 || len(result.Nodes) != 0 {
		t.Fatalf("unexpected empty notification result: %+v", result)
	}
}
