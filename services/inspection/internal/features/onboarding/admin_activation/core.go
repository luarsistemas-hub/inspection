// Package admin_activation owns the separate owner-to-Admin activation challenge.
package admin_activation

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/onboarding/coordinator"
	"inspection/services/inspection/internal/features/onboarding/session"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Provider interface {
	SetInitialPassword(context.Context, string, string) error
}
type Notifier interface {
	SendOTP(context.Context, string, string) error
}

type Service struct {
	DB       *gorm.DB
	Pepper   []byte
	Limits   session.Limits
	Notifier Notifier
	Provider Provider
	Clock    func() time.Time
}

type Activation struct {
	TenantID, IdentityID identity.ID
	Subject              string
	Purpose, Status      string
	ActivatedAt          *time.Time
}

func (s Service) RequestOTP(ctx context.Context, locator, csrf string) error {
	if s.DB == nil || len(s.Pepper) < 32 || s.Limits == nil || s.Notifier == nil {
		return errors.New("admin activation: missing dependency")
	}
	now := s.now()
	var owner database.OnboardingSession
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		owner, err = s.validateSession(tx, locator, csrf, now, false)
		return err
	})
	if err != nil {
		return err
	}
	if result, err := s.Limits.AllowSend(ctx, owner.Email+":activation"); err != nil {
		return apperror.Wrap(apperror.DependencyUnavailable, err)
	} else if !result.Allowed {
		return apperror.New(apperror.RateLimited, "", "retry later")
	}
	code, err := newCode()
	if err != nil {
		return err
	}
	// Keep the activation challenge separate from the onboarding challenge even
	// when both are sent to the same address.
	codeHash, err := security.HashOTP(normalizeCode(code), s.Pepper)
	if err != nil {
		return err
	}
	if err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.OnboardingOTPChallenge{}).Where("session_id=? AND purpose=? AND verified_at IS NULL", owner.ID, session.PurposeActivation).Update("expires_at", now).Error; err != nil {
			return err
		}
		return tx.Create(&database.OnboardingOTPChallenge{ID: identity.NewID(), TenantID: owner.TenantID, SessionID: owner.ID, Purpose: session.PurposeActivation, CodeHMAC: codeHash[:], ExpiresAt: now.Add(security.OTPTTL), CreatedAt: now}).Error
	}); err != nil {
		return err
	}
	return s.Notifier.SendOTP(ctx, owner.Email, code)
}

func (s Service) VerifyOTP(ctx context.Context, locator, csrf, code string) (Activation, error) {
	if s.DB == nil || len(s.Pepper) < 32 || s.Limits == nil {
		return Activation{}, errors.New("admin activation: missing dependency")
	}
	now := s.now()
	var result Activation
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		owner, err := s.validateSession(tx, locator, csrf, now, false)
		if err != nil {
			return err
		}
		if limit, err := s.Limits.AllowAttempt(ctx, owner.Email+":activation"); err != nil {
			return apperror.Wrap(apperror.DependencyUnavailable, err)
		} else if !limit.Allowed {
			return apperror.New(apperror.RateLimited, "", "retry later")
		}
		var challenge database.OnboardingOTPChallenge
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("session_id=? AND purpose=? AND verified_at IS NULL", owner.ID, session.PurposeActivation).Order("created_at DESC").First(&challenge).Error; err != nil || !security.Active(now, challenge.ExpiresAt, nil) || challenge.Attempts >= 5 {
			return apperror.New(apperror.InvalidInput, "code", "invalid code")
		}
		actual, err := security.HashOTP(normalizeCode(code), s.Pepper)
		if err != nil || !security.Equal(actual, bytes32(challenge.CodeHMAC)) {
			if updateErr := tx.Model(&challenge).UpdateColumn("attempts", gorm.Expr("attempts + 1")).Error; updateErr != nil {
				return updateErr
			}
			return apperror.New(apperror.InvalidInput, "code", "invalid code")
		}
		if err := tx.Model(&challenge).Updates(map[string]any{"verified_at": now}).Error; err != nil {
			return err
		}
		var membership database.Membership
		if err := tx.Where("tenant_id=? AND subject=? AND role=?", *owner.TenantID, owner.OwnerSubject, "TENANT_ADMIN").First(&membership).Error; err != nil {
			return apperror.New(apperror.InvalidState, "activation", "owner membership is not ready")
		}
		identityID := membership.IdentityID
		row := database.OnboardingActivation{TenantID: *owner.TenantID, IdentityID: identityID, Purpose: session.PurposeActivation, Status: "VERIFIED", IdempotencyKey: owner.ID.String(), CreatedAt: now, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}}, DoUpdates: clause.Assignments(map[string]any{"status": "VERIFIED", "updated_at": now})}).Create(&row).Error; err != nil {
			return err
		}
		result = Activation{TenantID: *owner.TenantID, IdentityID: identityID, Subject: owner.OwnerSubject, Purpose: session.PurposeActivation, Status: "VERIFIED"}
		return nil
	})
	return result, err
}

