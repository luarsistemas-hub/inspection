package record_component_answer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSetupValidatesDependencies(t *testing.T) {
	if err := Setup(Dependencies{}); err == nil {
		t.Fatal("missing dependencies accepted")
	}
	if err := Setup(Dependencies{Bus: mediator.New()}); err == nil {
		t.Fatal("missing database accepted")
	}
	if err := Setup(Dependencies{Bus: mediator.New(), Service: core.Service{DB: &gorm.DB{}}}); err != nil {
		t.Fatalf("valid dependencies rejected: %v", err)
	}
}

func TestCommandRecordsAnswerThroughMediator(t *testing.T) {
	db, tenant, responsibility := fixture(t)
	bus := register(t, db)
	result, err := bus.Send(context.Background(), Command{TenantID: tenant, ResponsibilityID: responsibility, RequirementKey: "env:kitchen", ComponentKey: "floor", ViewKey: "geral", State: "NOT_OBSERVED"})
	answer, ok := result.(database.ComponentAnswer)
	if err != nil || !ok || answer.Version != 1 || answer.State != "NOT_OBSERVED" {
		t.Fatalf("record answer = %#v, %v", result, err)
	}
}

func TestCommandRejectsInvalidAnswerWithPortugueseMessage(t *testing.T) {
	db, tenant, responsibility := fixture(t)
	_, err := register(t, db).Send(context.Background(), Command{TenantID: tenant, ResponsibilityID: responsibility, RequirementKey: "env:kitchen", ComponentKey: "floor", State: "NOT_APPLICABLE"})
	if !apperror.Is(err, apperror.InvalidInput) || !strings.Contains(err.Error(), "motivo") {
		t.Fatalf("invalid answer error = %v", err)
	}
}

func TestCommandPropagatesDatabaseFailure(t *testing.T) {
	db, tenant, responsibility := fixture(t)
	bus := register(t, db)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Send(context.Background(), Command{TenantID: tenant, ResponsibilityID: responsibility, RequirementKey: "env:kitchen", ComponentKey: "floor", State: "NOT_OBSERVED"}); err == nil {
		t.Fatal("closed database accepted answer")
	}
}

func register(t *testing.T, db *gorm.DB) *mediator.Bus {
	t.Helper()
	bus := mediator.New()
	service := core.Service{DB: db, Now: time.Now, Within: func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(db) }}
	if err := Setup(Dependencies{Bus: bus, Service: service}); err != nil {
		t.Fatal(err)
	}
	return bus
}

func fixture(t *testing.T) (*gorm.DB, identity.ID, identity.ID) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"capture", "media", "inspections", "audit"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		"CREATE TABLE capture.capture_drafts (id text, tenant_id text, responsibility_id text, kind text, media_kind text, template_version_id text, reference_payload text, policy_payload text, requirements text, status text, version integer, created_at datetime, updated_at datetime)",
		"CREATE TABLE capture.component_answers (id text, tenant_id text, draft_id text, requirement_key text, component_key text, view_key text, state text, reason text, evidence_ids text, version integer, actor_id text, created_at datetime, updated_at datetime)",
		"CREATE TABLE inspections.responsibilities (id text, tenant_id text, inspection_id text, participant_id text, status text, version integer, created_at datetime, updated_at datetime)",
		"CREATE TABLE inspections.scope_snapshots (id text, tenant_id text, inspection_id text, template_version_id text, profile text, document text, digest text, version integer, confirmed_by text, created_at datetime, updated_at datetime)",
		"CREATE TABLE audit.events (id text, tenant_id text, actor_id text, action text, target_type text, target_id text, outcome text, reason text, correlation_id text, occurred_at datetime)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenant, responsibility, inspection, participant := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	if err := db.Create(&database.Responsibility{ID: responsibility, TenantID: tenant, InspectionID: inspection, ParticipantID: participant, Status: "ACTIVE", Version: 1, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", MediaKind: "PHOTO", TemplateVersionID: identity.NewID(), ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[]`), Status: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.InspectionScopeSnapshot{ID: identity.NewID(), TenantID: tenant, InspectionID: inspection, TemplateVersionID: identity.NewID(), Profile: "PROPERTY_COMPONENTS", Document: json.RawMessage(`{"rooms":[{"key":"kitchen","name":"Cozinha","typeCode":"KITCHEN","components":[{"key":"floor","label":"Piso","views":["geral"]}]}],"handoverRecords":[]}`), Digest: "scope", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	return db, tenant, responsibility
}
