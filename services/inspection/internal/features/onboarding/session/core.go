// Package session owns the public onboarding identity boundary.
package session

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/onboarding/coordinator"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/ratelimit"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	PurposeOnboarding         = "ONBOARDING"
	PurposeActivation         = "ADMIN_ACTIVATION"
	StatePending              = "IDENTITY_PENDING"
	StateVerified             = "IDENTITY_VERIFIED"
	StateReplaced             = "IDENTITY_REPLACED"
	StepIdentity              = "IDENTITY"
	StepAgency                = "AGENCY"
	StepProperty              = "PROPERTY"
	StepOrigin                = "ORIGIN"
	StepParticipant           = "PARTICIPANT"
	StepReady                 = "READY_TO_SUBMIT"
	maxCheckpointPayloadBytes = 128 * 1024
)

var ErrPurposeMismatch = apperror.New(apperror.InvalidInput, "code", "invalid code")
var ErrAttemptReplaced = apperror.New(apperror.SessionExpired, "session", "onboarding session replaced")

type Owner struct {
	Name, Email, Subject, Issuer string
}

type Session struct {
	ID             identity.ID
	TenantID       *identity.ID
	Locator        string
	CSRF           string
	Owner          Owner
	ExistingAgency *Agency
	CompletedSteps map[string]coordinator.StepPayload
	State          string
	CurrentStep    string
	Version        int64
	ExpiresAt      time.Time
}

// Submission is the authenticated, server-confirmed onboarding aggregate used
// by the completion slice. Draft values from the browser are never trusted.
type Submission struct {
	Session Session
	Steps   map[string]coordinator.StepPayload
}

// Agency is an existing permanent onboarding resource associated with the
// verified email. It is informational during a restarted onboarding journey.
type Agency struct {
	TenantID         identity.ID
	BusinessUnitID   identity.ID
	Name             string
	BusinessUnitCode string
	Status           string
}

type promotionResult struct {
	Accepted             bool
	OwnerSubject         sql.NullString
	AgencyTenantID       sql.NullString
	AgencyBusinessUnitID sql.NullString
	AgencyName           sql.NullString
	AgencyCode           sql.NullString
	AgencyStatus         sql.NullString
}

type prepareResult struct {
	PreviousPendingSessionID sql.NullString
	VerifiedSessionID        sql.NullString
}

type agencyResult struct {
	TenantID       sql.NullString
	BusinessUnitID sql.NullString
	Name           sql.NullString
	Code           sql.NullString
	Status         sql.NullString
}

type Notifier interface {
	SendOTP(context.Context, string, string) error
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
	Stage    string
	Clock    func() time.Time
	NewID    func() identity.ID
}

type RequestResult struct {
	SessionID identity.ID
	Locator   string
	Status    string
}

func requestOTPLimitKeys(email, ip string) []string {
	return []string{"email:" + email, "ip:" + ip}
}

func (s Service) now() time.Time {
	if s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}

func (s Service) id() identity.ID {
	if s.NewID != nil {
		return s.NewID()
	}
	return identity.NewID()
}

func (s Service) validate() error {
	if s.DB == nil || len(s.Pepper) < 32 || s.Limits == nil || s.Notifier == nil {
		return errors.New("onboarding session: missing dependency")
	}
	return nil
}

func NormalizeEmail(value string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(value))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || !strings.Contains(email, "@") {
		return "", apperror.New(apperror.InvalidInput, "email", "invalid email")
	}
	if len(email) > 320 {
		return "", apperror.New(apperror.InvalidInput, "email", "invalid email")
	}
	return email, nil
}

func ValidateOwner(name, email string) (Owner, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Owner{}, apperror.New(apperror.InvalidInput, "name", "name is required")
	}
	if len([]rune(name)) > 200 {
		return Owner{}, apperror.New(apperror.InvalidInput, "name", "name is too long")
	}
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return Owner{}, err
	}
	return Owner{Name: name, Email: normalized}, nil
}

