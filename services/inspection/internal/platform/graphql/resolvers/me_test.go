package resolvers

import (
	"context"
	"testing"

	"inspection/libs/identity"
	listidentitymemberships "inspection/services/inspection/internal/features/access/list_identity_memberships"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestMeListsOnlyIdentityOwnedMembershipSummariesBeforeSelection(t *testing.T) {
	bus := mediator.New()
	membershipID, tenantID := identity.NewID(), identity.NewID()
	if err := bus.RegisterQuery(listidentitymemberships.Query{}, func(_ context.Context, raw any) (any, error) {
		query := raw.(listidentitymemberships.Query)
		if query.Issuer != "http://issuer.example" || query.Subject != "subject-1" {
			t.Fatalf("unexpected identity selector: %#v", query)
		}
		return listidentitymemberships.Result{Nodes: []listidentitymemberships.Summary{{
			MembershipID: membershipID, TenantID: tenantID, Role: "TENANT_ADMIN",
			MembershipStatus: "ACTIVE", MembershipVersion: 3, TenantStatus: "ACTIVE",
		}}}, nil
	}); err != nil {
		t.Fatal(err)
	}

	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{Principal: requestctx.Principal{
		IdentityID: identity.NewID(), Issuer: "http://issuer.example", Subject: "subject-1",
		Audience: "inspection-admin", Product: "ADMIN",
	}})
	got, err := (&queryResolver{Resolver: &Resolver{Bus: bus}}).Me(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Memberships) != 1 {
		t.Fatalf("got %d memberships, want 1", len(got.Memberships))
	}
	membership := got.Memberships[0]
	if membership.ID != membershipID.String() || membership.TenantID != tenantID.String() || membership.Role != "TENANT_ADMIN" || membership.Status != "ACTIVE" || membership.Version != 3 {
		t.Fatalf("unexpected membership summary: %#v", membership)
	}
	if len(membership.Scopes) != 0 || len(got.Roles) != 0 || len(got.ProductEntitlements) != 0 {
		t.Fatalf("selector leaked selected-membership authorization: %#v", got)
	}
}
