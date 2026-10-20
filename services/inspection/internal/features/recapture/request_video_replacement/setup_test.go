package request_video_replacement

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type noNotifications struct {
	notificationcore.NotificationService
}

type fixture struct {
	bus        *mediator.Bus
	tenantID   identity.ID
	inspection database.Inspection
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS inspections").Error; err != nil {
		t.Fatal(err)
	}
	_ = db.AutoMigrate(&database.Inspection{})
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	tenantID := identity.NewID()
	inspection := database.Inspection{ID: identity.NewID(), TenantID: tenantID, BusinessUnitID: identity.NewID(), AssetID: identity.NewID(), ParticipantID: identity.NewID(), TemplateID: identity.NewID(), TemplateVersionID: identity.NewID(), AnalysisPromptSnapshotID: identity.NewID(), Source: "MANUAL", SourceKey: "k", Status: "REVIEW_REQUIRED", MediaKind: "VIDEO", DueAt: now, DeadlineAt: now, ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{}`), Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&inspection).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	// A participant without verified channels proves the request reached the domain after authorization.
	if err := bus.RegisterQuery(participantget.Query{}, func(context.Context, any) (any, error) { return participantcore.ParticipantView{}, nil }); err != nil {
		t.Fatal(err)
	}
	err = Setup(Dependencies{
		DB: db, Bus: bus, Service: recapturecore.Service{DB: db, Now: func() time.Time { return now }}, Notifications: noNotifications{}, CaptureBaseURL: "https://capture.example.test",
		Within: func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(db) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{bus: bus, tenantID: tenantID, inspection: inspection}
}

func (f fixture) send(ctx context.Context) error {
	input := recapturecore.VideoReplacementInput{TenantID: f.tenantID, InspectionID: f.inspection.ID, MediaID: identity.NewID(), DeadlineAt: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), Reason: "regravar", IdempotencyKey: "m1"}
	_, err := f.bus.Send(ctx, Command{Input: input})
	return err
}

func (f fixture) principal(internal bool, tenant identity.ID, roles ...string) context.Context {
	principal := requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenant, Internal: internal, Roles: roles, Scopes: []requestctx.Scope{{Kind: "INSPECTION", ID: f.inspection.ID}}}
	return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenant, Principal: principal})
}

func TestSetupRejectsMissingDependencies(t *testing.T) {
	if err := Setup(Dependencies{}); err == nil {
		t.Fatal("setup accepted missing dependencies")
	}
}

func TestRequestVideoReplacementAuthorization(t *testing.T) {
	f := newFixture(t)
	other := identity.NewID()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want apperror.Code
	}{
		{"no principal", context.Background(), apperror.Forbidden},
		{"external participant", f.principal(false, f.tenantID, auth.Manager), apperror.Forbidden},
		{"employee", f.principal(true, f.tenantID, auth.Employee), apperror.Forbidden},
		{"customer viewer", f.principal(true, f.tenantID, auth.CustomerViewer), apperror.Forbidden},
		{"manager from another tenant", f.principal(true, other, auth.Manager), apperror.NotFound},
		{"manager", f.principal(true, f.tenantID, auth.Manager), apperror.InvalidState},
		{"tenant admin", f.principal(true, f.tenantID, auth.TenantAdmin), apperror.InvalidState},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := f.send(tc.ctx)
			if code, _, _ := apperror.Public(err); code != tc.want {
				t.Fatalf("want %s, got %v", tc.want, err)
			}
		})
	}
}
