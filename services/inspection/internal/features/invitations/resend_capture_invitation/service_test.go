package resend_capture_invitation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type notificationRecorder struct {
	requests []notificationcore.Notification
}

func (r *notificationRecorder) Send(_ context.Context, request notificationcore.Notification) (notificationcore.NotificationResult, error) {
	r.requests = append(r.requests, request)
	return notificationcore.NotificationResult{ID: identity.NewID(), State: notificationcore.StateQueued}, nil
}

func TestResendIssuesFreshInvitationAndQueuesEmail(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"inspections", "participants", "assets", "invitations", "notifications", "audit"} {
		if err := db.Exec(`ATTACH DATABASE ':memory:' AS ` + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.Inspection{}, &database.Responsibility{}, &database.Participant{}, &database.ParticipantContact{}, &database.ChannelSelection{}, &database.Invitation{}, &database.ExternalSession{}, &database.Delivery{}, &database.ChannelAttempt{}, &database.AuditEvent{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatalf("migrate %T: %v", model, err)
		}
	}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	tenantID, inspectionID, responsibilityID, participantID, contactID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	for _, row := range []any{
		&database.Inspection{ID: inspectionID, TenantID: tenantID, AssetID: identity.NewID(), ParticipantID: participantID, Status: "INVITED", DeadlineAt: now.Add(24 * time.Hour), ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{}`), Version: 1},
		&database.Responsibility{ID: responsibilityID, TenantID: tenantID, InspectionID: inspectionID, ParticipantID: participantID, Status: "PENDING", Version: 1},
		&database.Participant{ID: participantID, TenantID: tenantID, Name: "Responsável", Version: 1},
		&database.ParticipantContact{ID: contactID, TenantID: tenantID, ParticipantID: participantID, Channel: "EMAIL", Value: "person@example.test", Normalized: "person@example.test", Active: true},
		&database.ChannelSelection{ID: identity.NewID(), TenantID: tenantID, ParticipantID: participantID, ContactID: contactID},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("insert %T: %v", row, err)
		}
	}
	var recorder notificationRecorder
	service := Service{DB: db, Notifications: &recorder, CaptureBaseURL: "https://capture.example.test", Now: func() time.Time { return now }, Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}}
	result, err := service.Resend(context.Background(), tenantID, inspectionID, identity.NewID(), "admin:resend-1", "resend-correlation")
	if err != nil {
		t.Fatal(err)
	}
	if result.InspectionID != inspectionID || result.DeliveryStatus != string(notificationcore.StateQueued) || len(recorder.requests) != 1 {
		t.Fatalf("result=%+v requests=%d", result, len(recorder.requests))
	}
	request := recorder.requests[0]
	if request.Channel != notificationcore.ChannelEmail || request.Recipient.Destination != "person@example.test" || request.Execution == nil || request.Execution.InvitationID != result.InvitationID {
		t.Fatalf("unexpected notification request: %+v", request)
	}
	var invitation database.Invitation
	if err := db.First(&invitation, "id=?", result.InvitationID).Error; err != nil || invitation.Status != "ACTIVE" {
		t.Fatalf("invitation=%+v err=%v", invitation, err)
	}
	service.Now = func() time.Time { return now.Add(48 * time.Hour) }
	_, err = service.Resend(context.Background(), tenantID, inspectionID, identity.NewID(), "admin:resend-expired", "resend-correlation")
	code, field, message := apperror.Public(err)
	if code != apperror.InvalidState || field != "inspectionId" || message != "O prazo da vistoria venceu. Não é possível reenviar o convite." || len(recorder.requests) != 1 {
		t.Fatalf("expired resend: code=%s field=%s message=%q requests=%d", code, field, message, len(recorder.requests))
	}
}
