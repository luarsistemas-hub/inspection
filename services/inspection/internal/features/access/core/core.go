// Package core holds the access rules shared by the invitation and
// membership-access slices: identity validation, explicit scope validation,
// atomic access replacement, and synchronous invitation provisioning. It owns
// no GraphQL or mediator wiring; each slice keeps its own public setup.
package core

import (
	"net/mail"
	"strings"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// MaxScopes is the largest explicit grant set accepted by one command.
const MaxScopes = 100

// Scope is one explicit hierarchical grant requested for a membership.
type Scope struct {
	Kind       string
	ResourceID identity.ID
}

// KnownScopeKind reports whether kind is a grantable resource kind.
func KnownScopeKind(kind string) bool {
	switch kind {
	case "BUSINESS_UNIT", "ASSET", "PROJECT", "INSPECTION":
		return true
	default:
		return false
	}
}

// NormalizeIdentity validates and normalizes the person being invited.
func NormalizeIdentity(name, email string) (string, string, error) {
	name, email = strings.TrimSpace(name), strings.ToLower(strings.TrimSpace(email))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return "", "", apperror.New(apperror.InvalidInput, "email", "Informe um e-mail válido.")
	}
	if name == "" || len([]rune(name)) > 200 {
		return "", "", apperror.New(apperror.InvalidInput, "name", "Informe o nome da pessoa (até 200 caracteres).")
	}
	return name, email, nil
}

// ValidateScopeShape rejects unknown kinds, empty ids, duplicates, and
// oversized batches before any database access.
func ValidateScopeShape(scopes []Scope) error {
	if len(scopes) > MaxScopes {
		return apperror.New(apperror.InvalidInput, "scopes", "Informe no máximo 100 abrangências por vez.")
	}
	seen := make(map[Scope]struct{}, len(scopes))
	for _, scope := range scopes {
		if scope.ResourceID == (identity.ID{}) || !KnownScopeKind(scope.Kind) {
			return apperror.New(apperror.InvalidInput, "scopes", "A abrangência informada não é válida.")
		}
		if _, duplicate := seen[scope]; duplicate {
			return apperror.New(apperror.InvalidInput, "scopes", "A abrangência informada está repetida.")
		}
		seen[scope] = struct{}{}
	}
	return nil
}

// RequireTenantResources verifies, inside the tenant transaction, that every
// scope target exists in the tenant. A foreign or unknown resource is rejected
// without revealing whether it exists elsewhere.
func RequireTenantResources(tx *gorm.DB, tenantID identity.ID, scopes []Scope) error {
	for _, scope := range scopes {
		var model any
		switch scope.Kind {
		case "BUSINESS_UNIT":
			model = &database.BusinessUnit{}
		case "ASSET":
			model = &database.Asset{}
		case "PROJECT":
			model = &database.Project{}
		case "INSPECTION":
			model = &database.Inspection{}
		default:
			return apperror.New(apperror.InvalidInput, "scopes", "A abrangência informada não é válida.")
		}
		var count int64
		if err := tx.Model(model).Where("tenant_id = ? AND id = ?", tenantID, scope.ResourceID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return apperror.New(apperror.InvalidInput, "scopes", "A abrangência informada não pertence a esta imobiliária.")
		}
	}
	return nil
}

// RequireDataBearingScope enforces that a customer membership always carries at
// least one explicit grant, so it can never become a data-bearing Dashboard
// membership without a resource target.
func RequireDataBearingScope(role string, scopes []Scope) error {
	if role == auth.CustomerViewer && len(scopes) == 0 {
		return apperror.New(apperror.InvalidInput, "scopes", "Selecione ao menos uma abrangência para o cliente.")
	}
	return nil
}

// LockTenantAdmins serializes the changes that can remove a tenant
// administrator (disable, demote) for one tenant until the transaction ends, so
// two concurrent changes cannot each see the other administrator as remaining.
func LockTenantAdmins(tx *gorm.DB, tenantID identity.ID) error {
	return tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "tenant-admins:"+tenantID.String()).Error
}

// RequireAnotherTenantAdmin protects the last active tenant administrator. It
// fails with INVALID_STATE when removing target (disabling or demoting it)
// would leave none. Callers hold LockTenantAdmins and use it only for an
// active TENANT_ADMIN target, inside the tenant transaction.
func RequireAnotherTenantAdmin(tx *gorm.DB, tenantID, targetID identity.ID) error {
	var others int64
	if err := tx.Model(&database.Membership{}).Where("tenant_id = ? AND role = ? AND status = 'ACTIVE' AND id <> ?", tenantID, auth.TenantAdmin, targetID).Count(&others).Error; err != nil {
		return err
	}
	if others == 0 {
		return apperror.New(apperror.InvalidState, "membershipId", "A imobiliária precisa manter ao menos um administrador ativo.")
	}
	return nil
}
