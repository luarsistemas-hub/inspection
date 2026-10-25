//go:build integration

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	correctemail "inspection/services/inspection/internal/features/invitations/correct_responsible_email"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

type task3ReviewerNotification struct{}

func (task3ReviewerNotification) Send(context.Context, notificationcore.Notification) (notificationcore.NotificationResult, error) {
	return notificationcore.NotificationResult{ID: identity.NewID(), State: notificationcore.StateQueued}, nil
}

func TestUT133ResendAndRecapturePreserveReviewerMembership(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	for _, previousStatus := range []string{"EXPIRED", "REVOKED"} {
		t.Run("previous invitation "+previousStatus, func(t *testing.T) {
			fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			reviewerID := identity.NewID()
			oldInviteID := identity.NewID()
			if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
				if err := tx.Model(&database.Responsibility{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.ResponsibilityID).Updates(map[string]any{"status": "PENDING", "version": 1}).Error; err != nil {
					return err
				}
				if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Updates(map[string]any{"status": "INVITED", "deadline_at": now.Add(48 * time.Hour)}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.Membership{ID: reviewerID, TenantID: fixture.TenantID, IdentityID: fixture.IdentityID, Issuer: "ut133", Subject: reviewerID.String(), Role: "EMPLOYEE", Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.ResourceScope{ID: identity.NewID(), TenantID: fixture.TenantID, MembershipID: reviewerID, Kind: "BUSINESS_UNIT", ResourceID: fixture.BusinessUnitID, CreatedAt: now}).Error; err != nil {
					return err
				}
				return tx.Create(&database.Invitation{ID: oldInviteID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ReviewerMembershipID: &reviewerID, TokenHash: []byte(fmt.Sprintf("%064x", oldInviteID)), DeliveryIntents: json.RawMessage(`[]`), Status: previousStatus, ExpiresAt: now.Add(-time.Hour), CreatedAt: now.Add(-2 * time.Hour)}).Error
			}); err != nil {
				t.Fatal(err)
			}
			correction := correctemail.Service{DB: h.DB, Notifications: task3ReviewerNotification{}, CaptureBaseURL: "https://capture.example.test", Within: h.WithinTenant}
			corrected, err := correction.Correct(ctx, correctemail.Input{TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, ResponsibilityID: fixture.ResponsibilityID, Email: "corrected@example.test", EmailConfirmation: "corrected@example.test", IdempotencyKey: "ut133-correct-" + previousStatus, Source: "INTEGRATION", ExpectedResponsibilityVersion: 1})
			if err != nil {
				t.Fatalf("correct email resend: %v", err)
			}
			var resent database.Invitation
			if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
				return tx.Where("tenant_id=? AND id=?", fixture.TenantID, corrected.InvitationID).Take(&resent).Error
			}); err != nil {
				t.Fatal(err)
			}
			if resent.ReviewerMembershipID == nil || *resent.ReviewerMembershipID != reviewerID {
				t.Fatalf("corrected invitation reviewer=%v, want %s", resent.ReviewerMembershipID, reviewerID)
			}

			// Continue the same sent inspection through selected-item recapture.
			if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
				if err := tx.Model(&database.Responsibility{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.ResponsibilityID).Update("status", "SUBMITTED").Error; err != nil {
					return err
				}
				if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Update("status", "SUBMITTED").Error; err != nil {
					return err
				}
				requirements, _ := json.Marshal([]capturecore.Requirement{{Key: "room-1", EvidenceKind: "PHOTO", Required: true, Expected: true, MinimumMedia: 1, MaximumMedia: 20}})
				return tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).Updates(map[string]any{"requirements": requirements, "media_kind": "PHOTO", "status": "SUBMITTED"}).Error
			}); err != nil {
				t.Fatal(err)
			}
			recapture := Service{DB: h.DB, Within: h.WithinTenant}
			requested, err := recapture.Request(ctx, RequestInput{TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, Items: []Item{{RequirementKey: "room-1", Reason: "Retake the room photo"}}, Delivery: []invitationcore.DeliveryIntent{{Channel: "EMAIL", Destination: "corrected@example.test"}}, DeadlineAt: now.Add(24 * time.Hour), IdempotencyKey: "ut133-recapture-" + previousStatus})
			if err != nil {
				t.Fatalf("request recapture: %v", err)
			}
			var recaptureInvite database.Invitation
			if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
				return tx.Where("tenant_id=? AND responsibility_id=?", fixture.TenantID, requested.ResponsibilityID).Take(&recaptureInvite).Error
			}); err != nil {
				t.Fatal(err)
			}
			if recaptureInvite.ReviewerMembershipID == nil || *recaptureInvite.ReviewerMembershipID != reviewerID {
				t.Fatalf("recapture invitation reviewer=%v, want %s", recaptureInvite.ReviewerMembershipID, reviewerID)
			}
		})
	}

	t.Run("video replacement inherits the reviewer", func(t *testing.T) {
		fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		mediaID, preparationID, submissionID := identity.NewID(), identity.NewID(), identity.NewID()
		reviewerID, inviteID := identity.NewID(), identity.NewID()
		videoReq, _ := json.Marshal([]capturecore.Requirement{{Key: "video:main", EvidenceKind: "VIDEO", Required: true, MinimumMedia: 1, MaximumMedia: 1}})
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Update("status", "REVIEW_REQUIRED").Error; err != nil {
				return err
			}
			if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fixture.TenantID, fixture.ResponsibilityID).Updates(map[string]any{"media_kind": "VIDEO", "requirements": videoReq}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.Membership{ID: reviewerID, TenantID: fixture.TenantID, IdentityID: fixture.IdentityID, Issuer: "ut133-video", Subject: reviewerID.String(), Role: "EMPLOYEE", Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.Invitation{ID: inviteID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ReviewerMembershipID: &reviewerID, TokenHash: []byte(fmt.Sprintf("%064x", inviteID)), DeliveryIntents: json.RawMessage(`[]`), Status: "REVOKED", ExpiresAt: now.Add(-time.Hour), CreatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: fixture.TenantID, DraftID: fixture.DraftID, ResponsibilityID: fixture.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: json.RawMessage(`{}`), PayloadDigest: "ut133-video", SubmittedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "ut133/video/" + mediaID.String(), ContentType: "video/mp4", SHA256: "ut133-video", SizeBytes: 100, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "ut133-video-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, ManifestDigest: "ut133-manifest", PendingObjectKeys: []byte(`[]`), CreatedAt: now}).Error; err != nil {
				return err
			}
			return tx.Create(&database.VideoPlan{ID: identity.NewID(), TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, SubmissionID: submissionID, CurrentPreparationID: preparationID, Generation: 1, PlanDigest: "ut133-plan", Status: "BLOCKED", NextAttemptAt: now, CreatedAt: now}).Error
		}); err != nil {
			t.Fatal(err)
		}
		result, err := (Service{DB: h.DB, Within: h.WithinTenant}).RequestVideoReplacement(ctx, VideoReplacementInput{TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, MediaID: mediaID, Reason: "Video needs replacement", IdempotencyKey: "ut133-video-replacement", DeadlineAt: now.Add(24 * time.Hour), Delivery: []invitationcore.DeliveryIntent{{Channel: "EMAIL", Destination: "task06@example.test"}}})
		if err != nil {
			t.Fatalf("request video replacement: %v", err)
		}
		var replacementInvite database.Invitation
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=?", fixture.TenantID, result.InvitationID).Take(&replacementInvite).Error
		}); err != nil {
			t.Fatal(err)
		}
		if replacementInvite.ReviewerMembershipID == nil || *replacementInvite.ReviewerMembershipID != reviewerID {
			t.Fatalf("replacement invitation reviewer=%v, want %s", replacementInvite.ReviewerMembershipID, reviewerID)
		}
	})
}
