package capture_integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

type submissionFixture struct {
	db      *gorm.DB
	service capturecore.Service
	draft   database.CaptureDraft
	now     time.Time
}

func newSubmissionFixture(t *testing.T) submissionFixture {
	t.Helper()
	return submissionFixtureWithDB(t, captureDB(t))
}

func submissionFixtureWithDB(t *testing.T, db *gorm.DB) submissionFixture {
	t.Helper()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	draft := database.CaptureDraft{ID: identity.NewID(), TenantID: identity.NewID(), ResponsibilityID: identity.NewID(), Kind: "INSPECTION", Status: "OPEN", Version: 1, Requirements: mustJSON([]capturecore.Requirement{{Key: "room", Required: true, MinimumMedia: 1, MaximumMedia: 200, DescriptionRequired: true, ImpossibilityAllowed: true}}), PolicyPayload: json.RawMessage(`{"gpsRequired":false}`), ReferencePayload: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}
	acceptance := database.ProcessingAcceptance{ID: identity.NewID(), TenantID: draft.TenantID, ResponsibilityID: draft.ResponsibilityID, DisclosureVersion: "pt-BR-1", PhotoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now}
	tokenHash := sha256.Sum256([]byte(draft.ID.String()))
	invitation := database.Invitation{ID: identity.NewID(), TenantID: draft.TenantID, ResponsibilityID: draft.ResponsibilityID, TokenHash: tokenHash[:], DeliveryIntents: json.RawMessage(`[]`), Status: "ACTIVE", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	for _, row := range []any{&draft, &acceptance, &invitation} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	service := capturecore.Service{DB: db, Now: func() time.Time { return now }, Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}}
	return submissionFixture{db: db, service: service, draft: draft, now: now}
}

func (f submissionFixture) addMedia(t *testing.T, status, key, description string, associate bool) database.MediaObject {
	t.Helper()
	media := database.MediaObject{ID: identity.NewID(), TenantID: f.draft.TenantID, ResponsibilityID: f.draft.ResponsibilityID, ObjectKey: identity.NewID().String(), ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 100, Status: status, RequirementKey: key, Description: description, CaptureSource: "CAMERA", Flags: json.RawMessage(`[]`), CreatedAt: f.now}
	media.IdempotencyKey = media.ID.String()
	if err := f.db.Create(&media).Error; err != nil {
		t.Fatal(err)
	}
	if associate {
		var answer database.RequirementAnswer
		err := f.db.Where("draft_id=? AND requirement_key=?", f.draft.ID, key).First(&answer).Error
		ids := []identity.ID{}
		if err == gorm.ErrRecordNotFound {
			answer = database.RequirementAnswer{ID: identity.NewID(), TenantID: f.draft.TenantID, DraftID: f.draft.ID, RequirementKey: key, Version: 1, Flags: json.RawMessage(`[]`), CreatedAt: f.now, UpdatedAt: f.now}
		} else if err != nil {
			t.Fatal(err)
		} else if err := json.Unmarshal(answer.MediaIDs, &ids); err != nil {
			t.Fatal(err)
		}
		answer.MediaIDs = mustJSON(append(ids, media.ID))
		if err := f.db.Save(&answer).Error; err != nil {
			t.Fatal(err)
		}
	}
	return media
}

func (f submissionFixture) submit(incomplete bool) (database.SubmissionVersion, error) {
	return f.service.Submit(context.Background(), f.draft.TenantID, f.draft.ResponsibilityID, incomplete)
}

