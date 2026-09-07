package core

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/ratelimit"
	"inspection/services/inspection/internal/platform/security"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type DeliveryIntent struct{ Channel, Destination string }
type Notifier interface {
	SendOTP(context.Context, identity.ID, identity.ID, string, []DeliveryIntent) error
}
type Limits interface {
	AllowSend(context.Context, string) (ratelimit.Result, error)
	AllowResend(context.Context, string) (ratelimit.Result, error)
	AllowAttempt(context.Context, string) (ratelimit.Result, error)
}

type Service struct {
	DB       *gorm.DB
	Pepper   []byte
	Limits   Limits
	Notifier Notifier
	Clock    func() time.Time
	Within   func(context.Context, identity.ID, func(*gorm.DB) error) error
}
type Session struct {
	Token, CSRF      string
	ExpiresAt        time.Time
	ResponsibilityID identity.ID
}

type ProcessingConsent struct {
	DisclosureVersion string
	PhotoProcessing   bool
	AIAnalysis        bool
	GPSUse            bool
}

const PrivacyDisclosureVersion = "pt-BR-1"

func (s Service) AcceptProcessing(ctx context.Context, sessionToken, csrf string, consent ProcessingConsent) error {
	tenantID, sessionHash, err := scopedHash(sessionToken)
	if err != nil {
		return apperror.New(apperror.SessionExpired, "", "session expired")
	}
	if strings.TrimSpace(consent.DisclosureVersion) != PrivacyDisclosureVersion || !consent.PhotoProcessing || !consent.AIAnalysis || !consent.GPSUse {
		return apperror.New(apperror.InvalidInput, "consent", "photo processing, AI analysis, and GPS use must be accepted")
	}
	now := s.now()
	return s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var session database.ExternalSession
		if err := tx.Where("tenant_id = ? AND session_digest = ?", tenantID, sessionHash[:]).First(&session).Error; err != nil || !security.Active(now, session.ExpiresAt, session.RevokedAt) {
			return apperror.New(apperror.SessionExpired, "", "session expired")
		}
		if err := activeInvitation(tx, tenantID, session.InvitationID, now); err != nil {
			return err
		}
		proof, proofErr := security.CSRFProof(sessionHash, csrf, s.Pepper)
		if proofErr != nil || len(session.CSRFDigest) != 32 || !security.Equal(proof, bytes32(session.CSRFDigest)) {
			return apperror.New(apperror.Forbidden, "csrf", "invalid request proof")
		}
		row := database.ProcessingAcceptance{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: session.ResponsibilityID, DisclosureVersion: strings.TrimSpace(consent.DisclosureVersion), PhotoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
	})
}

func (s Service) HasProcessingAcceptance(ctx context.Context, tenantID, responsibilityID identity.ID) (bool, error) {
	var count int64
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.ProcessingAcceptance{}).Where("tenant_id=? AND responsibility_id=? AND photo_processing AND ai_analysis AND gps_use", tenantID, responsibilityID).Count(&count).Error
	})
	return count == 1, err
}

func (s Service) RequestOTP(ctx context.Context, linkToken string) error {
	tenantID, hash, err := scopedHash(linkToken)
	if err != nil {
		return apperror.New(apperror.InvalidInput, "linkToken", "invalid invitation")
	}
	now := s.now()
	var code string
	var invitation database.Invitation
	err = s.within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND (token_hash = ? OR previous_token_hash = ?)", tenantID, hash[:], hash[:]).First(&invitation).Error; err != nil {
			return safeInvitationError(err)
		}
		if invitation.Status != "ACTIVE" || !now.Before(invitation.ExpiresAt) || invitation.RevokedAt != nil {
			return apperror.New(apperror.InvalidState, "", "invitation unavailable")
		}
		key := invitation.ID.String()
		var existing database.OTPChallenge
		if err := tx.Where("invitation_id = ? AND verified_at IS NULL AND expires_at > ?", invitation.ID, now).Order("created_at desc").First(&existing).Error; err == nil {
			if result, limitErr := s.Limits.AllowResend(ctx, key); limitErr != nil {
				return dependency(limitErr)
			} else if !result.Allowed {
				return apperror.New(apperror.RateLimited, "", "retry later")
			}
		}
		if result, err := s.Limits.AllowSend(ctx, key); err != nil {
			return dependency(err)
		} else if !result.Allowed {
			return apperror.New(apperror.RateLimited, "", "retry later")
		}
		generated, err := generateCode()
		if err != nil {
			return err
		}
		code = generated
		codeHash, err := security.HashOTP(code, s.Pepper)
		if err != nil {
			return err
		}
		challenge := database.OTPChallenge{ID: identity.NewID(), TenantID: invitation.TenantID, InvitationID: invitation.ID, CodeHMAC: codeHash[:], Attempts: 0, SendCount: 1, LastSentAt: now, ExpiresAt: now.Add(security.OTPTTL), CreatedAt: now}
		if err := tx.Model(&database.OTPChallenge{}).Where("tenant_id=? AND invitation_id=? AND verified_at IS NULL", tenantID, invitation.ID).Update("expires_at", now).Error; err != nil {
			return err
		}
		return tx.Create(&challenge).Error
	})
	if err != nil {
		return err
	}
	var intents []DeliveryIntent
	if err := json.Unmarshal(invitation.DeliveryIntents, &intents); err != nil || len(intents) == 0 {
		return apperror.New(apperror.InvalidState, "", "invitation has no delivery channel")
	}
	if err := s.Notifier.SendOTP(ctx, invitation.TenantID, invitation.ID, code, intents); err != nil {
		return dependency(err)
	}
	return nil
}

