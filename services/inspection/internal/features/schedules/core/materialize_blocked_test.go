package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	inspectioncore "inspection/services/inspection/internal/features/inspections/core"
	createoccurrence "inspection/services/inspection/internal/features/inspections/create_occurrence"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMaterializeBlockedOriginContinuesBatchAndRetriesAfterBackoff(t *testing.T) {
	db := scheduleTestDB(t)
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	tenantID := identity.NewID()
	blockedID, succeedingID := identity.NewID(), identity.NewID()
	dueAt := now.Add(-time.Hour)
	initialBlockedAt := now.Add(-time.Hour)
	blockedReason := "ORIGIN_UNAVAILABLE"
	for _, row := range []database.Schedule{
		{ID: blockedID, TenantID: tenantID, NextDueAt: dueAt, DeadlineMinutes: 60, ReminderOffsets: json.RawMessage("[]"), Status: "ACTIVE", BlockedReason: &blockedReason, BlockedAt: &initialBlockedAt, UpdatedAt: now.Add(-time.Hour), RRule: "FREQ=DAILY", Timezone: "UTC", StartsAt: dueAt},
		{ID: succeedingID, TenantID: tenantID, NextDueAt: dueAt, DeadlineMinutes: 60, ReminderOffsets: json.RawMessage("[]"), Status: "ACTIVE", BlockedReason: &blockedReason, BlockedAt: &initialBlockedAt, UpdatedAt: now.Add(-time.Hour), RRule: "FREQ=DAILY", Timezone: "UTC", StartsAt: dueAt},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}

	bus := mediator.New()
	if err := bus.RegisterCommand(createoccurrence.Command{}, func(_ context.Context, raw any) (any, error) {
		command := raw.(createoccurrence.Command)
		if strings.HasPrefix(command.SourceKey, blockedID.String()) {
			return nil, apperror.New(apperror.InvalidState, "referenceVersionId", "active origin is unavailable")
		}
		return inspectioncore.View{Inspection: database.Inspection{ID: identity.NewID()}, EventID: identity.NewID()}, nil
	}); err != nil {
		t.Fatal(err)
	}
	within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
	service := Service{DB: db, Bus: bus, Within: within, Now: func() time.Time { return now }}
	materialized, err := service.MaterializeDue(context.Background(), tenantID, now, 10)
	if err != nil {
		t.Fatalf("MaterializeDue: %v", err)
	}
	if len(materialized) != 1 || materialized[0].Schedule.ID != succeedingID {
		t.Fatalf("materialized schedules = %v; want only succeeding schedule %s", materialized, succeedingID)
	}

	var blocked database.Schedule
	if err := db.Where("tenant_id=? AND id=?", tenantID, blockedID).Take(&blocked).Error; err != nil {
		t.Fatal(err)
	}
	if blocked.BlockedReason == nil || *blocked.BlockedReason != blockedOriginUnavailable || blocked.BlockedAt == nil || !blocked.BlockedAt.Equal(initialBlockedAt) || !blocked.NextDueAt.Equal(dueAt) {
		t.Fatalf("blocked schedule state = reason %v, blockedAt %v, nextDueAt %s", blocked.BlockedReason, blocked.BlockedAt, blocked.NextDueAt)
	}
	if !blocked.UpdatedAt.Equal(now) {
		t.Fatalf("retry timestamp = %s; want %s", blocked.UpdatedAt, now)
	}
	var cleared database.Schedule
	if err := db.Where("tenant_id=? AND id=?", tenantID, succeedingID).Take(&cleared).Error; err != nil {
		t.Fatal(err)
	}
	if cleared.BlockedReason != nil || cleared.BlockedAt != nil || !cleared.NextDueAt.After(dueAt) {
		t.Fatalf("successful schedule state = reason %v, blockedAt %v, nextDueAt %s", cleared.BlockedReason, cleared.BlockedAt, cleared.NextDueAt)
	}

	service.Now = func() time.Time { return now.Add(time.Minute) }
	materialized, err = service.MaterializeDue(context.Background(), tenantID, now.Add(time.Minute), 10)
	if err != nil {
		t.Fatalf("MaterializeDue during backoff: %v", err)
	}
	if len(materialized) != 0 {
		t.Fatalf("materialized %d schedules during retry backoff; want none", len(materialized))
	}
}

func TestMaterializeDoesNotBlockForUnrelatedErrors(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
	}{{name: "database not found", err: gorm.ErrRecordNotFound}, {name: "participant invalid state", err: apperror.New(apperror.InvalidState, "participantId", "participant unavailable")}} {
		t.Run(testCase.name, func(t *testing.T) {
			if unavailableOrigin(testCase.err) {
				t.Fatal("unrelated error was classified as unavailable origin")
			}
		})
	}
}

func TestMaterializeBlocksPinnedReferenceNoLongerActive(t *testing.T) {
	if !unavailableOrigin(apperror.New(apperror.Conflict, "referenceVersionId", "origin reference is no longer active")) {
		t.Fatal("superseded pinned reference was not classified as unavailable origin")
	}
}

func scheduleTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, statement := range []string{
		`ATTACH DATABASE ':memory:' AS schedules`,
		`CREATE TABLE schedules.schedules (id text PRIMARY KEY, tenant_id text, business_unit_id text, asset_id text, participant_id text, template_id text, template_version_id text, comparison_mode text, project_id text, stage_id text, reference_version_id text, r_rule text, timezone text, starts_at datetime, next_due_at datetime, deadline_minutes integer, reminder_offsets blob, status text, blocked_reason text, blocked_at datetime, version integer, idempotency_key text, created_at datetime, updated_at datetime)`,
		`CREATE TABLE schedules.occurrence_materializations (id text PRIMARY KEY, tenant_id text, schedule_id text, due_instant datetime, inspection_id text, event_id text, created_at datetime)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}
