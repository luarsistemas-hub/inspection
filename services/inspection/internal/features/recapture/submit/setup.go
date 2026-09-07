package submit

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

type Command struct {
	TenantID, ResponsibilityID, RequestID identity.ID
	ConfirmIncomplete                     bool
}

type Dependencies struct {
	DB      *gorm.DB
	Bus     *mediator.Bus
	Capture capturecore.Service
}

func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil || deps.Capture.DB == nil || deps.Capture.Finalizer == nil {
		return fmt.Errorf("slice recapture/submit: missing dependency")
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		var request database.RecaptureRequest
		err := (tenanttx.Runner{DB: deps.DB}).Within(ctx, command.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=? AND responsibility_id=?", command.TenantID, command.RequestID, command.ResponsibilityID).First(&request).Error
		})
		if err != nil {
			return nil, apperror.New(apperror.NotFound, "requestId", "recapture request not found")
		}
		if _, err := deps.Capture.Submit(ctx, command.TenantID, command.ResponsibilityID, command.ConfirmIncomplete); err != nil {
			return nil, err
		}
		request.Status = "SUBMITTED"
		return recapturecore.Result{RequestID: request.ID, ResponsibilityID: request.ResponsibilityID, Status: request.Status}, nil
	})
}