func (s Service) VerifyOTP(ctx context.Context, linkToken, code string) (Session, error) {
	tenantID, hash, err := scopedHash(linkToken)
	if err != nil {
		return Session{}, apperror.New(apperror.InvalidInput, "linkToken", "invalid invitation")
	}
	now := s.now()
	var result Session
	var rejection error
	err = s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var invitation database.Invitation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND (token_hash = ? OR previous_token_hash = ?)", tenantID, hash[:], hash[:]).First(&invitation).Error; err != nil {
			return safeInvitationError(err)
		}
		if invitation.Status != "ACTIVE" || !now.Before(invitation.ExpiresAt) || invitation.RevokedAt != nil {
			return apperror.New(apperror.SessionExpired, "", "session expired")
		}
		if allowed, err := s.Limits.AllowAttempt(ctx, invitation.ID.String()); err != nil {
			return dependency(err)
		} else if !allowed.Allowed {
			return apperror.New(apperror.RateLimited, "", "retry later")
		}
		var challenge database.OTPChallenge
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND invitation_id = ? AND expires_at > ?", tenantID, invitation.ID, now).Order("created_at desc").First(&challenge).Error; err != nil {
			return safeInvitationError(err)
		}
		if challenge.VerifiedAt != nil || !now.Before(challenge.ExpiresAt) || challenge.Attempts >= 5 {
			return apperror.New(apperror.InvalidInput, "code", "invalid code")
		}
		actual, err := security.HashOTP(code, s.Pepper)
		if err != nil || len(challenge.CodeHMAC) != 32 || !security.Equal(actual, bytes32(challenge.CodeHMAC)) {
			if updateErr := tx.Model(&challenge).UpdateColumn("attempts", gorm.Expr("attempts + 1")).Error; updateErr != nil {
				return updateErr
			}
			// Commit the attempt before returning the public rejection.
			rejection = apperror.New(apperror.InvalidInput, "code", "invalid code")
			return nil
		}
		challenge.VerifiedAt = &now
		if err := tx.Save(&challenge).Error; err != nil {
			return err
		}
		token, err := security.NewScopedToken(invitation.TenantID)
		if err != nil {
			return err
		}
		csrf, err := security.NewOpaqueToken()
		if err != nil {
			return err
		}
		sessionHash := security.HashToken(token)
		csrfHash, err := security.CSRFProof(sessionHash, csrf, s.Pepper)
		if err != nil {
			return err
		}
		expires := now.Add(security.SessionTTL)
		row := database.ExternalSession{ID: identity.NewID(), TenantID: invitation.TenantID, InvitationID: invitation.ID, ResponsibilityID: invitation.ResponsibilityID, SessionDigest: sessionHash[:], CSRFDigest: csrfHash[:], ExpiresAt: expires, CreatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		result = Session{Token: token, CSRF: csrf, ExpiresAt: expires, ResponsibilityID: invitation.ResponsibilityID}
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	return result, rejection
}

