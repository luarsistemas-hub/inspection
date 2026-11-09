package resolvers

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

type revokedReportMembership struct{}

func (revokedReportMembership) Resolve(context.Context, identity.ID, identity.ID) (requestctx.Principal, error) {
	return requestctx.Principal{}, apperror.New(apperror.NotFound, "", "Recurso não encontrado.")
}

// UT-046: a revoked staff membership is rejected before internal report or
// private-media reads can reach their data adapters.
func TestUT046RevokedStaffCannotReadInternalReportOrPrivateMedia(t *testing.T) {
	tenantID := identity.NewID()
	meta := requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.Manager}}}
	ctx := requestctx.WithMetadata(context.Background(), meta)
	resolver := &queryResolver{Resolver: &Resolver{Authorizer: auth.Authorizer{Store: revokedReportMembership{}}}}
	version := 1
	if report, err := resolver.Report(ctx, identity.NewID().String(), &version); err == nil || report != nil {
		t.Fatalf("revoked staff received report=%+v err=%v", report, err)
	}
	if download, err := resolver.ReportDownload(ctx, identity.NewID().String(), nil); err == nil || download != nil {
		t.Fatalf("revoked staff received private media=%+v err=%v", download, err)
	}
}

// UT-053: a revoked customer membership cannot fetch either the report body or
// its media download; both are denied before database or storage access.
func TestUT053RevokedCustomerCannotReadReportOrMedia(t *testing.T) {
	tenantID := identity.NewID()
	meta := requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.CustomerViewer}}}
	ctx := requestctx.WithMetadata(context.Background(), meta)
	resolver := &queryResolver{Resolver: &Resolver{Authorizer: auth.Authorizer{Store: revokedReportMembership{}}}}
	if report, err := resolver.customerReport(ctx, identity.NewID().String(), nil); err == nil || report != nil {
		t.Fatalf("revoked customer received report=%+v err=%v", report, err)
	}
	if download, err := resolver.ReportDownload(ctx, identity.NewID().String(), nil); err == nil || download != nil {
		t.Fatalf("revoked customer received media=%+v err=%v", download, err)
	}
}
