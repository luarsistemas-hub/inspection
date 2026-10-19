package declare_false_positive

import (
	"context"
	"errors"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/media/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

type Command struct {
	TenantID, ResponsibilityID, MediaID identity.ID
	Reason                              string
}
type Dependencies struct {
	DB      *gorm.DB
	Bus     *mediator.Bus
	Service core.Service
	Within  func(context.Context, identity.ID, func(*gorm.DB) error) error
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil || d.Service.DB == nil {
		return fmt.Errorf("slice media/declare_false_positive: missing dependency")
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		if err := d.Service.DeclareFalsePositive(ctx, command.TenantID, command.ResponsibilityID, command.MediaID, command.Reason); err != nil {
			var appErr *apperror.Error
			if errors.As(err, &appErr) {
				return nil, err
			}
			return nil, apperror.Wrap(apperror.DependencyUnavailable, err)
		}
		var media database.MediaObject
		within := d.Within
		if within == nil {
			within = (tenanttx.Runner{DB: d.DB}).Within
		}
		err := within(ctx, command.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=?", command.TenantID, command.MediaID).First(&media).Error
		})
		if err != nil {
			return nil, apperror.Wrap(apperror.DependencyUnavailable, err)
		}
		return media, nil
	})
}
