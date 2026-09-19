// Package alert_delivery consumes terminal capture-link delivery outcomes and
// notifies the onboarding owner without creating recursive delivery alerts.
package alert_delivery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	notificationrequest "inspection/services/inspection/internal/features/notifications/request"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/observability"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Dependencies are the worker-owned inputs for terminal delivery alerts.
type Dependencies struct {
	Notifications notificationcore.NotificationService
	DashboardURL  string
	Metrics       *observability.Metrics
}

// Setup returns an idempotent event handler. The inbox consumer provides
// exactly-once processing for a given terminal event; the notification
// idempotency key additionally protects retries at the delivery boundary.
func Setup(deps Dependencies) (func(context.Context, *gorm.DB, events.RawEnvelope) error, error) {
	if deps.Notifications == nil {
		return nil, errors.New("notifications/alert_delivery: missing notification service")
	}
	return func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		if envelope.Type != "notification.delivery_terminal.v1" || envelope.SchemaVersion != 1 {
			return messaging.ErrPermanent
		}
		var payload struct {
			DeliveryID      identity.ID  `json:"deliveryId"`
			InspectionID    identity.ID  `json:"inspectionId"`
			InvitationID    *identity.ID `json:"invitationId"`
			LogicalTemplate string       `json:"logicalTemplate"`
			State           string       `json:"state"`
			FailureCode     string       `json:"failureCode"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.DeliveryID == (identity.ID{}) || payload.InspectionID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		if payload.State != string(notificationcore.StateFailed) && payload.State != string(notificationcore.StateUnknown) {
			return messaging.ErrPermanent
		}
		if payload.LogicalTemplate != "capture-link" && payload.LogicalTemplate != "reminder" {
			return nil
		}

		var request database.OnboardingRequest
		if err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=?", envelope.TenantID, payload.InspectionID).First(&request).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		var session database.OnboardingSession
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, request.SessionID).First(&session).Error; err != nil {
			return err
		}
		if strings.TrimSpace(session.Email) == "" {
			return nil
		}
		var activation database.OnboardingActivation
		if err := tx.WithContext(ctx).Where("tenant_id=?", envelope.TenantID).First(&activation).Error; err != nil {
			return err
		}
		var membership database.Membership
		if err := tx.WithContext(ctx).Where("tenant_id=? AND identity_id=? AND role='TENANT_ADMIN'", envelope.TenantID, activation.IdentityID).First(&membership).Error; err != nil {
			return err
		}

		failureMessage := "O servidor de e-mail rejeitou a mensagem. Confira o endereço e reenvie."
		if payload.State == string(notificationcore.StateUnknown) {
			failureMessage = "Não foi possível confirmar o resultado do envio. Confira o endereço antes de reenviar."
		}
		inspectionName := payload.InspectionID.String()
		dashboardURL := strings.TrimRight(deps.DashboardURL, "/")
		if dashboardURL == "" {
			dashboardURL = "inspection/" + payload.InspectionID.String()
		} else {
			dashboardURL += "/governance/deliveries"
		}
		body := "Não foi possível confirmar o envio do link da vistoria " + inspectionName + ". " + failureMessage
		if err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&database.RecipientNotification{
			ID: identity.NewDeterministicID("inspection/notification-alert", envelope.ID.String()), TenantID: envelope.TenantID,
			RecipientMembershipID: membership.ID, EventID: envelope.ID, Kind: "RESPONSIBLE_EMAIL_DELIVERY",
			Title: "Problema no envio do link da vistoria", Body: body, ResourceKind: "INSPECTION", ResourceID: &payload.InspectionID,
		}).Error; err != nil {
			return err
		}
		_, err := deps.Notifications.Send(notificationrequest.InTransaction(ctx, tx), notificationcore.Notification{
			TenantID: envelope.TenantID, InspectionID: &payload.InspectionID, InvitationID: payload.InvitationID,
			Recipient: notificationcore.Recipient{ID: membership.ID.String(), Channel: "EMAIL", Destination: session.Email, Internal: true, Verified: true, Selected: true},
			Channel:   notificationcore.ChannelEmail, Template: notificationcore.TemplateRef{Name: "delivery-problem", Version: "v1"},
			Variables:     map[string]string{"inspectionName": inspectionName, "dashboardUrl": dashboardURL, "failureMessage": failureMessage},
			CorrelationID: "delivery-terminal-alert:" + payload.DeliveryID.String(), IdempotencyKey: "delivery-terminal-alert:" + envelope.ID.String(),
		})
		if err != nil {
			return err
		}
		observability.LogOwnerDeliveryAlertRequested(ctx, envelope.TenantID, envelope.ID, payload.InspectionID, payload.State)
		deps.Metrics.DeliveryAlert(payload.State)
		return nil
	}, nil
}
