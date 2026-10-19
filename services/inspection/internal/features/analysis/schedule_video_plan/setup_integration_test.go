//go:build integration

package schedule_video_plan

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/features/analysis/prepare_video_comparisons"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

func TestPreparedVideoSchedulesOneSealedPlanIT559IT560IT561IT563IT564IT565IT566IT568(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	mediaID, submissionID, preparationID, frameID, componentID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	inspectionID, responsibilityID, draftID := identity.NewID(), identity.NewID(), identity.NewID()
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.Inspection{ID: inspectionID, TenantID: fixture.TenantID, BusinessUnitID: fixture.BusinessUnitID, AssetID: fixture.AssetID, ParticipantID: fixture.ParticipantID, TemplateID: fixture.TemplateID, TemplateVersionID: fixture.TemplateVersionID, AnalysisPromptSnapshotID: fixture.PromptSnapshotID, ProjectID: &fixture.ProjectID, StageID: &fixture.StageID, Source: "MANUAL", SourceKey: "it559-" + inspectionID.String(), Status: "SUBMITTED", MediaKind: "VIDEO", DueAt: now, DeadlineAt: now.Add(time.Hour), ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{}`), Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Responsibility{ID: responsibilityID, TenantID: fixture.TenantID, InspectionID: inspectionID, ParticipantID: fixture.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.CaptureDraft{ID: draftID, TenantID: fixture.TenantID, ResponsibilityID: responsibilityID, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: fixture.TemplateVersionID, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[]`), Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ReferenceSnapshot{ID: identity.NewID(), TenantID: fixture.TenantID, InspectionID: inspectionID, ComparisonMode: "CHECKLIST_ONLY", Payload: json.RawMessage(`{"comparisonMode":"CHECKLIST_ONLY"}`), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: responsibilityID, ObjectKey: "it559/" + mediaID.String(), ContentType: "video/mp4", SHA256: digest, SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "it559-media-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"mediaKind": "VIDEO", "mediaId": mediaID})
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: fixture.TenantID, DraftID: draftID, ResponsibilityID: responsibilityID, VersionNumber: 1, Complete: true, Payload: payload, PayloadDigest: digest, SubmittedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, Attempts: 1, NextAttemptAt: now, ManifestDigest: digest, PendingObjectKeys: []byte("[]"), FirstReadyAt: &now, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: digest, ObjectKey: "it559/frame.jpg", Width: 1, Height: 1, Role: "BASELINE", SizeBytes: 1, CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoScopeItem{ID: componentID, TenantID: fixture.TenantID, PreparationID: preparationID, FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	scheduler, err := Setup(Dependencies{DB: h.DB, ClaimDB: h.AdminDB, Frames: prepare_video_comparisons.DBFrameReader{DB: h.DB}, Matcher: integrationMatcher{}, Verifier: integrationMatcher{}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	consumer := messaging.Consumer{DB: h.AdminDB, Registry: events.DefaultRegistry(), Name: "inspection.video.it559", Handle: scheduler.Handle}
	makeEvent := func(eventType string, aggregate identity.ID, payload any) []byte {
		body, marshalErr := json.Marshal(events.Envelope[any]{ID: identity.NewID(), Type: eventType, SchemaVersion: 1, OccurredAt: now, TenantID: fixture.TenantID, AggregateID: aggregate, CorrelationID: "it559-" + eventType, Payload: payload})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return body
	}
	t.Run("IT563_prepared_event_waits_for_authoritative_READY_state", func(t *testing.T) {
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", fixture.TenantID, preparationID).Update("status", "PROCESSING").Error; err != nil {
				return err
			}
			payload, _ := json.Marshal(events.VideoPreparedPayload{MediaID: mediaID, PreparationID: preparationID, ManifestDigest: digest})
			err := scheduler.Handle(ctx, tx, events.RawEnvelope{Type: "media.video_prepared.v1", TenantID: fixture.TenantID, Payload: payload})
			if !errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, messaging.ErrPermanent) {
				t.Errorf("non-READY event should be deferred: %v", err)
			}
			var count int64
			if countErr := tx.Model(&database.VideoPlan{}).Where("tenant_id=? AND submission_id=?", fixture.TenantID, submissionID).Count(&count).Error; countErr != nil {
				return countErr
			}
			if count != 0 {
				t.Errorf("non-READY event created %d plans", count)
			}
			return tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", fixture.TenantID, preparationID).Update("status", "READY").Error
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("IT560_digest_mismatch_is_terminal_without_downstream_mutation", func(t *testing.T) {
		payload, _ := json.Marshal(events.VideoPreparedPayload{MediaID: mediaID, PreparationID: preparationID, ManifestDigest: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"})
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			err := scheduler.Handle(ctx, tx, events.RawEnvelope{Type: "media.video_prepared.v1", TenantID: fixture.TenantID, Payload: payload})
			if !errors.Is(err, messaging.ErrPermanent) {
				t.Errorf("digest mismatch should be terminal: %v", err)
			}
			var count int64
			if countErr := tx.Model(&database.VideoPlan{}).Where("tenant_id=? AND submission_id=?", fixture.TenantID, submissionID).Count(&count).Error; countErr != nil {
				return countErr
			}
			if count != 0 {
				t.Errorf("digest mismatch created %d plans", count)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("IT565_missing_submission_identity_is_terminal_without_downstream_mutation", func(t *testing.T) {
		payload := json.RawMessage(`{"draftId":"` + draftID.String() + `","responsibilityId":"` + responsibilityID.String() + `","submissionId":"00000000-0000-0000-0000-000000000000","mediaId":"` + mediaID.String() + `","preparationStatus":"PENDING_VALIDATION"}`)
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			err := scheduler.Handle(ctx, tx, events.RawEnvelope{Type: "capture.video_submitted.v1", TenantID: fixture.TenantID, Payload: payload})
			if !errors.Is(err, messaging.ErrPermanent) {
				t.Errorf("missing submission identity should be terminal: %v", err)
			}
			var count int64
			if countErr := tx.Model(&database.VideoPlan{}).Where("tenant_id=? AND inspection_id=?", fixture.TenantID, inspectionID).Count(&count).Error; countErr != nil {
				return countErr
			}
			if count != 0 {
				t.Errorf("invalid submission created %d plans", count)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("IT568_submission_before_READY_is_deferred_without_sealing", func(t *testing.T) {
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", fixture.TenantID, preparationID).Update("status", "PROCESSING").Error; err != nil {
				return err
			}
			payload, _ := json.Marshal(map[string]any{"draftId": draftID, "responsibilityId": responsibilityID, "submissionId": submissionID, "mediaId": mediaID, "preparationStatus": "PENDING_VALIDATION"})
			if err := scheduler.Handle(ctx, tx, events.RawEnvelope{Type: "capture.video_submitted.v1", TenantID: fixture.TenantID, Payload: payload}); err != nil {
				return err
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if worked, err := scheduler.RunDue(ctx); err != nil || !worked {
			t.Fatalf("pending plan step = %t, %v", worked, err)
		}
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			var plan database.VideoPlan
			if err := tx.Where("tenant_id=? AND submission_id=?", fixture.TenantID, submissionID).Take(&plan).Error; err != nil {
				return err
			}
			if plan.Status != "BUILDING" || plan.PlanDigest != "" {
				t.Errorf("pending prerequisite was treated as ready: status=%s digest=%q", plan.Status, plan.PlanDigest)
			}
			if err := tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", fixture.TenantID, preparationID).Update("status", "READY").Error; err != nil {
				return err
			}
			return tx.Model(&database.VideoPlan{}).Where("tenant_id=? AND submission_id=?", fixture.TenantID, submissionID).Update("next_attempt_at", now).Error
		}); err != nil {
			t.Fatal(err)
		}
	})
	prepared := events.VideoPreparedPayload{MediaID: mediaID, PreparationID: preparationID, ManifestDigest: digest}
	preparedEvent := makeEvent("media.video_prepared.v1", mediaID, prepared)
	if processed, err := consumer.Process(ctx, preparedEvent, 0); err != nil || !processed {
		t.Fatalf("consume prepared event before submission event: processed=%t err=%v", processed, err)
	}
	submissionEvent := makeEvent("capture.video_submitted.v1", responsibilityID, map[string]any{"draftId": draftID, "responsibilityId": responsibilityID, "submissionId": submissionID, "mediaId": mediaID, "preparationStatus": "PENDING_VALIDATION"})
	if processed, err := consumer.Process(ctx, submissionEvent, 0); err != nil || !processed {
		t.Fatalf("consume submission: processed=%t err=%v", processed, err)
	}
	if processed, err := consumer.Process(ctx, submissionEvent, 0); err != nil || processed {
		t.Fatalf("duplicate submission changed result: processed=%t err=%v", processed, err)
	}
	if processed, err := consumer.Process(ctx, preparedEvent, 0); err != nil || processed {
		t.Fatalf("duplicate prepared event changed result: processed=%t err=%v", processed, err)
	}
	if worked, err := scheduler.RunDue(ctx); err != nil || !worked {
		t.Fatalf("RunDue = %t, %v", worked, err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var plans, jobs, requested, mainPreparations int64
		if err := tx.Model(&database.VideoPlan{}).Where("tenant_id=? AND submission_id=? AND status='SEALED' AND plan_digest<>''", fixture.TenantID, submissionID).Count(&plans).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.ComparisonJob{}).Where("tenant_id=? AND inspection_id=? AND video_plan_id IS NOT NULL", fixture.TenantID, inspectionID).Count(&jobs).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='analysis.video_comparison_requested.v1'", fixture.TenantID).Count(&requested).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoPlanPreparation{}).Where("tenant_id=? AND plan_id IN (SELECT id FROM analysis.video_plans WHERE tenant_id=? AND submission_id=?) AND role='MAIN'", fixture.TenantID, fixture.TenantID, submissionID).Count(&mainPreparations).Error; err != nil {
			return err
		}
		if plans != 1 || jobs != 1 || requested != 1 || mainPreparations != 1 {
			var plan database.VideoPlan
			_ = tx.Where("tenant_id=? AND submission_id=?", fixture.TenantID, submissionID).Take(&plan).Error
			t.Errorf("expected one sealed plan, job, MAIN preparation and MSG04 outbox event; got plans=%d jobs=%d preparations=%d requested=%d status=%s error=%s attempts=%d next=%s ref=%v origin=%v current=%s", plans, jobs, mainPreparations, requested, plan.Status, plan.ErrorCode, plan.Attempts, plan.NextAttemptAt, plan.ReferenceVersionID, plan.OriginPreparationID, plan.CurrentPreparationID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var sealedPlan database.VideoPlan
	if err := h.AdminDB.Where("tenant_id=? AND submission_id=? AND status='SEALED'", fixture.TenantID, submissionID).Take(&sealedPlan).Error; err != nil {
		t.Fatal(err)
	}
	err = h.AdminDB.Create(&database.VideoPlanPreparation{TenantID: identity.NewID(), PlanID: sealedPlan.ID, PreparationID: preparationID, Role: "MAIN", CreatedAt: now}).Error
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "foreign key constraint") {
		t.Fatalf("cross-tenant plan preparation should fail its composite foreign key, got %v", err)
	}
}

type integrationMatcher struct {
	componentByFrame map[identity.ID]identity.ID
}

func (m integrationMatcher) Propose(_ context.Context, origin, current []prepare_video_comparisons.Frame) ([]prepare_video_comparisons.Pair, error) {
	return m.sameViewPairs(origin, current), nil
}
func (m integrationMatcher) Verify(_ context.Context, origin, current []prepare_video_comparisons.Frame, _ []prepare_video_comparisons.Pair) ([]prepare_video_comparisons.Pair, error) {
	return m.sameViewPairs(origin, current), nil
}
