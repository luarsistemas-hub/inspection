package core

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/keycloak"
	"inspection/services/inspection/internal/platform/security"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// IdentityProvider ensures a verified identity exists for an invited email.
type IdentityProvider interface {
	EnsureInternalUser(context.Context, string, string) (keycloak.InternalUser, error)
}

// InvitationSender delivers the activation link. It is replaceable in tests.
type InvitationSender interface {
	SendInvitation(ctx context.Context, destination, link, membershipID, generation string) error
}

// Provisioner provisions one pending membership and sends its activation link.
// The provider call happens before any local write, so a provider failure
// leaves no membership, entitlement, or scope behind and the same command can
// be replayed; a delivery failure leaves the membership retryable as FAILED.
type Provisioner struct {
	DB               *gorm.DB
	Provider         IdentityProvider
	Sender           InvitationSender
	Limits           InvitationLimits
	Issuer           string
	ActivationOrigin string
	Now              func() time.Time
}

// Ready reports whether every dependency needed to provision is present.
func (p Provisioner) Ready() bool {
	return p.DB != nil && p.Provider != nil && p.Sender != nil && p.Limits != nil && p.Issuer != "" && p.ActivationOrigin != ""
}

// Request describes the membership access to create or refresh.
type Request struct {
	TenantID      identity.ID
	ActorID       identity.ID
	CorrelationID string
	Name, Email   string
	Role          string
	Scopes        []Scope
	// Products are the already normalized product entitlements.
	Products []string
	// ReplacePending synchronizes entitlements and scopes of a membership that
	// is still invited, which cannot read data before activation.
	ReplacePending bool
	// ReuseSent returns an invitation whose link was already sent instead of
	// issuing a new link; used by idempotent create commands.
	ReuseSent  bool
	AuditEvent string
	// ClientID is the client mutation id; Operation names the command so the
	// same id cannot be reused across operations. Both are required.
	ClientID, Operation string
}

// Result is the membership and the delivery state of its activation link.
type Result struct {
	Membership       database.Membership
	InvitationStatus string
}

