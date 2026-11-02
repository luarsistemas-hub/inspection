// Package disable_membership owns immediate revocation of an internal membership.
package disable_membership

import (
	"context"
	"fmt"
	"time"

	"inspection/libs/identity"
	accesscore "inspection/services/inspection/internal/features/access/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

// Command disables a membership using optimistic concurrency.
type Command struct {
	TenantID, MembershipID identity.ID
	ExpectedVersion        int64
}

// Dependencies are the adapters required by this slice.
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
	Now        func() time.Time
}

// Setup registers this slice with the application mediator.
func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil {
		return fmt.Errorf("slice access/disable_membership: missing dependency")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		return handle(ctx, deps, raw.(Command))
	})
}

func handle(ctx context.Context, deps Dependencies, cmd Command) (database.Membership, error) {
	if cmd.TenantID == (identity.ID{}) || cmd.MembershipID == (identity.ID{}) || cmd.ExpectedVersion < 1 {
		return database.Membership{}, apperror.New(apperror.InvalidInput, "membership", "Informe o acesso e a versão atual.")
	}
	principal, err := deps.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: cmd.TenantID, Product: auth.AdminProduct, Roles: []string{auth.TenantAdmin, auth.AccessAdmin}, Mutate: true})
	if err != nil {
		return database.Membership{}, err
	}
	if cmd.MembershipID == principal.MembershipID {
		return database.Membership{}, apperror.New(apperror.Forbidden, "membershipId", "Você não pode desativar o próprio acesso.")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	var membership database.Membership
	err = (tenanttx.Runner{DB: deps.DB}).Within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		if err := accesscore.LockTenantAdmins(tx, cmd.TenantID); err != nil {
			return err
		}
		if err := tx.Where("tenant_id = ? AND id = ?", cmd.TenantID, cmd.MembershipID).First(&membership).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return apperror.New(apperror.NotFound, "membershipId", "Acesso não encontrado.")
			}
			return err
		}
		if !auth.CanDelegateRole(principal.Roles, membership.Role) {
			return apperror.New(apperror.Forbidden, "membershipId", "Você não pode alterar este acesso.")
		}
		if membership.Status == "DISABLED" {
			return nil
		}
		if membership.Version != cmd.ExpectedVersion {
			return apperror.New(apperror.Conflict, "expectedVersion", "Este acesso foi alterado por outra pessoa. Atualize a página e tente novamente.")
		}
		if membership.Role == auth.TenantAdmin && membership.Status == "ACTIVE" {
			if err := accesscore.RequireAnotherTenantAdmin(tx, cmd.TenantID, membership.ID); err != nil {
				return err
			}
		}
		membership.Status, membership.Version, membership.UpdatedAt = "DISABLED", membership.Version+1, deps.Now().UTC()
		return tx.Model(&database.Membership{}).Where("tenant_id = ? AND id = ? AND version = ?", cmd.TenantID, cmd.MembershipID, cmd.ExpectedVersion).Updates(map[string]any{"status": membership.Status, "version": membership.Version, "updated_at": membership.UpdatedAt}).Error
	})
	if err != nil {
		return database.Membership{}, err
	}
	return membership, nil
}