func (s Service) RequestOTP(ctx context.Context, name, email, ip string) (RequestResult, error) {
	if err := s.validate(); err != nil {
		return RequestResult{}, err
	}
	owner, err := ValidateOwner(name, email)
	if err != nil {
		return RequestResult{}, err
	}
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return RequestResult{}, apperror.New(apperror.InvalidInput, "", "client address required")
	}
	if s.enforcesRateLimit() {
		for _, key := range requestOTPLimitKeys(owner.Email, ip) {
			if result, err := s.Limits.AllowSend(ctx, key); err != nil {
				return RequestResult{}, apperror.Wrap(apperror.DependencyUnavailable, err)
			} else if !result.Allowed {
				return RequestResult{}, apperror.New(apperror.RateLimited, "", "retry later")
			}
		}
	}
	code, err := newCode()
	if err != nil {
		return RequestResult{}, apperror.Wrap(apperror.Internal, err)
	}
	hash, err := security.HashOTP(code, s.Pepper)
	if err != nil {
		return RequestResult{}, apperror.Wrap(apperror.Internal, err)
	}
	locator, err := security.NewOpaqueToken()
	if err != nil {
		return RequestResult{}, apperror.Wrap(apperror.Internal, err)
	}
	now := s.now()
	locatorDigest := digest(locator)
	row := database.OnboardingSession{ID: s.id(), Email: owner.Email, OwnerName: owner.Name, SessionLocatorDigest: locatorDigest[:], State: StatePending, CurrentStep: StepIdentity, Version: 1, ExpiresAt: now.Add(security.SessionTTL), CreatedAt: now, UpdatedAt: now}
	challenge := database.OnboardingOTPChallenge{ID: s.id(), SessionID: row.ID, Purpose: PurposeOnboarding, CodeHMAC: hash[:], ExpiresAt: now.Add(security.OTPTTL), CreatedAt: now}
	if err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := setSessionDigest(tx, locatorDigest); err != nil {
			return err
		}
		var prepared prepareResult
		if err := tx.Raw(`SELECT * FROM onboarding.prepare_email_attempt(?, ?, ?)`, owner.Email, row.ID, now).Scan(&prepared).Error; err != nil {
			return err
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return tx.Create(&challenge).Error
	}); err != nil {
		return RequestResult{}, apperror.Wrap(apperror.Internal, err)
	}
	if s.sendsOTPEmail() {
		if err := s.Notifier.SendOTP(ctx, owner.Email, code); err != nil {
			_ = s.DB.WithContext(ctx).Exec(`SELECT onboarding.abort_email_attempt(?, ?, ?, ?)`, owner.Email, row.ID, "DELIVERY_FAILED", s.now()).Error
			return RequestResult{}, apperror.Wrap(apperror.DependencyUnavailable, err)
		}
	}
	return RequestResult{SessionID: row.ID, Locator: locator, Status: "SENT"}, nil
}

