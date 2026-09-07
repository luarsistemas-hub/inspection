package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	inspectioncore "inspection/services/inspection/internal/features/inspections/core"
	createoccurrence "inspection/services/inspection/internal/features/inspections/create_occurrence"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	"inspection/services/inspection/internal/features/templates/catalog"
	templateresolve "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Input struct {
	TenantID, AssetID, ParticipantID, TemplateID identity.ID
	ReferenceVersionID                           *identity.ID
	RRule, Timezone, IdempotencyKey              string
	StartsAt                                     time.Time
	DeadlineMinutes                              int
	ReminderOffsetsMinutes                       []int
}

type UpdateInput struct {
	TenantID, ScheduleID   identity.ID
	ExpectedVersion        int64
	RRule, Timezone        string
	StartsAt               time.Time
	DeadlineMinutes        int
	ReminderOffsetsMinutes []int
}

type ListResult struct {
	Schedules   []database.Schedule
	EndCursor   string
	HasNextPage bool
}

type Materialization struct {
	Schedule   database.Schedule
	Inspection inspectioncore.View
	DueInstant time.Time
	EventID    identity.ID
}

type ReminderDispatch struct {
	PlanID, EventID, InspectionID identity.ID
	RemindAt                      time.Time
}

type Service struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
	Now        func() time.Time
}

func (s Service) Create(ctx context.Context, in Input) (database.Schedule, error) {
	if in.TenantID == (identity.ID{}) || in.AssetID == (identity.ID{}) || in.ParticipantID == (identity.ID{}) || in.TemplateID == (identity.ID{}) || strings.TrimSpace(in.IdempotencyKey) == "" {
		return database.Schedule{}, apperror.New(apperror.InvalidInput, "input", "schedule prerequisites are required")
	}
	recurrence, err := ParseRecurrence(in.RRule, in.Timezone, in.StartsAt)
	if err != nil {
		return database.Schedule{}, apperror.New(apperror.InvalidInput, "rrule", err.Error())
	}
	if err := validateOffsets(in.DeadlineMinutes, in.ReminderOffsetsMinutes); err != nil {
		return database.Schedule{}, err
	}
	asset, template, err := s.prerequisites(ctx, in.TenantID, in.AssetID, in.ParticipantID, in.TemplateID)
	if err != nil {
		return database.Schedule{}, err
	}
	var document catalog.TemplateDocument
	if err := json.Unmarshal(template.Version.DefinitionJSON, &document); err != nil {
		return database.Schedule{}, fmt.Errorf("decode schedule template: %w", err)
	}
	if (document.ComparisonMode == catalog.FixedOrigin || document.ComparisonMode == catalog.PlannedStage || document.ComparisonMode == catalog.BeforeAfter) && in.ReferenceVersionID == nil {
		return database.Schedule{}, apperror.New(apperror.InvalidState, "referenceVersionId", "effective reference is required")
	}
	if _, err := s.Authorizer.Authorize(ctx, in.TenantID, []string{auth.TenantAdmin, auth.Manager}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: asset.BusinessUnitID}, true); err != nil {
		return database.Schedule{}, err
	}
	next, err := recurrence.Next(in.StartsAt.Add(-time.Nanosecond))
	if err != nil {
		return database.Schedule{}, apperror.New(apperror.InvalidInput, "rrule", err.Error())
	}
	offsets, _ := json.Marshal(in.ReminderOffsetsMinutes)
	now := s.now()
	row := database.Schedule{ID: identity.NewID(), TenantID: in.TenantID, BusinessUnitID: asset.BusinessUnitID, AssetID: in.AssetID, ParticipantID: in.ParticipantID, TemplateID: in.TemplateID, ReferenceVersionID: in.ReferenceVersionID, RRule: strings.TrimSpace(in.RRule), Timezone: strings.TrimSpace(in.Timezone), StartsAt: in.StartsAt.UTC(), NextDueAt: next.UTC(), DeadlineMinutes: in.DeadlineMinutes, ReminderOffsets: offsets, Status: "ACTIVE", Version: 1, IdempotencyKey: in.IdempotencyKey, CreatedAt: now, UpdatedAt: now}
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		var existing database.Schedule
		if err := tx.Where("tenant_id=? AND idempotency_key=?", in.TenantID, in.IdempotencyKey).First(&existing).Error; err == nil {
			row = existing
			return nil
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		return tx.Create(&row).Error
	})
	return row, err
}

