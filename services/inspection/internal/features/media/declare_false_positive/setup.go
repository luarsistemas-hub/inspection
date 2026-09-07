package declare_false_positive

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/media/core"
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
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil || d.Service.DB == nil {
		return fmt.Errorf("slice media/declare_false_positive: missing dependency")
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		if err := d.Service.DeclareFalsePositive(ctx, command.TenantID, command.ResponsibilityID, command.MediaID, command.Reason); err != nil {
			return nil, err
		}
		var media database.MediaObject
		err := (tenanttx.Runner{DB: d.DB}).Within(ctx, command.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=?", command.TenantID, command.MediaID).First(&media).Error
		})
		return media, err
	})
}
