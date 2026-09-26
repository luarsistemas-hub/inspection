package dispatch_capture_invitation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type recordingNotifications struct{ calls int }

func (r *recordingNotifications) Send(context.Context, notificationcore.Notification) (notificationcore.NotificationResult, error) {
	r.calls++
	return notificationcore.NotificationResult{ID: identity.NewID(), State: notificationcore.StateQueued}, nil
}

func TestExistingInvitationMarksInspectionInvitedWithoutDuplicateDelivery(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"inspections", "invitations"} {
		if err := db.Exec(`ATTACH DATABASE ':memory:' AS ` + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.Inspection{}, &database.Responsibility{}, &database.Invitation{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenantID, inspectionID, responsibilityID, participantID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Create(&database.Inspection{ID: inspectionID, TenantID: tenantID, Status: "PLANNED", ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{}`), DeadlineAt: time.Now().Add(time.Hour), Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.Responsibility{ID: responsibilityID, TenantID: tenantID, InspectionID: inspectionID, ParticipantID: participantID, Status: "PENDING", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.Invitation{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, TokenHash: []byte("token"), DeliveryIntents: json.RawMessage(`[]`), Status: "ACTIVE", ExpiresAt: time.Now().Add(time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	recorder := &recordingNotifications{}
	handler, err := Setup(Dependencies{Notifications: recorder, CaptureBaseURL: "https://capture.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"inspectionId": inspectionID, "responsibilityId": responsibilityID, "participantId": participantID})
	envelope := events.RawEnvelope{ID: identity.NewID(), Type: "origin.invitation_requested.v1", TenantID: tenantID, Payload: payload}
	if err := db.Transaction(func(tx *gorm.DB) error { return handler(context.Background(), tx, envelope) }); err != nil {
		t.Fatal(err)
	}
	var inspection database.Inspection
	if err := db.First(&inspection, "id=?", inspectionID).Error; err != nil {
		t.Fatal(err)
	}
	if inspection.Status != "INVITED" || inspection.Version != 2 || recorder.calls != 0 {
		t.Fatalf("inspection=%+v duplicate requests=%d", inspection, recorder.calls)
	}
}

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
