//go:build integration

package reanalyze_video_inspection

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/prepare_video_comparisons"
	"inspection/services/inspection/internal/features/analysis/schedule_video_plan"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestReanalysisSealsNextGenerationAndRejectsStaleExpectedPlanIT619(t *testing.T) {
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
	inspectionID, responsibilityID, draftID, mediaID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	submissionID, preparationID, frameID, componentID, planID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.Inspection{ID: inspectionID, TenantID: fixture.TenantID, BusinessUnitID: fixture.BusinessUnitID, AssetID: fixture.AssetID, ParticipantID: fixture.ParticipantID, TemplateID: fixture.TemplateID, TemplateVersionID: fixture.TemplateVersionID, AnalysisPromptSnapshotID: fixture.PromptSnapshotID, ProjectID: &fixture.ProjectID, StageID: &fixture.StageID, Source: "MANUAL", SourceKey: "it619-" + inspectionID.String(), Status: "SUBMITTED", MediaKind: "VIDEO", DueAt: now, DeadlineAt: now.Add(time.Hour), ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{}`), Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Responsibility{ID: responsibilityID, TenantID: fixture.TenantID, InspectionID: inspectionID, ParticipantID: fixture.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.CaptureDraft{ID: draftID, TenantID: fixture.TenantID, ResponsibilityID: responsibilityID, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: fixture.TemplateVersionID, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[]`), Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: responsibilityID, ObjectKey: "it619/" + mediaID.String(), ContentType: "video/mp4", SHA256: digest, SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "it619-media-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"mediaKind": "VIDEO", "mediaId": mediaID})
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: fixture.TenantID, DraftID: draftID, ResponsibilityID: responsibilityID, VersionNumber: 1, Complete: true, Payload: payload, PayloadDigest: digest, SubmittedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, Attempts: 1, NextAttemptAt: now, ManifestDigest: digest, PendingObjectKeys: []byte("[]"), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: digest, ObjectKey: "it619/frame.jpg", Width: 1, Height: 1, Role: "BASELINE", SizeBytes: 1, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoScopeItem{ID: componentID, TenantID: fixture.TenantID, PreparationID: preparationID, FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlan{ID: planID, TenantID: fixture.TenantID, InspectionID: inspectionID, SubmissionID: submissionID, CurrentPreparationID: preparationID, Generation: 1, PlanDigest: digest, Status: "SEALED", SealedAt: &now, CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.TenantID, inspectionID).Update("active_video_plan_id", planID).Error
	}); err != nil {
		t.Fatal(err)
	}

	bus := mediator.New()
	if err := Setup(Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	principal := requestctx.Principal{IdentityID: fixture.IdentityID, TenantID: fixture.TenantID, Roles: []string{auth.TenantAdmin}, Scopes: []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: fixture.BusinessUnitID}}}
	reanalysisCtx := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: fixture.TenantID, Principal: principal, CorrelationID: "it619-reanalysis"})
	command := Command{TenantID: fixture.TenantID, InspectionID: inspectionID, ExpectedPlanID: planID, ClientMutationID: "it619-reanalyze"}
	firstRaw, err := bus.Send(reanalysisCtx, command)
	if err != nil {
		t.Fatal(err)
	}
	first := firstRaw.(Result)
	if first.PlanID == planID || first.Generation != 2 {
		t.Fatalf("reanalyze result = %+v; want a new generation 2 plan", first)
	}
	replayRaw, err := bus.Send(reanalysisCtx, command)
	if err != nil {
		t.Fatal(err)
	}
	if replay := replayRaw.(Result); replay != first {
		t.Fatalf("replay = %+v; want original result %+v", replay, first)
	}
	var plans int64
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoPlan{}).Where("tenant_id=? AND inspection_id=? AND generation=2", fixture.TenantID, inspectionID).Count(&plans).Error
	}); err != nil {
		t.Fatal(err)
	}
	if plans != 1 {
		t.Fatalf("generation 2 plan count = %d; want one after replay", plans)
	}

	schedulerNow := time.Now().UTC().Add(time.Second)
	scheduler, err := schedule_video_plan.Setup(schedule_video_plan.Dependencies{DB: h.DB, ClaimDB: h.AdminDB, Frames: prepare_video_comparisons.DBFrameReader{DB: h.DB}, Matcher: reanalysisMatcher{}, Verifier: reanalysisMatcher{}, Now: func() time.Time { return schedulerNow }})
	if err != nil {
		t.Fatal(err)
	}
	worked, err := scheduler.RunDue(ctx)
	if err != nil || !worked {
		t.Fatalf("RunDue = %t, %v; want the queued generation to be sealed", worked, err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var generationTwo database.VideoPlan
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, first.PlanID).Take(&generationTwo).Error; err != nil {
			return err
		}
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, inspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		if generationTwo.Status != "SEALED" || generationTwo.PlanDigest == "" || generationTwo.Generation != 2 {
			t.Errorf("generation 2 plan = status %s, generation %d, digest %q", generationTwo.Status, generationTwo.Generation, generationTwo.PlanDigest)
		}
		if inspection.ActiveVideoPlanID == nil || *inspection.ActiveVideoPlanID != first.PlanID {
			t.Errorf("active plan = %v; want %s", inspection.ActiveVideoPlanID, first.PlanID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	stale := command
	stale.ExpectedPlanID = identity.NewID()
	stale.ClientMutationID = "it619-stale"
	_, err = bus.Send(reanalysisCtx, stale)
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.Conflict {
		t.Fatalf("stale expectedPlanId error = %v; want CONFLICT", err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoPlan{}).Where("tenant_id=? AND inspection_id=?", fixture.TenantID, inspectionID).Count(&plans).Error
	}); err != nil {
		t.Fatal(err)
	}
	if plans != 2 {
		t.Fatalf("plan count after stale request = %d; want no extra row", plans)
	}
}

type reanalysisMatcher struct{}

func (reanalysisMatcher) Propose(context.Context, []prepare_video_comparisons.Frame, []prepare_video_comparisons.Frame) ([]prepare_video_comparisons.Pair, error) {
	return nil, nil
}

func (reanalysisMatcher) Verify(context.Context, []prepare_video_comparisons.Frame, []prepare_video_comparisons.Frame, []prepare_video_comparisons.Pair) ([]prepare_video_comparisons.Pair, error) {
	return nil, nil
}