func (s Service) Update(ctx context.Context, in UpdateInput) (database.Schedule, error) {
	var current database.Schedule
	if err := s.withSchedule(ctx, in.TenantID, in.ScheduleID, func(_ *gorm.DB, row *database.Schedule) error { current = *row; return nil }); err != nil {
		return database.Schedule{}, err
	}
	if current.Status == "CANCELED" {
		return database.Schedule{}, apperror.New(apperror.InvalidState, "scheduleId", "schedule is canceled")
	}
	if _, err := s.Authorizer.Authorize(ctx, in.TenantID, []string{auth.TenantAdmin, auth.Manager}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: current.BusinessUnitID}, true); err != nil {
		return database.Schedule{}, err
	}
	recurrence, err := ParseRecurrence(in.RRule, in.Timezone, in.StartsAt)
	if err != nil {
		return database.Schedule{}, apperror.New(apperror.InvalidInput, "rrule", err.Error())
	}
	if err := validateOffsets(in.DeadlineMinutes, in.ReminderOffsetsMinutes); err != nil {
		return database.Schedule{}, err
	}
	now := s.now()
	nextAfter := now
	if current.NextDueAt.After(now) {
		nextAfter = current.NextDueAt.Add(-time.Nanosecond)
	}
	next, err := recurrence.Next(nextAfter)
	if err != nil {
		return database.Schedule{}, apperror.New(apperror.InvalidInput, "rrule", err.Error())
	}
	offsets, _ := json.Marshal(in.ReminderOffsetsMinutes)
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		r := tx.Model(&database.Schedule{}).Where("tenant_id=? AND id=? AND version=? AND status='ACTIVE'", in.TenantID, in.ScheduleID, in.ExpectedVersion).Updates(map[string]any{"rrule": strings.TrimSpace(in.RRule), "timezone": strings.TrimSpace(in.Timezone), "starts_at": in.StartsAt.UTC(), "next_due_at": next.UTC(), "deadline_minutes": in.DeadlineMinutes, "reminder_offsets": offsets, "version": in.ExpectedVersion + 1, "updated_at": now})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "stale schedule version")
		}
		return tx.Where("tenant_id=? AND id=?", in.TenantID, in.ScheduleID).First(&current).Error
	})
	return current, err
}

func (s Service) Cancel(ctx context.Context, tenantID, scheduleID identity.ID, expectedVersion int64) (database.Schedule, error) {
	var current database.Schedule
	if err := s.withSchedule(ctx, tenantID, scheduleID, func(_ *gorm.DB, row *database.Schedule) error { current = *row; return nil }); err != nil {
		return database.Schedule{}, err
	}
	if current.Status == "CANCELED" {
		return current, nil
	}
	if _, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin, auth.Manager}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: current.BusinessUnitID}, true); err != nil {
		return database.Schedule{}, err
	}
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		r := tx.Model(&database.Schedule{}).Where("tenant_id=? AND id=? AND version=?", tenantID, scheduleID, expectedVersion).Updates(map[string]any{"status": "CANCELED", "version": expectedVersion + 1, "updated_at": s.now()})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "stale schedule version")
		}
		return tx.Where("tenant_id=? AND id=?", tenantID, scheduleID).First(&current).Error
	})
	return current, err
}

func (s Service) List(ctx context.Context, tenantID identity.ID, first int, after string) (ListResult, error) {
	if first <= 0 {
		first = 25
	}
	if first > 100 {
		return ListResult{}, apperror.New(apperror.InvalidInput, "first", "page size cannot exceed 100")
	}
	if _, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin, auth.Manager, auth.Employee, auth.Viewer}, nil, false); err != nil {
		return ListResult{}, err
	}
	var rows []database.Schedule
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		q := tx.Where("tenant_id=?", tenantID).Order("created_at ASC, id ASC").Limit(first + 1)
		if after != "" {
			q = q.Where("id::text > ?", after)
		}
		return q.Find(&rows).Error
	})
	result := ListResult{HasNextPage: len(rows) > first}
	if result.HasNextPage {
		rows = rows[:first]
	}
	result.Schedules = rows
	if len(rows) != 0 {
		result.EndCursor = rows[len(rows)-1].ID.String()
	}
	return result, err
}