func (s Service) VerifyOTP(ctx context.Context, locator, code string) (Session, error) {
	if err := s.validate(); err != nil {
		return Session{}, err
	}
	if strings.TrimSpace(locator) == "" {
		return Session{}, apperror.New(apperror.InvalidInput, "sessionLocator", "invalid session")
	}
	locatorDigest := digest(locator)
	if s.enforcesRateLimit() {
		if result, err := s.Limits.AllowAttempt(ctx, hex.EncodeToString(locatorDigest[:])); err != nil {
			return Session{}, apperror.Wrap(apperror.DependencyUnavailable, err)
		} else if !result.Allowed {
			return Session{}, apperror.New(apperror.RateLimited, "", "retry later")
		}
	}
	now := s.now()
	var result Session
	invalidCode := false
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := setSessionDigest(tx, locatorDigest); err != nil {
			return err
		}
		var row database.OnboardingSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("session_locator_digest = ?", locatorDigest[:]).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.InvalidInput, "code", "invalid code")
			}
			return err
		}
		if !security.Active(now, row.ExpiresAt, nil) || row.State != StatePending || row.ReplacedAt != nil {
			return apperror.New(apperror.SessionExpired, "", "session expired")
		}
		var challenge database.OnboardingOTPChallenge
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("session_id=? AND purpose=? AND verified_at IS NULL", row.ID, PurposeOnboarding).Order("created_at DESC").First(&challenge).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.InvalidInput, "code", "invalid code")
			}
			return err
		}
		if !security.Active(now, challenge.ExpiresAt, nil) || challenge.Attempts >= 5 {
			return apperror.New(apperror.InvalidInput, "code", "invalid code")
		}
		if !s.acceptsOTPCode(code, challenge.CodeHMAC) {
			if err := tx.Model(&challenge).UpdateColumn("attempts", gorm.Expr("attempts + 1")).Error; err != nil {
				return err
			}
			invalidCode = true
			return nil
		}
		newLocator, err := security.NewOpaqueToken()
		if err != nil {
			return err
		}
		csrf, err := security.NewOpaqueToken()
		if err != nil {
			return err
		}
		newDigest := digest(newLocator)
		csrfDigest, err := security.CSRFProof(newDigest, csrf, s.Pepper)
		if err != nil {
			return err
		}
		var promoted promotionResult
		if err := tx.Raw(`SELECT * FROM onboarding.promote_email_session(?, ?, ?, ?, ?)`, row.Email, row.ID, newDigest[:], csrfDigest[:], now).Scan(&promoted).Error; err != nil {
			return err
		}
		if !promoted.Accepted {
			return ErrAttemptReplaced
		}
		var agency *Agency
		if promoted.AgencyTenantID.Valid && promoted.AgencyBusinessUnitID.Valid {
			agencyTenantID, err := identity.ParseID(promoted.AgencyTenantID.String)
			if err != nil {
				return err
			}
			agencyBusinessUnitID, err := identity.ParseID(promoted.AgencyBusinessUnitID.String)
			if err != nil {
				return err
			}
			agency = &Agency{TenantID: agencyTenantID, BusinessUnitID: agencyBusinessUnitID, Name: promoted.AgencyName.String, BusinessUnitCode: promoted.AgencyCode.String, Status: promoted.AgencyStatus.String}
		}
		subject := row.OwnerSubject
		if promoted.OwnerSubject.Valid {
			subject = promoted.OwnerSubject.String
		}
		result = Session{ID: row.ID, TenantID: row.TenantID, Locator: newLocator, CSRF: csrf, Owner: Owner{Name: row.OwnerName, Email: row.Email, Subject: subject}, ExistingAgency: agency, State: StateVerified, CurrentStep: StepAgency, Version: row.Version + 1, ExpiresAt: row.ExpiresAt}
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	if invalidCode {
		return Session{}, apperror.New(apperror.InvalidInput, "code", "invalid code")
	}
	return result, nil
}

func (s Service) Load(ctx context.Context, locator string) (Session, error) {
	if s.DB == nil || locator == "" {
		return Session{}, apperror.New(apperror.SessionExpired, "", "session expired")
	}
	now := s.now()
	locatorDigest := digest(locator)
	var row database.OnboardingSession
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := setSessionDigest(tx, locatorDigest); err != nil {
			return err
		}
		return tx.Where("session_locator_digest = ?", locatorDigest[:]).First(&row).Error
	})
	if err != nil || !security.Active(now, row.ExpiresAt, nil) || row.State == StatePending || row.State == StateReplaced || row.ReplacedAt != nil {
		return Session{}, apperror.New(apperror.SessionExpired, "", "session expired")
	}
	agency, err := s.loadAgency(ctx, row.ID, locator)
	if err != nil {
		return Session{}, apperror.Wrap(apperror.DependencyUnavailable, err)
	}
	return Session{ID: row.ID, TenantID: row.TenantID, Owner: Owner{Name: row.OwnerName, Email: row.Email, Subject: row.OwnerSubject}, ExistingAgency: agency, State: row.State, CurrentStep: row.CurrentStep, Version: row.Version, ExpiresAt: row.ExpiresAt}, nil
}

