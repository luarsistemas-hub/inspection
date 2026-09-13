package receive_twilio_status

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/notifications/callbacks"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/notifications"
	"inspection/services/inspection/internal/platform/observability"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

type Dependencies struct {
	DB                              *gorm.DB
	AuthToken, PublicURL, AccountID string
	Clock                           func() time.Time
	Runner                          TenantRunner
	Metrics                         *observability.Metrics
}

type TenantRunner interface {
	Within(context.Context, identity.ID, func(*gorm.DB) error) error
}

func Setup(mux *http.ServeMux, deps Dependencies) error {
	if mux == nil || deps.DB == nil || deps.AuthToken == "" || deps.PublicURL == "" {
		return errors.New("receive twilio status: missing dependency")
	}
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.Runner == nil {
		deps.Runner = tenanttx.Runner{DB: deps.DB}
	}
	mux.HandleFunc("POST /webhooks/twilio/status", func(w http.ResponseWriter, r *http.Request) { handle(w, r, deps) })
	return nil
}

func handle(w http.ResponseWriter, r *http.Request, deps Dependencies) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid callback", http.StatusBadRequest)
		return
	}
	tenantID, err := identity.ParseID(r.URL.Query().Get("tenantId"))
	if err != nil {
		http.Error(w, "invalid callback", http.StatusBadRequest)
		return
	}
	signedURL := strings.TrimRight(deps.PublicURL, "?")
	if r.URL.RawQuery != "" {
		signedURL += "?" + r.URL.RawQuery
	}
	if !notifications.VerifyTwilioSignature(deps.AuthToken, signedURL, r.PostForm, r.Header.Get("X-Twilio-Signature")) {
		http.Error(w, "invalid callback", http.StatusForbidden)
		return
	}
	receipt, status := r.PostForm.Get("MessageSid"), r.PostForm.Get("MessageStatus")
	if receipt == "" || status == "" {
		http.Error(w, "invalid callback", http.StatusBadRequest)
		return
	}
	sum := sha256.Sum256([]byte(receipt + "\x00" + status + "\x00" + r.Header.Get("X-Twilio-Signature")))
	callbackID := fmt.Sprintf("%x", sum[:])
	correlated := false
	err = deps.Runner.Within(r.Context(), tenantID, func(tx *gorm.DB) error {
		if err := callbacks.Record(r.Context(), tx, tenantID, "twilio", deps.AccountID, callbackID, receipt, status, deps.Clock()); err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&database.ChannelAttempt{}).Where("tenant_id=? AND provider=? AND provider_account=? AND receipt_id=?", tenantID, "twilio", deps.AccountID, receipt).Count(&count).Error; err != nil {
			return err
		}
		correlated = count > 0
		return nil
	})
	if err != nil {
		http.Error(w, "callback unavailable", http.StatusServiceUnavailable)
		return
	}
	if deps.Metrics != nil {
		deps.Metrics.Callback("twilio", correlated)
	}
	w.WriteHeader(http.StatusNoContent)
}
