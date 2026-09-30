package activate_internal_user

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/onboarding/session"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Provider interface {
	VerifyEmail(context.Context, string) error
	SetInitialPassword(context.Context, string, string) error
}

type Notifier interface {
	SendOTP(context.Context, string, string, string) error
}

type Service struct {
	DB       *gorm.DB
	Pepper   []byte
	Limits   session.Limits
	Notifier Notifier
	Provider Provider
	Stage    string
	Clock    func() time.Time
}

type Activation struct {
	MembershipID identity.ID
	Email        string
	Name         string
	NewIdentity  bool
	Status       string
}

type Claim struct {
	Locator, CSRF string
	ExpiresAt     time.Time
	Activation    Activation
}

const linkTTL = 24 * time.Hour

func (s Service) ClaimInvitation(ctx context.Context, token string) (Claim, error) {
	if s.DB == nil || len(s.Pepper) < 32 {
		return Claim{}, errors.New("internal user activation: missing dependency")
	}
	token = strings.TrimSpace(token)
	if len(token) != 43 {
		return Claim{}, apperror.New(apperror.SessionExpired, "token", "invitation is invalid or expired")
	}
	locator, err := security.NewOpaqueToken()
	if err != nil {
		return Claim{}, apperror.Wrap(apperror.Internal, err)
	}
	csrf, err := security.NewOpaqueToken()
	if err != nil {
		return Claim{}, apperror.Wrap(apperror.Internal, err)
	}
	now := s.now()
	expires := now.Add(security.SessionTTL)
	tokenDigest := security.HashToken(token)
	locatorDigest := security.HashToken(locator)
	csrfDigest, err := security.CSRFProof(locatorDigest, csrf, s.Pepper)
	if err != nil {
		return Claim{}, apperror.Wrap(apperror.Internal, err)
	}
	var invitation database.UserInvitation
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT set_config('app.user_invitation_digest', ?, true)", fmt.Sprintf("%x", tokenDigest[:])).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("token_digest=?", tokenDigest[:]).First(&invitation).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.SessionExpired, "token", "invitation is invalid or expired")
			}
			return err
		}
		if invitation.Status != "SENT" || !now.Before(invitation.ExpiresAt) {
			return apperror.New(apperror.SessionExpired, "token", "invitation is invalid or expired")
		}
		if err := tx.Exec("SELECT set_config('app.tenant_id', ?, true)", invitation.TenantID.String()).Error; err != nil {
			return err
		}
		result := tx.Model(&invitation).Where("status='SENT' AND expires_at>?", now).Updates(map[string]any{
			"status": "CLAIMED", "claimed_at": now, "session_digest": locatorDigest[:],
			"csrf_digest": csrfDigest[:], "session_expires_at": expires, "updated_at": now,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "invitation", "invitation was already used")
		}
		invitation.Status = "CLAIMED"
		return nil
	})
	if err != nil {
		return Claim{}, err
	}
	return Claim{Locator: locator, CSRF: csrf, ExpiresAt: expires, Activation: activationOf(invitation)}, nil
}

