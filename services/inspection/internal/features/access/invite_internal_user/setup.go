// Package invite_internal_user owns local membership provisioning for an OIDC identity.
package invite_internal_user

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
	"inspection/services/inspection/internal/platform/keycloak"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/notifications"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

// RegistryNotifier delivers invitation and activation e-mails through the
// configured notification registry.
type RegistryNotifier = accesscore.RegistryNotifier

// Command provisions or safely retries an internal membership invitation.
type Command struct {
	TenantID identity.ID
	Name     string
	Email    string
	Role     string
	Scopes   []Scope
	// ProductEntitlements are the explicit products requested for the member.
	// Empty selects the role default; ADMIN is accepted only for roles that
	// ADR-003 entitles to the Admin product.
	ProductEntitlements []string
	ClientID            string
}

// Result is the invited membership and its delivery state.
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
	// Sender overrides the registry-backed activation e-mail delivery.
	Sender accesscore.InvitationSender
	// Limits throttles invitation e-mails per tenant and per recipient.
	Limits accesscore.InvitationLimits
}

// Setup registers this slice with the application mediator.
func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil || deps.Provider == nil || (deps.Registry == nil && deps.Sender == nil) || deps.Limits == nil || deps.Issuer == "" || deps.AdminOrigin == "" {
		return fmt.Errorf("slice access/invite_internal_user: missing dependency")
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		return handle(ctx, deps, raw.(Command))
	})
}

func (d Dependencies) provisioner() accesscore.Provisioner {
	sender := d.Sender
	if sender == nil {
		sender = RegistryNotifier{Registry: d.Registry}
	}
	return accesscore.Provisioner{DB: d.DB, Provider: d.Provider, Sender: sender, Limits: d.Limits, Issuer: d.Issuer, ActivationOrigin: d.AdminOrigin, Now: d.Now}
}

func handle(ctx context.Context, deps Dependencies, cmd Command) (Result, error) {
	// Authorization precedes validation so an unauthenticated or unauthorized
	// caller learns nothing about input rules.
	principal, err := deps.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: cmd.TenantID, Product: auth.AdminProduct, Roles: []string{auth.TenantAdmin, auth.AccessAdmin}, Mutate: true})
	if err != nil {
		return Result{}, err
	}
	cmd.Role, cmd.ClientID = strings.TrimSpace(cmd.Role), strings.TrimSpace(cmd.ClientID)
	name, email, err := accesscore.NormalizeIdentity(cmd.Name, cmd.Email)
	if err != nil {
		return Result{}, err
	}
	if cmd.TenantID == (identity.ID{}) || cmd.Role == "" || cmd.ClientID == "" {
		return Result{}, apperror.New(apperror.InvalidInput, "membership", "Informe nome, e-mail, perfil e o identificador da solicitação.")
	}
	if !auth.IsKnownRole(cmd.Role) {
		return Result{}, apperror.New(apperror.InvalidInput, "role", "O perfil informado não é válido.")
	}
	products, err := auth.NormalizeProductEntitlements(cmd.Role, cmd.ProductEntitlements)
	if err != nil {
		return Result{}, err
	}
	scopes := make([]accesscore.Scope, 0, len(cmd.Scopes))
	for _, scope := range cmd.Scopes {
		scopes = append(scopes, accesscore.Scope{Kind: scope.Kind, ResourceID: scope.ResourceID})
	}
	if err := accesscore.ValidateScopeShape(scopes); err != nil {
		return Result{}, err
	}
	if !auth.CanDelegateRole(principal.Roles, cmd.Role) {
		return Result{}, apperror.New(apperror.Forbidden, "role", "Você não pode conceder este perfil.")
	}
	meta, _ := requestctx.FromContext(ctx)
	invited, err := deps.provisioner().Invite(ctx, accesscore.Request{
		TenantID: cmd.TenantID, ActorID: principal.IdentityID, CorrelationID: meta.CorrelationID,
		Name: name, Email: email, Role: cmd.Role, Scopes: scopes, Products: products,
		AuditEvent: "access.user_invited", ClientID: cmd.ClientID, Operation: "INVITE_INTERNAL",
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Membership: invited.Membership, InvitationStatus: invited.InvitationStatus}, nil
}
