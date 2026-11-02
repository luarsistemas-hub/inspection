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
	if code, field, message := apperror.Public(err); code != apperror.IntegrationDisabled || field != "channel" || message != "Este canal de notificação está temporariamente indisponível." {
		t.Fatalf("disabled channel public contract = code:%s field:%q message:%q", code, field, message)
	}
}

func TestDependencyOrPublicPreservesNotifierPublicErrors(t *testing.T) {
	public := apperror.New(apperror.IntegrationDisabled, "channel", "Este canal de notificação está temporariamente indisponível.")
	got := dependencyOrPublic(public)
	code, field, message := apperror.Public(got)
	if code != apperror.IntegrationDisabled || field != "channel" || message != "Este canal de notificação está temporariamente indisponível." {
		t.Fatalf("public notifier error was wrapped: code=%s field=%q message=%q", code, field, message)
	}
	internal := dependencyOrPublic(apperror.New(apperror.Internal, "", "segredo do provider"))
	if code, _, _ := apperror.Public(internal); code != apperror.DependencyUnavailable {
		t.Fatalf("internal notifier failure was not classified as dependency unavailable: %s", code)
	}
}
