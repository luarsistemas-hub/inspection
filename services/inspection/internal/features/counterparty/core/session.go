package core

import (
	"context"
	"errors"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
)

// ExternalSession contains the one-time credentials returned after OTP verification.
type ExternalSession struct {
	Token, CSRF string
	ExpiresAt   time.Time
}

// SessionOwner binds a verified counterparty session to one invitation.
type SessionOwner struct {
	TenantID, InvitationID identity.ID
}

// ValidateSession verifies the kind, invitation, expiry, revocation and CSRF proof.
func (s Service) ValidateSession(ctx context.Context, token, csrf string) (SessionOwner, error) {
	tenantID, digest, err := scopedTokenHash(token)
	if err != nil {
		return SessionOwner{}, apperror.New(apperror.Unauthenticated, "", "authentication required")
	}
	var row database.ExternalSession
	err = s.within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND session_digest=? AND kind='COUNTERPARTY'", tenantID, digest[:]).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.Unauthenticated, "", "authentication required")
			}
			return apperror.Wrap(apperror.DependencyUnavailable, err)
		}
		if row.CounterpartyInvitationID == nil {
			return apperror.New(apperror.Unauthenticated, "", "authentication required")
		}
		if !security.Active(s.now(), row.ExpiresAt, row.RevokedAt) {
			return apperror.New(apperror.Unauthenticated, "", "authentication required")
		}
		var invitation database.CounterpartyInvitation
		if err := tx.Where("tenant_id=? AND id=?", tenantID, *row.CounterpartyInvitationID).First(&invitation).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.Unauthenticated, "", "authentication required")
			}
			return apperror.Wrap(apperror.DependencyUnavailable, err)
		}
		proof, err := security.CSRFProof(digest, csrf, s.Pepper)
		if err != nil || len(row.CSRFDigest) != 32 || !security.Equal(proof, bytes32(row.CSRFDigest)) {
			return apperror.New(apperror.Forbidden, "csrf", "invalid request proof")
		}
		return nil
	})
	if err != nil {
		return SessionOwner{}, err
	}
	return SessionOwner{TenantID: tenantID, InvitationID: *row.CounterpartyInvitationID}, nil
}

func scopedTokenHash(token string) (identity.ID, [32]byte, error) {
	tenantID, err := security.ParseScopedToken(token)
	if err != nil {
		return identity.ID{}, [32]byte{}, err
	}
	return tenantID, security.HashToken(token), nil
}

func bytes32(value []byte) [32]byte {
	var result [32]byte
	copy(result[:], value)
	return result
}
