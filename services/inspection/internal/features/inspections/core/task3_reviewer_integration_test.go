//go:build integration

package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	dispatchinvitation "inspection/services/inspection/internal/features/invitations/dispatch_capture_invitation"
	consumeevents "inspection/services/inspection/internal/features/messaging/consume_events"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

type task3NotificationStub struct{}

func (task3NotificationStub) Send(context.Context, notificationcore.Notification) (notificationcore.NotificationResult, error) {
	return notificationcore.NotificationResult{ID: identity.NewID(), State: notificationcore.StateQueued}, nil
}

func TestUT130UT131UT132BusinessUnitReviewerMembershipCreateAndDispatch(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	t.Run("UT-130 EMPLOYEE scoped to asset business unit is persisted by dispatch", func(t *testing.T) {
		fx := newTask3CreateFixture(t, h, "PHOTO", 2, false)
		reviewerID := createTask3Reviewer(t, h, fx.seed.TenantID, fx.seed.IdentityID, "EMPLOYEE", "BUSINESS_UNIT", fx.seed.BusinessUnitID)
		view, err := fx.service.Create(ctx, CreateInput{TenantID: fx.seed.TenantID, AssetID: fx.seed.AssetID, ParticipantID: fx.seed.ParticipantID, TemplateID: &fx.version.Template.ID, TemplateVersionID: &fx.templateVersionID, ComparisonMode: catalog.ChecklistOnly, ReviewerMembershipID: &reviewerID, Source: SourceScheduled, SourceKey: "ut130-bu-reviewer", DueAt: time.Now().UTC().Add(time.Hour), DeadlineAt: time.Now().UTC().Add(24 * time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		var outbox database.OutboxIntent
		if err := h.AdminDB.Where("id=? AND tenant_id=?", view.EventID, fx.seed.TenantID).Take(&outbox).Error; err != nil {
			t.Fatal(err)
		}
		handler, err := dispatchinvitation.Setup(dispatchinvitation.Dependencies{Notifications: task3NotificationStub{}, CaptureBaseURL: "https://capture.example.test"})
		if err != nil {
			t.Fatal(err)
		}
		var createdEvent events.RawEnvelope
		if err := json.Unmarshal(outbox.Payload, &createdEvent); err != nil {
			t.Fatal(err)
		}
		if err := consumeevents.HandleLifecycleEvent(ctx, h.AdminDB, createdEvent); err != nil {
			t.Fatalf("derive invitation request: %v", err)
		}
		var invitationRequest database.OutboxIntent
		if err := h.AdminDB.Where("tenant_id=? AND type='origin.invitation_requested.v1' AND causation_id=?", fx.seed.TenantID, view.EventID.String()).Take(&invitationRequest).Error; err != nil {
			t.Fatal(err)
		}
		consumer := messaging.Consumer{DB: h.AdminDB, Registry: events.DefaultRegistry(), Name: "task3-ut130-dispatch", Handle: handler}
		if processed, err := consumer.Process(ctx, invitationRequest.Payload, 0); err != nil || !processed {
			t.Fatalf("origin.invitation_requested.v1 dispatch processed=%t err=%v", processed, err)
		}
		var invitation database.Invitation
		if err := h.WithinTenant(ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND responsibility_id=?", fx.seed.TenantID, view.Responsibility.ID).Take(&invitation).Error
		}); err != nil {
			t.Fatal(err)
		}
		if invitation.ReviewerMembershipID == nil || *invitation.ReviewerMembershipID != reviewerID {
			t.Fatalf("invitation reviewer=%v; want %s", invitation.ReviewerMembershipID, reviewerID)
		}
	})

	for _, tc := range []struct {
		name, role string
		other      bool
		id         string
	}{
		{name: "UT-131 VIEWER rejected", role: "VIEWER", id: "ut131-viewer"},
		{name: "UT-132 business unit outside asset scope rejected", role: "EMPLOYEE", other: true, id: "ut132-outside-bu"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newTask3CreateFixture(t, h, "PHOTO", 2, false)
			buID := fx.seed.BusinessUnitID
			if tc.other {
				buID = identity.NewID()
			}
			reviewerID := createTask3Reviewer(t, h, fx.seed.TenantID, fx.seed.IdentityID, tc.role, "BUSINESS_UNIT", buID)
			_, err := fx.service.Create(ctx, CreateInput{TenantID: fx.seed.TenantID, AssetID: fx.seed.AssetID, ParticipantID: fx.seed.ParticipantID, TemplateID: &fx.version.Template.ID, TemplateVersionID: &fx.templateVersionID, ComparisonMode: catalog.ChecklistOnly, ReviewerMembershipID: &reviewerID, Source: SourceScheduled, SourceKey: tc.id, DueAt: time.Now().UTC().Add(time.Hour), DeadlineAt: time.Now().UTC().Add(24 * time.Hour)})
			var appErr *apperror.Error
			if !errors.As(err, &appErr) || appErr.Code != apperror.InvalidInput || appErr.Field != "reviewerMembershipId" {
				t.Fatalf("Create error=%v; want INVALID_INPUT reviewerMembershipId", err)
			}
		})
	}

	t.Run("PROJECT-scoped membership is rejected", func(t *testing.T) {
		fx := newTask3CreateFixture(t, h, "PHOTO", 2, false)
		reviewerID := createTask3Reviewer(t, h, fx.seed.TenantID, fx.seed.IdentityID, "EMPLOYEE", "PROJECT", fx.seed.ProjectID)
		projectID := fx.seed.ProjectID
		_, err := fx.service.Create(ctx, CreateInput{TenantID: fx.seed.TenantID, AssetID: fx.seed.AssetID, ParticipantID: fx.seed.ParticipantID, TemplateID: &fx.version.Template.ID, TemplateVersionID: &fx.templateVersionID, ComparisonMode: catalog.ChecklistOnly, ReviewerMembershipID: &reviewerID, ProjectID: &projectID, Source: SourceScheduled, SourceKey: "ut130-project-additional", DueAt: time.Now().UTC().Add(time.Hour), DeadlineAt: time.Now().UTC().Add(24 * time.Hour)})
		var appErr *apperror.Error
		if !errors.As(err, &appErr) || appErr.Code != apperror.InvalidInput || appErr.Field != "reviewerMembershipId" {
			t.Fatalf("PROJECT scoped reviewer error=%v; want INVALID_INPUT reviewerMembershipId", err)
		}
	})
}

func createTask3Reviewer(t *testing.T, h *harness.Harness, tenantID, identityID identity.ID, role, scopeKind string, scopeID identity.ID) identity.ID {
	t.Helper()
	membershipID := identity.NewID()
	now := time.Now().UTC()
	if err := h.WithinTenant(context.Background(), tenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.Membership{ID: membershipID, TenantID: tenantID, IdentityID: identityID, Issuer: "task3-reviewer", Subject: membershipID.String(), Role: role, Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.ResourceScope{ID: identity.NewID(), TenantID: tenantID, MembershipID: membershipID, Kind: scopeKind, ResourceID: scopeID, CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	return membershipID
}