func (s Service) Revoke(ctx context.Context, linkToken string) error {
	tenantID, hash, err := scopedHash(linkToken)
	if err != nil {
		return apperror.New(apperror.InvalidInput, "linkToken", "invalid invitation")
	}
	now := s.now()
	return s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var invitation database.Invitation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND (token_hash = ? OR previous_token_hash = ?)", tenantID, hash[:], hash[:]).First(&invitation).Error; err != nil {
			return safeInvitationError(err)
		}
		invitation.Status = "REVOKED"
		invitation.RevokedAt = &now
		if err := tx.Save(&invitation).Error; err != nil {
			return err
		}
		return tx.Model(&database.ExternalSession{}).Where("invitation_id = ? AND revoked_at IS NULL", invitation.ID).Update("revoked_at", now).Error
	})
}

func (s Service) ValidateSession(ctx context.Context, token, csrf string) (identity.ID, error) {
	tenantID, hash, err := scopedHash(token)
	if err != nil {
		return identity.ID{}, apperror.New(apperror.SessionExpired, "", "session expired")
	}
	now := s.now()
	var row database.ExternalSession
	err = s.within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND session_digest = ?", tenantID, hash[:]).First(&row).Error; err != nil {
			return err
		}
		return activeInvitation(tx, tenantID, row.InvitationID, now)
	})
	if err != nil {
		return identity.ID{}, apperror.New(apperror.SessionExpired, "", "session expired")
	}
	if !security.Active(now, row.ExpiresAt, row.RevokedAt) {
		return identity.ID{}, apperror.New(apperror.SessionExpired, "", "session expired")
	}
	proof, err := security.CSRFProof(hash, csrf, s.Pepper)
	if err != nil || len(row.CSRFDigest) != 32 || !security.Equal(proof, bytes32(row.CSRFDigest)) {
		return identity.ID{}, apperror.New(apperror.Forbidden, "csrf", "invalid request proof")
	}
	return row.ResponsibilityID, nil
}

func activeInvitation(tx *gorm.DB, tenantID, invitationID identity.ID, now time.Time) error {
	var invitation database.Invitation
	if err := tx.Where("tenant_id=? AND id=?", tenantID, invitationID).First(&invitation).Error; err != nil || invitation.Status != "ACTIVE" || !security.Active(now, invitation.ExpiresAt, invitation.RevokedAt) {
		return apperror.New(apperror.SessionExpired, "", "session expired")
	}
	return nil
}

// ValidateCaptureRead permits only confirmation reads after a completed capture.
// It never restores mutation authority to the revoked session.
func (s Service) ValidateCaptureRead(ctx context.Context, token, csrf string) (identity.ID, error) {
	if responsibility, err := s.ValidateSession(ctx, token, csrf); err == nil {
		return responsibility, nil
	}
	tenantID, hash, err := scopedHash(token)
	if err != nil {
		return identity.ID{}, apperror.New(apperror.SessionExpired, "", "session expired")
	}
	var session database.ExternalSession
	err = s.within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND session_digest=?", tenantID, hash[:]).First(&session).Error; err != nil || !s.now().Before(session.ExpiresAt) {
			return apperror.New(apperror.SessionExpired, "", "session expired")
		}
		proof, err := security.CSRFProof(hash, csrf, s.Pepper)
		if err != nil || len(session.CSRFDigest) != 32 || !security.Equal(proof, bytes32(session.CSRFDigest)) {
			return apperror.New(apperror.Forbidden, "csrf", "invalid request proof")
		}
		var invitation database.Invitation
		if err := tx.Where("tenant_id=? AND id=? AND responsibility_id=? AND status='COMPLETED'", tenantID, session.InvitationID, session.ResponsibilityID).First(&invitation).Error; err != nil {
			return apperror.New(apperror.SessionExpired, "", "session expired")
		}
		return nil
	})
	return session.ResponsibilityID, err
}

func (s Service) now() time.Time {
	if s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}

func (s Service) within(ctx context.Context, tenantID identity.ID, fn func(*gorm.DB) error) error {
	if s.Within != nil {
		return s.Within(ctx, tenantID, fn)
	}
	return (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, fn)
}

func scopedHash(value string) (identity.ID, [32]byte, error) {
	tenantID, err := security.ParseScopedToken(value)
	if err != nil {
		return identity.ID{}, [32]byte{}, err
	}
	return tenantID, security.HashToken(value), nil
}
func bytes32(value []byte) [32]byte { var result [32]byte; copy(result[:], value); return result }
func dependency(err error) error    { return apperror.Wrap(apperror.DependencyUnavailable, err) }
func safeInvitationError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperror.New(apperror.InvalidInput, "linkToken", "invalid invitation")
	}
	return dependency(err)
}
func generateCode() (string, error) {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(raw[:])%1_000_000), nil
}
