package auth

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"
)

// GORMMembershipStore resolves current membership and scopes without trusting token roles.
type GORMMembershipStore struct{ DB *gorm.DB }

func (s GORMMembershipStore) ResolveOIDC(ctx context.Context, issuer, subject string) (requestctx.Principal, error) {
	if s.DB == nil {
		return requestctx.Principal{}, fmt.Errorf("membership store: missing database")
	}
	if issuer == "" || subject == "" {
		return requestctx.Principal{}, gorm.ErrRecordNotFound
	}
	return requestctx.Principal{
		IdentityID: identity.NewDeterministicID("inspection/oidc-identity", issuer+"\x00"+subject),
		Issuer:     issuer,
		Subject:    subject,
	}, nil
}

func (s GORMMembershipStore) Resolve(ctx context.Context, tenantID, identityID identity.ID) (requestctx.Principal, error) {
	if s.DB == nil {
		return requestctx.Principal{}, fmt.Errorf("membership store: missing database")
	}
	var membership database.Membership
	var tenant database.Tenant
	var rows []database.ResourceScope
	var entitlements []database.ProductEntitlement
	if err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND identity_id=?", tenantID, identityID).First(&membership).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND id=?", tenantID, tenantID).First(&tenant).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND membership_id=?", tenantID, membership.ID).Find(&rows).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND membership_id=?", tenantID, membership.ID).Find(&entitlements).Error
	}); err != nil {
		return requestctx.Principal{}, err
	}
	p := requestctx.Principal{IdentityID: identityID, MembershipID: membership.ID, MembershipVersion: membership.Version, TenantID: tenantID, Roles: []string{membership.Role}, Disabled: membership.Status != "ACTIVE" || tenant.Status != "ACTIVE"}
	for _, entitlement := range entitlements {
		p.ProductEntitlements = append(p.ProductEntitlements, entitlement.Product)
	}
	for _, row := range rows {
		p.Scopes = append(p.Scopes, requestctx.Scope{Kind: row.Kind, ID: row.ResourceID})
	}
	return p, nil
}

// ResolveMembership reloads the selected membership and validates that it is
// owned by the verified OIDC identity before setting any tenant context.
func (s GORMMembershipStore) ResolveMembership(ctx context.Context, issuer, subject string, membershipID identity.ID) (requestctx.Principal, error) {
	if s.DB == nil || issuer == "" || subject == "" || membershipID == (identity.ID{}) {
		return requestctx.Principal{}, fmt.Errorf("membership store: invalid membership context")
	}
	var membership database.Membership
	if err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT set_config('app.oidc_issuer', ?, true)`, issuer).Error; err != nil {
			return err
		}
		if err := tx.Exec(`SELECT set_config('app.oidc_subject', ?, true)`, subject).Error; err != nil {
			return err
		}
		return tx.Where("id=? AND issuer=? AND subject=?", membershipID, issuer, subject).First(&membership).Error
	}); err != nil {
		return requestctx.Principal{}, err
	}
	principal, err := s.Resolve(ctx, membership.TenantID, membership.IdentityID)
	if err != nil {
		return requestctx.Principal{}, err
	}
	if principal.MembershipID != membershipID {
		return requestctx.Principal{}, gorm.ErrRecordNotFound
	}
	principal.Issuer, principal.Subject = issuer, subject
	return principal, nil
}
