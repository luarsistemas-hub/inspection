package assign_role_scope

import (
	"context"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxBatch = 100

type Assignment struct {
	Kind       string
	ResourceID identity.ID
}
type Command struct {
	TenantID, MembershipID identity.ID
	Role                   string
	Assignments            []Assignment
	ExpectedVersion        int64
}
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
}

func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil {
		return fmt.Errorf("slice access/assign_role_scope: missing dependency")
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) { return handle(ctx, deps, raw.(Command)) })
}
func handle(ctx context.Context, deps Dependencies, cmd Command) (int, error) {
	if !validRole(cmd.Role) {
		return 0, apperror.New(apperror.InvalidInput, "role", "unknown role")
	}
	if len(cmd.Assignments) > maxBatch {
		return 0, apperror.New(apperror.InvalidInput, "assignments", "batch exceeds 100")
	}
	principal, err := deps.Authorizer.Authorize(ctx, cmd.TenantID, []string{auth.TenantAdmin, auth.Manager}, nil, true)
	if err != nil {
		return 0, err
	}
	if cmd.Role == auth.TenantAdmin && !has(principal.Roles, auth.TenantAdmin) {
		return 0, apperror.New(apperror.Forbidden, "", "access denied")
	}
	err = (tenanttx.Runner{DB: deps.DB}).Within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		var membership database.Membership
		if err := tx.Where("tenant_id = ? AND id = ?", cmd.TenantID, cmd.MembershipID).First(&membership).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return apperror.New(apperror.NotFound, "membershipId", "membership not found")
			}
			return err
		}
		if membership.Status != "ACTIVE" {
			return apperror.New(apperror.InvalidState, "membershipId", "membership is disabled")
		}
		if cmd.ExpectedVersion > 0 && membership.Version != cmd.ExpectedVersion {
			return apperror.New(apperror.Conflict, "expectedVersion", "stale membership version")
		}
		for _, a := range cmd.Assignments {
			if err := validateAssignment(tx, cmd.TenantID, cmd.Role, a); err != nil {
				return err
			}
			scope := database.ResourceScope{ID: identity.NewID(), TenantID: cmd.TenantID, MembershipID: cmd.MembershipID, Kind: a.Kind, ResourceID: a.ResourceID}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&scope).Error; err != nil {
				return err
			}
		}
		membership.Role = cmd.Role
		membership.Version++
		membership.UpdatedAt = time.Now().UTC()
		return tx.Model(&database.Membership{}).Where("tenant_id = ? AND id = ? AND version = ?", cmd.TenantID, cmd.MembershipID, membership.Version-1).Updates(map[string]any{"role": membership.Role, "version": membership.Version, "updated_at": membership.UpdatedAt}).Error
	})
	if err != nil {
		return 0, err
	}
	return len(cmd.Assignments), nil
}
func validRole(r string) bool {
	return r == auth.TenantAdmin || r == auth.Manager || r == auth.Employee || r == auth.Viewer
}
func has(v []string, w string) bool {
	for _, x := range v {
		if x == w {
			return true
		}
	}
	return false
}

func validateAssignment(tx *gorm.DB, tenantID identity.ID, role string, assignment Assignment) error {
	if assignment.ResourceID == (identity.ID{}) || (assignment.Kind != "BUSINESS_UNIT" && assignment.Kind != "ASSET" && assignment.Kind != "INSPECTION") {
		return apperror.New(apperror.InvalidInput, "assignments", "unknown resource")
	}
	if role == auth.Viewer && assignment.Kind != "BUSINESS_UNIT" {
		return apperror.New(apperror.InvalidInput, "assignments", "viewer scope must target a business unit")
	}
	if role == auth.Manager && assignment.Kind != "BUSINESS_UNIT" {
		return apperror.New(apperror.InvalidInput, "assignments", "manager scope must target a business unit")
	}
	var count int64
	var model any
	switch assignment.Kind {
	case "BUSINESS_UNIT":
		model = &database.BusinessUnit{}
	case "ASSET":
		model = &database.Asset{}
	case "INSPECTION":
		model = &database.Inspection{}
	}
	if err := tx.Model(model).Where("tenant_id = ? AND id = ?", tenantID, assignment.ResourceID).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return apperror.New(apperror.InvalidInput, "assignments", "resource does not belong to tenant")
	}
	return nil
}
