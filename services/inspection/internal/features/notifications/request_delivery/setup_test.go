package request_delivery

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/notifications/core"
	"inspection/services/inspection/internal/platform/apperror"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type notificationRecorder struct {
	keys               []string
	digests            map[string]string
	accepted           int
	channelDisabled    bool
	invalidDestination string
	sendErr            error
}

func (r *notificationRecorder) Send(_ context.Context, notification core.Notification) (core.NotificationResult, error) {
	r.keys = append(r.keys, notification.IdempotencyKey)
	if r.sendErr != nil {
		return core.NotificationResult{}, r.sendErr
	}
	if r.channelDisabled {
		return core.NotificationResult{}, apperror.New(apperror.IntegrationDisabled, "channel", "disabled")
	}
	if notification.Recipient.Destination == r.invalidDestination {
		return core.NotificationResult{}, &core.Error{Code: core.InvalidRecipient, Field: "recipient"}
	}
	digest, err := core.CanonicalDigest(notification)
	if err != nil {
		return core.NotificationResult{}, err
	}
	if r.digests == nil {
		r.digests = map[string]string{}
	}
	if existing, ok := r.digests[notification.IdempotencyKey]; ok {
		if existing != digest {
			return core.NotificationResult{}, &core.Error{Code: core.IdempotencyConflict, Field: "idempotencyKey"}
		}
		return core.NotificationResult{ID: identity.NewID(), State: core.StateQueued, Reused: true}, nil
	}
	r.digests[notification.IdempotencyKey] = digest
	r.accepted++
	return core.NotificationResult{ID: identity.NewID(), State: core.StateQueued}, nil
}

func TestRequestDeliveryDeduplicatesByClassifiedEvent(t *testing.T) {
	service := &notificationRecorder{}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	tenantID, inspectionID, membershipID := identity.NewID(), identity.NewID(), identity.NewID()
	input := Input{
		TenantID: tenantID, InspectionID: inspectionID,
		Previous: "ATTENTION", Current: "CRITICAL",
		Recipients: []Recipient{{ID: membershipID.String(), ChannelID: identity.NewID().String(), Channel: "EMAIL", Destination: "reviewer@example.test", Internal: true, Verified: true, Selected: true}},
	}
	if err := Request(context.Background(), db, service, input); err != nil {
		t.Fatal(err)
	}
	firstKey := service.keys[0]
	if err := Request(context.Background(), db, service, input); err != nil {
		t.Fatal(err)
	}
	if len(service.keys) != 2 || service.keys[1] != firstKey {
		t.Fatalf("same event keys = %v, want stable idempotency key", service.keys)
	}
	if err := Request(context.Background(), db, service, input); err != nil {
		t.Fatal(err)
	}
	if len(service.keys) != 3 || service.keys[2] != firstKey {
		t.Fatalf("new event key = %v, want one stable alert intent per inspection", service.keys)
	}
}

func TestRequestDeliveryKeysEachRecipientChannelSeparately(t *testing.T) {
	service := &notificationRecorder{}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	in := Input{TenantID: identity.NewID(), InspectionID: identity.NewID(), Previous: "ATTENTION", Current: "CRITICAL", Recipients: []Recipient{
		{ID: "membership", ChannelID: "channel-one", Channel: "EMAIL", Destination: "one@example.test", Internal: true, Verified: true, Selected: true},
		{ID: "membership", ChannelID: "channel-two", Channel: "EMAIL", Destination: "two@example.test", Internal: true, Verified: true, Selected: true},
	}}
	if err := Request(context.Background(), db, service, in); err != nil {
		t.Fatal(err)
	}
	if len(service.keys) != 2 || service.keys[0] == service.keys[1] {
		t.Fatalf("per-channel keys = %v, want two distinct idempotency keys", service.keys)
	}
}

func TestRequestDeliverySkipsDigestConflictAndKeepsOneAlertWhenDestinationChanges(t *testing.T) {
	service := &notificationRecorder{}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	in := Input{TenantID: identity.NewID(), InspectionID: identity.NewID(), Previous: "ATTENTION", Current: "CRITICAL", Recipients: []Recipient{{ID: "membership", ChannelID: "channel", Channel: "EMAIL", Destination: "first@example.test", Internal: true, Verified: true, Selected: true}}}
	if err := Request(context.Background(), db, service, in); err != nil {
		t.Fatal(err)
	}
	in.Previous, in.Current = "ATTENTION", "CRITICAL"
	in.Recipients[0].Destination = "changed@example.test"
	if err := Request(context.Background(), db, service, in); err != nil {
		t.Fatalf("digest conflict must be treated as already alerted: %v", err)
	}
	if service.accepted != 1 || len(service.keys) != 2 || service.keys[0] != service.keys[1] {
		t.Fatalf("accepted=%d keys=%v, want one accepted alert and stable key", service.accepted, service.keys)
	}
}

