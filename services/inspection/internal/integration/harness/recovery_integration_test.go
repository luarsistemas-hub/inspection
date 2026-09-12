//go:build integration

package harness

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/notifications/core"
	"inspection/services/inspection/internal/features/notifications/execute_delivery"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/notifications"

	"gorm.io/gorm"
)

type blockingAdapter struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (a *blockingAdapter) Send(context.Context, notifications.DeliveryIntent) (notifications.Receipt, error) {
	a.calls.Add(1)
	close(a.started)
	<-a.release
	return notifications.Receipt{}, &notifications.DeliveryError{Kind: notifications.ErrorUnknown, Code: "provider_interrupted"}
}

func TestE2E009ExpiredWorkerLeaseBecomesUnknownWithoutDuplicateSend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h, err := NewFromEnv(ctx)
	if err != nil {
		t.Fatalf("E2E-009 requires PostgreSQL, RabbitMQ and local provider infrastructure: %v", err)
	}
	defer h.Close()
	now := time.Now().UTC().Truncate(time.Microsecond)
	tenantID, deliveryID, attemptID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := h.AdminDB.Create(&database.Tenant{ID: tenantID, TenantID: tenantID, Name: "E2E-009", Language: "pt-BR", DefaultTimezone: "UTC", Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Create(&database.Delivery{ID: deliveryID, TenantID: tenantID, IntentID: identity.NewID(), Status: string(core.StateQueued), LogicalTemplate: "reminder", TemplateVersion: "v1", CorrelationID: "e2e-009", IdempotencyKey: "e2e-009", RequestDigest: "e2e-009", SelectedProvider: "twilio", CreatedAt: now.Add(-2 * time.Minute), UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Create(&database.ChannelAttempt{ID: attemptID, TenantID: tenantID, DeliveryID: deliveryID, Channel: "SMS", Destination: "+15550000001", Status: string(core.StateQueued), Provider: "twilio", TemplateVariables: []byte(`{"recipientName":"test"}`), NextAttemptAt: now, CreatedAt: now.Add(-2 * time.Minute), UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	adapter := &blockingAdapter{started: make(chan struct{}), release: make(chan struct{})}
	gateway, err := notifications.NewGateway(map[notifications.Channel]map[notifications.Provider]notifications.Adapter{"SMS": {notifications.ProviderTwilio: adapter}})
	if err != nil {
		t.Fatal(err)
	}
	executor, err := execute_delivery.Setup(execute_delivery.Dependencies{DB: h.AdminDB, Gateway: gateway, Now: func() time.Time { return now }, LeaseDuration: time.Minute, MaxAttempts: 4, RetryDelays: []time.Duration{5 * time.Second, 30 * time.Second, 5 * time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	firstRun := make(chan struct {
		processed bool
		err       error
	}, 1)
	go func() {
		processed, err := executor.RunDue(ctx)
		firstRun <- struct {
			processed bool
			err       error
		}{processed: processed, err: err}
	}()
	select {
	case <-adapter.started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider call did not start")
	}

	var processing database.ChannelAttempt
	if err := h.AdminDB.Where("tenant_id=? AND id=?", tenantID, attemptID).First(&processing).Error; err != nil {
		t.Fatal(err)
	}
	if processing.Status != string(core.StateProcessing) || processing.LeaseExpiresAt == nil {
		t.Fatalf("provider reservation status=%s lease=%v", processing.Status, processing.LeaseExpiresAt)
	}

	restarted, err := execute_delivery.Setup(execute_delivery.Dependencies{DB: h.AdminDB, Gateway: gateway, Now: func() time.Time { return now.Add(2 * time.Minute) }, LeaseDuration: time.Minute, MaxAttempts: 4, RetryDelays: []time.Duration{5 * time.Second, 30 * time.Second, 5 * time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := restarted.RunDue(ctx); err != nil || processed {
		t.Fatalf("restart recovery processing=%v err=%v", processed, err)
	}
	var attempt database.ChannelAttempt
	if err := h.AdminDB.Where("tenant_id=? AND id=?", tenantID, attemptID).First(&attempt).Error; err != nil {
		t.Fatal(err)
	}
	if attempt.Status != string(core.StateUnknown) || adapter.calls.Load() != 1 {
		t.Fatalf("unsafe recovery: status=%s provider_calls=%d", attempt.Status, adapter.calls.Load())
	}
	var delivery database.Delivery
	if err := h.AdminDB.Where("tenant_id=? AND id=?", tenantID, deliveryID).First(&delivery).Error; err != nil && err != gorm.ErrRecordNotFound {
		t.Fatal(err)
	}
	if delivery.Status != string(core.StateUnknown) {
		t.Fatalf("aggregate status=%s", delivery.Status)
	}
	close(adapter.release)
	select {
	case result := <-firstRun:
		if result.err != nil || !result.processed {
			t.Fatalf("crashed worker completion processed=%v err=%v", result.processed, result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("provider call did not release")
	}
}
