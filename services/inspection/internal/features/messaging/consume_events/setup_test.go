package consume_events

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSetupRejectsMissingDependencies(t *testing.T) {
	if _, err := Setup(Dependencies{}); err == nil {
		t.Fatal("missing dependencies accepted")
	}
}

func TestLifecycleOutcomesIT106IT127IT129IT551ToIT554(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`ATTACH DATABASE ':memory:' AS messaging`,
		`ATTACH DATABASE ':memory:' AS schedules`,
		`ATTACH DATABASE ':memory:' AS invitations`,
		`CREATE TABLE messaging.outbox (id blob primary key, tenant_id blob not null, type text not null, schema_version integer not null, payload blob not null, correlation_id text not null, causation_id text, status text not null, attempts integer not null default 0, next_attempt_at datetime not null, claimed_at datetime, last_error text, published_at datetime, created_at datetime)`,
		`CREATE TABLE schedules.reminder_plans (id blob primary key, tenant_id blob not null, inspection_id blob not null, remind_at datetime not null, status text not null, created_at datetime)`,
		`CREATE TABLE invitations.external_sessions (id blob primary key, tenant_id blob not null, invitation_id blob not null, responsibility_id blob not null, session_digest blob not null, csrf_digest blob not null, expires_at datetime not null, revoked_at datetime, created_at datetime)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, inspectionID, responsibilityID, participantID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	created := lifecycleEnvelope(t, tenantID, inspectionID, "inspection.created.v1", map[string]any{"inspectionId": inspectionID, "responsibilityId": responsibilityID, "participantId": participantID})
	if err := HandleLifecycleEvent(context.Background(), db, created); err != nil {
		t.Fatal(err)
	}
	if err := HandleLifecycleEvent(context.Background(), db, created); err != nil {
		t.Fatal(err)
	}
	var invitations int64
	if err := db.Model(&database.OutboxIntent{}).Where("type='origin.invitation_requested.v1'").Count(&invitations).Error; err != nil || invitations != 1 {
		t.Fatalf("invitation outcome count=%d err=%v", invitations, err)
	}

	now := time.Now().UTC()
	plan := database.ReminderPlan{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, RemindAt: now.Add(time.Hour), Status: "PLANNED", CreatedAt: now}
	session := database.ExternalSession{ID: identity.NewID(), TenantID: tenantID, InvitationID: identity.NewID(), ResponsibilityID: responsibilityID, SessionDigest: []byte("session"), CSRFDigest: []byte("csrf"), ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	changed := lifecycleEnvelope(t, tenantID, inspectionID, "inspection.state_changed.v1", map[string]any{"inspectionId": inspectionID, "responsibilityId": responsibilityID, "to": "CANCELED", "revokeSessions": true})
	if err := HandleLifecycleEvent(context.Background(), db, changed); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&plan, "id=?", plan.ID).Error; err != nil || plan.Status != "CANCELED" {
		t.Fatalf("future reminder was not canceled: %+v %v", plan, err)
	}
	if err := db.First(&session, "id=?", session.ID).Error; err != nil || session.RevokedAt == nil {
		t.Fatalf("external session was not revoked: %+v %v", session, err)
	}
}

func lifecycleEnvelope(t *testing.T, tenantID, aggregateID identity.ID, eventType string, payload any) events.RawEnvelope {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return events.RawEnvelope{ID: identity.NewID(), Type: eventType, SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: tenantID, AggregateID: aggregateID, CorrelationID: "lifecycle", Payload: raw}
}