// LoadAndRefreshCSRF restores a verified browser session and rotates its CSRF
// proof. The proof is returned in a response header, so a cross-site caller
// cannot read it even when the HttpOnly session cookie is sent by the browser.
func (s Service) LoadAndRefreshCSRF(ctx context.Context, locator string) (Session, error) {
	if s.DB == nil || len(s.Pepper) < 32 || locator == "" {
		return Session{}, apperror.New(apperror.SessionExpired, "", "session expired")
	}
	csrf, err := security.NewOpaqueToken()
	if err != nil {
		return Session{}, apperror.Wrap(apperror.Internal, err)
	}
	now := s.now()
	locatorDigest := digest(locator)
	var row database.OnboardingSession
	steps := map[string]coordinator.StepPayload{}
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := setSessionDigest(tx, locatorDigest); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("session_locator_digest = ?", locatorDigest[:]).First(&row).Error; err != nil {
			return err
		}
		if err := s.validateCurrentSession(tx, row, now); err != nil {
			return err
		}
		proof, err := security.CSRFProof(locatorDigest, csrf, s.Pepper)
		if err != nil {
			return err
		}
		if err := tx.Model(&row).Updates(map[string]any{"csrf_digest": proof[:], "updated_at": now}).Error; err != nil {
			return err
		}
		var records []database.OnboardingStepRecord
		if err := tx.Where("session_id = ?", row.ID).Order("version ASC").Find(&records).Error; err != nil {
			return err
		}
		for _, record := range records {
			var payload coordinator.StepPayload
			if err := json.Unmarshal(record.Payload, &payload); err != nil {
				return err
			}
			steps[strings.ToLower(record.Step)] = payload
		}
		return nil
	})
	if err != nil {
		return Session{}, apperror.New(apperror.SessionExpired, "", "session expired")
	}
	agency, err := s.loadAgency(ctx, row.ID, locator)
	if err != nil {
		return Session{}, apperror.Wrap(apperror.DependencyUnavailable, err)
	}
	return Session{ID: row.ID, TenantID: row.TenantID, CSRF: csrf, Owner: Owner{Name: row.OwnerName, Email: row.Email, Subject: row.OwnerSubject}, ExistingAgency: agency, CompletedSteps: steps, State: row.State, CurrentStep: row.CurrentStep, Version: row.Version, ExpiresAt: row.ExpiresAt}, nil
}

// ValidateCSRF authenticates a public onboarding mutation without changing
// the session version or rotating the browser's CSRF proof.
func (s Service) ValidateCSRF(ctx context.Context, locator, csrf string) (Session, error) {
	if s.DB == nil || len(s.Pepper) < 32 || locator == "" || csrf == "" {
		return Session{}, apperror.New(apperror.SessionExpired, "", "session expired")
	}
	now := s.now()
	locatorDigest := digest(locator)
	var row database.OnboardingSession
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := setSessionDigest(tx, locatorDigest); err != nil {
			return err
		}
		if err := tx.Where("session_locator_digest=?", locatorDigest[:]).First(&row).Error; err != nil {
			return err
		}
		if err := s.validateCurrentSession(tx, row, now); err != nil {
			return err
		}
		proof, err := security.CSRFProof(locatorDigest, csrf, s.Pepper)
		if err != nil || len(row.CSRFDigest) != 32 || !security.Equal(proof, bytes32(row.CSRFDigest)) {
			return apperror.New(apperror.Forbidden, "csrf", "invalid request proof")
		}
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	return Session{ID: row.ID, TenantID: row.TenantID, Owner: Owner{Name: row.OwnerName, Email: row.Email, Subject: row.OwnerSubject}, State: row.State, CurrentStep: row.CurrentStep, Version: row.Version, ExpiresAt: row.ExpiresAt}, nil
}

// LoadSubmission authenticates the mutation proof and returns the immutable
// checkpoint records that make up the onboarding request.
func (s Service) LoadSubmission(ctx context.Context, locator, csrf string) (Submission, error) {
	if s.DB == nil || len(s.Pepper) < 32 || locator == "" {
		return Submission{}, apperror.New(apperror.SessionExpired, "", "session expired")
	}
	now := s.now()
	locatorDigest := digest(locator)
	var row database.OnboardingSession
	steps := map[string]coordinator.StepPayload{}
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := setSessionDigest(tx, locatorDigest); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("session_locator_digest = ?", locatorDigest[:]).First(&row).Error; err != nil {
			return err
		}
		if err := s.validateCurrentSession(tx, row, now); err != nil {
			return err
		}
		proof, err := security.CSRFProof(locatorDigest, csrf, s.Pepper)
		if err != nil || len(row.CSRFDigest) != 32 || !security.Equal(proof, bytes32(row.CSRFDigest)) {
			return apperror.New(apperror.Forbidden, "csrf", "invalid request proof")
		}
		var records []database.OnboardingStepRecord
		if err := tx.Where("session_id = ?", row.ID).Order("version ASC").Find(&records).Error; err != nil {
			return err
		}
		for _, record := range records {
			var payload coordinator.StepPayload
			if err := json.Unmarshal(record.Payload, &payload); err != nil {
				return fmt.Errorf("decode onboarding step %s: %w", record.Step, err)
			}
			steps[record.Step] = payload
		}
		return nil
	})
	if err != nil {
		return Submission{}, err
	}
	return Submission{Session: Session{ID: row.ID, TenantID: row.TenantID, Owner: Owner{Name: row.OwnerName, Email: row.Email, Subject: row.OwnerSubject}, State: row.State, CurrentStep: row.CurrentStep, Version: row.Version, ExpiresAt: row.ExpiresAt}, Steps: steps}, nil
}

