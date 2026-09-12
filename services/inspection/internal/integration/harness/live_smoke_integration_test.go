//go:build integration

package harness

import (
	"context"
	"net/http"
	"testing"
	"time"

	"inspection/services/inspection/internal/platform/notifications"
)

func liveConfig(t *testing.T, provider string) LiveSmokeConfig {
	t.Helper()
	cfg, reason, err := LoadLiveSmokeConfig()
	if err != nil {
		t.Fatalf("E2E live %s configuration: %v", provider, err)
	}
	if !cfg.Enabled {
		t.Skipf("E2E live %s skipped: %s", provider, reason)
	}
	if err := cfg.Validate(provider); err != nil {
		t.Fatalf("E2E live %s configuration: %v", provider, err)
	}
	return cfg
}

func TestE2E012LiveTwilioSMSSmoke(t *testing.T) {
	cfg := liveConfig(t, "twilio-sms")
	receipt, err := (notifications.TwilioSMSSender{BaseURL: cfg.TwilioBaseURL, AccountSID: cfg.TwilioAccountSID, AuthToken: cfg.TwilioAuthToken, From: cfg.TwilioSMSFrom, Client: &http.Client{Timeout: 30 * time.Second}}).Send(context.Background(), notifications.DeliveryIntent{ID: "task06-live-sms", Destination: cfg.TwilioSMSRecipient, Text: "Inspection live smoke"})
	if err != nil || receipt.ID == "" {
		t.Fatalf("E2E-012 live Twilio SMS failed: %v", err)
	}
}

func TestE2E013LiveTwilioWhatsAppSmoke(t *testing.T) {
	cfg := liveConfig(t, "twilio-whatsapp")
	receipt, err := (notifications.TwilioWhatsAppSender{TwilioSMSSender: notifications.TwilioSMSSender{BaseURL: cfg.TwilioBaseURL, AccountSID: cfg.TwilioAccountSID, AuthToken: cfg.TwilioAuthToken, From: cfg.TwilioWhatsAppFrom, Client: &http.Client{Timeout: 30 * time.Second}}, ContentSIDs: map[string]string{"capture-link:v1:pt-BR": cfg.TwilioWhatsAppTemplate}}).Send(context.Background(), notifications.DeliveryIntent{ID: "task06-live-whatsapp", Destination: cfg.TwilioWhatsAppRecipient, Template: "capture-link:v1", Language: "pt-BR", TemplateParameters: []string{"Inspection live smoke"}})
	if err != nil || receipt.ID == "" {
		t.Fatalf("E2E-013 live Twilio WhatsApp failed: %v", err)
	}
}

func TestE2E014LiveMetaWhatsAppSmoke(t *testing.T) {
	cfg := liveConfig(t, "meta-whatsapp")
	language := cfg.MetaLanguage
	if language == "" {
		language = "pt-BR"
	}
	receipt, err := (notifications.MetaWhatsAppSender{BaseURL: cfg.MetaBaseURL, APIVersion: cfg.MetaAPIVersion, PhoneNumberID: cfg.MetaPhoneNumberID, AccessToken: cfg.MetaAccessToken, Templates: map[string]string{"capture-link:v1:" + language: cfg.MetaTemplate}, Client: &http.Client{Timeout: 30 * time.Second}}).Send(context.Background(), notifications.DeliveryIntent{ID: "task06-live-meta", Destination: cfg.MetaRecipient, Template: "capture-link:v1", Language: language, TemplateParameters: []string{"Inspection live smoke"}})
	if err != nil || receipt.ID == "" {
		t.Fatalf("E2E-014 live Meta WhatsApp failed: %v", err)
	}
}

func TestE2E015LiveSMTPSmoke(t *testing.T) {
	cfg := liveConfig(t, "smtp")
	receipt, err := (notifications.SMTPSender{Address: cfg.SMTPAddress, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, From: cfg.SMTPFrom, TLSMode: cfg.SMTPTLSMode, Timeout: 30 * time.Second}).Send(context.Background(), notifications.Intent{ID: "task06-live-smtp", Destination: cfg.SMTPRecipient, Template: "Inspection live smoke", Parameters: map[string]string{"body": "Inspection live smoke", "html": "<p>Inspection live smoke</p>"}})
	if err != nil || receipt.ID == "" {
		t.Fatalf("E2E-015 live SMTP failed: %v", err)
	}
}
