package create_tenant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

const sliceName = "tenancy/create_tenant"

type Command struct{ Name, Language, Timezone, BusinessUnitCode, BusinessUnitName, Issuer, Subject, IdempotencyKey string }
type Result struct {
	TenantID, BusinessUnitID, MembershipID identity.ID
	Status                                 string
}
type Dependencies struct {
	DB    *gorm.DB
	Bus   *mediator.Bus
	Now   func() time.Time
	NewID func() identity.ID
}

// Setup registers the tenant bootstrap command as this slice's sole entry point.
func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil {
		return fmt.Errorf("slice %s: missing dependency", sliceName)
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.NewID == nil {
		deps.NewID = identity.NewID
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) { return handle(ctx, deps, raw.(Command)) })
}

func handle(ctx context.Context, deps Dependencies, cmd Command) (Result, error) {
	cmd.Name = strings.TrimSpace(cmd.Name)
	if cmd.Name == "" {
		return Result{}, apperror.New(apperror.InvalidInput, "name", "name is required")
	}
	if len([]rune(cmd.Name)) > 200 {
		return Result{}, apperror.New(apperror.InvalidInput, "name", "name is too long")
	}
	if cmd.Language == "" {
		cmd.Language = "pt-BR"
	}
	if cmd.Timezone == "" {
		cmd.Timezone = "America/Sao_Paulo"
	}
	if _, err := time.LoadLocation(cmd.Timezone); err != nil {
		return Result{}, apperror.New(apperror.InvalidInput, "timezone", "invalid timezone")
	}
	if cmd.BusinessUnitName == "" || cmd.BusinessUnitCode == "" {
		return Result{}, apperror.New(apperror.InvalidInput, "businessUnit", "initial business unit is required")
	}
	if cmd.Issuer == "" || cmd.Subject == "" || cmd.IdempotencyKey == "" {
		return Result{}, apperror.New(apperror.InvalidInput, "identity", "identity and idempotency key are required")
	}
	// An unbound OIDC identity can bootstrap exactly one tenant. Deriving its
	// tenant ID lets the runtime set the mandatory RLS context before the first
	// insert and makes a retry select the same tenant-scoped request.
	result := Result{TenantID: identity.NewDeterministicID("inspection/bootstrap-tenant", cmd.Issuer+"\x00"+cmd.Subject), BusinessUnitID: deps.NewID(), MembershipID: deps.NewID(), Status: "ACTIVE"}
	now := deps.Now()
	digestBytes := sha256.Sum256([]byte(cmd.Name + "\x00" + cmd.Language + "\x00" + cmd.Timezone + "\x00" + cmd.BusinessUnitCode + "\x00" + cmd.BusinessUnitName))
	digest := hex.EncodeToString(digestBytes[:])
	// PostgreSQL text values cannot contain a NUL byte. A digest preserves an
	// unambiguous issuer/subject identity key without leaking either into an
	// indexable bootstrap key.
	subjectDigest := sha256.Sum256([]byte(cmd.Issuer + "\x00" + cmd.Subject))
	subjectKey := hex.EncodeToString(subjectDigest[:])
	err := (tenanttx.Runner{DB: deps.DB}).Within(ctx, result.TenantID, func(tx *gorm.DB) error {
		var prior database.BootstrapRequest
		err := tx.Where("subject_key=? AND idempotency_key=?", subjectKey, cmd.IdempotencyKey).First(&prior).Error
		if err == nil {
			if prior.PayloadDigest != digest {
				return apperror.New(apperror.Conflict, "idempotencyKey", "idempotency payload conflict")
			}
			return json.Unmarshal(prior.Result, &result)
		}
		if err != gorm.ErrRecordNotFound {
			return err
		}
		var existing database.BootstrapRequest
		if err := tx.Where("subject_key=?", subjectKey).First(&existing).Error; err == nil {
			return apperror.New(apperror.Conflict, "identity", "identity is already provisioned")
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		tenant := database.Tenant{ID: result.TenantID, TenantID: result.TenantID, Name: cmd.Name, Language: cmd.Language, DefaultTimezone: cmd.Timezone, Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&tenant).Error; err != nil {
			return err
		}
		unit := database.BusinessUnit{ID: result.BusinessUnitID, TenantID: result.TenantID, Code: cmd.BusinessUnitCode, Name: cmd.BusinessUnitName, Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&unit).Error; err != nil {
			return err
		}
		membership := database.Membership{ID: result.MembershipID, TenantID: result.TenantID, IdentityID: result.MembershipID, Issuer: cmd.Issuer, Subject: cmd.Subject, Role: "TENANT_ADMIN", Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&membership).Error; err != nil {
			return err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Create(&database.BootstrapRequest{ID: deps.NewID(), TenantID: result.TenantID, SubjectKey: subjectKey, IdempotencyKey: cmd.IdempotencyKey, PayloadDigest: digest, Result: raw, CreatedAt: now}).Error
	})
	if err != nil {
		return Result{}, apperror.Wrap(apperror.Internal, fmt.Errorf("bootstrap tenant: %w", err))
	}
	return result, nil
}
