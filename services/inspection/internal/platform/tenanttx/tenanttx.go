package tenanttx

import (
	"context"
	"fmt"

	"inspection/libs/identity"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type transactionContextKey struct{}

type transactionContext struct {
	tenantID identity.ID
	tx       *gorm.DB
}

// Runner establishes transaction-local tenant state before any tenant I/O.
type Runner struct {
	DB *gorm.DB
}

// Within executes fn under forced-RLS tenant context.
func (r Runner) Within(ctx context.Context, tenantID identity.ID, fn func(*gorm.DB) error) error {
	if r.DB == nil || fn == nil {
		return fmt.Errorf("tenant transaction: missing dependency")
	}
	if tenantID == uuid.Nil {
		return fmt.Errorf("tenant transaction: empty tenant")
	}
	if active, ok := ctx.Value(transactionContextKey{}).(transactionContext); ok {
		if active.tenantID != tenantID {
			return fmt.Errorf("tenant transaction: nested tenant mismatch")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return fn(active.tx.WithContext(ctx))
	}
	return r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var bypass bool
		var superuser bool
		if err := tx.Raw(`SELECT rolbypassrls, rolsuper FROM pg_roles WHERE rolname = current_user`).Row().Scan(&bypass, &superuser); err != nil {
			return fmt.Errorf("verify runtime role: %w", err)
		}
		if bypass || superuser {
			return fmt.Errorf("tenant transaction: unsafe database role")
		}
		if err := tx.Exec(`SELECT set_config('app.tenant_id', ?, true)`, tenantID.String()).Error; err != nil {
			return fmt.Errorf("set tenant context: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		txCtx := context.WithValue(ctx, transactionContextKey{}, transactionContext{tenantID: tenantID, tx: tx})
		return fn(tx.WithContext(txCtx))
	})
}
