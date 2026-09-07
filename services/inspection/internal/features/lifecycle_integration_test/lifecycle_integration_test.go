package lifecycle_integration_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	inspectioncore "inspection/services/inspection/internal/features/inspections/core"
	inspectionoccurrence "inspection/services/inspection/internal/features/inspections/create_occurrence"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	projectcore "inspection/services/inspection/internal/features/projects/core"
	schedulecore "inspection/services/inspection/internal/features/schedules/core"
	"inspection/services/inspection/internal/features/templates/catalog"
	templateresolve "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestLifecycleIT101ToIT140IT201ToIT220IT380ToIT382(t *testing.T) {
	adminDSN, runtimeDSN := os.Getenv("INSPECTION_TEST_DATABASE_URL"), os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL")
	if adminDSN == "" || runtimeDSN == "" {
		t.Skip("lifecycle PostgreSQL integration requires INSPECTION_TEST_DATABASE_URL and INSPECTION_TEST_RUNTIME_DATABASE_URL")
	}
	admin, err := gorm.Open(postgres.Open(adminDSN), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := (database.Migrator{DB: admin}).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	runtimeDB, err := gorm.Open(postgres.Open(runtimeDSN), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}

	fixture := seedLifecycle(t, admin)
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fixture.tenantID, Principal: requestctx.Principal{IdentityID: fixture.identityID, MembershipID: fixture.membershipID, TenantID: fixture.tenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "lifecycle-integration", StartedAt: fixture.now})
	bus := mediator.New()
	authorizer := auth.Authorizer{Store: auth.GORMMembershipStore{DB: runtimeDB}}
	for _, setup := range []func() error{
		func() error {
			return assetget.Setup(assetget.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return participantget.Setup(participantget.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		},
		func() error { return templateresolve.Setup(templateresolve.Dependencies{DB: runtimeDB, Bus: bus}) },
		func() error {
			return inspectionoccurrence.Setup(inspectionoccurrence.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		},
	} {
		if err := setup(); err != nil {
			t.Fatal(err)
		}
	}

	inspections := inspectioncore.Service{DB: runtimeDB, Bus: bus, Authorizer: authorizer, Now: func() time.Time { return fixture.now }}
	schedules := schedulecore.Service{DB: runtimeDB, Bus: bus, Authorizer: authorizer, Now: func() time.Time { return fixture.now }}
	projects := projectcore.Service{DB: runtimeDB, Bus: bus, Authorizer: authorizer, Now: func() time.Time { return fixture.now }}

	if _, err := schedules.Create(ctx, schedulecore.Input{TenantID: fixture.tenantID, AssetID: fixture.assetID, ParticipantID: fixture.participantID, TemplateID: fixture.templateID, RRule: "FREQ=HOURLY", Timezone: "UTC", StartsAt: fixture.now, DeadlineMinutes: 60, IdempotencyKey: "invalid"}); errorCode(err) != apperror.InvalidInput {
		t.Fatalf("sub-daily schedule accepted: %v", err)
	}
	scheduleInput := schedulecore.Input{TenantID: fixture.tenantID, AssetID: fixture.assetID, ParticipantID: fixture.participantID, TemplateID: fixture.templateID, RRule: "FREQ=DAILY", Timezone: "America/Sao_Paulo", StartsAt: fixture.now, DeadlineMinutes: 1440, ReminderOffsetsMinutes: []int{60, 120, 180}, IdempotencyKey: "schedule"}
	schedule, err := schedules.Create(ctx, scheduleInput)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := schedules.Create(ctx, scheduleInput)
	if err != nil || replayed.ID != schedule.ID {
		t.Fatalf("schedule replay failed: %v", err)
	}
	materialized, err := schedules.MaterializeDue(ctx, fixture.tenantID, fixture.now, 100)
	if err != nil || len(materialized) != 1 {
		t.Fatalf("materialization failed: %v %#v", err, materialized)
	}
	second, err := schedules.MaterializeDue(ctx, fixture.tenantID, fixture.now, 100)
	if err != nil || len(second) != 0 {
		t.Fatalf("due instant repeated: %v %#v", err, second)
	}
	var occurrenceCount, eventCount, reminderCount int64
	if err := admin.Model(&database.OccurrenceMaterialization{}).Where("tenant_id=? AND schedule_id=?", fixture.tenantID, schedule.ID).Count(&occurrenceCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='inspection.created.v1'", fixture.tenantID).Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Model(&database.ReminderPlan{}).Where("tenant_id=?", fixture.tenantID).Count(&reminderCount).Error; err != nil {
		t.Fatal(err)
	}
	if occurrenceCount != 1 || eventCount != 1 || reminderCount != 3 {
		t.Fatalf("idempotency snapshot mismatch: occurrences=%d events=%d reminders=%d", occurrenceCount, eventCount, reminderCount)
	}

	manualInput := inspectioncore.CreateInput{TenantID: fixture.tenantID, AssetID: fixture.assetID, ParticipantID: fixture.participantID, TemplateID: &fixture.templateID, Source: inspectioncore.SourceManual, SourceKey: "manual", Reason: "requested inspection", DueAt: fixture.now, DeadlineAt: fixture.now.Add(time.Hour), ReminderInstants: []time.Time{fixture.now.Add(30 * time.Minute)}}
	manual, err := inspections.Create(ctx, manualInput)
	if err != nil {
		t.Fatal(err)
	}
	manualReplay, err := inspections.Create(ctx, manualInput)
	if err != nil || manualReplay.Inspection.ID != manual.Inspection.ID {
		t.Fatalf("manual replay failed: %v", err)
	}
	canceled, err := inspections.Cancel(ctx, fixture.tenantID, manual.Inspection.ID, 1)
	if err != nil || canceled.Inspection.Status != "CANCELED" || canceled.Responsibility.Status != "CANCELED" {
		t.Fatalf("cancellation failed: %v", err)
	}
	if _, err := inspections.Invalidate(ctx, fixture.tenantID, materialized[0].Inspection.Inspection.ID, 1, "reason"); errorCode(err) != apperror.InvalidState {
		t.Fatalf("invalidation without evidence accepted: %v", err)
	}

	project, err := projects.Create(ctx, projectcore.CreateInput{TenantID: fixture.tenantID, AssetID: fixture.assetID, ParticipantID: fixture.participantID, TemplateID: &fixture.templateID, IdempotencyKey: "project"})
	if err != nil {
		t.Fatal(err)
	}
	if project.Project.ReportMode != "CONSOLIDATED" || len(project.Stages) != 2 || project.Stages[0].Kind != "ORIGIN" {
		t.Fatalf("project snapshot mismatch: %#v", project)
	}
	project, err = projects.SkipStage(ctx, projectcore.TransitionInput{TenantID: fixture.tenantID, ProjectID: project.Project.ID, StageID: &project.Stages[0].ID, ExpectedVersion: project.Project.Version, Reason: "not required"})
	if err != nil {
		t.Fatal(err)
	}
	project, err = projects.SkipStage(ctx, projectcore.TransitionInput{TenantID: fixture.tenantID, ProjectID: project.Project.ID, StageID: &project.Stages[1].ID, ExpectedVersion: project.Project.Version, Reason: "not required"})
	if err != nil {
		t.Fatal(err)
	}
	project, err = projects.Close(ctx, projectcore.TransitionInput{TenantID: fixture.tenantID, ProjectID: project.Project.ID, ExpectedVersion: project.Project.Version})
	if err != nil || project.Project.Status != projectcore.ProjectClosed {
		t.Fatalf("project close failed: %v", err)
	}
	project, err = projects.Reopen(ctx, projectcore.TransitionInput{TenantID: fixture.tenantID, ProjectID: project.Project.ID, ExpectedVersion: project.Project.Version, Reason: "new work"})
	if err != nil || project.Project.Status != projectcore.ProjectActive {
		t.Fatalf("project reopen failed: %v", err)
	}
}

type lifecycleFixture struct {
	tenantID, unitID, identityID, membershipID, participantID, templateID, assetID identity.ID
	now                                                                            time.Time
}

func seedLifecycle(t *testing.T, db *gorm.DB) lifecycleFixture {
	t.Helper()
	f := lifecycleFixture{tenantID: identity.NewID(), unitID: identity.NewID(), identityID: identity.NewID(), membershipID: identity.NewID(), participantID: identity.NewID(), templateID: identity.NewID(), assetID: identity.NewID(), now: time.Now().UTC().Truncate(time.Second)}
	contactID, templateVersionID, segmentVersionID, profileVersionID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	document := catalog.TemplateDocument{SchemaVersion: 1, SegmentVersionID: segmentVersionID.String(), ParticipantRoles: []string{"RESPONSIBLE"}, ComparisonMode: catalog.ChecklistOnly, Requirements: []catalog.CaptureRequirement{{Key: "overview", Section: "general", Label: "Overview", EvidenceKind: "PHOTO", MinimumCount: 1, MaximumCount: 2, Required: true, DescriptionRequired: true, CaptureSourcePolicy: "CAMERA_DEFAULT", ComparisonTarget: catalog.ChecklistOnly}}, MultiStage: true, Stages: []catalog.Stage{{Key: "origin", Label: "Origin", Position: 1}, {Key: "inspection", Label: "Inspection", Position: 2}}, ReportMode: "CONSOLIDATED", AnalysisProfile: profileVersionID.String(), Policy: catalog.Policy{GPSRequired: true, GeofenceMeters: 150, AllowGallery: true}}
	definition, _ := json.Marshal(document)
	rows := []any{
		&database.Tenant{ID: f.tenantID, TenantID: f.tenantID, Name: "Lifecycle", Language: "pt-BR", DefaultTimezone: "America/Sao_Paulo", Status: "ACTIVE", Version: 1, CreatedAt: f.now, UpdatedAt: f.now},
		&database.BusinessUnit{ID: f.unitID, TenantID: f.tenantID, Code: "main-" + f.tenantID.String(), Name: "Main", Status: "ACTIVE", Version: 1, CreatedAt: f.now, UpdatedAt: f.now},
		&database.Membership{ID: f.membershipID, TenantID: f.tenantID, IdentityID: f.identityID, Issuer: "test", Subject: f.identityID.String(), Role: auth.TenantAdmin, Status: "ACTIVE", Version: 1, CreatedAt: f.now, UpdatedAt: f.now},
		&database.Participant{ID: f.participantID, TenantID: f.tenantID, BusinessUnitID: f.unitID, Name: "Responsible", SegmentRole: "RESPONSIBLE", Status: "ACTIVE", Version: 1, IdempotencyKey: "participant-" + f.tenantID.String(), CreatedAt: f.now, UpdatedAt: f.now},
		&database.ParticipantContact{ID: contactID, TenantID: f.tenantID, ParticipantID: f.participantID, Channel: "EMAIL", Value: "lifecycle@example.com", Normalized: "lifecycle@example.com", Active: true, CreatedAt: f.now, UpdatedAt: f.now},
		&database.ContactVerification{ID: identity.NewID(), TenantID: f.tenantID, ContactID: contactID, IdempotencyKey: "verify-" + f.tenantID.String(), Status: "VERIFIED", VerifiedAt: &f.now, CreatedAt: f.now},
		&database.ChannelSelection{ID: identity.NewID(), TenantID: f.tenantID, ParticipantID: f.participantID, ContactID: contactID, CreatedAt: f.now},
		&database.Template{ID: f.templateID, TenantID: f.tenantID, Key: "lifecycle-" + f.tenantID.String(), Name: "Lifecycle", SegmentVersionID: segmentVersionID, ActiveVersionID: &templateVersionID, Version: 1, CreatedAt: f.now, UpdatedAt: f.now},
		&database.TemplateVersion{ID: templateVersionID, TenantID: f.tenantID, TemplateID: f.templateID, VersionNumber: 1, SchemaVersion: 1, DefinitionJSON: definition, CanonicalDigest: "digest", Status: "ACTIVE", IdempotencyKey: "template-" + f.tenantID.String(), PublishedAt: f.now, CreatedBy: f.identityID},
		&database.Asset{ID: f.assetID, TenantID: f.tenantID, BusinessUnitID: f.unitID, SegmentVersionID: segmentVersionID, TemplateID: &f.templateID, Name: "Asset", ExternalKey: "asset-" + f.tenantID.String(), Address: "Street", GeofenceMeters: 150, PolicyOverrides: json.RawMessage(`{}`), Status: "ACTIVE", Version: 1, IdempotencyKey: "asset-" + f.tenantID.String(), CreatedAt: f.now, UpdatedAt: f.now},
		&database.AssetAttributeVersion{ID: identity.NewID(), TenantID: f.tenantID, AssetID: f.assetID, SegmentVersionID: segmentVersionID, VersionNumber: 1, AttributesJSON: json.RawMessage(`{}`), CanonicalDigest: "digest", CreatedAt: f.now},
		&database.AssetAssignment{ID: identity.NewID(), TenantID: f.tenantID, AssetID: f.assetID, ParticipantID: f.participantID, Role: "RESPONSIBLE", Active: true, CreatedAt: f.now},
	}
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func errorCode(err error) apperror.Code {
	if err == nil {
		return ""
	}
	code, _, _ := apperror.Public(err)
	return code
}
