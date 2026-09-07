package link_delivery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/notifications"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type recordingSender struct {
	receipt notifications.Receipt
	err     error
	calls   int
}

func (s *recordingSender) Send(context.Context, notifications.Intent) (notifications.Receipt, error) {
	s.calls++
	return s.receipt, s.err
}

func TestIT076PartialDeliveryAndRetryAreRecordedPerChannel(t *testing.T) {
	db := deliveryDB(t)
	tenantID, invitationID := identity.NewID(), identity.NewID()
	good := &recordingSender{receipt: notifications.Receipt{Provider: "smtp", ID: "mail-1"}}
	bad := &recordingSender{err: errors.New("provider unavailable")}
	registry, err := notifications.NewRegistry(map[notifications.Channel]notifications.Sender{notifications.Email: good, notifications.SMS: bad})
	if err != nil {
		t.Fatal(err)
	}
	intents := []invitationcore.DeliveryIntent{{Channel: string(notifications.Email), Destination: "owner@example.com"}, {Channel: string(notifications.SMS), Destination: "+5511999999999"}}
	within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
	if err := deliver(context.Background(), registry, tenantID, invitationID, "opaque-token", intents, "capture", time.Now().UTC(), within); err != nil {
		t.Fatal(err)
	}
	var aggregate database.Delivery
	if err := db.Where("tenant_id=? AND intent_id=?", tenantID, invitationID).First(&aggregate).Error; err != nil {
		t.Fatal(err)
	}
	if aggregate.Status != "DELIVERED" {
		t.Fatalf("partial success must deliver aggregate: %+v", aggregate)
	}
	var attempts []database.ChannelAttempt
	if err := db.Where("tenant_id=? AND delivery_id=?", tenantID, aggregate.ID).Order("channel").Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].Status != "SENT" || attempts[1].Status != "FAILED" {
		t.Fatalf("channel outcomes were not retained: %+v", attempts)
	}

	bad.err = nil
	bad.receipt = notifications.Receipt{Provider: "twilio", ID: "sms-1"}
	if err := deliver(context.Background(), registry, tenantID, invitationID, "opaque-token", intents, "capture", time.Now().UTC(), within); err != nil {
		t.Fatal(err)
	}
	if good.calls != 1 || bad.calls != 2 {
		t.Fatalf("successful channel was retried: email=%d sms=%d", good.calls, bad.calls)
	}
}

func TestIT226AllFailedDeliveryRemainsVisibleForRetry(t *testing.T) {
	db := deliveryDB(t)
	tenantID, invitationID := identity.NewID(), identity.NewID()
	bad := &recordingSender{err: errors.New("provider endpoint must remain private")}
	registry, err := notifications.NewRegistry(map[notifications.Channel]notifications.Sender{notifications.Email: bad})
	if err != nil {
		t.Fatal(err)
	}
	within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
	err = deliver(context.Background(), registry, tenantID, invitationID, "opaque-token", []invitationcore.DeliveryIntent{{Channel: "EMAIL", Destination: "owner@example.com"}}, "recapture", time.Now().UTC(), within)
	code, _, public := apperror.Public(err)
	if code != apperror.DependencyUnavailable || strings.Contains(public, "provider endpoint") {
		t.Fatalf("unsafe delivery error: code=%s public=%q", code, public)
	}
	var aggregate database.Delivery
	if err := db.Where("tenant_id=? AND intent_id=?", tenantID, invitationID).First(&aggregate).Error; err != nil || aggregate.Status != "FAILED" {
		t.Fatalf("failed delivery was not retained: %+v %v", aggregate, err)
	}
	var attempt database.ChannelAttempt
	if err := db.Where("delivery_id=?", aggregate.ID).First(&attempt).Error; err != nil || attempt.Status != "FAILED" || attempt.LastError == "" {
		t.Fatalf("failed channel was not retryable: %+v %v", attempt, err)
	}
}

func deliveryDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS notifications").Error; err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&database.Delivery{}, &database.ChannelAttempt{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	return db
}
