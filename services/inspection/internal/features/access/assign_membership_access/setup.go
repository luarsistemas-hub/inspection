// Package assign_membership_access atomically replaces the role, product
// entitlements and explicit resource scopes of one membership. A stale
// expectedVersion fails with CONFLICT and leaves every access row untouched.
package assign_membership_access

import (
	"context"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	accesscore "inspection/services/inspection/internal/features/access/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Scope is one explicit grant that the membership must hold after the command.
type Scope struct {
	Kind       string
	ResourceID identity.ID
}

// Command replaces the whole access state of a membership.
type Command struct {
	TenantID, MembershipID identity.ID
	Role                   string
	// ProductEntitlements are the products the membership holds afterwards.
	// Empty selects the role default.
	ProductEntitlements []string
	Scopes              []Scope
	// ExpectedVersion is required: the membership version the caller loaded.
	ExpectedVersion int64
}

// Dependencies are the concrete boundaries needed by this vertical slice.
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
	Now        func() time.Time
}

// Setup registers the command with the application mediator.
func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil {
		return fmt.Errorf("slice access/assign_membership_access: missing dependency")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		return handle(ctx, deps, raw.(Command))
	})
}

func handle(ctx context.Context, deps Dependencies, cmd Command) (database.Membership, error) {
	// Authorization precedes validation so an unauthenticated or unauthorized
	// caller learns nothing about input rules.
	principal, err := deps.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: cmd.TenantID, Product: auth.AdminProduct, Roles: []string{auth.TenantAdmin, auth.AccessAdmin}, Mutate: true})
	if err != nil {
		return database.Membership{}, err
	}
	if cmd.TenantID == (identity.ID{}) || cmd.MembershipID == (identity.ID{}) {
		return database.Membership{}, apperror.New(apperror.InvalidInput, "membershipId", "Informe o acesso que será alterado.")
	}
	cmd.Role = strings.TrimSpace(cmd.Role)
	if !auth.IsKnownRole(cmd.Role) {
		return database.Membership{}, apperror.New(apperror.InvalidInput, "role", "O perfil informado não é válido.")
	}
	if cmd.ExpectedVersion <= 0 {
		return database.Membership{}, apperror.New(apperror.InvalidInput, "expectedVersion", "Informe a versão atual do acesso.")
	}
	products, err := auth.NormalizeProductEntitlements(cmd.Role, cmd.ProductEntitlements)
	if err != nil {
		return database.Membership{}, err
	}
	scopes := make([]accesscore.Scope, 0, len(cmd.Scopes))
	for _, scope := range cmd.Scopes {
		scopes = append(scopes, accesscore.Scope{Kind: scope.Kind, ResourceID: scope.ResourceID})
	}
	if err := accesscore.ValidateScopeShape(scopes); err != nil {
		return database.Membership{}, err
	}
	if err := accesscore.RequireDataBearingScope(cmd.Role, scopes); err != nil {
		return database.Membership{}, err
	}
	if !auth.CanDelegateRole(principal.Roles, cmd.Role) {
		return database.Membership{}, apperror.New(apperror.Forbidden, "role", "Você não pode conceder este perfil.")
	}
	if cmd.MembershipID == principal.MembershipID && !hasRole(principal.Roles, auth.TenantAdmin) {
		return database.Membership{}, apperror.New(apperror.Forbidden, "membershipId", "Você não pode alterar o próprio acesso.")
	}
	meta, _ := requestctx.FromContext(ctx)
	if deps.Now == nil {
		deps.Now = time.Now
	}
	now := deps.Now().UTC()
	var result database.Membership
	err = (tenanttx.Runner{DB: deps.DB}).Within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		if err := accesscore.LockTenantAdmins(tx, cmd.TenantID); err != nil {
			return err
		}
		var membership database.Membership
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND id = ?", cmd.TenantID, cmd.MembershipID).First(&membership).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return apperror.New(apperror.NotFound, "membershipId", "Acesso não encontrado.")
			}
			return err
		}
		if !auth.CanDelegateRole(principal.Roles, membership.Role) {
			return apperror.New(apperror.Forbidden, "membershipId", "Você não pode alterar este acesso.")
		}
		if membership.Role == auth.TenantAdmin && membership.Status == "ACTIVE" && cmd.Role != auth.TenantAdmin {
			if err := accesscore.RequireAnotherTenantAdmin(tx, cmd.TenantID, membership.ID); err != nil {
				return err
			}
		}
		if membership.Status != "ACTIVE" && membership.Status != "INVITED" {
			return apperror.New(apperror.InvalidState, "membershipId", "Este acesso está desativado.")
		}
		if membership.Version != cmd.ExpectedVersion {
			return apperror.New(apperror.Conflict, "expectedVersion", "Este acesso foi alterado por outra pessoa. Atualize a página e tente novamente.")
		}
		if err := accesscore.RequireTenantResources(tx, cmd.TenantID, scopes); err != nil {
			return err
		}
		update := tx.Model(&database.Membership{}).Where("tenant_id = ? AND id = ? AND version = ?", cmd.TenantID, cmd.MembershipID, cmd.ExpectedVersion).
			Updates(map[string]any{"role": cmd.Role, "version": cmd.ExpectedVersion + 1, "updated_at": now})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "expectedVersion", "Este acesso foi alterado por outra pessoa. Atualize a página e tente novamente.")
		}
		if err := accesscore.ReplaceAccess(tx, cmd.TenantID, cmd.MembershipID, products, scopes, now); err != nil {
			return err
		}
		reason := fmt.Sprintf("from=%s;to=%s;products=%s;scopes=%d;version=%d", membership.Role, cmd.Role, strings.Join(products, ","), len(scopes), cmd.ExpectedVersion+1)
		if err := accesscore.RecordAudit(tx, cmd.TenantID, principal.IdentityID, "access.membership_access_assigned", "membership", cmd.MembershipID.String(), "ACCEPTED", reason, meta.CorrelationID, now); err != nil {
			return err
		}
		membership.Role, membership.Version, membership.UpdatedAt = cmd.Role, cmd.ExpectedVersion+1, now
		result = membership
		return nil
	})
	if err != nil {
		return database.Membership{}, err
	}
	return result, nil
}

func hasRole(roles []string, wanted string) bool {
	for _, role := range roles {
		if role == wanted {
			return true
		}
	}
	return false
}