func (s Service) MaterializeDue(ctx context.Context, tenantID identity.ID, now time.Time, limit int) ([]Materialization, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	var due []database.Schedule
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("tenant_id=? AND status='ACTIVE' AND next_due_at<=?", tenantID, now.UTC()).Order("next_due_at ASC").Limit(limit).Find(&due).Error
	})
	if err != nil {
		return nil, err
	}
	results := make([]Materialization, 0, len(due))
	for _, schedule := range due {
		dueAt := schedule.NextDueAt.UTC()
		var offsets []int
		if err := json.Unmarshal(schedule.ReminderOffsets, &offsets); err != nil {
			return nil, err
		}
		deadline := dueAt.Add(time.Duration(schedule.DeadlineMinutes) * time.Minute)
		reminders := make([]time.Time, 0, len(offsets))
		for _, offset := range offsets {
			reminders = append(reminders, dueAt.Add(time.Duration(offset)*time.Minute))
		}
		raw, err := s.Bus.Send(ctx, createoccurrence.Command{TenantID: tenantID, AssetID: schedule.AssetID, ParticipantID: schedule.ParticipantID, TemplateID: &schedule.TemplateID, ReferenceVersionID: schedule.ReferenceVersionID, Source: inspectioncore.SourceScheduled, SourceKey: schedule.ID.String() + "/" + dueAt.Format(time.RFC3339Nano), DueAt: dueAt, DeadlineAt: deadline, ReminderInstants: reminders})
		if err != nil {
			return results, err
		}
		inspection := raw.(inspectioncore.View)
		recurrence, err := ParseRecurrence(schedule.RRule, schedule.Timezone, schedule.StartsAt)
		if err != nil {
			return results, err
		}
		next, err := recurrence.Next(dueAt)
		if err != nil {
			return results, err
		}
		eventID := inspection.EventID
		err = (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
			row := database.OccurrenceMaterialization{ID: identity.NewID(), TenantID: tenantID, ScheduleID: schedule.ID, DueInstant: dueAt, InspectionID: inspection.Inspection.ID, EventID: eventID, CreatedAt: s.now()}
			if err := tx.Create(&row).Error; err != nil {
				var existing database.OccurrenceMaterialization
				if loadErr := tx.Where("tenant_id=? AND schedule_id=? AND due_instant=?", tenantID, schedule.ID, dueAt).First(&existing).Error; loadErr != nil {
					return err
				}
				eventID = existing.EventID
				return nil
			}
			for _, remindAt := range reminders {
				plan := database.ReminderPlan{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspection.Inspection.ID, RemindAt: remindAt, Status: "PLANNED", CreatedAt: s.now()}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&plan).Error; err != nil {
					return err
				}
			}
			return tx.Model(&database.Schedule{}).Where("tenant_id=? AND id=? AND next_due_at=?", tenantID, schedule.ID, dueAt).Updates(map[string]any{"next_due_at": next.UTC(), "version": gorm.Expr("version + 1"), "updated_at": s.now()}).Error
		})
		if err != nil {
			return results, err
		}
		results = append(results, Materialization{Schedule: schedule, Inspection: inspection, DueInstant: dueAt, EventID: eventID})
	}
	return results, nil
}

