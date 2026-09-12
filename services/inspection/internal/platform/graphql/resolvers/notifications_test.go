package resolvers

import (
	"context"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestNotificationsAllowTenantAdmin(t *testing.T) {
	tenantID := identity.NewID()
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{
		TenantID: tenantID,
		Principal: requestctx.Principal{
			IdentityID: identity.NewID(),
			TenantID:   tenantID,
			Roles:      []string{auth.TenantAdmin},
		},
	})

	result, err := (&queryResolver{Resolver: &Resolver{}}).MyNotifications(ctx, nil, nil, nil)
	if err != nil {
		t.Fatalf("tenant admin notifications denied: %v", err)
	}
	if result.UnreadCount != 0 || len(result.Nodes) != 0 {
		t.Fatalf("unexpected empty notification result: %+v", result)
	}
}

func TestNotificationDeliveriesExposeSafeOrderedChannelState(t *testing.T) {
	db := task06ResolverDB(t)
	tenantID := identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	delivery := database.Delivery{ID: identity.NewID(), TenantID: tenantID, IntentID: identity.NewID(), Status: "SENT", SelectedProvider: "twilio", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []database.ChannelAttempt{
		{ID: identity.NewID(), TenantID: tenantID, DeliveryID: delivery.ID, Channel: "SMS", Destination: "+15550000001", Status: "DELIVERED", Provider: "twilio", ReceiptID: "safe-receipt", Attempts: 1, LastError: "private provider failure", CreatedAt: now, UpdatedAt: now},
		{ID: identity.NewID(), TenantID: tenantID, DeliveryID: delivery.ID, Channel: "WHATSAPP", Destination: "+15550000002", Status: "FAILED", Provider: "twilio", Attempts: 4, LastError: "private provider failure", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	server := task06GraphQLServer(db)
	adminContext := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "notification-safe"})
	query := `query { notificationDeliveries(first: 1) { nodes { status aggregateStatus selectedProvider channels { channel status provider receiptId attempts createdAt updatedAt } } pageInfo { hasNextPage } } }`
	response := executeTask06GraphQL(t, server, query, adminContext)
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"aggregateStatus":"SENT"`) || !strings.Contains(response, `"channel":"SMS"`) || !strings.Contains(response, `"channel":"WHATSAPP"`) {
		t.Fatalf("unexpected authorized response: %s", response)
	}
	if strings.Contains(response, "+15550000001") || strings.Contains(response, "private provider failure") {
		t.Fatalf("notification response leaked unsafe channel data: %s", response)
	}
	customerContext := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.CustomerViewer}}, CorrelationID: "notification-forbidden"})
	if response := executeTask06GraphQL(t, server, query, customerContext); !strings.Contains(response, "FORBIDDEN") {
		t.Fatalf("customer role received delivery state: %s", response)
	}
}
