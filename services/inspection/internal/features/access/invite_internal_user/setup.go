// Package invite_internal_user owns local membership provisioning for an OIDC identity.
package invite_internal_user

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/keycloak"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/notifications"
	"inspection/services/inspection/internal/platform/security"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Command provisions or safely retries an internal membership invitation.
type Command struct {
	TenantID identity.ID
	Name     string
	Email    string
	Role     string
	Scopes   []Scope
	ClientID string
}

type Result struct {
	Membership       database.Membership
	InvitationStatus string
}

// Scope is a requested hierarchical assignment.
type Scope struct {
	Kind       string
	ResourceID identity.ID
}

// Dependencies are the adapters assembled by the composition root.
type Dependencies struct {
	DB          *gorm.DB
	Bus         *mediator.Bus
	Authorizer  auth.Authorizer
	Now         func() time.Time
	Issuer      string
	AdminOrigin string
	Provider    interface {
		EnsureInternalUser(context.Context, string, string) (keycloak.InternalUser, error)
	}
	Registry *notifications.Registry
}

// Setup registers this slice with the application mediator.
func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil || deps.Provider == nil || deps.Registry == nil || deps.Issuer == "" || deps.AdminOrigin == "" {
		return fmt.Errorf("slice access/invite_internal_user: missing dependency")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		return handle(ctx, deps, raw.(Command))
	})
}