func (s Service) DispatchDueReminders(ctx context.Context, tenantID identity.ID, now time.Time, limit int) ([]ReminderDispatch, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	result := make([]ReminderDispatch, 0)
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		var plans []database.ReminderPlan
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("tenant_id=? AND status='PLANNED' AND remind_at<=?", tenantID, now.UTC()).Order("remind_at ASC, id ASC").Limit(limit).Find(&plans).Error; err != nil {
			return err
		}
		metadata, _ := requestctx.FromContext(ctx)
		for _, plan := range plans {
			eventID := uuid.NewSHA1(plan.ID, []byte("notification.delivery_requested.v1"))
			correlationID := metadata.CorrelationID
			if correlationID == "" {
				correlationID = eventID.String()
			}
			payload, _ := json.Marshal(map[string]any{"inspectionId": plan.InspectionID, "reminderPlanId": plan.ID, "scheduledAt": plan.RemindAt})
			envelope, _ := json.Marshal(map[string]any{"id": eventID, "type": "notification.delivery_requested.v1", "schemaVersion": 1, "occurredAt": now.UTC(), "tenantId": tenantID, "aggregateId": plan.InspectionID, "correlationId": correlationID, "payload": json.RawMessage(payload)})
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&database.OutboxIntent{ID: eventID, TenantID: tenantID, Type: "notification.delivery_requested.v1", SchemaVersion: 1, Payload: envelope, CorrelationID: correlationID, Status: "PENDING", NextAttemptAt: now.UTC(), CreatedAt: now.UTC()}).Error; err != nil {
				return err
			}
			if err := tx.Model(&database.ReminderPlan{}).Where("tenant_id=? AND id=? AND status='PLANNED'", tenantID, plan.ID).Update("status", "DISPATCHED").Error; err != nil {
				return err
			}
			result = append(result, ReminderDispatch{PlanID: plan.ID, EventID: eventID, InspectionID: plan.InspectionID, RemindAt: plan.RemindAt})
		}
		return nil
	})
	return result, err
}

func (s Service) prerequisites(ctx context.Context, tenantID, assetID, participantID, templateID identity.ID) (database.Asset, templateresolve.Result, error) {
	if s.Bus == nil {
		return database.Asset{}, templateresolve.Result{}, fmt.Errorf("schedule: missing mediator")
	}
	assetRaw, err := s.Bus.Ask(ctx, assetget.Query{TenantID: tenantID, AssetID: assetID})
	if err != nil {
		return database.Asset{}, templateresolve.Result{}, err
	}
	asset := assetRaw.(assetcore.View)
	if asset.Asset.Status != "ACTIVE" {
		return database.Asset{}, templateresolve.Result{}, apperror.New(apperror.InvalidState, "assetId", "asset is not active")
	}
	participantRaw, err := s.Bus.Ask(ctx, participantget.Query{TenantID: tenantID, ParticipantID: participantID})
	if err != nil {
		return database.Asset{}, templateresolve.Result{}, err
	}
	participant := participantRaw.(participantcore.ParticipantView)
	if participant.Participant.Status != "ACTIVE" || len(participant.Selected) == 0 {
		return database.Asset{}, templateresolve.Result{}, apperror.New(apperror.InvalidState, "participantId", "participant requires an active verified delivery channel")
	}
	assigned := false
	for _, assignment := range asset.Assignments {
		assigned = assigned || (assignment.Active && assignment.ParticipantID == participantID)
	}
	if !assigned {
		return database.Asset{}, templateresolve.Result{}, apperror.New(apperror.InvalidInput, "participantId", "participant is not assigned to the asset")
	}
	templateRaw, err := s.Bus.Ask(ctx, templateresolve.Query{TenantID: tenantID, TemplateID: templateID})
	if err != nil {
		return database.Asset{}, templateresolve.Result{}, err
	}
	return asset.Asset, templateRaw.(templateresolve.Result), nil
}

func (s Service) withSchedule(ctx context.Context, tenantID, scheduleID identity.ID, fn func(*gorm.DB, *database.Schedule) error) error {
	return (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		var row database.Schedule
		if err := tx.Where("tenant_id=? AND id=?", tenantID, scheduleID).First(&row).Error; err != nil {
			return apperror.New(apperror.NotFound, "scheduleId", "schedule not found")
		}
		return fn(tx, &row)
	})
}

func validateOffsets(deadlineMinutes int, offsets []int) error {
	if deadlineMinutes <= 0 {
		return apperror.New(apperror.InvalidInput, "deadlineMinutes", "deadline is required")
	}
	if len(offsets) > 3 {
		return apperror.New(apperror.InvalidInput, "reminderOffsetsMinutes", "at most three reminders are allowed")
	}
	seen := map[int]struct{}{}
	for _, offset := range offsets {
		if offset < 0 || offset > deadlineMinutes {
			return apperror.New(apperror.InvalidInput, "reminderOffsetsMinutes", "reminder must precede expiry")
		}
		if _, exists := seen[offset]; exists {
			return apperror.New(apperror.InvalidInput, "reminderOffsetsMinutes", "duplicate reminder")
		}
		seen[offset] = struct{}{}
	}
	return nil
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
