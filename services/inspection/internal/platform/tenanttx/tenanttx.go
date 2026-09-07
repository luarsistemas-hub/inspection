package tenanttx

import (
	"context"
	"fmt"

	"inspection/libs/identity"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

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
		return fn(tx.WithContext(ctx))
	})
}
