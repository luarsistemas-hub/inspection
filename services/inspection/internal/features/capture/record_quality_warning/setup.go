// Package record_quality_warning registers non-blocking capture quality warnings.
package record_quality_warning

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/mediator"
)

type Command struct {
	TenantID, ResponsibilityID identity.ID
	RequirementKey, Code       string
	Ignored                    bool
}
type Dependencies struct {
	Bus     *mediator.Bus
	Service core.Service
}

func Setup(d Dependencies) error {
	if d.Bus == nil || d.Service.DB == nil {
		return fmt.Errorf("slice capture/record_quality_warning: missing dependency")
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		cmd := raw.(Command)
		return d.Service.RecordQualityWarning(ctx, core.RecordQualityWarningInput{TenantID: cmd.TenantID, ResponsibilityID: cmd.ResponsibilityID, RequirementKey: cmd.RequirementKey, Code: cmd.Code, Ignored: cmd.Ignored})
	})
}
