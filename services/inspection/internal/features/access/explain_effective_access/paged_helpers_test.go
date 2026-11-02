package explain_effective_access

import (
	"context"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/access/accesstest"
	"inspection/services/inspection/internal/platform/database"
)

// accesstestContext binds one authenticated Admin request to its tenant.
type accesstestContext struct {
	ctx      context.Context
	tenantID identity.ID
}

func newOwner(membership database.Membership, tenantID identity.ID) accesstestContext {
	return accesstestContext{ctx: accesstest.Context(membership, "inspection-admin"), tenantID: tenantID}
}