func (s Service) RequestOTP(ctx context.Context, locator, csrf string) (Activation, error) {
	if s.DB == nil || s.Limits == nil || s.Notifier == nil || len(s.Pepper) < 32 {
		return Activation{}, errors.New("internal user activation: missing dependency")
	}
	invitation, err := s.loadSession(ctx, locator, csrf)
	if err != nil {
		return Activation{}, err
	}
	if invitation.Status != "CLAIMED" && invitation.Status != "OTP_SENT" {
		return Activation{}, apperror.New(apperror.InvalidState, "invitation", "invitation cannot request a code")
	}
	if limit, err := s.Limits.AllowSend(ctx, invitation.Email+":user-invitation"); err != nil {
		return Activation{}, apperror.Wrap(apperror.DependencyUnavailable, err)
	} else if !limit.Allowed {
		return Activation{}, apperror.New(apperror.RateLimited, "", "retry later")
	}
	code, err := newCode()
	if err != nil {
		return Activation{}, err
	}
	codeHash, err := security.HashOTP(code, s.Pepper)
	if err != nil {
		return Activation{}, err
	}
	challengeID := identity.NewID()
	now := s.now()
	err = s.withSession(ctx, locator, csrf, func(tx *gorm.DB, current database.UserInvitation) error {
		if current.Status != "CLAIMED" && current.Status != "OTP_SENT" {
			return apperror.New(apperror.InvalidState, "invitation", "invitation cannot request a code")
		}
		if err := tx.Model(&database.UserInvitationOTP{}).Where("tenant_id=? AND invitation_id=? AND verified_at IS NULL", current.TenantID, current.ID).Update("expires_at", now).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.UserInvitationOTP{ID: challengeID, TenantID: current.TenantID, InvitationID: current.ID, CodeHMAC: codeHash[:], ExpiresAt: now.Add(security.OTPTTL), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Model(&current).Updates(map[string]any{"status": "OTP_SENT", "updated_at": now}).Error
	})
	if err != nil {
		return Activation{}, err
	}
	if err := s.Notifier.SendOTP(ctx, invitation.Email, code, challengeID.String()); err != nil {
		return Activation{}, apperror.Wrap(apperror.DependencyUnavailable, err)
	}
	return activationOf(invitation), nil
}

func (s Service) VerifyOTP(ctx context.Context, locator, csrf, code string) (Activation, error) {
	if s.DB == nil || s.Limits == nil || len(s.Pepper) < 32 {
		return Activation{}, errors.New("internal user activation: missing dependency")
	}
	invitation, err := s.loadSession(ctx, locator, csrf)
	if err != nil {
		return Activation{}, err
	}
	if invitation.Status != "OTP_SENT" {
		return Activation{}, apperror.New(apperror.InvalidState, "invitation", "request an activation code first")
	}
	if limit, err := s.Limits.AllowAttempt(ctx, invitation.Email+":user-invitation"); err != nil {
		return Activation{}, apperror.Wrap(apperror.DependencyUnavailable, err)
	} else if !limit.Allowed {
		return Activation{}, apperror.New(apperror.RateLimited, "", "retry later")
	}
	now := s.now()
	var result Activation
	var rejection error
	err = s.withSession(ctx, locator, csrf, func(tx *gorm.DB, current database.UserInvitation) error {
		var challenge database.UserInvitationOTP
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND invitation_id=? AND verified_at IS NULL", current.TenantID, current.ID).Order("created_at DESC").First(&challenge).Error; err != nil || !security.Active(now, challenge.ExpiresAt, nil) || challenge.Attempts >= 5 {
			return apperror.New(apperror.InvalidInput, "code", "invalid code")
		}
		actual, hashErr := security.HashOTP(code, s.Pepper)
		accepted := hashErr == nil && len(challenge.CodeHMAC) == 32 && security.Equal(actual, bytes32(challenge.CodeHMAC))
		if !accepted {
			if err := tx.Model(&challenge).UpdateColumn("attempts", gorm.Expr("attempts + 1")).Error; err != nil {
				return err
			}
			rejection = apperror.New(apperror.InvalidInput, "code", "invalid code")
			return nil
		}
		if err := tx.Model(&challenge).Update("verified_at", now).Error; err != nil {
			return err
		}
		if err := tx.Model(&current).Updates(map[string]any{"status": "VERIFIED", "updated_at": now}).Error; err != nil {
			return err
		}
		current.Status = "VERIFIED"
		result = activationOf(current)
		return nil
	})
	if err != nil {
		return Activation{}, err
	}
	return result, rejection
}

