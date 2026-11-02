package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/ratelimit"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

// InvitationLimits throttles invitation e-mails. Implemented by the shared
// OTP policy.
type InvitationLimits interface {
	AllowSend(context.Context, string) (ratelimit.Result, error)
}

// payloadDigest binds a client mutation id to the exact command it first
// carried: reusing the id with different data is a conflict, not a replay.
func (r Request) payloadDigest() string {
	products := append([]string(nil), r.Products...)
	sort.Strings(products)
	scopes := make([]string, 0, len(r.Scopes))
	for _, scope := range r.Scopes {
		scopes = append(scopes, scope.Kind+":"+scope.ResourceID.String())
	}
	sort.Strings(scopes)
	sum := sha256.Sum256([]byte(strings.Join([]string{r.Operation, r.TenantID.String(), r.Name, r.Email, r.Role, strings.Join(products, ","), strings.Join(scopes, ","), fmt.Sprint(r.ReplacePending)}, "\x00")))
	return hex.EncodeToString(sum[:])
}

type receipt struct {
	PayloadDigest    string
	MembershipID     *identity.ID
	InvitationStatus string
}

// replay looks up the durable receipt of req.ClientID. A finished invitation
// (link sent or already accepted) is returned as-is; a failed or pending one
// is retried under the same key; another payload under the key is a CONFLICT.
func (p Provisioner) replay(ctx context.Context, req Request, digest string) (Result, bool, error) {
	var out Result
	found := false
	err := (tenanttx.Runner{DB: p.DB}).Within(ctx, req.TenantID, func(tx *gorm.DB) error {
		var rows []receipt
		if err := tx.Raw("SELECT payload_digest, membership_id, invitation_status FROM access.invitation_receipts WHERE tenant_id=? AND client_mutation_id=?", req.TenantID, req.ClientID).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		if rows[0].PayloadDigest != digest {
			return conflictingMutation()
		}
		if rows[0].InvitationStatus != "SENT" && rows[0].InvitationStatus != "ACCEPTED" || rows[0].MembershipID == nil {
			return nil
		}
		var membership database.Membership
		if err := tx.Where("tenant_id=? AND id=?", req.TenantID, *rows[0].MembershipID).First(&membership).Error; err != nil {
			return err
		}
		status := rows[0].InvitationStatus
		if membership.Status == "ACTIVE" {
			status = "ACCEPTED"
		}
		out, found = Result{Membership: membership, InvitationStatus: status}, true
		return nil
	})
	return out, found, err
}

func conflictingMutation() error {
	return apperror.New(apperror.Conflict, "clientMutationId", "Este identificador de solicitação já foi usado com outros dados.")
}

// saveReceipt records the outcome of req under its client mutation id in the
// caller's tenant transaction. A receipt holding another payload is a CONFLICT.
func saveReceipt(tx *gorm.DB, req Request, digest string, membershipID identity.ID, status string) error {
	var stored []string
	err := tx.Raw(`INSERT INTO access.invitation_receipts (tenant_id, client_mutation_id, operation, payload_digest, membership_id, invitation_status)
VALUES (?,?,?,?,?,?)
ON CONFLICT (tenant_id, client_mutation_id) DO UPDATE SET membership_id=EXCLUDED.membership_id, invitation_status=EXCLUDED.invitation_status, updated_at=now()
WHERE access.invitation_receipts.payload_digest=EXCLUDED.payload_digest
RETURNING payload_digest`, req.TenantID, req.ClientID, req.Operation, digest, membershipID, status).Scan(&stored).Error
	if err != nil {
		return err
	}
	if len(stored) == 0 {
		return conflictingMutation()
	}
	return nil
}

// allowSend applies the per-tenant and per-recipient invitation limits.
func (p Provisioner) allowSend(ctx context.Context, req Request) error {
	for _, key := range []string{"access-invite:tenant:" + req.TenantID.String(), "access-invite:email:" + req.TenantID.String() + ":" + req.Email} {
		decision, err := p.Limits.AllowSend(ctx, key)
		if err != nil {
			return apperror.Wrap(apperror.DependencyUnavailable, err)
		}
		if !decision.Allowed {
			return apperror.New(apperror.RateLimited, "", "Muitas tentativas. Aguarde alguns minutos e tente novamente.")
		}
	}
	return nil
}
