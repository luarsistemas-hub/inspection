package core

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/notifications"
)

type recordingOTPSender struct{ calls int }

func (s *recordingOTPSender) Send(context.Context, notifications.Intent) (notifications.Receipt, error) {
	s.calls++
	return notifications.Receipt{Provider: "test"}, nil
}

func TestChannelNotifierSkipsOnlyEmailOutsideProduction(t *testing.T) {
	email := &recordingOTPSender{}
	sms := &recordingOTPSender{}
	registry, err := notifications.NewRegistry(map[notifications.Channel]notifications.Sender{
		notifications.Email: email,
		notifications.SMS:   sms,
	})
	if err != nil {
		t.Fatal(err)
	}

	notifier := ChannelNotifier{Registry: registry, Stage: "dev"}
	if err := notifier.SendOTP(context.Background(), identity.NewID(), identity.NewID(), "123456", []DeliveryIntent{{Channel: "EMAIL", Destination: "owner@example.test"}}); err != nil {
		t.Fatalf("email-only delivery failed: %v", err)
	}
	if email.calls != 0 {
		t.Fatalf("email sender called %d times in dev", email.calls)
	}

	if err := notifier.SendOTP(context.Background(), identity.NewID(), identity.NewID(), "123456", []DeliveryIntent{{Channel: "EMAIL", Destination: "owner@example.test"}, {Channel: "SMS", Destination: "+5511999999999"}}); err != nil {
		t.Fatalf("mixed delivery failed: %v", err)
	}
	if email.calls != 0 || sms.calls != 1 {
		t.Fatalf("unexpected sender calls: email=%d sms=%d", email.calls, sms.calls)
	}
}

func TestChannelNotifierSendsEmailInProduction(t *testing.T) {
	email := &recordingOTPSender{}
	registry, err := notifications.NewRegistry(map[notifications.Channel]notifications.Sender{notifications.Email: email})
	if err != nil {
		t.Fatal(err)
	}

	if err := (ChannelNotifier{Registry: registry, Stage: "production"}).SendOTP(context.Background(), identity.NewID(), identity.NewID(), "123456", []DeliveryIntent{{Channel: "EMAIL", Destination: "owner@example.test"}}); err != nil {
		t.Fatalf("production delivery failed: %v", err)
	}
	if email.calls != 1 {
		t.Fatalf("email sender called %d times in production", email.calls)
	}
}
