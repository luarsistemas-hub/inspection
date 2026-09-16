package dispatch_capture_invitation

import (
	"strings"
	"testing"

	"inspection/libs/identity"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
)

func TestDeliveryIdempotencyKeyAcceptsEmailDestination(t *testing.T) {
	invit := identity.NewID()
	target := invitationcore.DeliveryIntent{Channel: "EMAIL", Destination: "owner@example.com"}
	key := deliveryIdempotencyKey(invit, target)
	if !strings.HasPrefix(key, invit.String()+":EMAIL:") {
		t.Fatalf("key=%q", key)
	}
	if strings.Contains(key, target.Destination) || strings.Contains(key, "@") {
		t.Fatalf("destination leaked into idempotency key: %q", key)
	}
	if len(key) > 200 {
		t.Fatalf("key exceeds validation limit: %d", len(key))
	}
	if err := notificationcore.Validate(notificationcore.Notification{
		TenantID: identity.NewID(), Recipient: notificationcore.Recipient{Destination: target.Destination},
		Channel: notificationcore.ChannelEmail, Template: notificationcore.TemplateRef{Name: "capture-link", Version: "v1"},
		Variables:     map[string]string{"captureUrl": "http://localhost:3003/capture/test", "recipientName": "participante"},
		CorrelationID: "corr-1", IdempotencyKey: key,
	}, notificationcore.DefaultCatalog()); err != nil {
		t.Fatalf("generated key is rejected by notification validation: %v", err)
	}
}

func TestDeliveryIdempotencyKeyDiffersPerDestination(t *testing.T) {
	invit := identity.NewID()
	first := deliveryIdempotencyKey(invit, invitationcore.DeliveryIntent{Channel: "EMAIL", Destination: "first@example.com"})
	second := deliveryIdempotencyKey(invit, invitationcore.DeliveryIntent{Channel: "EMAIL", Destination: "second@example.com"})
	if first == second {
		t.Fatalf("different destinations share key %q", first)
	}
}
