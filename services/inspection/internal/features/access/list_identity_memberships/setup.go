// Package listidentitymemberships exposes the identity-owned tenant selector.
package listidentitymemberships

import (
	"context"
	"fmt"
	"strings"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

const sliceName = "access/list_identity_memberships"

// Query lists only membership summaries owned by one verified OIDC subject.
type Query struct {
	Issuer, Subject, After string
	First                  int
}

// Summary contains selector-safe membership and tenant state, never business data.
type Summary struct {
	MembershipID, TenantID                           identity.ID
	TenantName, TenantStatus, Role, MembershipStatus string
	MembershipVersion                                int64
}

// Result is a stable cursor page for the identity-owned selector.
type Result struct {
	Nodes       []Summary
	EndCursor   string
	HasNextPage bool
}

// Dependencies wire the slice's sole query handler.
type Dependencies struct {
	DB  *gorm.DB
	Bus *mediator.Bus
}

// Setup registers the identity-owned membership selector.
func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil {
		return fmt.Errorf("slice %s: missing dependency", sliceName)
	}
	return deps.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		return handle(ctx, deps.DB, raw.(Query))
	})
}

func handle(ctx context.Context, db *gorm.DB, query Query) (Result, error) {
	query.Issuer, query.Subject = strings.TrimSpace(query.Issuer), strings.TrimSpace(query.Subject)
	if query.Issuer == "" || query.Subject == "" {
		return Result{}, apperror.New(apperror.Unauthenticated, "", "authentication required")
	}
	if query.First <= 0 {
		query.First = 25
	}
	if query.First > 100 {
		return Result{}, apperror.New(apperror.InvalidInput, "first", "first must be at most 100")
	}
	var after identity.ID
	var err error
	if query.After != "" {
		after, err = identity.ParseID(query.After)
		if err != nil {
			return Result{}, apperror.New(apperror.InvalidInput, "after", "invalid cursor")
		}
	}
	var memberships []database.Membership
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT set_config('app.oidc_issuer', ?, true)`, query.Issuer).Error; err != nil {
			return err
		}
		if err := tx.Exec(`SELECT set_config('app.oidc_subject', ?, true)`, query.Subject).Error; err != nil {
			return err
		}
		statement := tx.Where("issuer=? AND subject=?", query.Issuer, query.Subject).Order("id ASC").Limit(query.First + 1)
		if query.After != "" {
			statement = statement.Where("id > ?", after)
		}
		return statement.Find(&memberships).Error
	})
	if err != nil {
		return Result{}, err
	}
	result := Result{HasNextPage: len(memberships) > query.First}
	if result.HasNextPage {
		memberships = memberships[:query.First]
	}
	for _, membership := range memberships {
		var tenant database.Tenant
		if err := (tenanttx.Runner{DB: db}).Within(ctx, membership.TenantID, func(tx *gorm.DB) error {
			return tx.Where("id=?", membership.TenantID).First(&tenant).Error
		}); err != nil {
			return Result{}, err
		}
		result.Nodes = append(result.Nodes, Summary{MembershipID: membership.ID, TenantID: membership.TenantID, TenantName: tenant.Name, TenantStatus: tenant.Status, Role: membership.Role, MembershipStatus: membership.Status, MembershipVersion: membership.Version})
	}
	if len(memberships) > 0 {
		result.EndCursor = memberships[len(memberships)-1].ID.String()
	}
	return result, nil
}
