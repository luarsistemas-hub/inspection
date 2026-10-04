package core

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/notifications"
)

func TestChannelNotifierReportsDisabledChannel(t *testing.T) {
	registry, err := notifications.NewRegistry(map[notifications.Channel]notifications.Sender{})
	if err != nil {
		t.Fatal(err)
	}
	err = ChannelNotifier{Registry: registry, Stage: "production"}.SendOTP(context.Background(), identity.NewID(), identity.NewID(), "123456", []DeliveryIntent{{Channel: "SMS", Destination: "+5511999999999"}})
	if !apperror.Is(err, apperror.IntegrationDisabled) {
		t.Fatalf("expected INTEGRATION_DISABLED, got %v", err)
	}
}
