package notifications

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"
)

type fakeSender struct {
	receipt Receipt
	err     error
	calls   int
}

func TestIT378IT379IT541IT542CallbackProof(t *testing.T) {
	rawURL := "https://api.example/webhooks/twilio/status"
	form := url.Values{"MessageSid": {"SM1"}, "MessageStatus": {"delivered"}}
	mac := hmac.New(sha1.New, []byte("token"))
	_, _ = mac.Write([]byte(rawURL + "MessageSiddelivered" + "MessageStatusSM1"))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if VerifyTwilioSignature("token", rawURL, form, signature) {
		t.Fatal("fixture must use sorted key/value concatenation")
	}
	mac = hmac.New(sha1.New, []byte("token"))
	_, _ = mac.Write([]byte(rawURL + "MessageSidSM1" + "MessageStatusdelivered"))
	signature = base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if !VerifyTwilioSignature("token", rawURL, form, signature) {
		t.Fatal("valid signature rejected")
	}
	if VerifyTwilioSignature("token", rawURL, form, "bad") {
		t.Fatal("bad signature accepted")
	}
	now := time.Now()
	if ValidateCallbackTimestamp(fmt.Sprint(now.Unix()), now, 5*time.Minute) != nil {
		t.Fatal("current callback rejected")
	}
	if ValidateCallbackTimestamp(fmt.Sprint(now.Add(-6*time.Minute).Unix()), now, 5*time.Minute) == nil {
		t.Fatal("stale callback accepted")
	}
}

func (f *fakeSender) Send(context.Context, Intent) (Receipt, error) {
	f.calls++
	return f.receipt, f.err
}
func TestUT013UT046IT376FirstSuccessDelivers(t *testing.T) {
	good := &fakeSender{receipt: Receipt{Provider: "smtp", ID: "1", AcceptedAt: time.Now()}}
	bad := &fakeSender{err: errors.New("down")}
	r, _ := NewRegistry(map[Channel]Sender{Email: good, SMS: bad})
	result := Deliver(context.Background(), r, map[Channel]Intent{Email: {ID: "1"}, SMS: {ID: "2"}})
	if result.Status != "DELIVERED" || good.calls != 1 || bad.calls != 1 {
		t.Fatalf("unexpected aggregate: %+v", result)
	}
}
func TestUT014UT047IT377AllFailuresRemainTyped(t *testing.T) {
	bad := &fakeSender{err: errors.New("down")}
	r, _ := NewRegistry(map[Channel]Sender{Email: bad})
	result := Deliver(context.Background(), r, map[Channel]Intent{Email: {ID: "1"}, SMS: {ID: "2"}})
	if result.Status != "FAILED" || len(result.Attempts) != 2 {
		t.Fatalf("unexpected aggregate: %+v", result)
	}
	if _, err := r.Sender(SMS); !errors.Is(err, ErrUnknownChannel) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTwilioCallbackIsTenantScoped(t *testing.T) {
	got, err := tenantCallbackURL("https://api.example/webhooks/twilio/status", "tenant-1")
	if err != nil || got != "https://api.example/webhooks/twilio/status?tenantId=tenant-1" {
		t.Fatalf("callback=%q err=%v", got, err)
	}
	if _, err := tenantCallbackURL("https://api.example/webhooks/twilio/status", ""); err == nil {
		t.Fatal("unscoped callback accepted")
	}
}
