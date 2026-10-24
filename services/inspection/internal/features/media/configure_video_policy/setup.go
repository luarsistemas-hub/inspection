// Package configure_video_policy owns tenant video limits and their optimistic updates.
package configure_video_policy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	DefaultDurationMinutes = 15
	DefaultOriginDays      = 30
	DefaultCurrentDays     = 30
	MaxDurationMinutes     = 60
	MaxRetentionDays       = 36500
)

type Query struct{ TenantID identity.ID }

// CaptureQuery reads the effective policy after the capture session has been
// validated at the public GraphQL boundary.
type CaptureQuery struct{ TenantID identity.ID }
type Command struct {
	TenantID                                                      identity.ID
	MaxDurationMinutes, OriginRetentionDays, CurrentRetentionDays int
	ExpectedVersion                                               int64
	MutationID                                                    string
}
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
	Within     func(context.Context, identity.ID, func(*gorm.DB) error) error
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice media/configure_video_policy: missing dependency")
	}
	return errorsJoin(
		errorsJoin(d.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
			q := raw.(Query)
			if _, err := d.Authorizer.Authorize(ctx, q.TenantID, []string{auth.TenantAdmin, auth.InspectionConfigAdmin, auth.Manager, auth.Employee, auth.Viewer}, nil, false); err != nil {
				return nil, err
			}
			return load(ctx, d.DB, d.Within, q.TenantID)
		}), d.Bus.RegisterQuery(CaptureQuery{}, func(ctx context.Context, raw any) (any, error) {
			q := raw.(CaptureQuery)
			if _, ok := requestctx.ExternalCredentialsFromContext(ctx); !ok || q.TenantID == (identity.ID{}) {
				return nil, apperror.New(apperror.Unauthenticated, "", "capture session required")
			}
			return load(ctx, d.DB, d.Within, q.TenantID)
		})),
		d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
			c := raw.(Command)
			if _, err := d.Authorizer.Authorize(ctx, c.TenantID, []string{auth.TenantAdmin, auth.InspectionConfigAdmin}, nil, true); err != nil {
				return nil, err
			}
			return configure(ctx, d.DB, d.Within, c)
		}),
	)
}

func errorsJoin(first, second error) error {
	if first != nil {
		return first
	}
	return second
}

func Defaults(tenantID identity.ID) database.VideoPolicy {
	return database.VideoPolicy{TenantID: tenantID, MaxDurationMinutes: DefaultDurationMinutes, OriginRetentionDays: DefaultOriginDays, CurrentRetentionDays: DefaultCurrentDays, SupersededBaselineRetentionDays: 365, Version: 1}
}

func Validate(maxDuration, originDays, currentDays int) error {
	if maxDuration < 1 || maxDuration > MaxDurationMinutes {
		return apperror.New(apperror.InvalidInput, "maxDurationMinutes", "duration must be between 1 and 60 minutes")
	}
	if originDays < 1 || originDays > MaxRetentionDays {
		return apperror.New(apperror.InvalidInput, "originRetentionDays", "retention must be between 1 and 36500 days")
	}
	if currentDays < 1 || currentDays > MaxRetentionDays {
		return apperror.New(apperror.InvalidInput, "currentRetentionDays", "retention must be between 1 and 36500 days")
	}
	return nil
}

func load(ctx context.Context, db *gorm.DB, within func(context.Context, identity.ID, func(*gorm.DB) error) error, tenantID identity.ID) (database.VideoPolicy, error) {
	policy := Defaults(tenantID)
	run := within
	if run == nil {
		run = (tenanttx.Runner{DB: db}).Within
	}
	err := run(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=?", tenantID).First(&policy).Error; err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		return nil
	})
	return policy, err
}

func configure(ctx context.Context, db *gorm.DB, within func(context.Context, identity.ID, func(*gorm.DB) error) error, cmd Command) (database.VideoPolicy, error) {
	if cmd.TenantID == (identity.ID{}) || cmd.MutationID == "" {
		return database.VideoPolicy{}, apperror.New(apperror.InvalidInput, "input", "invalid policy update")
	}
	if err := Validate(cmd.MaxDurationMinutes, cmd.OriginRetentionDays, cmd.CurrentRetentionDays); err != nil {
		return database.VideoPolicy{}, err
	}
	meta, _ := requestctx.FromContext(ctx)
	values := fmt.Sprintf("%d:%d:%d", cmd.MaxDurationMinutes, cmd.OriginRetentionDays, cmd.CurrentRetentionDays)
	sum := sha256.Sum256([]byte(values))
	digest := hex.EncodeToString(sum[:])
	policy := Defaults(cmd.TenantID)
	run := within
	if run == nil {
		run = (tenanttx.Runner{DB: db}).Within
	}
	err := run(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		var current database.VideoPolicy
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=?", cmd.TenantID).First(&current).Error
		if err == gorm.ErrRecordNotFound {
			current = Defaults(cmd.TenantID)
		} else if err != nil {
			return err
		}
		if current.LastMutationID == cmd.MutationID {
			if current.LastPayloadDigest != digest {
				return apperror.New(apperror.Conflict, "clientMutationId", "mutation payload changed")
			}
			policy = current
			return nil
		}
		var tenant database.Tenant
		if err := tx.Where("tenant_id=?", cmd.TenantID).First(&tenant).Error; err != nil {
			return err
		}
		if tenant.Status != "ACTIVE" {
			return apperror.New(apperror.InvalidState, "tenant", "inactive tenant cannot change video policy")
		}
		if current.Version != cmd.ExpectedVersion {
			return apperror.New(apperror.Conflict, "expectedVersion", "video policy has changed")
		}
		now := time.Now().UTC()
		policy = database.VideoPolicy{TenantID: cmd.TenantID, MaxDurationMinutes: cmd.MaxDurationMinutes, OriginRetentionDays: cmd.OriginRetentionDays, CurrentRetentionDays: cmd.CurrentRetentionDays, SupersededBaselineRetentionDays: current.SupersededBaselineRetentionDays, Version: current.Version + 1, LastMutationID: cmd.MutationID, LastPayloadDigest: digest, UpdatedAt: now}
		if err := tx.Save(&policy).Error; err != nil {
			return err
		}
		return tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: cmd.TenantID, ActorID: meta.Principal.IdentityID, Action: "video.policy_configured", TargetType: "video_policy", TargetID: cmd.TenantID.String(), Outcome: "ACCEPTED", CorrelationID: meta.CorrelationID, OccurredAt: now}).Error
	})
	return policy, err
}
