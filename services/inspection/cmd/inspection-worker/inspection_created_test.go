package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestInspectionCreatedQueuesCaptureInvitationAndInAppNotice(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`ATTACH DATABASE ':memory:' AS messaging`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.OutboxIntent{}); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
		t.Fatal(err)
	}
	tenantID, inspectionID, responsibilityID, participantID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	payload, _ := json.Marshal(map[string]any{"inspectionId": inspectionID, "responsibilityId": responsibilityID, "participantId": participantID})
	envelope := events.RawEnvelope{ID: identity.NewID(), Type: "inspection.created.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: tenantID, AggregateID: inspectionID, CorrelationID: "test", Payload: payload}
	called := 0
	handler := inspectionCreatedHandler(func(_ context.Context, _ *gorm.DB, _ events.RawEnvelope) error { called++; return nil })
	for range 2 {
		if err := db.Transaction(func(tx *gorm.DB) error { return handler(context.Background(), tx, envelope) }); err != nil {
			t.Fatal(err)
		}
	}
	var outbox []database.OutboxIntent
	if err := db.Where("type=?", "origin.invitation_requested.v1").Find(&outbox).Error; err != nil {
		t.Fatal(err)
	}
	if called != 2 || len(outbox) != 1 {
		t.Fatalf("in-app calls=%d invitation events=%d", called, len(outbox))
	}
	var forwarded events.RawEnvelope
	if err := json.Unmarshal(outbox[0].Payload, &forwarded); err != nil {
		t.Fatal(err)
	}
	var invitation struct {
		InspectionID     identity.ID `json:"inspectionId"`
		ResponsibilityID identity.ID `json:"responsibilityId"`
		ParticipantID    identity.ID `json:"participantId"`
	}
	if err := json.Unmarshal(forwarded.Payload, &invitation); err != nil {
		t.Fatal(err)
	}
	if invitation.InspectionID != inspectionID || invitation.ResponsibilityID != responsibilityID || invitation.ParticipantID != participantID {
		t.Fatalf("wrong invitation payload: %+v", invitation)
	}

	want := errors.New("in-app failed")
	failed := inspectionCreatedHandler(func(context.Context, *gorm.DB, events.RawEnvelope) error { return want })
	if err := db.Transaction(func(tx *gorm.DB) error {
		return failed(context.Background(), tx, events.RawEnvelope{ID: identity.NewID(), Type: envelope.Type, SchemaVersion: envelope.SchemaVersion, OccurredAt: envelope.OccurredAt, TenantID: tenantID, AggregateID: inspectionID, CorrelationID: envelope.CorrelationID, Payload: payload})
	}); !errors.Is(err, want) {
		t.Fatalf("error=%v, want %v", err, want)
	}
	var count int64
	if err := db.Model(&database.OutboxIntent{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("failed notice left invitation event: count=%d error=%v", count, err)
	}
}
