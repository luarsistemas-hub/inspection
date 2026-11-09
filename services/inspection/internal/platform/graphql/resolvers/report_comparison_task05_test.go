package resolvers

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"
)

// UT-075 (error): only authorized staff can revise pairings or comparison
// decisions; internal viewers, customers, other tenants and anonymous callers
// are refused before any data is read (the resolver has no database here).
func TestUT075OnlyAuthorizedStaffCanReviseComparisonPairing(t *testing.T) {
	input := graphql1.ReviseComparisonPairingInput{InspectionID: identity.NewID().String(), ItemIdentityID: identity.NewID().String(), Pairs: []*graphql1.ComparisonPairInput{{BaselineEvidenceID: identity.NewID().String(), CurrentEvidenceID: identity.NewID().String()}}, ExpectedVersion: 1, ClientMutationID: "ut-075"}
	resolver := &mutationResolver{Resolver: &Resolver{}}
	if payload, err := resolver.reviseComparisonPairing(context.Background(), input); err == nil || payload != nil {
		t.Fatalf("anonymous caller = %+v, %v", payload, err)
	}
	tenantID := identity.NewID()
	for _, role := range []string{auth.Viewer, auth.CustomerViewer} {
		meta := requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{role}, ProductEntitlements: auth.ProductsForRole(role), Product: auth.DashboardProduct}}
		payload, err := resolver.reviseComparisonPairing(requestctx.WithMetadata(context.Background(), meta), input)
		if err == nil || payload != nil || !apperror.Is(err, apperror.Forbidden) || err.Error() != "Acesso negado." {
			t.Fatalf("%s revised a comparison: %+v, %v", role, payload, err)
		}
	}
	employee := auth.Employee
	employeeMeta := requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{employee}, ProductEntitlements: auth.ProductsForRole(employee), Product: auth.DashboardProduct}}
	employeeInput := input
	employeeInput.InspectionID = "invalid-id"
	if _, err := resolver.reviseComparisonPairing(requestctx.WithMetadata(context.Background(), employeeMeta), employeeInput); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("employee was refused before input validation: %v", err)
	}
	other := identity.NewID()
	meta := requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: other, Roles: []string{auth.Manager}, ProductEntitlements: auth.ProductsForRole(auth.Manager)}}
	if payload, err := resolver.reviseComparisonPairing(requestctx.WithMetadata(context.Background(), meta), input); err == nil || payload != nil {
		t.Fatalf("a manager of another tenant revised a comparison: %+v, %v", payload, err)
	}
}

// UT-079 (resolver side): an invalid comparison filter is refused with a safe
// Portuguese error before any report is read.
func TestUT079CustomerComparisonFilterResolverRejectsInvalidFilterSafely(t *testing.T) {
	invalid := graphql1.ComparisonOutcome("EVERYTHING_FINE")
	resolver := &queryResolver{Resolver: &Resolver{}}
	tenantID := identity.NewID()
	meta := requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.CustomerViewer}, ProductEntitlements: auth.ProductsForRole(auth.CustomerViewer)}}
	report, err := resolver.CustomerReport(requestctx.WithMetadata(context.Background(), meta), identity.NewID().String(), nil, &invalid)
	if report != nil || !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("invalid filter = %+v, %v", report, err)
	}
	if _, field, message := apperror.Public(err); field != "comparisonOutcome" || message != "Filtro de comparação inválido." {
		t.Fatalf("error = %q/%q; want the safe Portuguese filter message", field, message)
	}
	valid := graphql1.ComparisonOutcomeNoBasis
	if outcome, err := comparisonOutcomeFilter(&valid); err != nil || outcome != "NO_BASIS" {
		t.Fatalf("valid filter = %q, %v", outcome, err)
	}
}
