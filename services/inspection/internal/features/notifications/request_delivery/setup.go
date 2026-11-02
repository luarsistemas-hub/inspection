// Package request_delivery creates first-critical operational notifications.
package request_delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/notifications/core"
	notificationrequest "inspection/services/inspection/internal/features/notifications/request"
	"inspection/services/inspection/internal/platform/apperror"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Recipient struct {
	ID, Channel, Destination     string
	ChannelID                    string
	Internal, Verified, Selected bool
}
type Input struct {
	TenantID, InspectionID identity.ID
	Previous, Current      string
	Recipients             []Recipient
}

// Request persists one central request per selected verified internal
// recipient. Its deterministic keys prevent duplicate first-critical alerts.
func Request(ctx context.Context, tx *gorm.DB, service core.NotificationService, in Input) error {
	if tx == nil || service == nil || in.TenantID == (identity.ID{}) || in.InspectionID == (identity.ID{}) {
		return fmt.Errorf("notification delivery: missing tenant, inspection, or service")
	}
	allowed := make([]core.Recipient, 0, len(in.Recipients))
	for _, recipient := range in.Recipients {
		allowed = append(allowed, core.Recipient{ID: recipient.ID, Channel: recipient.Channel, ChannelID: recipient.ChannelID, Destination: recipient.Destination, Internal: recipient.Internal, Verified: recipient.Verified, Selected: recipient.Selected})
	}
	recipients := core.FirstCritical(in.Previous, in.Current, allowed)
	if len(recipients) == 0 {
		return nil
	}
	correlationID := "notification-critical-" + in.InspectionID.String()
	// The alert is a first-critical notification, once per inspection and
	// recipient channel. A later reclassification must not create another alert.
	intentID := identity.ID(uuid.NewSHA1(uuid.Nil, []byte("critical:"+in.TenantID.String()+":"+in.InspectionID.String())))
	inspectionID := in.InspectionID
	for _, recipient := range recipients {
		if recipient.ChannelID == "" {
			return &core.Error{Code: core.InvalidInput, Field: "channelId"}
		}
		channel := core.Channel(recipient.Channel)
		_, err := service.Send(notificationrequest.InTransaction(ctx, tx), core.Notification{
			TenantID: in.TenantID, InspectionID: &inspectionID, Recipient: recipient, Channel: channel,
			Template:      core.TemplateRef{Name: "critical-alert", Version: "v1"},
			Variables:     map[string]string{"inspectionName": in.InspectionID.String(), "dashboardUrl": "inspection/" + in.InspectionID.String()},
			CorrelationID: correlationID, IdempotencyKey: intentID.String() + ":" + recipient.ID + ":" + recipient.ChannelID + ":" + string(channel),
		})
		if err != nil {
			if skippableRecipientError(err) {
				slog.WarnContext(ctx, "critical alert recipient skipped", "reason", recipientSkipReason(err), "tenantId", in.TenantID.String(), "inspectionId", in.InspectionID.String())
				continue
			}
			return err
		}
	}
	return nil
}

// skippableRecipientError reports recipient-scoped failures that must not
// abort the alert for the remaining recipients: an alert already requested
// with another digest, an invalid recipient, an unknown channel, or a disabled
// channel integration. Any other error, including invalid input and template
// or catalog failures, is propagated to the caller.
func skippableRecipientError(err error) bool {
	var validationError *core.Error
	if errors.As(err, &validationError) {
		switch validationError.Code {
		case core.IdempotencyConflict, core.InvalidRecipient, core.UnknownChannel:
			return true
		default:
			return false
		}
	}
	return apperror.Is(err, apperror.IntegrationDisabled)
}

func recipientSkipReason(err error) string {
	var validationError *core.Error
	if errors.As(err, &validationError) {
		switch validationError.Code {
		case core.IdempotencyConflict:
			return "already_alerted"
		case core.InvalidRecipient:
			return "invalid_recipient"
		case core.UnknownChannel:
			return "unknown_channel"
		}
	}
	return "channel_disabled"
}