func (s Service) SetInitialPassword(ctx context.Context, activation Activation, password string) (Activation, error) {
	if s.DB == nil || s.Provider == nil {
		return Activation{}, errors.New("admin activation: missing dependency")
	}
	if strings.TrimSpace(password) == "" || len(password) < 12 {
		return Activation{}, apperror.New(apperror.InvalidInput, "password", "password does not meet policy")
	}
	var row database.OnboardingActivation
	if err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT set_config('app.tenant_id', ?, true)", activation.TenantID.String()).Error; err != nil {
			return err
		}
		return tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND identity_id=? AND purpose=?", activation.TenantID, activation.IdentityID, session.PurposeActivation).First(&row).Error
	}); err != nil {
		return Activation{}, apperror.New(apperror.Unauthenticated, "activation", "activation verification required")
	}
	if row.Status == "ACTIVE" {
		return Activation{TenantID: row.TenantID, IdentityID: row.IdentityID, Subject: activation.Subject, Purpose: row.Purpose, Status: row.Status, ActivatedAt: row.ActivatedAt}, nil
	}
	if row.Status != "VERIFIED" {
		return Activation{}, apperror.New(apperror.Conflict, "activation", "activation is already in progress")
	}
	if err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT set_config('app.tenant_id', ?, true)", activation.TenantID.String()).Error; err != nil {
			return err
		}
		result := tx.Model(&database.OnboardingActivation{}).
			Where("tenant_id=? AND identity_id=? AND purpose=? AND status=?", activation.TenantID, activation.IdentityID, session.PurposeActivation, "VERIFIED").
			Updates(map[string]any{"status": "ACTIVATING", "updated_at": s.now()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "activation", "activation is already in progress")
		}
		return nil
	}); err != nil {
		return Activation{}, err
	}
	if err := s.Provider.SetInitialPassword(ctx, activation.Subject, password); err != nil {
		// Release the claim only when the row is still ours. If this update
		// fails, leaving ACTIVATING is fail-closed: a retry cannot replace a
		// credential whose provider outcome is unknown.
		_ = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SELECT set_config('app.tenant_id', ?, true)", activation.TenantID.String()).Error; err != nil {
				return err
			}
			return tx.Model(&database.OnboardingActivation{}).
				Where("tenant_id=? AND identity_id=? AND purpose=? AND status=?", activation.TenantID, activation.IdentityID, session.PurposeActivation, "ACTIVATING").
				Updates(map[string]any{"status": "VERIFIED", "updated_at": s.now()}).Error
		})
		return Activation{}, apperror.Wrap(apperror.DependencyUnavailable, err)
	}
	now := s.now()
	if err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT set_config('app.tenant_id', ?, true)", activation.TenantID.String()).Error; err != nil {
			return err
		}
		result := tx.Model(&database.OnboardingActivation{}).Where("tenant_id=? AND identity_id=? AND purpose=? AND status=?", activation.TenantID, activation.IdentityID, session.PurposeActivation, "ACTIVATING").Updates(map[string]any{"status": "ACTIVE", "activated_at": now, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "activation", "activation is already in progress")
		}
		return tx.Model(&database.Membership{}).Where("tenant_id=? AND identity_id=? AND role=?", activation.TenantID, activation.IdentityID, "TENANT_ADMIN").Updates(map[string]any{"status": "ACTIVE", "updated_at": now}).Error
	}); err != nil {
		return Activation{}, apperror.Wrap(apperror.DependencyUnavailable, err)
	}
	return Activation{TenantID: row.TenantID, IdentityID: row.IdentityID, Subject: activation.Subject, Purpose: row.Purpose, Status: "ACTIVE", ActivatedAt: &now}, nil
}

