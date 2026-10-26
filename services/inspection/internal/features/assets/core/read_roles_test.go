package core

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestAuditorMayListAssets(t *testing.T) {
	tenant := identity.NewID()
	ctx := func(role string) context.Context {
		return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenant, Roles: []string{role}}})
	}
	// Without business unit scopes the listing is empty before any database read.
	views, _, _, err := (Service{}).List(ctx(auth.Auditor), tenant, nil, "", 10, "")
	if err != nil || len(views) != 0 {
		t.Fatalf("auditor listing = %v, %v", views, err)
	}
	if _, _, _, err := (Service{}).List(ctx(auth.GovernanceAdmin), tenant, nil, "", 10, ""); !apperror.Is(err, apperror.Forbidden) {
		t.Fatalf("governance admin listing = %v, want forbidden", err)
	}
}
