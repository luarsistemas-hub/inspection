// Package onboarding_bootstrap provisions the verified onboarding owner.
package onboarding_bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Input struct {
	Name, Language, Timezone, BusinessUnitCode, BusinessUnitName string
	Issuer, Subject, IdempotencyKey                              string
}

type Result struct {
	TenantID, BusinessUnitID, MembershipID, IdentityID identity.ID
	Status                                             string
}

type Runner interface {
	Within(context.Context, identity.ID, func(*gorm.DB) error) error
}

type Service struct {
	DB     *gorm.DB
	Runner Runner
	Now    func() time.Time
	NewID  func() identity.ID
}

func (s Service) Provision(ctx context.Context, input Input) (Result, error) {
	if s.DB == nil {
		return Result{}, errors.New("onboarding bootstrap: missing database")
	}
	if s.Runner == nil {
		s.Runner = tenanttx.Runner{DB: s.DB}
	}
	if s.Now == nil {
		s.Now = func() time.Time { return time.Now().UTC() }
	}
	if s.NewID == nil {
		s.NewID = identity.NewID
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Language = strings.TrimSpace(input.Language)
	input.Timezone = strings.TrimSpace(input.Timezone)
	input.BusinessUnitCode = strings.TrimSpace(input.BusinessUnitCode)
	input.BusinessUnitName = strings.TrimSpace(input.BusinessUnitName)
	if input.Name == "" || len([]rune(input.Name)) > 200 {
		return Result{}, apperror.New(apperror.InvalidInput, "name", "invalid name")
	}
	if input.Language == "" {
		input.Language = "pt-BR"
	}
	if input.Timezone == "" {
		input.Timezone = "America/Sao_Paulo"
	}
	if _, err := time.LoadLocation(input.Timezone); err != nil {
		return Result{}, apperror.New(apperror.InvalidInput, "timezone", "invalid timezone")
	}
	if input.BusinessUnitCode == "" || input.BusinessUnitName == "" {
		return Result{}, apperror.New(apperror.InvalidInput, "businessUnit", "initial business unit is required")
	}
	if input.Issuer == "" || input.Subject == "" || input.IdempotencyKey == "" {
		return Result{}, apperror.New(apperror.InvalidInput, "identity", "identity and idempotency key are required")
	}
	identityID := identity.NewDeterministicID("inspection/oidc-identity", input.Issuer+"\x00"+input.Subject)
	tenantID := identity.NewDeterministicID("inspection/onboarding-tenant", input.Issuer+"\x00"+input.Subject)
	result := Result{TenantID: tenantID, BusinessUnitID: s.NewID(), MembershipID: s.NewID(), IdentityID: identityID, Status: "ACTIVE"}
	now := s.Now()
	payloadDigest := sha256.Sum256([]byte(strings.Join([]string{input.Name, input.Language, input.Timezone, input.BusinessUnitCode, input.BusinessUnitName, input.Issuer, input.Subject}, "\x00")))
	subjectDigest := sha256.Sum256([]byte(input.Issuer + "\x00" + input.Subject))
	subjectKey := hex.EncodeToString(subjectDigest[:])
	err := s.Runner.Within(ctx, tenantID, func(tx *gorm.DB) error {
		var prior database.BootstrapRequest
		if err := tx.Where("subject_key=? AND idempotency_key=?", subjectKey, input.IdempotencyKey).First(&prior).Error; err == nil {
			if prior.PayloadDigest != hex.EncodeToString(payloadDigest[:]) {
				return apperror.New(apperror.Conflict, "idempotencyKey", "idempotency payload conflict")
			}
			return json.Unmarshal(prior.Result, &result)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var existing database.BootstrapRequest
		if err := tx.Where("subject_key=?", subjectKey).First(&existing).Error; err == nil {
			return apperror.New(apperror.Conflict, "identity", "identity is already provisioned")
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&database.Tenant{ID: tenantID, TenantID: tenantID, Name: input.Name, Language: input.Language, DefaultTimezone: input.Timezone, Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.BusinessUnit{ID: result.BusinessUnitID, TenantID: tenantID, Code: input.BusinessUnitCode, Name: input.BusinessUnitName, Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		membership := database.Membership{ID: result.MembershipID, TenantID: tenantID, IdentityID: identityID, Issuer: input.Issuer, Subject: input.Subject, Role: "TENANT_ADMIN", Status: "PENDING_ACTIVATION", Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&membership).Error; err != nil {
			return err
		}
		for _, product := range []string{"ADMIN", "DASHBOARD"} {
			if err := tx.Create(&database.ProductEntitlement{ID: s.NewID(), TenantID: tenantID, MembershipID: membership.ID, Product: product, CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Create(&database.BootstrapRequest{ID: s.NewID(), TenantID: tenantID, SubjectKey: subjectKey, IdempotencyKey: input.IdempotencyKey, PayloadDigest: hex.EncodeToString(payloadDigest[:]), Result: raw, CreatedAt: now}).Error
	})
	if err != nil {
		var public *apperror.Error
		if errors.As(err, &public) {
			return Result{}, err
		}
		return Result{}, apperror.Wrap(apperror.DependencyUnavailable, fmt.Errorf("owner bootstrap: %w", err))
	}
	return result, nil
}
