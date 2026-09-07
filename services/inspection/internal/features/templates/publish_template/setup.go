package publish_template

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/templates/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/mediator"
)

type Command struct {
	TenantID                  identity.ID
	Key, Name, IdempotencyKey string
	DefinitionJSON            []byte
}
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice templates/publish_template: missing dependency")
	}
	s := core.Service{DB: d.DB, Bus: d.Bus, Authorizer: d.Authorizer}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		c := raw.(Command)
		return s.Publish(ctx, c.TenantID, c.Key, c.Name, c.IdempotencyKey, c.DefinitionJSON)
	})
}
