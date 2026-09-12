package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUT022UT025TwilioSMSContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, password, ok := r.BasicAuth(); !ok || user != "AC1" || password != "token" {
			t.Error("missing basic authentication")
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := r.Form.Get("To"); got != "+5511999999999" {
			t.Errorf("to=%q", got)
		}
		if got := r.Form.Get("From"); got != "+15550000001" {
			t.Errorf("from=%q", got)
		}
		if got := r.Form.Get("Body"); got != "Olá" {
			t.Errorf("body=%q", got)
		}
		if got := r.Form.Get("StatusCallback"); got != "https://api.test/status?tenantId=tenant-1" {
			t.Errorf("callback=%q", got)
		}
		_, _ = io.WriteString(w, `{"sid":"SM123"}`)
	}))
	defer server.Close()
	receipt, err := (TwilioSMSSender{BaseURL: server.URL, AccountSID: "AC1", AuthToken: "token", From: "+15550000001", StatusCallback: "https://api.test/status"}).Send(context.Background(), DeliveryIntent{Destination: "+5511999999999", Text: "Olá", TenantID: "tenant-1"})
	if err != nil || receipt.Provider != "twilio" || receipt.ID != "SM123" {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestUT023UT024TwilioWhatsAppTemplateContract(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("To") != "whatsapp:+5511999999999" || r.Form.Get("From") != "whatsapp:+15550000001" || r.Form.Get("Body") != "" {
			t.Fatalf("invalid WhatsApp form: %#v", r.Form)
		}
		if r.Form.Get("ContentSid") != "HX1" {
			t.Fatalf("content sid=%q", r.Form.Get("ContentSid"))
		}
		var values map[string]string
		if err := json.Unmarshal([]byte(r.Form.Get("ContentVariables")), &values); err != nil || values["1"] != "A" || values["2"] != "B" {
			t.Fatalf("variables=%q err=%v", r.Form.Get("ContentVariables"), err)
		}
		_, _ = io.WriteString(w, `{"sid":"SM124"}`)
	}))
	defer server.Close()
	sender := TwilioWhatsAppSender{TwilioSMSSender: TwilioSMSSender{BaseURL: server.URL, AccountSID: "AC1", AuthToken: "token", From: "whatsapp:+15550000001"}, ContentSIDs: map[string]string{"recapture-link:v1:pt-BR": "HX1"}}
	if _, err := sender.Send(context.Background(), DeliveryIntent{Destination: "whatsapp:+5511999999999", Template: "recapture-link:v1", Language: "pt-BR", TemplateParameters: []string{"A", "B"}}); err != nil {
		t.Fatal(err)
	}
	_, err := sender.Send(context.Background(), DeliveryIntent{Destination: "+5511999999999", Template: "missing:v1", Language: "pt-BR"})
	var deliveryError *DeliveryError
	if !errors.As(err, &deliveryError) || deliveryError.Kind != ErrorPermanent || !deliveryError.PreSend || requests != 1 {
		t.Fatalf("err=%v requests=%d", err, requests)
	}
}

func TestUT026UT027TwilioErrorsAreSafeAndRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "17")
		http.Error(w, "secret +5511999999999", http.StatusTooManyRequests)
	}))
	defer server.Close()
	_, err := (TwilioSMSSender{BaseURL: server.URL, AccountSID: "AC1", AuthToken: "token", From: "+1"}).Send(context.Background(), DeliveryIntent{Destination: "+5511999999999"})
	var deliveryError *DeliveryError
	if !errors.As(err, &deliveryError) || deliveryError.Kind != ErrorTransient || deliveryError.RetryAfter != 17*time.Second || strings.Contains(err.Error(), "+5511") {
		t.Fatalf("error=%v", err)
	}
}

func TestUT028ToUT032MetaPayloadReceiptAndErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v22.0/phone-1/messages" || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("path=%q authorization=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["messaging_product"] != "whatsapp" || payload["to"] != "5511999999999" {
			t.Fatalf("payload=%#v", payload)
		}
		_, _ = io.WriteString(w, `{"messages":[{"id":"wamid.123"}]}`)
	}))
	defer server.Close()
	sender := MetaWhatsAppSender{BaseURL: server.URL, APIVersion: "v22.0", PhoneNumberID: "phone-1", AccessToken: "token", Templates: map[string]string{"capture-link:v1:pt-BR": "capture"}}
	receipt, err := sender.Send(context.Background(), DeliveryIntent{Destination: "+5511999999999", Template: "capture-link:v1", Language: "pt-BR", TemplateParameters: []string{"A"}})
	if err != nil || receipt.Provider != "meta" || receipt.ID != "wamid.123" {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	transient := classifyHTTPError("meta", http.StatusServiceUnavailable, "11")
	var deliveryError *DeliveryError
	if !errors.As(transient, &deliveryError) || deliveryError.Kind != ErrorTransient || deliveryError.RetryAfter != 11*time.Second {
		t.Fatalf("error=%v", transient)
	}
}

func TestUT033ToUT040SMTPMIMEHeaderSafetyAndCancellation(t *testing.T) {
	message, err := buildMIMEMessage("sender@example.test", "reply@example.test", "person@example.test", "Olá", "texto", "<b>html</b>")
	if err != nil || !strings.Contains(string(message), "multipart/alternative") || !strings.Contains(string(message), "\r\n") || !strings.Contains(string(message), "Reply-To: reply@example.test") {
		t.Fatalf("message=%q err=%v", message, err)
	}
	if err := validateMailHeaders("safe", "bad\r\nBcc: injected"); err == nil {
		t.Fatal("header injection accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = (SMTPSender{Address: "127.0.0.1:1", From: "sender@example.test", TLSMode: "none"}).Send(ctx, Intent{Destination: "person@example.test"})
	var deliveryError *DeliveryError
	if !errors.As(err, &deliveryError) || deliveryError.Kind != ErrorCanceled {
		t.Fatalf("cancellation=%v", err)
	}
}

func TestGatewayHasNoFallbackAndProviderResolverIsFixed(t *testing.T) {
	resolver, err := NewProviderResolver(ProviderMeta)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := resolver.ProviderFor(WhatsApp)
	if err != nil || provider != ProviderMeta {
		t.Fatalf("provider=%q err=%v", provider, err)
	}
	gateway, err := NewGateway(map[Channel]map[Provider]Adapter{SMS: {ProviderTwilio: TwilioSMSSender{}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = gateway.Send(context.Background(), SMS, ProviderMeta, DeliveryIntent{})
	var deliveryError *DeliveryError
	if !errors.As(err, &deliveryError) || deliveryError.Code != "provider_not_configured" {
		t.Fatalf("fallback occurred: %v", err)
	}
	if whatsAppAddress("whatsapp:+1") != "whatsapp:+1" || whatsAppAddress("+1") != "whatsapp:+1" {
		t.Fatal("invalid WhatsApp prefixing")
	}
	if got := orderedVariables([]string{"A", "B"}); got["1"] != "A" {
		t.Fatalf("variables=%v", got)
	}
}