func TestRequestDeliverySkipsInvalidRecipientAndIntegrationDisabled(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	service := &notificationRecorder{invalidDestination: "invalid@example.test"}
	in := Input{TenantID: identity.NewID(), InspectionID: identity.NewID(), Previous: "ATTENTION", Current: "CRITICAL", Recipients: []Recipient{
		{ID: "bad", ChannelID: "bad-channel", Channel: "EMAIL", Destination: "invalid@example.test", Internal: true, Verified: true, Selected: true},
		{ID: "good", ChannelID: "good-channel", Channel: "EMAIL", Destination: "good@example.test", Internal: true, Verified: true, Selected: true},
	}}
	if err := Request(context.Background(), db, service, in); err != nil {
		t.Fatalf("invalid destination should be skipped: %v", err)
	}
	if service.accepted != 1 {
		t.Fatalf("accepted=%d, want valid recipient only", service.accepted)
	}
	service.channelDisabled = true
	if err := Request(context.Background(), db, service, in); err != nil {
		t.Fatalf("IntegrationDisabled should be skipped: %v", err)
	}
}

func TestRequestDeliveryRequiresChannelID(t *testing.T) {
	service := &notificationRecorder{}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	in := Input{TenantID: identity.NewID(), InspectionID: identity.NewID(), Previous: "ATTENTION", Current: "CRITICAL", Recipients: []Recipient{{ID: "membership", Channel: "EMAIL", Destination: "private@example.test", Internal: true, Verified: true, Selected: true}}}
	var requestError *core.Error
	if err := Request(context.Background(), db, service, in); !errors.As(err, &requestError) || requestError.Field != "channelId" {
		t.Fatalf("Request error=%v, want missing channelId", err)
	}
	if len(service.keys) != 0 {
		t.Fatalf("service called with missing channel ID: %v", service.keys)
	}
}

func TestRequestDeliverySkipsOnlyRecipientScopedErrors(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	in := Input{TenantID: identity.NewID(), InspectionID: identity.NewID(), Previous: "ATTENTION", Current: "CRITICAL", Recipients: []Recipient{
		{ID: "first", ChannelID: "first-channel", Channel: "EMAIL", Destination: "first@example.test", Internal: true, Verified: true, Selected: true},
		{ID: "second", ChannelID: "second-channel", Channel: "EMAIL", Destination: "second@example.test", Internal: true, Verified: true, Selected: true},
	}}
	for _, code := range []core.ErrorCode{core.IdempotencyConflict, core.InvalidRecipient, core.UnknownChannel} {
		service := &notificationRecorder{sendErr: &core.Error{Code: code, Field: "recipient"}}
		if err := Request(context.Background(), db, service, in); err != nil {
			t.Fatalf("%s must be skipped per recipient: %v", code, err)
		}
		if len(service.keys) != 2 {
			t.Fatalf("%s keys=%v, want every recipient attempted", code, service.keys)
		}
	}
	for _, code := range []core.ErrorCode{core.InvalidInput, core.UnknownTemplate, core.MissingVariable, core.IncompatibleVariables} {
		service := &notificationRecorder{sendErr: &core.Error{Code: code, Field: "template"}}
		err := Request(context.Background(), db, service, in)
		if !core.Is(err, code) {
			t.Fatalf("%s error=%v, want propagated", code, err)
		}
		if len(service.keys) != 1 {
			t.Fatalf("%s keys=%v, want request aborted on first failure", code, service.keys)
		}
	}
	dependencyFailure := errors.New("catalog unavailable")
	service := &notificationRecorder{sendErr: dependencyFailure}
	if err := Request(context.Background(), db, service, in); !errors.Is(err, dependencyFailure) {
		t.Fatalf("dependency error=%v, want propagated", err)
	}
}

func TestRequestDeliveryLogsAlreadyAlertedWithTenantAndInspection(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	in := Input{TenantID: identity.NewID(), InspectionID: identity.NewID(), Previous: "ATTENTION", Current: "CRITICAL", Recipients: []Recipient{{ID: "membership", ChannelID: "channel", Channel: "EMAIL", Destination: "reviewer@example.test", Internal: true, Verified: true, Selected: true}}}
	service := &notificationRecorder{sendErr: &core.Error{Code: core.IdempotencyConflict, Field: "idempotencyKey"}}
	if err := Request(context.Background(), db, service, in); err != nil {
		t.Fatal(err)
	}
	logged := output.String()
	for _, want := range []string{"reason=already_alerted", "tenantId=" + in.TenantID.String(), "inspectionId=" + in.InspectionID.String()} {
		if !strings.Contains(logged, want) {
			t.Fatalf("log %q missing %q", logged, want)
		}
	}
}