func handle(ctx context.Context, deps Dependencies, cmd Command) (Result, error) {
	cmd.Name, cmd.Email, cmd.Role, cmd.ClientID = strings.TrimSpace(cmd.Name), strings.ToLower(strings.TrimSpace(cmd.Email)), strings.TrimSpace(cmd.Role), strings.TrimSpace(cmd.ClientID)
	parsed, emailErr := mail.ParseAddress(cmd.Email)
	if emailErr != nil || parsed.Address != cmd.Email {
		return Result{}, apperror.New(apperror.InvalidInput, "email", "invalid email")
	}
	if cmd.TenantID == (identity.ID{}) || cmd.Name == "" || len([]rune(cmd.Name)) > 200 || cmd.Role == "" || cmd.ClientID == "" {
		return Result{}, apperror.New(apperror.InvalidInput, "membership", "name, email, role, and client mutation id are required")
	}
	if !auth.IsKnownRole(cmd.Role) {
		return Result{}, apperror.New(apperror.InvalidInput, "role", "unknown role")
	}
	if len(cmd.Scopes) > 100 {
		return Result{}, apperror.New(apperror.InvalidInput, "scopes", "batch exceeds 100")
	}
	principal, err := deps.Authorizer.Authorize(ctx, cmd.TenantID, []string{auth.TenantAdmin, auth.AccessAdmin}, nil, true)
	if err != nil {
		return Result{}, err
	}
	if !auth.CanDelegateRole(principal.Roles, cmd.Role) {
		return Result{}, apperror.New(apperror.Forbidden, "role", "access denied")
	}
	if err := validateScopes(cmd.Scopes); err != nil {
		return Result{}, err
	}
	user, err := deps.Provider.EnsureInternalUser(ctx, cmd.Email, cmd.Name)
	if err != nil {
		return Result{}, apperror.Wrap(apperror.DependencyUnavailable, err)
	}
	now := deps.Now().UTC()
	token, err := security.NewOpaqueToken()
	if err != nil {
		return Result{}, err
	}
	digest := security.HashToken(token)
	var result Result
	err = (tenanttx.Runner{DB: deps.DB}).Within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		var membership database.Membership
		newIdentity := user.Created
		lookup := tx.Where("tenant_id=? AND issuer=? AND subject=?", cmd.TenantID, deps.Issuer, user.ID).First(&membership)
		if lookup.Error == nil {
			if membership.Role != cmd.Role {
				return apperror.New(apperror.Conflict, "email", "account already has a different role")
			}
			if membership.Status == "DISABLED" {
				return apperror.New(apperror.Conflict, "email", "account is disabled")
			}
			if membership.Status == "ACTIVE" {
				result = Result{Membership: membership, InvitationStatus: "ACCEPTED"}
				return nil
			}
		} else if lookup.Error != gorm.ErrRecordNotFound {
			return lookup.Error
		} else {
			membership = database.Membership{ID: identity.NewID(), TenantID: cmd.TenantID, IdentityID: identity.NewDeterministicID("inspection/oidc-identity", deps.Issuer+"\x00"+user.ID), Issuer: deps.Issuer, Subject: user.ID, Name: cmd.Name, Email: cmd.Email, Role: cmd.Role, Status: "INVITED", InvitationStatus: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&membership).Error; err != nil {
				return err
			}
			for _, scope := range cmd.Scopes {
				if err := validateScope(tx, cmd.TenantID, scope, cmd.Role); err != nil {
					return err
				}
				if err := tx.Create(&database.ResourceScope{ID: identity.NewID(), TenantID: cmd.TenantID, MembershipID: membership.ID, Kind: scope.Kind, ResourceID: scope.ResourceID}).Error; err != nil {
					return err
				}
			}
			for _, product := range auth.ProductsForRole(cmd.Role) {
				if err := tx.Create(&database.ProductEntitlement{ID: identity.NewID(), TenantID: cmd.TenantID, MembershipID: membership.ID, Product: product, CreatedAt: now}).Error; err != nil {
					return err
				}
			}
		}
		membership.Name, membership.Email, membership.Status, membership.InvitationStatus = cmd.Name, cmd.Email, "INVITED", "PENDING"
		membership.UpdatedAt = now
		if err := tx.Save(&membership).Error; err != nil {
			return err
		}
		invitation := database.UserInvitation{ID: identity.NewID(), TenantID: cmd.TenantID, MembershipID: membership.ID, Issuer: deps.Issuer, Subject: user.ID, Email: cmd.Email, Name: cmd.Name, NewIdentity: newIdentity, Status: "PENDING", TokenDigest: digest[:], ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "membership_id"}}, DoUpdates: clause.Assignments(map[string]any{"email": cmd.Email, "name": cmd.Name, "new_identity": newIdentity, "status": "PENDING", "token_digest": digest[:], "expires_at": invitation.ExpiresAt, "claimed_at": nil, "session_digest": nil, "csrf_digest": nil, "session_expires_at": nil, "password_set_at": nil, "accepted_at": nil, "updated_at": now})}).Create(&invitation).Error; err != nil {
			return err
		}
		result = Result{Membership: membership, InvitationStatus: "PENDING"}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	if result.InvitationStatus == "ACCEPTED" {
		return result, nil
	}
	activationURL, err := url.Parse(strings.TrimRight(deps.AdminOrigin, "/") + "/activate/invitation")
	if err != nil {
		return Result{}, err
	}
	query := activationURL.Query()
	query.Set("token", token)
	activationURL.RawQuery = query.Encode()
	notifier := RegistryNotifier{Registry: deps.Registry}
	if err := notifier.SendInvitation(ctx, cmd.Email, activationURL.String(), result.Membership.ID.String(), hex.EncodeToString(digest[:])); err != nil {
		if persistErr := (tenanttx.Runner{DB: deps.DB}).Within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
			if updateErr := tx.Model(&database.UserInvitation{}).Where("tenant_id=? AND membership_id=? AND token_digest=?", cmd.TenantID, result.Membership.ID, digest[:]).Update("status", "FAILED").Error; updateErr != nil {
				return updateErr
			}
			return tx.Model(&database.Membership{}).Where("tenant_id=? AND id=?", cmd.TenantID, result.Membership.ID).Update("invitation_status", "FAILED").Error
		}); persistErr != nil {
			return Result{}, persistErr
		}
		result.InvitationStatus = "FAILED"
		return result, nil
	}
	if err := (tenanttx.Runner{DB: deps.DB}).Within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		if updateErr := tx.Model(&database.UserInvitation{}).Where("tenant_id=? AND membership_id=? AND token_digest=?", cmd.TenantID, result.Membership.ID, digest[:]).Update("status", "SENT").Error; updateErr != nil {
			return updateErr
		}
		return tx.Model(&database.Membership{}).Where("tenant_id=? AND id=?", cmd.TenantID, result.Membership.ID).Update("invitation_status", "SENT").Error
	}); err != nil {
		return Result{}, err
	}
	result.InvitationStatus = "SENT"
	result.Membership.InvitationStatus = "SENT"
	return result, nil
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
