package session

import (
	"context"
	"errors"
	"strings"
	"testing"

	"inspection/services/inspection/internal/platform/notifications"
)

type recordingSender struct {
	intent notifications.Intent
	err    error
}

func (s *recordingSender) Send(_ context.Context, intent notifications.Intent) (notifications.Receipt, error) {
	s.intent = intent
	if s.err != nil {
		return notifications.Receipt{}, s.err
	}
	return notifications.Receipt{Provider: "smtp", ID: "mail-1"}, nil
}

func TestRegistryNotifierRetainsDeliveryFailureForDiagnostics(t *testing.T) {
	deliveryFailure := &notifications.DeliveryError{Kind: notifications.ErrorPermanent, Code: "smtp_auth_failed", PreSend: true}
	registry, err := notifications.NewRegistry(map[notifications.Channel]notifications.Sender{
		notifications.Email: &recordingSender{err: deliveryFailure},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = (RegistryNotifier{Registry: registry}).SendActivationInvitation(context.Background(), "ana@example.test", "https://admin.example.test/activate")
	if !errors.Is(err, deliveryFailure) {
		t.Fatalf("delivery error was lost: %v", err)
	}
}

func TestRegistryNotifierSendsReadableOTPEmail(t *testing.T) {
	sender := &recordingSender{}
	registry, err := notifications.NewRegistry(map[notifications.Channel]notifications.Sender{
		notifications.Email: sender,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := (RegistryNotifier{Registry: registry}).SendOTP(context.Background(), "ana@example.test", "123456"); err != nil {
		t.Fatal(err)
	}

	if sender.intent.Template != "Seu código de confirmação | Inspection" {
		t.Fatalf("subject=%q", sender.intent.Template)
	}
	if sender.intent.Parameters["body"] == "" || sender.intent.Parameters["html"] == "" {
		t.Fatalf("email content is incomplete: %#v", sender.intent.Parameters)
	}
	for _, content := range sender.intent.Parameters {
		if !strings.Contains(content, "123456") {
			t.Fatalf("code missing from email content: %q", content)
		}
	}
}

func TestRegistryNotifierSendsAdminActivationLink(t *testing.T) {
	sender := &recordingSender{}
	registry, err := notifications.NewRegistry(map[notifications.Channel]notifications.Sender{
		notifications.Email: sender,
	})
	if err != nil {
		t.Fatal(err)
	}

	activationURL := "https://admin.example.test/activate"
	if err := (RegistryNotifier{Registry: registry}).SendActivationInvitation(context.Background(), "ana@example.test", activationURL); err != nil {
		t.Fatal(err)
	}

	if sender.intent.Template != "Ative seu acesso administrativo | Inspection" {
		t.Fatalf("subject=%q", sender.intent.Template)
	}
	for _, content := range sender.intent.Parameters {
		if !strings.Contains(content, activationURL) || !strings.Contains(content, "criar") && !strings.Contains(content, "Criar") {
			t.Fatalf("activation instructions are incomplete: %q", content)
		}
	}
}
