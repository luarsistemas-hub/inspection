// Package receive_meta_status authenticates and durably records Meta callbacks.
package receive_meta_status

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/notifications/callbacks"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/observability"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

// Dependencies are callback settings owned by the API composition root.
type Dependencies struct {
	DB                                *gorm.DB
	VerifyToken, AppSecret, AccountID string
	Clock                             func() time.Time
	Runner                            TenantRunner
	Resolver                          TenantResolver
	Metrics                           *observability.Metrics
}

// TenantResolver finds the tenant owning a provider account and receipt using
// durable delivery metadata. Webhook requests themselves are not tenant-scoped.
type TenantResolver interface {
	Resolve(context.Context, string, string) (identity.ID, error)
}

// TenantRunner applies the request's tenant RLS scope.
type TenantRunner interface {
	Within(context.Context, identity.ID, func(*gorm.DB) error) error
}

// Setup registers Meta's challenge and signed callback endpoints.
func Setup(mux *http.ServeMux, deps Dependencies) error {
	if mux == nil || deps.DB == nil || deps.VerifyToken == "" || deps.AppSecret == "" {
		return errors.New("receive meta status: missing dependency")
	}
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.Runner == nil {
		deps.Runner = tenanttx.Runner{DB: deps.DB}
	}
	if deps.Resolver == nil {
		deps.Resolver = durableTenantResolver{DB: deps.DB}
	}
	mux.HandleFunc("GET /webhooks/meta", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("hub.verify_token") != deps.VerifyToken {
			http.Error(w, "invalid callback", http.StatusForbidden)
			return
		}
		challenge := r.URL.Query().Get("hub.challenge")
		if challenge == "" {
			http.Error(w, "invalid callback", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(challenge))
	})
	mux.HandleFunc("POST /webhooks/meta", func(w http.ResponseWriter, r *http.Request) { handle(w, r, deps) })
	return nil
}

func handle(w http.ResponseWriter, r *http.Request, deps Dependencies) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "invalid callback", http.StatusBadRequest)
		return
	}
	if !validSignature(deps.AppSecret, r.Header.Get("X-Hub-Signature-256"), body) {
		http.Error(w, "invalid callback", http.StatusForbidden)
		return
	}
	items, err := parse(body)
	if err != nil || len(items) == 0 {
		http.Error(w, "invalid callback", http.StatusBadRequest)
		return
	}
	account := items[0].AccountID
	if account == "" {
		account = deps.AccountID
	}
	tenantID, err := deps.Resolver.Resolve(r.Context(), account, items[0].ReceiptID)
	if err != nil {
		http.Error(w, "invalid callback", http.StatusBadRequest)
		return
	}
	for _, item := range items[1:] {
		itemAccount := item.AccountID
		if itemAccount == "" {
			itemAccount = account
		}
		if itemAccount != account {
			http.Error(w, "invalid callback", http.StatusBadRequest)
			return
		}
		resolved, resolveErr := deps.Resolver.Resolve(r.Context(), itemAccount, item.ReceiptID)
		if resolveErr != nil || resolved != tenantID {
			http.Error(w, "invalid callback", http.StatusBadRequest)
			return
		}
	}
	correlated := false
	if err := deps.Runner.Within(r.Context(), tenantID, func(tx *gorm.DB) error {
		for _, item := range items {
			itemAccount := item.AccountID
			if itemAccount == "" {
				itemAccount = account
			}
			if itemAccount != account {
				return errors.New("callback contains multiple provider accounts")
			}
			if err := callbacks.Record(r.Context(), tx, tenantID, "meta", itemAccount, item.ID, item.ReceiptID, item.Status, deps.Clock()); err != nil {
				return err
			}
			var count int64
			if err := tx.Model(&database.ChannelAttempt{}).Where("tenant_id=? AND provider=? AND provider_account=? AND receipt_id=?", tenantID, "meta", itemAccount, item.ReceiptID).Count(&count).Error; err != nil {
				return err
			}
			correlated = correlated || count > 0
		}
		return nil
	}); err != nil {
		http.Error(w, "callback unavailable", http.StatusServiceUnavailable)
		return
	}
	if deps.Metrics != nil {
		deps.Metrics.Callback("meta", correlated)
	}
	w.WriteHeader(http.StatusNoContent)
}

func validSignature(secret, signature string, body []byte) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	expected, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hmac.Equal(expected, mac.Sum(nil))
}

type callback struct {
	ID, ReceiptID, Status, AccountID string
}

type durableTenantResolver struct{ DB *gorm.DB }

func (r durableTenantResolver) Resolve(ctx context.Context, account, receipt string) (identity.ID, error) {
	var tenantID identity.ID
	if r.DB == nil || account == "" || receipt == "" {
		return tenantID, errors.New("callback metadata unavailable")
	}
	result := r.DB.WithContext(ctx).Raw(`SELECT notifications.resolve_meta_callback_tenant(?, ?)`, account, receipt).Scan(&tenantID)
	if result.Error != nil {
		return tenantID, result.Error
	}
	if tenantID == (identity.ID{}) {
		return tenantID, errors.New("callback metadata not found")
	}
	return tenantID, nil
}

func parse(body []byte) ([]callback, error) {
	// The compact shape makes simulator and direct provider callbacks equally
	// testable; the production shape below reads Meta status entries verbatim.
	var compact struct {
		CallbackID string `json:"callbackId"`
		ReceiptID  string `json:"receiptId"`
		Status     string `json:"status"`
		AccountID  string `json:"phoneNumberId"`
	}
	if err := json.Unmarshal(body, &compact); err != nil {
		return nil, err
	}
	if compact.CallbackID != "" && compact.ReceiptID != "" && compact.Status != "" {
		return []callback{{ID: compact.CallbackID, ReceiptID: compact.ReceiptID, Status: compact.Status, AccountID: compact.AccountID}}, nil
	}
	var payload struct {
		Entry []struct {
			Changes []struct {
				Value struct {
					Metadata struct {
						PhoneNumberID string `json:"phone_number_id"`
					} `json:"metadata"`
					Statuses []struct {
						ID     string `json:"id"`
						Status string `json:"status"`
					} `json:"statuses"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	var result []callback
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			for _, status := range change.Value.Statuses {
				if status.ID == "" || status.Status == "" {
					continue
				}
				sum := sha256.Sum256([]byte(status.ID + "\x00" + status.Status))
				result = append(result, callback{ID: hex.EncodeToString(sum[:]), ReceiptID: status.ID, Status: status.Status, AccountID: change.Value.Metadata.PhoneNumberID})
			}
		}
	}
	return result, nil
}
