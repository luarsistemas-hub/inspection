package listidentitymemberships

import (
	"context"

	"inspection/services/inspection/internal/platform/requestctx"
)

func accesstestContext(principal requestctx.Principal) context.Context {
	principal.Audience, principal.Product = "inspection-dashboard", "DASHBOARD"
	return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: principal.TenantID, Principal: principal})
}
