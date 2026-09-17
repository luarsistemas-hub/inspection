package core

import (
	"context"
	"errors"
	"strings"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/notifications"
)

type ChannelNotifier struct {
	Registry    *notifications.Registry
	CallbackURL string
	Stage       string
}

func (n ChannelNotifier) SendOTP(ctx context.Context, tenantID, invitationID identity.ID, code string, destinations []DeliveryIntent) error {
	intents := make(map[notifications.Channel]notifications.Intent, len(destinations))
	for _, destination := range destinations {
		if isNonProductionStage(n.Stage) && strings.EqualFold(strings.TrimSpace(destination.Channel), string(notifications.Email)) {
			continue
		}
		channel := notifications.Channel(destination.Channel)
		intents[channel] = notifications.Intent{ID: invitationID.String() + ":" + destination.Channel, Destination: destination.Destination, Template: "Seu código de acesso", Parameters: map[string]string{"body": "Código: " + code, "tenantId": tenantID.String(), "callbackUrl": n.CallbackURL}}
	}
	if len(intents) == 0 {
		return nil
	}
	result := notifications.Deliver(ctx, n.Registry, intents)
	if result.Status != "DELIVERED" {
		return errors.New("all notification channels failed")
	}
	return nil
}

func isNonProductionStage(stage string) bool {
	stage = strings.TrimSpace(stage)
	return stage != "" && !strings.EqualFold(stage, "production")
}
