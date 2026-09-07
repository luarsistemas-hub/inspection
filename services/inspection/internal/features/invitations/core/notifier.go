package core

import (
	"context"
	"errors"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/notifications"
)

type ChannelNotifier struct {
	Registry    *notifications.Registry
	CallbackURL string
}

func (n ChannelNotifier) SendOTP(ctx context.Context, tenantID, invitationID identity.ID, code string, destinations []DeliveryIntent) error {
	intents := make(map[notifications.Channel]notifications.Intent, len(destinations))
	for _, destination := range destinations {
		channel := notifications.Channel(destination.Channel)
		intents[channel] = notifications.Intent{ID: invitationID.String() + ":" + destination.Channel, Destination: destination.Destination, Template: "Seu codigo de acesso", Parameters: map[string]string{"body": "Codigo: " + code, "tenantId": tenantID.String(), "callbackUrl": n.CallbackURL}}
	}
	result := notifications.Deliver(ctx, n.Registry, intents)
	if result.Status != "DELIVERED" {
		return errors.New("all notification channels failed")
	}
	return nil
}
