package manage_environment

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
	db, _, _ := fixture(t)
	if err := Setup(Dependencies{}); err == nil {
		t.Fatal("Setup accepted missing dependencies")
	}
	if err := Setup(Dependencies{Bus: mediator.New()}); err == nil {
		t.Fatal("Setup accepted a service without a database")
	}
	if err := Setup(Dependencies{Bus: mediator.New(), Service: core.Service{DB: db}}); err != nil {
		t.Fatalf("Setup rejected valid dependencies: %v", err)
	}
}

func TestCommandRenamesAndRemovesAddedEnvironment(t *testing.T) {
	db, tenant, responsibility := fixture(t)
	bus := register(t, db)
	renamed, err := bus.Send(context.Background(), Command{TenantID: tenant, ResponsibilityID: responsibility, RequirementKey: "env:new:added", Name: "  Quarto   azul "})
	if err != nil || renamed.(core.Requirement).EnvironmentName != "Quarto azul" {
		t.Fatalf("rename = %+v, %v", renamed, err)
	}
	removed, err := bus.Send(context.Background(), Command{TenantID: tenant, ResponsibilityID: responsibility, RequirementKey: "env:new:added", Remove: true})
	if err != nil || !removed.(core.Requirement).Deleted {
		t.Fatalf("remove = %+v, %v", removed, err)
	}
	if _, err := bus.Send(context.Background(), Command{TenantID: tenant, ResponsibilityID: responsibility, RequirementKey: "env:new:added", Name: "Quarto verde"}); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("rename of a removed environment = %v, want InvalidState", err)
	}
}

func TestCommandRejectsTemplateEnvironment(t *testing.T) {
	db, tenant, responsibility := fixture(t)
	bus := register(t, db)
	if _, err := bus.Send(context.Background(), Command{TenantID: tenant, ResponsibilityID: responsibility, RequirementKey: "env:expected", Name: "Sala"}); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("template environment change = %v, want InvalidInput", err)
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
	if _, err := bus.Send(context.Background(), Command{TenantID: tenant, ResponsibilityID: responsibility, RequirementKey: "env:new:added", Name: "Quarto"}); err == nil {
		t.Fatal("closed database accepted the command")
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
	for _, schema := range []string{"capture", "media", "messaging"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.CaptureDraft{}, &database.RequirementAnswer{}, &database.MediaObject{}, &database.OutboxIntent{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	requirements, err := json.Marshal([]core.Requirement{
		{Key: "env:expected", Label: "Sala", EnvironmentTypeCode: "LIVING_ROOM", EnvironmentName: "Sala", Expected: true},
		{Key: "env:new:added", Label: "Quarto", EnvironmentTypeCode: "BEDROOM", EnvironmentName: "Quarto", AddedDuringCapture: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	tenant, responsibility := identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: identity.NewID(), ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: requirements, Status: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	return db, tenant, responsibility
}
