package correct_responsible_email

import (
	"context"
	"encoding/json"
	"errors"
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
	err      error
}

func (r *notificationRecorder) Send(_ context.Context, request notificationcore.Notification) (notificationcore.NotificationResult, error) {
	if r.err != nil {
		return notificationcore.NotificationResult{}, r.err
	}
	r.requests = append(r.requests, request)
	return notificationcore.NotificationResult{ID: identity.NewID(), State: notificationcore.StateQueued}, nil
}

func correctionFixture(t *testing.T) (*gorm.DB, Service, Input, *notificationRecorder) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"inspections", "participants", "assets", "invitations", "notifications", "audit"} {
		if err := db.Exec(`ATTACH DATABASE ':memory:' AS ` + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.Inspection{}, &database.Responsibility{}, &database.Participant{}, &database.ParticipantContact{}, &database.ContactVerification{}, &database.ChannelSelection{}, &database.Asset{}, &database.Invitation{}, &database.ExternalSession{}, &database.Delivery{}, &database.ChannelAttempt{}, &database.AuditEvent{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatalf("migrate %T: %v", model, err)
		}
	}
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	tenantID, inspectionID, responsibilityID, participantID, assetID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	for _, row := range []any{
		&database.Inspection{ID: inspectionID, TenantID: tenantID, AssetID: assetID, ParticipantID: participantID, Status: "PLANNED", DeadlineAt: now.Add(24 * time.Hour), ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{}`), Version: 1},
		&database.Responsibility{ID: responsibilityID, TenantID: tenantID, InspectionID: inspectionID, ParticipantID: participantID, Status: "PENDING", Version: 1},
		&database.Participant{ID: participantID, TenantID: tenantID, Name: "Responsável", Version: 1},
		&database.Asset{ID: assetID, TenantID: tenantID, Name: "Imóvel", Address: "Rua 1", PolicyOverrides: json.RawMessage(`{}`)},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("insert %T: %v", row, err)
		}
	}
	recorder := &notificationRecorder{}
	service := Service{DB: db, Notifications: recorder, CaptureBaseURL: "https://capture.example.test", Now: func() time.Time { return now }, Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}}
	input := Input{TenantID: tenantID, InspectionID: inspectionID, ResponsibilityID: responsibilityID, Email: "new@example.test", EmailConfirmation: "new@example.test", ExpectedResponsibilityVersion: 1, IdempotencyKey: "onboarding:mutation-1", Source: "ONBOARDING"}
	return db, service, input, recorder
}

func TestCorrectCreatesFirstInvitationWhenInitialDispatchWasMissed(t *testing.T) {
	db, service, input, recorder := correctionFixture(t)
	result, err := service.Correct(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.DeliveryStatus != string(notificationcore.StateQueued) || result.ResponsibilityVersion != 2 || result.Recipient != input.Email || len(recorder.requests) != 1 {
		t.Fatalf("correction result=%+v requests=%d", result, len(recorder.requests))
	}
	var invitation database.Invitation
	if err := db.First(&invitation, "id=?", result.InvitationID).Error; err != nil {
		t.Fatal(err)
	}
	if invitation.Status != "ACTIVE" || invitation.ResponsibilityID != input.ResponsibilityID {
		t.Fatalf("invitation=%+v", invitation)
	}
	var inspection database.Inspection
	if err := db.First(&inspection, "id=?", input.InspectionID).Error; err != nil {
		t.Fatal(err)
	}
	if inspection.Status != "INVITED" {
		t.Fatalf("inspection status=%s", inspection.Status)
	}
	var selected int64
	if err := db.Model(&database.ChannelSelection{}).Count(&selected).Error; err != nil || selected != 1 {
		t.Fatalf("selected contacts=%d error=%v", selected, err)
	}
	if recorder.requests[0].Execution == nil || recorder.requests[0].Execution.InvitationID != result.InvitationID {
		t.Fatal("capture token was not queued")
	}

	if err := db.Create(&database.Delivery{ID: *result.DeliveryID, TenantID: input.TenantID, InvitationID: &result.InvitationID, Status: string(notificationcore.StateQueued)}).Error; err != nil {
		t.Fatal(err)
	}
	input.ExpectedResponsibilityVersion = 1 // A retry keeps the original request version.
	replayed, err := service.Correct(context.Background(), input)
	if err != nil || replayed.InvitationID != result.InvitationID || replayed.Recipient != input.Email || len(recorder.requests) != 1 {
		t.Fatalf("replay=%+v error=%v requests=%d", replayed, err, len(recorder.requests))
	}
	input.Email, input.EmailConfirmation = "other@example.test", "other@example.test"
	if _, err := service.Correct(context.Background(), input); err == nil {
		t.Fatal("reused mutation ID accepted a different recipient")
	} else if code, _, _ := apperror.Public(err); code != apperror.Conflict {
		t.Fatalf("error=%v", err)
	}
}

func TestCorrectReplacesExistingInvitation(t *testing.T) {
	db, service, input, _ := correctionFixture(t)
	oldID := identity.NewID()
	intents, _ := json.Marshal([]invitationDelivery{{Channel: "EMAIL", Destination: "old@example.test"}})
	if err := db.Create(&database.Invitation{ID: oldID, TenantID: input.TenantID, ResponsibilityID: input.ResponsibilityID, TokenHash: []byte("old"), DeliveryIntents: intents, Status: "ACTIVE", ExpiresAt: service.Now().Add(time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.Correct(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	var old database.Invitation
	if err := db.First(&old, "id=?", oldID).Error; err != nil {
		t.Fatal(err)
	}
	if old.Status != "REVOKED" || old.RevokedAt == nil {
		t.Fatalf("old invitation=%+v", old)
	}
}

func TestCorrectRollsBackWhenNotificationRequestFails(t *testing.T) {
	db, service, input, recorder := correctionFixture(t)
	recorder.err = errors.New("notification unavailable")
	if _, err := service.Correct(context.Background(), input); !errors.Is(err, recorder.err) {
		t.Fatalf("error=%v", err)
	}
	var count int64
	if err := db.Model(&database.Invitation{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("invitations=%d error=%v", count, err)
	}
	var responsibility database.Responsibility
	if err := db.First(&responsibility, "id=?", input.ResponsibilityID).Error; err != nil {
		t.Fatal(err)
	}
	if responsibility.Version != 1 {
		t.Fatalf("responsibility version=%d", responsibility.Version)
	}
}