// MarkSubmitted persists the terminal public-onboarding transition after
// the durable request and inspection have been created.
func (s Service) MarkSubmitted(ctx context.Context, locator, csrf string) (Session, error) {
	submission, err := s.LoadSubmission(ctx, locator, csrf)
	if err != nil {
		return Session{}, err
	}
	locatorDigest := digest(locator)
	now := s.now()
	row := submission.Session
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := setSessionDigest(tx, locatorDigest); err != nil {
			return err
		}
		result := tx.Model(&database.OnboardingSession{}).Where("id = ? AND version = ?", row.ID, row.Version).Updates(map[string]any{"state": coordinator.StateSubmitted, "current_step": StepReady, "version": row.Version + 1, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "session", "stale onboarding session")
		}
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	row.State, row.CurrentStep, row.Version = coordinator.StateSubmitted, StepReady, row.Version+1
	return row, nil
}

func (s Service) validateCurrentSession(tx *gorm.DB, row database.OnboardingSession, now time.Time) error {
	if !security.Active(now, row.ExpiresAt, nil) || row.State == StatePending || row.State == StateReplaced || row.ReplacedAt != nil {
		return apperror.New(apperror.SessionExpired, "", "session expired")
	}
	var current bool
	if err := tx.Raw(`SELECT onboarding.is_current_verified_session(?, ?)`, row.Email, row.ID).Scan(&current).Error; err != nil {
		return err
	}
	if !current {
		return ErrAttemptReplaced
	}
	return nil
}

func (s Service) Checkpoint(ctx context.Context, locator, csrf, step string, expectedVersion int64, payload map[string]any) (Session, error) {
	if s.DB == nil || len(s.Pepper) < 32 || locator == "" {
		return Session{}, apperror.New(apperror.SessionExpired, "", "session expired")
	}
	step = strings.ToUpper(strings.TrimSpace(step))
	if step == StepIdentity || step == "" {
		return Session{}, apperror.New(apperror.InvalidInput, "step", "invalid step")
	}
	now := s.now()
	var result Session
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := s.validateCheckpoint(tx, locator, csrf, step, expectedVersion, payload, now)
		if err != nil {
			return err
		}
		raw, err := marshalCheckpointPayload(payload)
		if err != nil {
			return err
		}
		digestPayload := sha256.Sum256(raw)
		record := database.OnboardingStepRecord{ID: s.id(), SessionID: row.ID, TenantID: row.TenantID, Step: step, Payload: raw, PayloadDigest: hex.EncodeToString(digestPayload[:]), Version: expectedVersion + 1, CompletedAt: now}
		if err := tx.Where("session_id=? AND step=?", row.ID, step).First(&database.OnboardingStepRecord{}).Error; err == nil {
			return apperror.New(apperror.Conflict, "step", "step already saved")
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		nextState := row.State
		if step == StepAgency {
			nextState = "AGENCY_SAVED"
		} else if step == StepProperty {
			nextState = "PROPERTY_SAVED"
		} else if step == StepParticipant {
			nextState = "PARTICIPANT_SAVED"
		}
		if err := tx.Model(&row).Updates(map[string]any{"current_step": step, "state": nextState, "version": expectedVersion + 1, "updated_at": now}).Error; err != nil {
			return err
		}
		result = Session{ID: row.ID, TenantID: row.TenantID, Owner: Owner{Name: row.OwnerName, Email: row.Email, Subject: row.OwnerSubject}, State: nextState, CurrentStep: step, Version: expectedVersion + 1, ExpiresAt: row.ExpiresAt}
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	if agency, agencyErr := s.loadAgency(ctx, result.ID, locator); agencyErr != nil {
		return Session{}, apperror.Wrap(apperror.DependencyUnavailable, agencyErr)
	} else {
		result.ExistingAgency = agency
	}
	return result, nil
}

func (s Service) loadAgency(ctx context.Context, sessionID identity.ID, locator string) (*Agency, error) {
	var value agencyResult
	locatorDigest := digest(locator)
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := setSessionDigest(tx, locatorDigest); err != nil {
			return err
		}
		return tx.Raw(`SELECT * FROM onboarding.load_session_agency(?, ?)`, sessionID, locatorDigest[:]).Scan(&value).Error
	})
	if err != nil {
		return nil, err
	}
	if !value.TenantID.Valid || !value.BusinessUnitID.Valid {
		return nil, nil
	}
	tenantID, err := identity.ParseID(value.TenantID.String)
	if err != nil {
		return nil, err
	}
	unitID, err := identity.ParseID(value.BusinessUnitID.String)
	if err != nil {
		return nil, err
	}
	return &Agency{TenantID: tenantID, BusinessUnitID: unitID, Name: value.Name.String, BusinessUnitCode: value.Code.String, Status: value.Status.String}, nil
}

