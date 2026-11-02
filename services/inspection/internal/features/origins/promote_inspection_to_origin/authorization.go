package promote_inspection_to_origin

import (
	"context"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

func authorizeNewWork(ctx context.Context, authorizer auth.Authorizer, tenantID identity.ID, roles []string, businessUnitID, inspectionID identity.ID) (requestctx.Principal, error) {
	_, err := authorizer.Authorize(ctx, auth.AuthorizationRequest{
		TenantID: tenantID,
		Roles:    roles,
		Resource: &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: businessUnitID},
		Mutate:   true,
	})
	if err != nil {
		return requestctx.Principal{}, err
	}
	return authorizer.Authorize(ctx, auth.AuthorizationRequest{
		TenantID: tenantID,
		Roles:    roles,
		Resource: &requestctx.Scope{Kind: "INSPECTION", ID: inspectionID},
		Mutate:   true,
		NewWork:  true,
	})
}
