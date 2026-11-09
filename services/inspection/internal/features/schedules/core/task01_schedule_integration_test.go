//go:build integration

package core

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	createoccurrence "inspection/services/inspection/internal/features/inspections/create_occurrence"
	originresolve "inspection/services/inspection/internal/features/origins/resolve_reference"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	"inspection/services/inspection/internal/features/templates/catalog"
	templateresolve "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/gorm"
)

func TestTask01IT018CreateScheduleRequiresApprovedPhotoBaselinePostgres(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })

	fixture, bus := newFixedOriginFixture(t)
	fixture.inventory = false
	service := Service{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}
	_, err = service.Create(principalContext(fixture.input.TenantID, auth.TenantAdmin), fixture.input)
	if !apperror.Is(err, apperror.InvalidState) || !strings.Contains(err.Error(), "catalogada e aprovada") {
		t.Fatalf("IT-018 schedule creation error=%v, want Portuguese baseline-required error", err)
	}
	if err := h.WithinTenant(ctx, fixture.input.TenantID, func(tx *gorm.DB) error {
		var count int64
		if err := tx.Table("schedules.schedules").Where("tenant_id=?", fixture.input.TenantID).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			t.Fatalf("IT-018 persisted %d schedules without an approved baseline", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTask01IT058MaterializationBlocksUnavailableBaselinesAndContinuesBatchPostgres(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	seed, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	dueAt := now.Add(-time.Hour)
	schedule := database.Schedule{ID: identity.NewID(), TenantID: seed.TenantID, BusinessUnitID: seed.BusinessUnitID, AssetID: seed.AssetID, ParticipantID: seed.ParticipantID, TemplateID: seed.TemplateID, TemplateVersionID: &seed.TemplateVersionID, ComparisonMode: "FIXED_ORIGIN", RRule: "FREQ=DAILY", Timezone: "UTC", StartsAt: dueAt, NextDueAt: dueAt, DeadlineMinutes: 60, ReminderOffsets: []byte("[]"), Status: "ACTIVE", Version: 1, IdempotencyKey: "task01-it058-" + identity.NewID().String(), CreatedAt: now, UpdatedAt: now}
	mediaOnlyVersionID, draftVersionID, originID := identity.NewID(), identity.NewID(), identity.NewID()
	draftVersionIDPtr := draftVersionID
	draftSchedule := schedule
	draftSchedule.ID = identity.NewID()
	draftSchedule.IdempotencyKey += "-draft"
	draftSchedule.ReferenceVersionID = &draftVersionIDPtr
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		origin := database.Origin{ID: originID, TenantID: seed.TenantID, AssetID: seed.AssetID, ActiveVersionID: &mediaOnlyVersionID, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&origin).Error; err != nil {
			return err
		}
		mediaOnly := database.OriginVersion{ID: mediaOnlyVersionID, TenantID: seed.TenantID, OriginID: originID, VersionNumber: 1, Status: "ACTIVE", MediaKind: "PHOTO", HasInventory: false, IdempotencyKey: "task01-it058-media-only"}
		if err := tx.Create(&mediaOnly).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.OriginEvidence{ID: identity.NewID(), TenantID: seed.TenantID, OriginVersionID: mediaOnlyVersionID, MediaID: seed.MediaID, Category: "ROOM", Description: "Referência fotográfica sem inventário", AttentionItems: []byte("[]"), CreatedAt: now}).Error; err != nil {
			return err
		}
		draft := database.OriginVersion{ID: draftVersionID, TenantID: seed.TenantID, OriginID: originID, VersionNumber: 2, Status: "DRAFT", MediaKind: "PHOTO", HasInventory: true, IdempotencyKey: "task01-it058-draft"}
		if err := tx.Create(&draft).Error; err != nil {
			return err
		}
		if err := tx.Create(&schedule).Error; err != nil {
			return err
		}
		return tx.Create(&draftSchedule).Error
	}); err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	definition, _ := json.Marshal(catalog.TemplateDocument{SchemaVersion: 2, CaptureLayout: catalog.EnvironmentsLayout, DefaultComparisonMode: catalog.ChecklistOnly, AnalysisType: "REAL_ESTATE", ParticipantRoles: []string{"TENANT_PARTICIPANT"}})
	registerQuery := func(query any, handler func(context.Context, any) (any, error)) error {
		return bus.RegisterQuery(query, handler)
	}
	if err := registerQuery(assetget.Query{}, func(context.Context, any) (any, error) {
		return assetcore.View{Asset: database.Asset{ID: seed.AssetID, TenantID: seed.TenantID, BusinessUnitID: seed.BusinessUnitID, Status: "ACTIVE", Version: 1}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := registerQuery(participantget.Query{}, func(context.Context, any) (any, error) {
		return participantcore.ParticipantView{Participant: database.Participant{ID: seed.ParticipantID, TenantID: seed.TenantID, BusinessUnitID: seed.BusinessUnitID, Status: "ACTIVE"}, Selected: []identity.ID{identity.NewID()}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := registerQuery(templateresolve.Query{}, func(context.Context, any) (any, error) {
		return templateresolve.Result{Template: database.Template{ID: seed.TemplateID, TenantID: seed.TenantID}, Version: database.TemplateVersion{ID: seed.TemplateVersionID, TenantID: seed.TenantID, TemplateID: seed.TemplateID, DefinitionJSON: definition}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := originresolve.Setup(originresolve.Dependencies{DB: h.DB, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	if err := createoccurrence.Setup(createoccurrence.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	service := Service{DB: h.DB, Bus: bus, Now: func() time.Time { return now }}
	materialized, err := service.MaterializeDue(ctx, seed.TenantID, now, 10)
	if err != nil || len(materialized) != 0 {
		t.Fatalf("IT-058 materialized=%d err=%v, want blocked with no inspection", len(materialized), err)
	}
	principal := principalContext(seed.TenantID, auth.TenantAdmin)
	listed, err := service.List(principal, seed.TenantID, 25, "")
	if err != nil {
		t.Fatal(err)
	}
	var visible *database.Schedule
	for i := range listed.Schedules {
		if listed.Schedules[i].ID == schedule.ID {
			visible = &listed.Schedules[i]
			break
		}
	}
	blocked := 0
	for i := range listed.Schedules {
		row := &listed.Schedules[i]
		if (row.ID == schedule.ID || row.ID == draftSchedule.ID) && row.BlockedReason != nil && *row.BlockedReason == blockedOriginUnavailable && row.BlockedAt != nil {
			blocked++
		}
	}
	if visible == nil || visible.BlockedReason == nil || *visible.BlockedReason != blockedOriginUnavailable || visible.BlockedAt == nil || blocked != 2 {
		t.Fatalf("IT-058 manager-visible blocked schedules=%d first=%+v, want both ORIGIN_UNAVAILABLE", blocked, visible)
	}
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND source_key IN ?", seed.TenantID, []string{schedule.ID.String() + "/" + dueAt.Format(time.RFC3339Nano), draftSchedule.ID.String() + "/" + dueAt.Format(time.RFC3339Nano)}).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			t.Fatalf("IT-058 created %d inspections for the unavailable baseline", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
