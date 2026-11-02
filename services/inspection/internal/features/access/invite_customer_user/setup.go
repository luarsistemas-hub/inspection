// Package invite_customer_user provisions a least-privilege customer
// membership: CUSTOMER_VIEWER, Dashboard-only, with at least one explicit
// resource grant. The membership stays INVITED, and unable to read any data,
// until the customer verifies the e-mail code and activates the account.
package invite_customer_user

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

// Scope is one requested explicit grant for the customer.
type Scope struct {
	Kind       string
	ResourceID identity.ID
}

// Command invites one customer. The role is fixed by the slice.
type Command struct {
	TenantID identity.ID
	Name     string
	Email    string
	Scopes   []Scope
	// ProductEntitlements is optional and may only name DASHBOARD.
	ProductEntitlements []string
	ClientID            string
}

// Result is the customer membership and the delivery state of its invitation.
type Result struct {
	Membership       database.Membership
	InvitationStatus string
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

// Setup registers the customer invitation command with the mediator.
func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil || deps.Provider == nil || (deps.Registry == nil && deps.Sender == nil) || deps.Limits == nil || deps.Issuer == "" || deps.AdminOrigin == "" {
		return fmt.Errorf("slice access/invite_customer_user: missing dependency")
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		return handle(ctx, deps, raw.(Command))
	})
}

func handle(ctx context.Context, deps Dependencies, cmd Command) (Result, error) {
	// Authorization precedes validation so an unauthenticated or unauthorized
	// caller learns nothing about input rules.
	principal, err := deps.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: cmd.TenantID, Product: auth.AdminProduct, Roles: []string{auth.TenantAdmin, auth.AccessAdmin}, Mutate: true})
	if err != nil {
		return Result{}, err
	}
	cmd.ClientID = strings.TrimSpace(cmd.ClientID)
	name, email, err := accesscore.NormalizeIdentity(cmd.Name, cmd.Email)
	if err != nil {
		return Result{}, err
	}
	if cmd.TenantID == (identity.ID{}) || cmd.ClientID == "" {
		return Result{}, apperror.New(apperror.InvalidInput, "membership", "Informe nome, e-mail e o identificador da solicitação.")
	}
	products, err := auth.NormalizeProductEntitlements(auth.CustomerViewer, cmd.ProductEntitlements)
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
	if err := accesscore.RequireDataBearingScope(auth.CustomerViewer, scopes); err != nil {
		return Result{}, err
	}
	if !auth.CanDelegateRole(principal.Roles, auth.CustomerViewer) {
		return Result{}, apperror.New(apperror.Forbidden, "role", "Você não pode conceder este perfil.")
	}
	meta, _ := requestctx.FromContext(ctx)
	var sender accesscore.InvitationSender = deps.Sender
	if sender == nil {
		sender = accesscore.RegistryNotifier{Registry: deps.Registry}
	}
	provisioner := accesscore.Provisioner{DB: deps.DB, Provider: deps.Provider, Sender: sender, Limits: deps.Limits, Issuer: deps.Issuer, ActivationOrigin: deps.AdminOrigin, Now: deps.Now}
	invited, err := provisioner.Invite(ctx, accesscore.Request{
		TenantID: cmd.TenantID, ActorID: principal.IdentityID, CorrelationID: meta.CorrelationID,
		Name: name, Email: email, Role: auth.CustomerViewer, Scopes: scopes, Products: products,
		ReplacePending: true, ReuseSent: true, AuditEvent: "access.customer_invited", ClientID: cmd.ClientID, Operation: "INVITE_CUSTOMER",
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Membership: invited.Membership, InvitationStatus: invited.InvitationStatus}, nil
}