// ValidateCheckpoint performs the session, CSRF, version, ordering, and
// payload checks without persisting a checkpoint. It is used to reject stale
// or invalid agency requests before external owner provisioning starts.
func (s Service) ValidateCheckpoint(ctx context.Context, locator, csrf, step string, expectedVersion int64, payload map[string]any) error {
	if s.DB == nil || len(s.Pepper) < 32 || locator == "" {
		return apperror.New(apperror.SessionExpired, "", "session expired")
	}
	step = strings.ToUpper(strings.TrimSpace(step))
	if step == StepIdentity || step == "" {
		return apperror.New(apperror.InvalidInput, "step", "invalid step")
	}
	now := s.now()
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, err := s.validateCheckpoint(tx, locator, csrf, step, expectedVersion, payload, now)
		return err
	})
}

func (s Service) validateCheckpoint(tx *gorm.DB, locator, csrf, step string, expectedVersion int64, payload map[string]any, now time.Time) (database.OnboardingSession, error) {
	if _, err := marshalCheckpointPayload(payload); err != nil {
		return database.OnboardingSession{}, err
	}
	if err := setSessionDigest(tx, digest(locator)); err != nil {
		return database.OnboardingSession{}, err
	}
	locatorDigest := digest(locator)
	var row database.OnboardingSession
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("session_locator_digest = ?", locatorDigest[:]).First(&row).Error; err != nil || !security.Active(now, row.ExpiresAt, nil) || row.State == StatePending || row.State == StateReplaced || row.ReplacedAt != nil {
		return database.OnboardingSession{}, apperror.New(apperror.SessionExpired, "", "session expired")
	}
	var current bool
	if err := tx.Raw(`SELECT onboarding.is_current_verified_session(?, ?)`, row.Email, row.ID).Scan(&current).Error; err != nil {
		return database.OnboardingSession{}, err
	}
	if !current {
		return database.OnboardingSession{}, ErrAttemptReplaced
	}
	proof, err := security.CSRFProof(digest(locator), csrf, s.Pepper)
	if err != nil || len(row.CSRFDigest) != 32 || !security.Equal(proof, bytes32(row.CSRFDigest)) {
		return database.OnboardingSession{}, apperror.New(apperror.Forbidden, "csrf", "invalid request proof")
	}
	if expectedVersion != row.Version {
		return database.OnboardingSession{}, apperror.New(apperror.Conflict, "expectedVersion", "stale onboarding session")
	}
	if err := coordinator.ValidateCheckpointOrder(row.CurrentStep, step); err != nil {
		return database.OnboardingSession{}, err
	}
	if stepRank(step) < stepRank(row.CurrentStep) {
		return database.OnboardingSession{}, apperror.New(apperror.InvalidState, "step", "step already completed")
	}
	if err := coordinator.ValidateCheckpointPayload(step, coordinator.StepPayload(payload), now); err != nil {
		return database.OnboardingSession{}, err
	}
	if step == StepOrigin {
		if err := validateOriginPhotoSelection(tx, row, coordinator.StepPayload(payload)); err != nil {
			return database.OnboardingSession{}, err
		}
	}
	return row, nil
}

