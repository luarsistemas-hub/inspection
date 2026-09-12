package receive_twilio_status

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type sqliteRunner struct{ db *gorm.DB }

func (r sqliteRunner) Within(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
	return r.db.WithContext(ctx).Transaction(fn)
}

func callbackDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`ATTACH DATABASE ':memory:' AS notifications`).Error; err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE notifications.deliveries (id blob primary key,tenant_id blob,intent_id blob,inspection_id blob,status text,logical_template text,template_version text,correlation_id text,idempotency_key text,request_digest text,recipient_id text,selected_provider text,scheduled_at datetime,lease_expires_at datetime,created_at datetime,updated_at datetime)`,
		`CREATE TABLE notifications.channel_attempts (id blob primary key,tenant_id blob,delivery_id blob,channel text,destination text,status text,provider text,provider_account text,receipt_id text,attempts integer,last_error text,template_variables blob,next_attempt_at datetime,lease_expires_at datetime,last_attempt_at datetime,created_at datetime,updated_at datetime)`,
		`CREATE TABLE notifications.provider_callbacks (id blob primary key,tenant_id blob,provider text,provider_account text,callback_id text,receipt_id text,channel_attempt_id blob,status text,received_at datetime,UNIQUE(provider,provider_account,callback_id))`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func signedCallback(t *testing.T, publicURL, token string, tenantID identity.ID, timestamp time.Time, signatureOverride string) *http.Request {
	t.Helper()
	form := url.Values{"MessageSid": {"SM1"}, "MessageStatus": {"delivered"}}
	rawURL := publicURL + "?tenantId=" + url.QueryEscape(tenantID.String())
	mac := hmac.New(sha1.New, []byte(token))
	_, _ = mac.Write([]byte(rawURL + "MessageSidSM1" + "MessageStatusdelivered"))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if signatureOverride != "" {
		signature = signatureOverride
	}
	req := httptest.NewRequest(http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = timestamp // Twilio's status callback signature has no timestamp contract.
	req.Header.Set("X-Twilio-Signature", signature)
	return req
}

func TestIT378IT379IT541ValidCallbackUpdatesOnce(t *testing.T) {
	db := callbackDB(t)
	tenantID := identity.NewID()
	delivery := database.Delivery{ID: identity.NewID(), TenantID: tenantID, IntentID: identity.NewID(), Status: "SENT"}
	attempt := database.ChannelAttempt{ID: identity.NewID(), TenantID: tenantID, DeliveryID: delivery.ID, Channel: "SMS", Destination: "+15550000000", Status: "SENT", Provider: "twilio", ProviderAccount: "AC-local", ReceiptID: "SM1"}
	if err := db.Create(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&attempt).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0).UTC()
	deps := Dependencies{DB: db, AuthToken: "token", PublicURL: "https://api.example/webhooks/twilio/status", AccountID: "AC-local", Clock: func() time.Time { return now }, Runner: sqliteRunner{db: db}}
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		handle(w, signedCallback(t, deps.PublicURL, deps.AuthToken, tenantID, now, ""), deps)
		if w.Code != http.StatusNoContent {
			t.Fatalf("callback %d status=%d body=%q", i, w.Code, w.Body.String())
		}
	}
	var callbackCount int64
	if err := db.Model(&database.ProviderCallback{}).Count(&callbackCount).Error; err != nil || callbackCount != 1 {
		t.Fatalf("callback count=%d err=%v", callbackCount, err)
	}
	if err := db.First(&delivery, "id=?", delivery.ID).Error; err != nil || delivery.Status != "DELIVERED" {
		t.Fatalf("delivery=%+v err=%v", delivery, err)
	}
}

func TestIT542InvalidCallbackMakesNoMutation(t *testing.T) {
	db := callbackDB(t)
	tenantID := identity.NewID()
	now := time.Unix(1000, 0).UTC()
	deps := Dependencies{DB: db, AuthToken: "token", PublicURL: "https://api.example/webhooks/twilio/status", AccountID: "AC-local", Clock: func() time.Time { return now }, Runner: sqliteRunner{db: db}}
	for name, req := range map[string]*http.Request{
		"bad signature": signedCallback(t, deps.PublicURL, deps.AuthToken, tenantID, now, "bad"),
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handle(w, req, deps)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
			}
		})
	}
	var count int64
	if err := db.Model(&database.ProviderCallback{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("callbacks=%d err=%v", count, err)
	}
}
