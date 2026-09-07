package purge_data

import (
	"encoding/json"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestLockInspectionAndCheckLegalHoldBlocksActiveHold(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS inspections").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS retention").Error; err != nil {
		t.Fatal(err)
	}
	if err := createRetentionLockTables(db); err != nil {
		t.Fatal(err)
	}
	tenantID := identity.ID(uuid.New())
	inspectionID := identity.ID(uuid.New())
	inspection := database.Inspection{ID: inspectionID, TenantID: tenantID, BusinessUnitID: identity.ID(uuid.New()), AssetID: identity.ID(uuid.New()), ParticipantID: identity.ID(uuid.New()), TemplateID: identity.ID(uuid.New()), TemplateVersionID: identity.ID(uuid.New()), AnalysisProfileVersionID: identity.ID(uuid.New()), Source: "test", SourceKey: uuid.NewString(), Status: "COMPLETED", ReminderInstants: json.RawMessage("[]"), ContextSnapshot: json.RawMessage("{}")}
	if err := db.Create(&inspection).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.LegalHold{ID: identity.ID(uuid.New()), TenantID: tenantID, InspectionID: inspectionID, Reason: "audit", Active: true}).Error; err != nil {
		t.Fatal(err)
	}

	err = db.Transaction(func(tx *gorm.DB) error {
		return lockInspectionAndCheckLegalHold(tx, tenantID, inspectionID)
	})
	if err != gorm.ErrInvalidData {
		t.Fatalf("expected active legal hold to block purge, got %v", err)
	}
}

func TestLockInspectionAndCheckLegalHoldAllowsNoActiveHold(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS inspections").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS retention").Error; err != nil {
		t.Fatal(err)
	}
	if err := createRetentionLockTables(db); err != nil {
		t.Fatal(err)
	}
	tenantID := identity.ID(uuid.New())
	inspectionID := identity.ID(uuid.New())
	inspection := database.Inspection{ID: inspectionID, TenantID: tenantID, BusinessUnitID: identity.ID(uuid.New()), AssetID: identity.ID(uuid.New()), ParticipantID: identity.ID(uuid.New()), TemplateID: identity.ID(uuid.New()), TemplateVersionID: identity.ID(uuid.New()), AnalysisProfileVersionID: identity.ID(uuid.New()), Source: "test", SourceKey: uuid.NewString(), Status: "COMPLETED", ReminderInstants: json.RawMessage("[]"), ContextSnapshot: json.RawMessage("{}")}
	if err := db.Create(&inspection).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return lockInspectionAndCheckLegalHold(tx, tenantID, inspectionID)
	}); err != nil {
		t.Fatalf("released legal hold should not block purge: %v", err)
	}
}

func createRetentionLockTables(db *gorm.DB) error {
	if err := db.Exec(`CREATE TABLE inspections.inspections (
		id text PRIMARY KEY, tenant_id text NOT NULL, business_unit_id text NOT NULL,
		asset_id text NOT NULL, participant_id text NOT NULL, template_id text NOT NULL,
		template_version_id text NOT NULL, analysis_profile_version_id text NOT NULL,
		project_id text, stage_id text, source text NOT NULL, source_key text NOT NULL,
		source_reason text, state_reason text, status text NOT NULL,
		evidence_count integer NOT NULL DEFAULT 0, due_at datetime NOT NULL,
		deadline_at datetime NOT NULL, reminder_instants blob NOT NULL,
		context_snapshot blob NOT NULL, version integer NOT NULL DEFAULT 1,
		created_at datetime NOT NULL, updated_at datetime
	)`).Error; err != nil {
		return err
	}
	return db.Exec(`CREATE TABLE retention.legal_holds (
		id text PRIMARY KEY, tenant_id text NOT NULL, inspection_id text NOT NULL,
		reason text NOT NULL, active numeric NOT NULL DEFAULT 1,
		created_at datetime, released_at datetime
	)`).Error
}