func (s Service) SetInitialPasswordForSession(ctx context.Context, locator, csrf, password string) (Activation, error) {
	if s.DB == nil {
		return Activation{}, errors.New("admin activation: missing database")
	}
	now := s.now()
	var owner database.OnboardingSession
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		owner, err = s.validateSession(tx, locator, csrf, now, true)
		return err
	})
	if err != nil {
		return Activation{}, err
	}
	var membership database.Membership
	if err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT set_config('app.tenant_id', ?, true)", owner.TenantID.String()).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND subject=? AND role=?", *owner.TenantID, owner.OwnerSubject, "TENANT_ADMIN").First(&membership).Error
	}); err != nil {
		return Activation{}, apperror.New(apperror.InvalidState, "activation", "owner membership is not ready")
	}
	activation := Activation{TenantID: *owner.TenantID, IdentityID: membership.IdentityID, Subject: owner.OwnerSubject, Purpose: session.PurposeActivation}
	return s.SetInitialPassword(ctx, activation, password)
}

func (s Service) now() time.Time {
	if s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}

func (s Service) validateSession(tx *gorm.DB, locator, csrf string, now time.Time, requireVerified bool) (database.OnboardingSession, error) {
	if err := setSessionDigest(tx, locator); err != nil {
		return database.OnboardingSession{}, err
	}
	var owner database.OnboardingSession
	if err := tx.Where("session_locator_digest=?", hash(locator)).First(&owner).Error; err != nil || owner.TenantID == nil || owner.OwnerSubject == "" || !security.Active(now, owner.ExpiresAt, nil) {
		return database.OnboardingSession{}, apperror.New(apperror.SessionExpired, "session", "verified onboarding session required")
	}
	if err := validateCSRF(locator, csrf, owner.CSRFDigest, s.Pepper); err != nil {
		return database.OnboardingSession{}, err
	}
	if requireVerified && !activationSessionState(owner.State) {
		return database.OnboardingSession{}, apperror.New(apperror.SessionExpired, "session", "verified onboarding session required")
	}
	return owner, nil
}

func activationSessionState(state string) bool {
	switch state {
	case session.StateVerified,
		coordinator.StateAgencySaved,
		coordinator.StatePropertySaved,
		coordinator.StateParticipantSaved,
		coordinator.StateReadyToSubmit:
		return true
	default:
		return false
	}
}

func validateCSRF(locator, csrf string, digest []byte, pepper []byte) error {
	proof, err := security.CSRFProof(security.HashToken(locator), csrf, pepper)
	if err != nil || len(digest) != 32 || !security.Equal(proof, bytes32(digest)) {
		return apperror.New(apperror.Forbidden, "csrf", "invalid request proof")
	}
	return nil
}
func hash(value string) []byte      { digest := security.HashToken(value); return digest[:] }
func bytes32(value []byte) [32]byte { var result [32]byte; copy(result[:], value); return result }
func setSessionDigest(tx *gorm.DB, locator string) error {
	digest := security.HashToken(locator)
	return tx.Exec("SELECT set_config('app.onboarding_session_digest', ?, true)", fmt.Sprintf("%x", digest[:])).Error
}
func normalizeCode(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 6 {
		return value
	}
	return "000000"
}

func newCode() (string, error) {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(raw[:])%1000000), nil
}
