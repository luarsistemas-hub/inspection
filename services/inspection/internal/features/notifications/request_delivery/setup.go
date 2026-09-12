// Package request_delivery creates first-critical operational notifications.
package request_delivery

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/notifications/core"
	notificationrequest "inspection/services/inspection/internal/features/notifications/request"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Recipient struct {
	ID, Channel, Destination     string
	Internal, Verified, Selected bool
}
type Input struct {
	TenantID, InspectionID identity.ID
	Previous, Current      string
	Recipients             []Recipient
	CorrelationID          string
}

// Request persists one central request per selected verified internal
// recipient. Its deterministic keys prevent duplicate first-critical alerts.
func Request(ctx context.Context, tx *gorm.DB, service core.NotificationService, in Input) error {
	if tx == nil || service == nil || in.TenantID == (identity.ID{}) || in.InspectionID == (identity.ID{}) {
		return fmt.Errorf("notification delivery: missing tenant, inspection, or service")
	}
	allowed := make([]core.Recipient, 0, len(in.Recipients))
	for _, recipient := range in.Recipients {
		allowed = append(allowed, core.Recipient{ID: recipient.ID, Channel: recipient.Channel, Destination: recipient.Destination, Internal: recipient.Internal, Verified: recipient.Verified, Selected: recipient.Selected})
	}
	recipients := core.FirstCritical(in.Previous, in.Current, allowed)
	if len(recipients) == 0 {
		return nil
	}
	if in.CorrelationID == "" {
		in.CorrelationID = "notification-critical-" + in.InspectionID.String()
	}
	intentID := identity.ID(uuid.NewSHA1(uuid.Nil, []byte("critical:"+in.TenantID.String()+":"+in.InspectionID.String()+":"+in.Current)))
	for _, recipient := range recipients {
		channel := core.Channel(recipient.Channel)
		_, err := service.Send(notificationrequest.InTransaction(ctx, tx), core.Notification{
			TenantID: in.TenantID, Recipient: recipient, Channel: channel,
			Template:      core.TemplateRef{Name: "critical-alert", Version: "v1"},
			Variables:     map[string]string{"inspectionName": in.InspectionID.String(), "dashboardUrl": "inspection/" + in.InspectionID.String()},
			CorrelationID: in.CorrelationID, IdempotencyKey: intentID.String() + ":" + recipient.ID + ":" + string(channel),
		})
		if err != nil {
			return err
		}
	}
	return nil
}