// Invite provisions or safely retries the invitation described by req.
func (p Provisioner) Invite(ctx context.Context, req Request) (Result, error) {
	if !p.Ready() {
		return Result{}, fmt.Errorf("access invitation: missing dependency")
	}
	if req.ClientID == "" || req.Operation == "" {
		return Result{}, apperror.New(apperror.InvalidInput, "clientMutationId", "Informe o identificador da solicitação.")
	}
	now := p.now()
	payload := req.payloadDigest()
	if replayed, found, err := p.replay(ctx, req, payload); err != nil {
		return Result{}, err
	} else if found {
		return replayed, nil
	}
	if err := p.allowSend(ctx, req); err != nil {
		return Result{}, err
	}
	user, err := p.Provider.EnsureInternalUser(ctx, req.Email, req.Name)
	if err != nil {
		return Result{}, apperror.Wrap(apperror.DependencyUnavailable, err)
	}
	token, err := security.NewOpaqueToken()
	if err != nil {
		return Result{}, err
	}
	digest := security.HashToken(token)
	var result Result
	err = (tenanttx.Runner{DB: p.DB}).Within(ctx, req.TenantID, func(tx *gorm.DB) error {
		audited := false
		if err := RequireTenantResources(tx, req.TenantID, req.Scopes); err != nil {
			return err
		}
		var membership database.Membership
		lookup := tx.Where("tenant_id=? AND issuer=? AND subject=?", req.TenantID, p.Issuer, user.ID).First(&membership)
		switch {
		case lookup.Error == nil:
			if membership.Role != req.Role {
				return apperror.New(apperror.Conflict, "email", "Este e-mail já possui acesso com outro perfil.")
			}
			if membership.Status == "DISABLED" {
				return apperror.New(apperror.Conflict, "email", "O acesso deste e-mail está desativado.")
			}
			if membership.Status == "ACTIVE" {
				result = Result{Membership: membership, InvitationStatus: "ACCEPTED"}
				return saveReceipt(tx, req, payload, membership.ID, "ACCEPTED")
			}
			if req.ReplacePending {
				if err := ReplaceAccess(tx, req.TenantID, membership.ID, req.Products, req.Scopes, now); err != nil {
					return err
				}
				membership.Version++
				// A replay that rewrites pending entitlements and scopes is an access
				// change too: audit it even when no new link is issued.
				if req.AuditEvent != "" {
					reason := fmt.Sprintf("replay;role=%s;products=%s;scopes=%d;version=%d", req.Role, strings.Join(req.Products, ","), len(req.Scopes), membership.Version)
					if err := RecordAudit(tx, req.TenantID, req.ActorID, req.AuditEvent, "membership", membership.ID.String(), "ACCEPTED", reason, req.CorrelationID, now); err != nil {
						return err
					}
					audited = true
				}
			}
			if req.ReuseSent && membership.InvitationStatus == "SENT" {
				membership.UpdatedAt = now
				if err := tx.Save(&membership).Error; err != nil {
					return err
				}
				result = Result{Membership: membership, InvitationStatus: "SENT"}
				return saveReceipt(tx, req, payload, membership.ID, "SENT")
			}
		case lookup.Error == gorm.ErrRecordNotFound:
			membership = database.Membership{ID: identity.NewID(), TenantID: req.TenantID, IdentityID: identity.NewDeterministicID("inspection/oidc-identity", p.Issuer+"\x00"+user.ID), Issuer: p.Issuer, Subject: user.ID, Name: req.Name, Email: req.Email, Role: req.Role, Status: "INVITED", InvitationStatus: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&membership).Error; err != nil {
				return err
			}
			if err := ReplaceAccess(tx, req.TenantID, membership.ID, req.Products, req.Scopes, now); err != nil {
				return err
			}
		default:
			return lookup.Error
		}
		membership.Name, membership.Email, membership.Status, membership.InvitationStatus = req.Name, req.Email, "INVITED", "PENDING"
		membership.UpdatedAt = now
		if err := tx.Save(&membership).Error; err != nil {
			return err
		}
		invitation := database.UserInvitation{ID: identity.NewID(), TenantID: req.TenantID, MembershipID: membership.ID, Issuer: p.Issuer, Subject: user.ID, Email: req.Email, Name: req.Name, NewIdentity: user.Created, Status: "PENDING", TokenDigest: digest[:], ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "membership_id"}}, DoUpdates: clause.Assignments(map[string]any{"email": req.Email, "name": req.Name, "new_identity": user.Created, "status": "PENDING", "token_digest": digest[:], "expires_at": invitation.ExpiresAt, "claimed_at": nil, "session_digest": nil, "csrf_digest": nil, "session_expires_at": nil, "password_set_at": nil, "accepted_at": nil, "updated_at": now})}).Create(&invitation).Error; err != nil {
			return err
		}
		if req.AuditEvent != "" && !audited {
			reason := fmt.Sprintf("role=%s;products=%s;scopes=%d", req.Role, strings.Join(req.Products, ","), len(req.Scopes))
			if err := RecordAudit(tx, req.TenantID, req.ActorID, req.AuditEvent, "membership", membership.ID.String(), "ACCEPTED", reason, req.CorrelationID, now); err != nil {
				return err
			}
		}
		result = Result{Membership: membership, InvitationStatus: "PENDING"}
		return saveReceipt(tx, req, payload, membership.ID, "PENDING")
	})
	if err != nil {
		return Result{}, err
	}
	if result.InvitationStatus == "ACCEPTED" || result.InvitationStatus == "SENT" {
		return result, nil
	}
	activationURL, err := url.Parse(strings.TrimRight(p.ActivationOrigin, "/") + "/activate/invitation")
	if err != nil {
		return Result{}, err
	}
	query := activationURL.Query()
	query.Set("token", token)
	activationURL.RawQuery = query.Encode()
	if err := p.Sender.SendInvitation(ctx, req.Email, activationURL.String(), result.Membership.ID.String(), hex.EncodeToString(digest[:])); err != nil {
		if persistErr := p.markDelivery(ctx, req, result.Membership.ID, digest[:], "FAILED"); persistErr != nil {
			return Result{}, persistErr
		}
		result.InvitationStatus = "FAILED"
		result.Membership.InvitationStatus = "FAILED"
		return result, nil
	}
	if err := p.markDelivery(ctx, req, result.Membership.ID, digest[:], "SENT"); err != nil {
		return Result{}, err
	}
	result.InvitationStatus = "SENT"
	result.Membership.InvitationStatus = "SENT"
	return result, nil
}

func (p Provisioner) markDelivery(ctx context.Context, req Request, membershipID identity.ID, digest []byte, status string) error {
	tenantID := req.TenantID
	return (tenanttx.Runner{DB: p.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Exec("UPDATE access.invitation_receipts SET invitation_status=?, updated_at=now() WHERE tenant_id=? AND client_mutation_id=?", status, tenantID, req.ClientID).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.UserInvitation{}).Where("tenant_id=? AND membership_id=? AND token_digest=?", tenantID, membershipID, digest).Update("status", status).Error; err != nil {
			return err
		}
		return tx.Model(&database.Membership{}).Where("tenant_id=? AND id=?", tenantID, membershipID).Update("invitation_status", status).Error
	})
}

func (p Provisioner) now() time.Time {
	if p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}
