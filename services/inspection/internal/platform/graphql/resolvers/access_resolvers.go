package resolvers

import (
	"context"

	"inspection/libs/identity"
	assignaccess "inspection/services/inspection/internal/features/access/assign_membership_access"
	explainaccess "inspection/services/inspection/internal/features/access/explain_effective_access"
	invitecustomer "inspection/services/inspection/internal/features/access/invite_customer_user"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

// membershipUserError converts validation and state outcomes into a payload
// user error. Authorization, availability and internal failures stay GraphQL
// errors so they can never be mistaken for a correctable form problem.
func membershipUserError(err error) (*graphql1.UserError, bool) {
	code, _, _ := apperror.Public(err)
	switch code {
	case apperror.InvalidInput, apperror.Conflict, apperror.InvalidState:
		return userErrorFrom(err), true
	default:
		return nil, false
	}
}

func parseScopeInputs(inputs []*graphql1.ScopeAssignmentInput) ([]scopeInput, error) {
	scopes := make([]scopeInput, 0, len(inputs))
	for _, input := range inputs {
		if input == nil {
			return nil, graphql1Error("scopes")
		}
		id, err := identity.ParseID(input.ResourceID)
		if err != nil {
			return nil, invalidID("scopes")
		}
		scopes = append(scopes, scopeInput{Kind: input.Kind, ID: id})
	}
	return scopes, nil
}

// scopeInput is the parsed transport form of one requested scope.
type scopeInput struct {
	Kind string
	ID   identity.ID
}

func (r *mutationResolver) inviteCustomerUser(ctx context.Context, input graphql1.InviteCustomerUserInput) (*graphql1.MembershipPayload, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	parsed, err := parseScopeInputs(input.Scopes)
	if err != nil {
		return nil, err
	}
	scopes := make([]invitecustomer.Scope, 0, len(parsed))
	for _, scope := range parsed {
		scopes = append(scopes, invitecustomer.Scope{Kind: scope.Kind, ResourceID: scope.ID})
	}
	raw, err := r.Bus.Send(ctx, invitecustomer.Command{TenantID: meta.TenantID, Name: input.Name, Email: input.Email, Scopes: scopes, ProductEntitlements: input.ProductEntitlements, ClientID: requestctx.IdempotencyKey(ctx, input.ClientMutationID)})
	if err != nil {
		if userError, ok := membershipUserError(err); ok {
			return &graphql1.MembershipPayload{UserErrors: []*graphql1.UserError{userError}, ClientMutationID: input.ClientMutationID}, nil
		}
		return nil, err
	}
	result := raw.(invitecustomer.Result)
	membership, err := mapMembership(ctx, r.DB, result.Membership)
	if err != nil {
		return nil, err
	}
	membership.InvitationStatus = result.InvitationStatus
	return &graphql1.MembershipPayload{Membership: membership, UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}, nil
}

func (r *mutationResolver) assignMembershipAccess(ctx context.Context, input graphql1.AssignMembershipAccessInput) (*graphql1.MembershipPayload, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	membershipID, err := identity.ParseID(input.MembershipID)
	if err != nil {
		return nil, invalidID("membershipId")
	}
	parsed, err := parseScopeInputs(input.Scopes)
	if err != nil {
		return nil, err
	}
	scopes := make([]assignaccess.Scope, 0, len(parsed))
	for _, scope := range parsed {
		scopes = append(scopes, assignaccess.Scope{Kind: scope.Kind, ResourceID: scope.ID})
	}
	raw, err := r.Bus.Send(ctx, assignaccess.Command{TenantID: meta.TenantID, MembershipID: membershipID, Role: input.Role, ProductEntitlements: input.ProductEntitlements, Scopes: scopes, ExpectedVersion: int64(input.ExpectedVersion)})
	if err != nil {
		if userError, ok := membershipUserError(err); ok {
			return &graphql1.MembershipPayload{UserErrors: []*graphql1.UserError{userError}, ClientMutationID: input.ClientMutationID}, nil
		}
		return nil, err
	}
	mapped, err := mapMembership(ctx, r.DB, raw.(database.Membership))
	if err != nil {
		return nil, err
	}
	return &graphql1.MembershipPayload{Membership: mapped, UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}, nil
}

func (r *queryResolver) effectiveAccess(ctx context.Context, membershipID string, first *int, after *string) (*graphql1.EffectiveAccess, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok || meta.Principal.IdentityID == (identity.ID{}) {
		return nil, unauthenticated()
	}
	id, err := identity.ParseID(membershipID)
	if err != nil {
		return nil, invalidID("membershipId")
	}
	raw, err := r.Bus.Ask(ctx, explainaccess.EffectiveAccessQuery{TenantID: meta.TenantID, MembershipID: id, First: intValue(first), After: stringValue(after)})
	if err != nil {
		return nil, err
	}
	result := raw.(explainaccess.EffectiveAccessResult)
	grants := make([]*graphql1.AccessResource, 0, len(result.Grants))
	for _, grant := range result.Grants {
		grants = append(grants, mapAccessResource(grant))
	}
	return &graphql1.EffectiveAccess{
		MembershipID: result.MembershipID.String(), Role: result.Role, Status: result.Status, Version: int(result.Version),
		ProductEntitlements: result.ProductEntitlements, TenantWide: result.TenantWide, Grants: grants,
		Descendants: mapAccessEntries(result.Descendants),
	}, nil
}

func (r *queryResolver) scopePreview(ctx context.Context, kind string, resourceID string, first *int, after *string) (*graphql1.ScopePreview, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok || meta.Principal.IdentityID == (identity.ID{}) {
		return nil, unauthenticated()
	}
	id, err := identity.ParseID(resourceID)
	if err != nil {
		return nil, invalidID("resourceId")
	}
	raw, err := r.Bus.Ask(ctx, explainaccess.ScopePreviewQuery{TenantID: meta.TenantID, Kind: kind, ResourceID: id, First: intValue(first), After: stringValue(after)})
	if err != nil {
		return nil, err
	}
	result := raw.(explainaccess.ScopePreviewResult)
	return &graphql1.ScopePreview{Target: mapAccessResource(result.Target), Descendants: mapAccessEntries(result.Descendants)}, nil
}

func mapAccessResource(resource explainaccess.Resource) *graphql1.AccessResource {
	return &graphql1.AccessResource{Kind: resource.Kind, ResourceID: resource.ResourceID.String(), Name: resource.Name, Status: resource.Status, Historical: resource.Historical, Grantable: resource.Grantable()}
}

func mapAccessEntries(page explainaccess.Page) *graphql1.EffectiveAccessEntryConnection {
	nodes := make([]*graphql1.EffectiveAccessEntry, 0, len(page.Entries))
	for _, entry := range page.Entries {
		nodes = append(nodes, &graphql1.EffectiveAccessEntry{
			Kind: entry.Kind, ResourceID: entry.ResourceID.String(), Name: entry.Name, Status: entry.Status,
			Historical: entry.Historical, Grantable: entry.Grantable(),
			InheritedFrom: &graphql1.Scope{Kind: entry.InheritedFrom.Kind, ResourceID: entry.InheritedFrom.ID.String()},
		})
	}
	return &graphql1.EffectiveAccessEntryConnection{Nodes: nodes, PageInfo: pageInfo(page.EndCursor, page.HasNextPage)}
}

// membershipProducts loads the product entitlements of one membership inside
// its tenant transaction.
func membershipProducts(tx *gorm.DB, tenantID, membershipID identity.ID) ([]string, error) {
	var rows []database.ProductEntitlement
	if err := tx.Where("tenant_id = ? AND membership_id = ?", tenantID, membershipID).Order("product").Find(&rows).Error; err != nil {
		return nil, err
	}
	products := make([]string, 0, len(rows))
	for _, row := range rows {
		products = append(products, row.Product)
	}
	return products, nil
}