func validateOriginPhotoSelection(tx *gorm.DB, session database.OnboardingSession, payload coordinator.StepPayload) error {
	ids, err := coordinator.OriginMediaIDs(payload)
	if err != nil || len(ids) == 0 {
		return err
	}
	if session.TenantID == nil {
		return apperror.New(apperror.InvalidState, "referencePhotos", "agency setup is incomplete")
	}
	if err := tx.Exec("SELECT set_config('app.tenant_id', ?, true)", session.TenantID.String()).Error; err != nil {
		return err
	}
	var media []database.MediaObject
	if err := tx.Where("tenant_id=? AND responsibility_id=? AND id IN ?", *session.TenantID, session.ID, ids).Find(&media).Error; err != nil {
		return err
	}
	if len(media) != len(ids) {
		return apperror.New(apperror.InvalidInput, "referencePhotos", "reference photos were not uploaded")
	}
	for _, photo := range media {
		if strings.TrimSpace(photo.Description) == "" || photo.RequirementKey != "reference" || (photo.Status != "VERIFIED" && photo.Status != "READY") {
			return apperror.New(apperror.InvalidState, "referencePhotos", "reference photo is unavailable")
		}
	}
	return nil
}

func marshalCheckpointPayload(payload map[string]any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) > maxCheckpointPayloadBytes {
		return nil, apperror.New(apperror.InvalidInput, "payload", "invalid step payload")
	}
	return raw, nil
}

func (s Service) BindOwner(ctx context.Context, locator, issuer, subject string, tenantID identity.ID) error {
	if s.DB == nil || locator == "" || issuer == "" || subject == "" || tenantID == (identity.ID{}) {
		return apperror.New(apperror.InvalidInput, "identity", "verified owner identity is required")
	}
	locatorDigest := digest(locator)
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT set_config('app.onboarding_session_digest', ?, true)", hex.EncodeToString(locatorDigest[:])).Error; err != nil {
			return err
		}
		result := tx.Model(&database.OnboardingSession{}).Where("session_locator_digest=? AND state <> ? AND state <> ?", locatorDigest[:], StatePending, StateReplaced).Updates(map[string]any{"owner_subject": subject, "tenant_id": tenantID, "updated_at": s.now()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return apperror.New(apperror.Unauthenticated, "session", "verified onboarding session required")
		}
		var current bool
		if err := tx.Raw(`SELECT onboarding.is_current_verified_session((SELECT email FROM onboarding.sessions WHERE session_locator_digest = ?), (SELECT id FROM onboarding.sessions WHERE session_locator_digest = ?))`, locatorDigest[:], locatorDigest[:]).Scan(&current).Error; err != nil {
			return err
		}
		if !current {
			return ErrAttemptReplaced
		}
		return nil
	})
	if err != nil {
		var public *apperror.Error
		if errors.As(err, &public) {
			return err
		}
		return apperror.Wrap(apperror.DependencyUnavailable, err)
	}
	return nil
}

func stepRank(step string) int {
	switch strings.ToUpper(step) {
	case StepIdentity:
		return 0
	case StepAgency:
		return 1
	case StepProperty:
		return 2
	case StepOrigin:
		return 3
	case StepParticipant:
		return 4
	case StepReady:
		return 5
	default:
		return -1
	}
}

func digest(value string) [32]byte { return sha256.Sum256([]byte(value)) }

func bytes32(value []byte) [32]byte {
	var result [32]byte
	copy(result[:], value)
	return result
}

func setSessionDigest(tx *gorm.DB, value [32]byte) error {
	return tx.Exec("SELECT set_config('app.onboarding_session_digest', ?, true)", hex.EncodeToString(value[:])).Error
}

func newCode() (string, error) {
	value, err := security.NewOpaqueToken()
	if err != nil {
		return "", err
	}
	// Six digits are derived from random bytes without logging or persisting the code.
	var number uint64
	for _, char := range value[:8] {
		number = number*31 + uint64(char)
	}
	return fmt.Sprintf("%06d", number%1000000), nil
}

func (s Service) sendsOTPEmail() bool {
	return s.enforcesRateLimit()
}

func (s Service) enforcesRateLimit() bool {
	stage := strings.TrimSpace(s.Stage)
	return stage == "" || strings.EqualFold(stage, "production")
}

func (s Service) acceptsOTPCode(code string, expected []byte) bool {
	if !validOTPCode(code) {
		return false
	}
	if !s.sendsOTPEmail() {
		return true
	}
	actual, err := security.HashOTP(code, s.Pepper)
	return err == nil && security.Equal(actual, bytes32(expected))
}

func validOTPCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, char := range code {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