func (s Service) Complete(ctx context.Context, locator, csrf, password string) (Activation, error) {
	if s.DB == nil || s.Provider == nil || len(s.Pepper) < 32 {
		return Activation{}, errors.New("internal user activation: missing dependency")
	}
	invitation, err := s.loadSession(ctx, locator, csrf)
	if err != nil {
		return Activation{}, err
	}
	if invitation.Status == "ACCEPTED" {
		return activationOf(invitation), nil
	}
	if invitation.Status != "VERIFIED" {
		return Activation{}, apperror.New(apperror.InvalidState, "invitation", "email verification is required")
	}
	if invitation.NewIdentity && !s.passwordAllowed(password) {
		return Activation{}, apperror.New(apperror.InvalidInput, "password", "password does not meet policy")
	}
	var membership database.Membership
	if err := s.withSession(ctx, locator, csrf, func(tx *gorm.DB, current database.UserInvitation) error {
		return tx.Where("tenant_id=? AND id=? AND status='INVITED'", current.TenantID, current.MembershipID).First(&membership).Error
	}); err != nil {
		return Activation{}, apperror.New(apperror.InvalidState, "invitation", "membership is unavailable")
	}
	if err := s.Provider.VerifyEmail(ctx, membership.Subject); err != nil {
		return Activation{}, apperror.Wrap(apperror.DependencyUnavailable, err)
	}
	if invitation.NewIdentity {
		if err := s.Provider.SetInitialPassword(ctx, membership.Subject, password); err != nil {
			return Activation{}, apperror.Wrap(apperror.DependencyUnavailable, err)
		}
	}
	now := s.now()
	var result Activation
	err = s.withSession(ctx, locator, csrf, func(tx *gorm.DB, current database.UserInvitation) error {
		if current.Status == "ACCEPTED" {
			result = activationOf(current)
			return nil
		}
		if current.Status != "VERIFIED" {
			return apperror.New(apperror.Conflict, "invitation", "invitation is no longer active")
		}
		if current.NewIdentity {
			if err := tx.Model(&current).Update("password_set_at", now).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&database.Membership{}).Where("tenant_id=? AND id=? AND status='INVITED'", current.TenantID, current.MembershipID).Updates(map[string]any{"status": "ACTIVE", "invitation_status": "ACCEPTED", "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&current).Updates(map[string]any{"status": "ACCEPTED", "accepted_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		current.Status = "ACCEPTED"
		result = activationOf(current)
		return nil
	})
	return result, err
}

func (s Service) loadSession(ctx context.Context, locator, csrf string) (database.UserInvitation, error) {
	var invitation database.UserInvitation
	digest := security.HashToken(locator)
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT set_config('app.user_invitation_session', ?, true)", fmt.Sprintf("%x", digest[:])).Error; err != nil {
			return err
		}
		if err := tx.Where("session_digest=?", digest[:]).First(&invitation).Error; err != nil {
			return apperror.New(apperror.SessionExpired, "session", "invitation session expired")
		}
		if invitation.SessionExpiresAt == nil || !s.now().Before(*invitation.SessionExpiresAt) || invitation.AcceptedAt != nil {
			return apperror.New(apperror.SessionExpired, "session", "invitation session expired")
		}
		if err := validateCSRF(locator, csrf, invitation.CSRFDigest, s.Pepper); err != nil {
			return err
		}
		return nil
	})
	return invitation, err
}

func (s Service) withSession(ctx context.Context, locator, csrf string, work func(*gorm.DB, database.UserInvitation) error) error {
	digest := security.HashToken(locator)
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT set_config('app.user_invitation_session', ?, true)", fmt.Sprintf("%x", digest[:])).Error; err != nil {
			return err
		}
		var invitation database.UserInvitation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("session_digest=?", digest[:]).First(&invitation).Error; err != nil {
			return apperror.New(apperror.SessionExpired, "session", "invitation session expired")
		}
		if invitation.SessionExpiresAt == nil || !s.now().Before(*invitation.SessionExpiresAt) {
			return apperror.New(apperror.SessionExpired, "session", "invitation session expired")
		}
		if err := validateCSRF(locator, csrf, invitation.CSRFDigest, s.Pepper); err != nil {
			return err
		}
		if err := tx.Exec("SELECT set_config('app.tenant_id', ?, true)", invitation.TenantID.String()).Error; err != nil {
			return err
		}
		return work(tx, invitation)
	})
}

func (s Service) now() time.Time {
	if s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}

func (s Service) passwordAllowed(password string) bool {
	if strings.TrimSpace(password) == "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(s.Stage), "dev") {
		return true
	}
	runes := []rune(password)
	if len(runes) < 6 {
		return false
	}
	var uppercase, special bool
	for _, char := range runes {
		uppercase = uppercase || unicode.IsUpper(char)
		special = special || unicode.IsPunct(char) || unicode.IsSymbol(char)
	}
	return uppercase && special
}

func activationOf(invitation database.UserInvitation) Activation {
	return Activation{MembershipID: invitation.MembershipID, Email: invitation.Email, Name: invitation.Name, NewIdentity: invitation.NewIdentity, Status: invitation.Status}
}

func validateCSRF(locator, csrf string, digest []byte, pepper []byte) error {
	proof, err := security.CSRFProof(security.HashToken(locator), csrf, pepper)
	if err != nil || len(digest) != 32 || !security.Equal(proof, bytes32(digest)) {
		return apperror.New(apperror.Forbidden, "csrf", "invalid request proof")
	}
	return nil
}

func bytes32(value []byte) [32]byte { var result [32]byte; copy(result[:], value); return result }

func newCode() (string, error) {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(raw[:])%1_000_000), nil
}
