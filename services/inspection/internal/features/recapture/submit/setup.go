package submit

import (
	"context"
	"errors"
	"fmt"
	"time"

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
	DB               *gorm.DB
	Bus              *mediator.Bus
	Capture          capturecore.Service
	AnalysisDisabled bool
	Now              func() time.Time
	Within           func(context.Context, identity.ID, func(*gorm.DB) error) error
}

func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil || deps.Capture.DB == nil || deps.Capture.Finalizer == nil {
		return fmt.Errorf("slice recapture/submit: missing dependency")
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		if deps.AnalysisDisabled {
			return nil, apperror.New(apperror.IntegrationDisabled, "", "Analysis is temporarily unavailable")
		}
		command := raw.(Command)
		now := time.Now
		if deps.Now != nil {
			now = deps.Now
		}
		within := deps.Within
		if within == nil {
			runner := tenanttx.Runner{DB: deps.DB}
			within = runner.Within
		}
		var request database.RecaptureRequest
		err := within(ctx, command.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=? AND responsibility_id=?", command.TenantID, command.RequestID, command.ResponsibilityID).First(&request).Error
		})
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, apperror.New(apperror.NotFound, "requestId", "recapture request not found")
			}
			return nil, err
		}
		if request.Status != "REQUESTED" && request.Status != "ACCESSED" && request.Status != "RECEIVED" {
			return nil, apperror.New(apperror.InvalidState, "requestId", "recapture is not accepting a submission")
		}
		// A received recapture may be replayed after its deadline; only open
		// requests stop accepting evidence once the deadline passes.
		if request.Status != "RECEIVED" && !now().Before(request.DeadlineAt) {
			return nil, apperror.New(apperror.InvalidState, "requestId", "recapture deadline has expired")
		}
		if _, err := deps.Capture.Submit(ctx, command.TenantID, command.ResponsibilityID, command.ConfirmIncomplete); err != nil {
			return nil, err
		}
		// Capture finalization persists RECEIVED for video supplements and leaves
		// visual fulfillment to the preparation/analysis path. Reload the durable
		// status instead of returning a success-shaped local value.
		if err := within(ctx, command.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=? AND responsibility_id=?", command.TenantID, command.RequestID, command.ResponsibilityID).First(&request).Error
		}); err != nil {
			return nil, err
		}
		return recapturecore.Result{RequestID: request.ID, ResponsibilityID: request.ResponsibilityID, Status: request.Status}, nil
	})
}
