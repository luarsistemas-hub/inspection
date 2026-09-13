package receive_meta_status

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type metaSQLiteRunner struct{ db *gorm.DB }

func (r metaSQLiteRunner) Within(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
	return r.db.WithContext(ctx).Transaction(fn)
}

type metaResolver struct{ tenantID identity.ID }

func (r metaResolver) Resolve(context.Context, string, string) (identity.ID, error) {
	return r.tenantID, nil
}

func metaCallbackDB(t *testing.T) *gorm.DB {
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

func signedMetaCallback(t *testing.T, secret string) *http.Request {
	t.Helper()
	body := []byte(`{"callbackId":"callback-1","receiptId":"wamid.1","status":"delivered","phoneNumberId":"phone-1"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/meta", strings.NewReader(string(body)))
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	return req
}

func TestUT051UT052MetaVerificationAndRawSignature(t *testing.T) {
	body := []byte(`{"callbackId":"callback-1","receiptId":"wamid.1","status":"delivered"}`)
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !validSignature("secret", signature, body) {
		t.Fatal("valid raw callback rejected")
	}
	if validSignature("secret", signature, []byte(`{"status":"delivered","receiptId":"wamid.1","callbackId":"callback-1"}`)) {
		t.Fatal("re-serialized callback accepted")
	}
	items, err := parse(body)
	if err != nil || len(items) != 1 || items[0].ReceiptID != "wamid.1" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestMetaStatusParsesDurableProviderAccount(t *testing.T) {
	body := []byte(`{"entry":[{"changes":[{"value":{"metadata":{"phone_number_id":"phone-1"},"statuses":[{"id":"wamid.1","status":"delivered"}]}}]}]}`)
	items, err := parse(body)
	if err != nil || len(items) != 1 || items[0].AccountID != "phone-1" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestMetaStatusSuccessfulCallbackWithoutMetrics(t *testing.T) {
	db := metaCallbackDB(t)
	tenantID := identity.NewID()
	now := time.Unix(1000, 0).UTC()
	deps := Dependencies{
		DB:        db,
		AppSecret: "secret",
		Runner:    metaSQLiteRunner{db: db},
		Resolver:  metaResolver{tenantID: tenantID},
		Clock:     func() time.Time { return now },
	}

	w := httptest.NewRecorder()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("callback panicked without metrics: %v", recovered)
		}
	}()
	handle(w, signedMetaCallback(t, deps.AppSecret), deps)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestMetaStatusPersistsEarlyCallbackBeforeReceiptCorrelation(t *testing.T) {
	db := metaCallbackDB(t)
	tenantID := identity.NewID()
	deps := Dependencies{
		DB:        db,
		AppSecret: "secret",
		Runner:    metaSQLiteRunner{db: db},
		Resolver:  metaResolver{tenantID: tenantID},
		Clock:     func() time.Time { return time.Unix(1000, 0).UTC() },
	}

	w := httptest.NewRecorder()
	handle(w, signedMetaCallback(t, deps.AppSecret), deps)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}

	var count int64
	if err := db.Table("notifications.provider_callbacks").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("early callback count=%d, want 1", count)
	}
}