func TestIT188IT198UnresolvedMediaCannotUseIncompleteConfirmation(t *testing.T) {
	for _, status := range []string{"UPLOADING", "UPLOADED", "VERIFIED", "SCREENED", "REJECTED"} {
		t.Run(status, func(t *testing.T) {
			f := newSubmissionFixture(t)
			media := f.addMedia(t, status, "room", "room overview", false)
			for _, incomplete := range []bool{false, true} {
				_, err := f.submit(incomplete)
				if code(err) != apperror.InvalidState || !strings.Contains(fmt.Sprint(err), "media verification") {
					t.Fatalf("unchecked %s accepted (incomplete=%t): %v", media.ID, incomplete, err)
				}
			}
			var count int64
			if err := f.db.Model(&database.SubmissionVersion{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("failed submit committed: %d %v", count, err)
			}
		})
	}
}

func TestIT181InvalidExtraDescriptionAndMalformedAnswerBlockSubmission(t *testing.T) {
	t.Run("blank extra", func(t *testing.T) {
		f := newSubmissionFixture(t)
		f.addMedia(t, "READY", "extra:damage", " ", true)
		if _, err := f.submit(true); code(err) != apperror.InvalidInput {
			t.Fatalf("blank extra accepted: %v", err)
		}
	})
	t.Run("malformed media IDs", func(t *testing.T) {
		f := newSubmissionFixture(t)
		f.addMedia(t, "READY", "room", "room overview", true)
		if err := f.db.Model(&database.RequirementAnswer{}).Where("draft_id=?", f.draft.ID).Update("media_ids", json.RawMessage(`{}`)).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := f.submit(false); code(err) != apperror.InvalidInput {
			t.Fatalf("malformed answer accepted: %v", err)
		}
	})
}

func TestIT182EmptySubmissionRequiresExplicitIncompleteConfirmation(t *testing.T) {
	f := newSubmissionFixture(t)
	if _, err := f.submit(false); code(err) != apperror.InvalidState {
		t.Fatalf("empty submission accepted without confirmation: %v", err)
	}
	result, err := f.submit(true)
	if err != nil || result.Complete || !result.RequiresAttention {
		t.Fatalf("empty result mislabeled normal: %+v %v", result, err)
	}
}

func TestIT184ForeignMediaCannotSatisfyCapture(t *testing.T) {
	f := newSubmissionFixture(t)
	media := f.addMedia(t, "READY", "room", "room overview", true)
	if err := f.db.Model(&media).Update("responsibility_id", identity.NewID()).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.submit(false); code(err) != apperror.InvalidInput {
		t.Fatalf("foreign media satisfied capture: %v", err)
	}
}

func TestIT185IT186IT187IT190LargeSubmissionReplayKeepsOneImmutableOutcome(t *testing.T) {
	f := newSubmissionFixture(t)
	for range 200 {
		f.addMedia(t, "READY", "room", "room overview", true)
	}
	first, err := f.submit(false)
	if err != nil || !first.Complete {
		t.Fatalf("large submission failed: %+v %v", first, err)
	}
	second, err := f.submit(false)
	if err != nil || second.ID != first.ID || second.PayloadDigest != first.PayloadDigest {
		t.Fatalf("retry changed outcome: %+v %v", second, err)
	}
	for _, model := range []any{&database.SubmissionVersion{}, &database.OutboxIntent{}} {
		var count int64
		if err := f.db.Model(model).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("duplicate %T: %d %v", model, count, err)
		}
	}
	var invitation database.Invitation
	if err := f.db.First(&invitation).Error; err != nil || invitation.Status != "COMPLETED" || invitation.RevokedAt == nil {
		t.Fatalf("completed capture can reauthenticate: %+v %v", invitation, err)
	}
	if err := f.service.DeclareImpossibility(context.Background(), f.draft.TenantID, f.draft.ResponsibilityID, "room", "changed", 0); code(err) != apperror.InvalidState {
		t.Fatalf("submitted answer mutated: %v", err)
	}
	view, err := f.service.Load(context.Background(), f.draft.TenantID, f.draft.ResponsibilityID)
	if err != nil || !view.ConfirmationOnly || view.Draft.Kind != "" || view.Draft.TemplateVersionID != (identity.ID{}) || len(view.Requirements) != 0 || len(view.Answers) != 0 {
		t.Fatalf("submitted capture exposed editable state: %+v %v", view, err)
	}
}

func TestIT183SubmissionRechecksStageMediaLimit(t *testing.T) {
	f := newSubmissionFixture(t)
	for range 201 {
		f.addMedia(t, "READY", "room", "room overview", true)
	}
	if _, err := f.submit(true); code(err) != apperror.InvalidState {
		t.Fatalf("stage limit bypassed: %v", err)
	}
}
