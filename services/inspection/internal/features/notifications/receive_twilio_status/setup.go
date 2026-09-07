package receive_twilio_status

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/notifications"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Dependencies struct {
	DB                   *gorm.DB
	AuthToken, PublicURL string
	Clock                func() time.Time
	Runner               TenantRunner
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
	timestamp := r.Header.Get("X-Twilio-Request-Timestamp")
	signedURL := strings.TrimRight(deps.PublicURL, "?")
	if r.URL.RawQuery != "" {
		signedURL += "?" + r.URL.RawQuery
	}
	if notifications.ValidateCallbackTimestamp(timestamp, deps.Clock().UTC(), 5*time.Minute) != nil || !notifications.VerifyTwilioSignature(deps.AuthToken, signedURL, r.PostForm, r.Header.Get("X-Twilio-Signature")) {
		http.Error(w, "invalid callback", http.StatusForbidden)
		return
	}
	receipt, status := r.PostForm.Get("MessageSid"), r.PostForm.Get("MessageStatus")
	if receipt == "" || status == "" {
		http.Error(w, "invalid callback", http.StatusBadRequest)
		return
	}
	callbackID := receipt + ":" + status + ":" + timestamp
	err = deps.Runner.Within(r.Context(), tenantID, func(tx *gorm.DB) error {
		var attempt database.ChannelAttempt
		if err := tx.Where("tenant_id=? AND receipt_id=?", tenantID, receipt).First(&attempt).Error; err != nil {
			return err
		}
		callback := database.ProviderCallback{ID: identity.NewID(), TenantID: tenantID, Provider: "twilio", CallbackID: callbackID, ReceiptID: receipt, Status: status, ReceivedAt: deps.Clock().UTC()}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&callback)
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		normalized := "SENT"
		if status == "delivered" {
			normalized = "DELIVERED"
		} else if status == "failed" || status == "undelivered" {
			normalized = "FAILED"
		}
		if err := tx.Model(&database.ChannelAttempt{}).Where("id=?", attempt.ID).Updates(map[string]any{"status": normalized, "updated_at": deps.Clock().UTC()}).Error; err != nil {
			return err
		}
		if normalized == "DELIVERED" {
			return tx.Model(&database.Delivery{}).Where("id=?", attempt.DeliveryID).Updates(map[string]any{"status": "DELIVERED", "updated_at": deps.Clock().UTC()}).Error
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			http.Error(w, "callback target unavailable", http.StatusNotFound)
			return
		}
		http.Error(w, "callback unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
