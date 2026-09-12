package request

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

type Command struct{ Input recapturecore.RequestInput }
type Dependencies struct {
	DB             *gorm.DB
	Bus            *mediator.Bus
	Service        recapturecore.Service
	Notifications  notificationcore.NotificationService
	CaptureBaseURL string
	Authorizer     auth.Authorizer
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil || d.Service.DB == nil || d.Notifications == nil || d.CaptureBaseURL == "" {
		return fmt.Errorf("slice recapture/request: missing dependency")
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		input := raw.(Command).Input
		var inspection database.Inspection
		if err := (tenanttx.Runner{DB: d.DB}).Within(ctx, input.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=?", input.TenantID, input.InspectionID).First(&inspection).Error
		}); err != nil {
			return nil, apperror.New(apperror.NotFound, "inspectionId", "inspection not found")
		}
		if _, err := d.Authorizer.Authorize(ctx, input.TenantID, []string{auth.TenantAdmin, auth.Manager, auth.Employee}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: inspection.BusinessUnitID}, true); err != nil {
			return nil, err
		}
		participantRaw, err := d.Bus.Ask(ctx, participantget.Query{TenantID: input.TenantID, ParticipantID: inspection.ParticipantID})
		if err != nil {
			return nil, err
		}
		input.Delivery = deliveries(participantRaw.(participantcore.ParticipantView))
		if len(input.Delivery) == 0 {
			return nil, apperror.New(apperror.InvalidState, "participant", "a selected verified channel is required")
		}
		result, err := d.Service.Request(ctx, input)
		if err != nil {
			return nil, err
		}
		if result.LinkToken != "" {
			if err := deliverRecapture(ctx, d.DB, d.Notifications, d.CaptureBaseURL, input.TenantID, result.RequestID, result.ResponsibilityID, result.LinkToken, input.Delivery); err != nil {
				return nil, err
			}
		}
		return result, nil
	})
}

func deliveries(participant participantcore.ParticipantView) []invitationcore.DeliveryIntent {
	selected := map[identity.ID]bool{}
	for _, id := range participant.Selected {
		selected[id] = true
	}
	result := []invitationcore.DeliveryIntent{}
	for _, contact := range participant.Contacts {
		if selected[contact.ID] && participant.Verified[contact.ID] && contact.Active {
			result = append(result, invitationcore.DeliveryIntent{Channel: contact.Channel, Destination: contact.Value})
		}
	}
	return result
}

func deliverRecapture(ctx context.Context, db *gorm.DB, service notificationcore.NotificationService, baseURL string, tenantID, requestID, responsibilityID identity.ID, token string, delivery []invitationcore.DeliveryIntent) error {
	var invitation database.Invitation
	if err := (tenanttx.Runner{DB: db}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).First(&invitation).Error
	}); err != nil {
		return err
	}
	for _, target := range delivery {
		_, err := service.Send(ctx, notificationcore.Notification{TenantID: tenantID, Recipient: notificationcore.Recipient{Destination: target.Destination}, Channel: notificationcore.Channel(target.Channel), Template: notificationcore.TemplateRef{Name: "recapture-link", Version: "v1"}, Variables: map[string]string{"recipientName": "participante"}, CorrelationID: "recapture-" + requestID.String(), IdempotencyKey: requestID.String() + ":" + target.Channel + ":" + target.Destination, Execution: &notificationcore.ExecutionPayload{InvitationID: invitation.ID, Token: token, URLVariable: "recaptureUrl", BaseURL: baseURL, ExpiresAt: invitation.ExpiresAt.Unix()}})
		if err != nil {
			return err
		}
	}
	return nil
}
