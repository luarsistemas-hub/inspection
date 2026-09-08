// Package invite_internal_user owns local membership provisioning for an OIDC identity.
package invite_internal_user

import (
	"context"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

// Command provisions or safely retries an internal membership invitation.
type Command struct {
	TenantID identity.ID
	Issuer   string
	Subject  string
	Role     string
	Scopes   []Scope
	ClientID string
}

// Scope is a requested hierarchical assignment.
type Scope struct {
	Kind       string
	ResourceID identity.ID
}

// Dependencies are the adapters assembled by the composition root.
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
	Now        func() time.Time
}

// Setup registers this slice with the application mediator.
func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil {
		return fmt.Errorf("slice access/invite_internal_user: missing dependency")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		return handle(ctx, deps, raw.(Command))
	})
}

func handle(ctx context.Context, deps Dependencies, cmd Command) (database.Membership, error) {
	cmd.Issuer, cmd.Subject, cmd.Role, cmd.ClientID = strings.TrimSpace(cmd.Issuer), strings.TrimSpace(cmd.Subject), strings.TrimSpace(cmd.Role), strings.TrimSpace(cmd.ClientID)
	if cmd.TenantID == (identity.ID{}) || cmd.Issuer == "" || cmd.Subject == "" || cmd.Role == "" || cmd.ClientID == "" {
		return database.Membership{}, apperror.New(apperror.InvalidInput, "membership", "identity, role, and client mutation id are required")
	}
	if !validRole(cmd.Role) {
		return database.Membership{}, apperror.New(apperror.InvalidInput, "role", "unknown role")
	}
	if len(cmd.Scopes) > 100 {
		return database.Membership{}, apperror.New(apperror.InvalidInput, "scopes", "batch exceeds 100")
	}
	principal, err := deps.Authorizer.Authorize(ctx, cmd.TenantID, []string{auth.TenantAdmin}, nil, true)
	if err != nil {
		return database.Membership{}, err
	}
	if cmd.Role == auth.TenantAdmin && !has(principal.Roles, auth.TenantAdmin) {
		return database.Membership{}, apperror.New(apperror.Forbidden, "role", "access denied")
	}
	if err := validateScopes(cmd.Scopes); err != nil {
		return database.Membership{}, err
	}

	var membership database.Membership
	err = (tenanttx.Runner{DB: deps.DB}).Within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		var existing database.Membership
		lookup := tx.Where("tenant_id = ? AND issuer = ? AND subject = ?", cmd.TenantID, cmd.Issuer, cmd.Subject).First(&existing)
		if lookup.Error == nil {
			if existing.Role != cmd.Role {
				return apperror.New(apperror.Conflict, "identity", "identity already has a different membership")
			}
			membership = existing
			return nil
		}
		if lookup.Error != gorm.ErrRecordNotFound {
			return lookup.Error
		}
		membership = database.Membership{ID: identity.NewID(), TenantID: cmd.TenantID, IdentityID: identity.NewDeterministicID("inspection/identity", cmd.Issuer+"\x00"+cmd.Subject), Issuer: cmd.Issuer, Subject: cmd.Subject, Role: cmd.Role, Status: "ACTIVE", Version: 1, CreatedAt: deps.Now().UTC(), UpdatedAt: deps.Now().UTC()}
		if err := tx.Create(&membership).Error; err != nil {
			return err
		}
		for _, scope := range cmd.Scopes {
			if err := validateScope(tx, cmd.TenantID, scope, cmd.Role); err != nil {
				return err
			}
			row := database.ResourceScope{ID: identity.NewID(), TenantID: cmd.TenantID, MembershipID: membership.ID, Kind: scope.Kind, ResourceID: scope.ResourceID}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		products := []string{auth.DashboardProduct}
		if cmd.Role == auth.TenantAdmin {
			products = []string{auth.AdminProduct, auth.DashboardProduct}
		}
		for _, product := range products {
			if err := tx.Create(&database.ProductEntitlement{ID: identity.NewID(), TenantID: cmd.TenantID, MembershipID: membership.ID, Product: product, CreatedAt: deps.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return database.Membership{}, err
	}
	return membership, nil
}

func validRole(role string) bool {
	return role == auth.TenantAdmin || role == auth.Manager || role == auth.Employee || role == auth.Viewer || role == auth.CustomerViewer
}

func has(roles []string, wanted string) bool {
	for _, role := range roles {
		if role == wanted {
			return true
		}
	}
	return false
}

func validateScopes(scopes []Scope) error {
	for _, scope := range scopes {
		if scope.ResourceID == (identity.ID{}) || (scope.Kind != "BUSINESS_UNIT" && scope.Kind != "ASSET" && scope.Kind != "PROJECT" && scope.Kind != "INSPECTION") {
			return apperror.New(apperror.InvalidInput, "scopes", "unknown resource scope")
		}
	}
	return nil
}

func validateScope(tx *gorm.DB, tenantID identity.ID, scope Scope, role string) error {
	if role == auth.Viewer && scope.Kind != "BUSINESS_UNIT" {
		return apperror.New(apperror.InvalidInput, "scopes", "viewer scope must target a business unit")
	}
	if role == auth.Manager && scope.Kind != "BUSINESS_UNIT" {
		return apperror.New(apperror.InvalidInput, "scopes", "manager scope must target a business unit")
	}
	var count int64
	var model any
	switch scope.Kind {
	case "BUSINESS_UNIT":
		model = &database.BusinessUnit{}
	case "ASSET":
		model = &database.Asset{}
	case "INSPECTION":
		model = &database.Inspection{}
	case "PROJECT":
		model = &database.Project{}
	default:
		return apperror.New(apperror.InvalidInput, "scopes", "unknown resource scope")
	}
	if err := tx.Model(model).Where("tenant_id = ? AND id = ?", tenantID, scope.ResourceID).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return apperror.New(apperror.InvalidInput, "scopes", "resource does not belong to tenant")
	}
	return nil
}
