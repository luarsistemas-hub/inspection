package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type replacementFixture struct {
	db                     *gorm.DB
	service                Service
	tenantID, inspectionID identity.ID
	mediaID                identity.ID
	now                    *time.Time
}

func TestVideoReplacementKeepsTheFailedEnvironmentRequirement(t *testing.T) {
	encoded, err := mainVideoRequirement(json.RawMessage(`[{"key":"env:kitchen","required":false},{"key":"env:bathroom","required":false}]`), "env:bathroom")
	if err != nil {
		t.Fatal(err)
	}
	var requirements []capturecore.Requirement
	if err := json.Unmarshal(encoded, &requirements); err != nil {
		t.Fatal(err)
	}
	if len(requirements) != 1 || requirements[0].Key != "env:bathroom" || !requirements[0].Required || requirements[0].ImpossibilityAllowed {
		t.Fatalf("replacement requirements = %+v", requirements)
	}
}

func newReplacementFixture(t *testing.T, inspectionStatus, planErrorCode string) replacementFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"capture", "recapture", "inspections", "media", "invitations", "messaging", "analysis", "audit"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	models := []any{&database.CaptureDraft{}, &database.Inspection{}, &database.Responsibility{}, &database.MediaObject{}, &database.Invitation{}, &database.ExternalSession{}, &database.OutboxIntent{}, &database.VideoReplacement{}, &database.VideoPlan{}, &database.VideoPlanPreparation{}, &database.VideoPreparationJob{}, &database.SubmissionVersion{}, &database.AuditEvent{}}
	for _, model := range models {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	tenantID, inspectionID, responsibilityID, mediaID, submissionID, preparationID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	requirements := mustMarshal([]capturecore.Requirement{{Key: "video:main", Label: "Vídeo principal", EvidenceKind: "VIDEO", Required: true, MinimumMedia: 1, MaximumMedia: 1, ImpossibilityAllowed: true}})
	inspection := database.Inspection{ID: inspectionID, TenantID: tenantID, BusinessUnitID: identity.NewID(), AssetID: identity.NewID(), ParticipantID: identity.NewID(), TemplateID: identity.NewID(), TemplateVersionID: identity.NewID(), AnalysisPromptSnapshotID: identity.NewID(), Source: "MANUAL", SourceKey: identity.NewID().String(), Status: inspectionStatus, MediaKind: "VIDEO", DueAt: now, DeadlineAt: now.Add(time.Hour), ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{}`), Version: 1, CreatedAt: now, UpdatedAt: now}
	responsibility := database.Responsibility{ID: responsibilityID, TenantID: tenantID, InspectionID: inspectionID, ParticipantID: inspection.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}
	draft := database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, Kind: "INSPECTION", MediaKind: "VIDEO", TemplateVersionID: inspection.TemplateVersionID, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{"gpsRequired":false}`), Requirements: requirements, Status: "SUBMITTED", Version: 2, CreatedAt: now, UpdatedAt: now}
	media := database.MediaObject{ID: mediaID, TenantID: tenantID, ResponsibilityID: responsibilityID, ObjectKey: "video/1", ContentType: "video/mp4", SHA256: strings.Repeat("a", 64), SizeBytes: 100, Status: "SCREENED", MediaKind: "VIDEO", RequirementKey: "video:main", Flags: json.RawMessage(`[]`), CreatedAt: now}
	job := database.VideoPreparationJob{ID: preparationID, TenantID: tenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "FAILED", Phase: "SCREENING", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, PendingObjectKeys: []byte(`[]`), ErrorCode: "SENSITIVE_CONTENT", CreatedAt: now}
	plan := database.VideoPlan{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, SubmissionID: submissionID, CurrentPreparationID: preparationID, Generation: 1, Status: "BLOCKED", ErrorCode: planErrorCode, NextAttemptAt: now, CreatedAt: now}
	for _, row := range []any{&inspection, &responsibility, &draft, &media, &job, &plan} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	clock := now
	within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
	service := Service{DB: db, Within: within, Now: func() time.Time { return clock }}
	return replacementFixture{db: db, service: service, tenantID: tenantID, inspectionID: inspectionID, mediaID: mediaID, now: &clock}
}

func (f replacementFixture) input(key string) VideoReplacementInput {
	return VideoReplacementInput{TenantID: f.tenantID, InspectionID: f.inspectionID, MediaID: f.mediaID, ActorID: identity.NewID(), DeadlineAt: f.now.Add(48 * time.Hour), Reason: "Gravar novamente sem pessoas", IdempotencyKey: key, Delivery: []invitationcore.DeliveryIntent{{Channel: "EMAIL", Destination: "owner@example.com"}}}
}

func (f replacementFixture) inspection(t *testing.T) database.Inspection {
	t.Helper()
	var row database.Inspection
	if err := f.db.Where("id=?", f.inspectionID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

func (f replacementFixture) planStatus(t *testing.T) (string, string) {
	t.Helper()
	var row database.VideoPlan
	if err := f.db.Where("inspection_id=?", f.inspectionID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row.Status, row.ErrorCode
}

func TestRequestVideoReplacementCreatesSameInspectionInvitation(t *testing.T) {
	fixture := newReplacementFixture(t, "REVIEW_REQUIRED", "SENSITIVE_CONTENT")
	result, err := fixture.service.RequestVideoReplacement(context.Background(), fixture.input("request-1"))
	if err != nil {
		t.Fatal(err)
	}
	if result.LinkToken == "" || result.Status != "REQUESTED" || !result.DeadlineAt.Equal(fixture.now.Add(48*time.Hour)) {
		t.Fatalf("unexpected result: %+v", result)
	}
	inspection := fixture.inspection(t)
	if inspection.Status != "REPLACEMENT_PENDING" || inspection.Version != 2 || inspection.StateReason != "Gravar novamente sem pessoas" {
		t.Fatalf("inspection not moved to replacement: %+v", inspection)
	}
	if status, code := fixture.planStatus(t); status != "BLOCKED" || code != "SENSITIVE_CONTENT" {
		t.Fatalf("previous plan must be preserved until the replacement arrives: %s/%s", status, code)
	}
	var draft database.CaptureDraft
	if err := fixture.db.Where("responsibility_id=?", result.ResponsibilityID).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	if draft.Kind != "VIDEO_REPLACEMENT" || draft.MediaKind != "VIDEO" || draft.Status != "OPEN" || !strings.Contains(string(draft.Requirements), "video:main") {
		t.Fatalf("unexpected replacement draft: %+v", draft)
	}
	var invitation database.Invitation
	if err := fixture.db.Where("id=?", result.InvitationID).Take(&invitation).Error; err != nil || invitation.Status != "ACTIVE" || !invitation.ExpiresAt.Equal(result.DeadlineAt) {
		t.Fatalf("invitation must expire at the replacement deadline: %+v %v", invitation, err)
	}
	var audits, outbox int64
	fixture.db.Model(&database.AuditEvent{}).Where("action='video.replacement_requested' AND target_id=?", fixture.inspectionID.String()).Count(&audits)
	fixture.db.Model(&database.OutboxIntent{}).Where("tenant_id=?", fixture.tenantID).Count(&outbox)
	if audits != 1 || outbox != 1 {
		t.Fatalf("expected one audit and one deadline event, got %d and %d", audits, outbox)
	}
	var original database.CaptureDraft
	if err := fixture.db.Where("kind='INSPECTION'").Take(&original).Error; err != nil || original.Status != "SUBMITTED" {
		t.Fatalf("original submission must stay immutable: %+v %v", original, err)
	}
}

func TestRequestVideoReplacementIsIdempotent(t *testing.T) {
	fixture := newReplacementFixture(t, "REVIEW_REQUIRED", "SENSITIVE_CONTENT")
	first, err := fixture.service.RequestVideoReplacement(context.Background(), fixture.input("same"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.RequestVideoReplacement(context.Background(), fixture.input("same"))
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.LinkToken != "" {
		t.Fatalf("replay must return the same request without a new link: %+v %+v", first, second)
	}
	var count int64
	fixture.db.Model(&database.VideoReplacement{}).Count(&count)
	if count != 1 {
		t.Fatalf("replay created %d replacements", count)
	}
	other := fixture.input("same")
	other.MediaID = identity.NewID()
	if _, err := fixture.service.RequestVideoReplacement(context.Background(), other); errorCode(err) != apperror.Conflict {
		t.Fatalf("key reuse with another media accepted: %v", err)
	}
}

func TestRequestVideoReplacementRejectsInvalidRequests(t *testing.T) {
	cases := []struct {
		name   string
		status string
		change func(*VideoReplacementInput)
		want   apperror.Code
	}{
		{"inspection still analyzing", "ANALYZING", func(*VideoReplacementInput) {}, apperror.InvalidState},
		{"deadline in the past", "REVIEW_REQUIRED", func(in *VideoReplacementInput) { in.DeadlineAt = in.DeadlineAt.Add(-72 * time.Hour) }, apperror.InvalidInput},
		{"deadline beyond window", "REVIEW_REQUIRED", func(in *VideoReplacementInput) { in.DeadlineAt = in.DeadlineAt.Add(40 * 24 * time.Hour) }, apperror.InvalidInput},
		{"blank reason", "REVIEW_REQUIRED", func(in *VideoReplacementInput) { in.Reason = "  " }, apperror.InvalidInput},
		{"unrelated media", "REVIEW_REQUIRED", func(in *VideoReplacementInput) { in.MediaID = identity.NewID() }, apperror.InvalidInput},
		{"no verified channel", "REVIEW_REQUIRED", func(in *VideoReplacementInput) { in.Delivery = nil }, apperror.InvalidState},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newReplacementFixture(t, tc.status, "SENSITIVE_CONTENT")
			input := fixture.input("request")
			tc.change(&input)
			if _, err := fixture.service.RequestVideoReplacement(context.Background(), input); errorCode(err) != tc.want {
				t.Fatalf("want %s, got %v", tc.want, err)
			}
			if got := fixture.inspection(t).Status; got != tc.status {
				t.Fatalf("rejected request changed status to %s", got)
			}
		})
	}
}

func submitReplacement(t *testing.T, fixture replacementFixture, result VideoReplacementResult, at time.Time) (database.CaptureDraft, database.SubmissionVersion) {
	t.Helper()
	var draft database.CaptureDraft
	if err := fixture.db.Where("responsibility_id=?", result.ResponsibilityID).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	media := database.MediaObject{ID: identity.NewID(), TenantID: fixture.tenantID, ResponsibilityID: result.ResponsibilityID, ObjectKey: "video/2", ContentType: "video/mp4", SHA256: strings.Repeat("b", 64), SizeBytes: 100, Status: "RECEIVED", MediaKind: "VIDEO", RequirementKey: "video:main", Flags: json.RawMessage(`[]`), CreatedAt: at}
	submission := database.SubmissionVersion{ID: identity.NewID(), TenantID: fixture.tenantID, DraftID: draft.ID, ResponsibilityID: result.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: json.RawMessage(`{}`), PayloadDigest: strings.Repeat("c", 64), SubmittedAt: at}
	for _, row := range []any{&media, &submission} {
		if err := fixture.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return draft, submission
}

func TestFinalizeVideoReplacementResumesAnalysisOnSameInspection(t *testing.T) {
	fixture := newReplacementFixture(t, "REVIEW_REQUIRED", "SENSITIVE_CONTENT")
	result, err := fixture.service.RequestVideoReplacement(context.Background(), fixture.input("request"))
	if err != nil {
		t.Fatal(err)
	}
	draft, submission := submitReplacement(t, fixture, result, fixture.now.Add(time.Hour))
	if err := fixture.db.Transaction(func(tx *gorm.DB) error { return fixture.service.FinalizeVideoReplacementTx(tx, draft, submission) }); err != nil {
		t.Fatal(err)
	}
	inspection := fixture.inspection(t)
	if inspection.ID != fixture.inspectionID || inspection.Status != "ANALYZING" || inspection.StateReason != "" || inspection.EvidenceCount != 1 {
		t.Fatalf("analysis did not resume on the same inspection: %+v", inspection)
	}
	if status, code := fixture.planStatus(t); status != "FAILED" || code != "REPLACED" {
		t.Fatalf("blocked plan must be closed as replaced, got %s/%s", status, code)
	}
	var replacement database.VideoReplacement
	if err := fixture.db.Take(&replacement).Error; err != nil || replacement.Status != "SUBMITTED" {
		t.Fatalf("replacement not marked submitted: %+v %v", replacement, err)
	}
	var invitation database.Invitation
	if err := fixture.db.Where("id=?", result.InvitationID).Take(&invitation).Error; err != nil || invitation.Status != "REVOKED" {
		t.Fatalf("invitation must be revoked after submission: %+v %v", invitation, err)
	}
	var original database.MediaObject
	if err := fixture.db.Where("id=?", fixture.mediaID).Take(&original).Error; err != nil || original.Status != "SCREENED" {
		t.Fatalf("history of the blocked media must be preserved: %+v %v", original, err)
	}
}

func TestFinalizeVideoReplacementRejectsLateSubmission(t *testing.T) {
	fixture := newReplacementFixture(t, "REVIEW_REQUIRED", "SENSITIVE_CONTENT")
	result, err := fixture.service.RequestVideoReplacement(context.Background(), fixture.input("request"))
	if err != nil {
		t.Fatal(err)
	}
	draft, submission := submitReplacement(t, fixture, result, fixture.now.Add(72*time.Hour))
	err = fixture.db.Transaction(func(tx *gorm.DB) error { return fixture.service.FinalizeVideoReplacementTx(tx, draft, submission) })
	if errorCode(err) != apperror.InvalidState {
		t.Fatalf("late replacement accepted: %v", err)
	}
	if got := fixture.inspection(t).Status; got != "REPLACEMENT_PENDING" {
		t.Fatalf("late submission changed inspection to %s", got)
	}
}

func TestExpireVideoReplacementReturnsInspectionToAttention(t *testing.T) {
	for _, tc := range []struct{ planCode, want string }{{"SENSITIVE_CONTENT", "REVIEW_REQUIRED"}, {"PREPARATION_FAILED", "PROCESSING_FAILED"}} {
		t.Run(tc.want, func(t *testing.T) {
			fixture := newReplacementFixture(t, "REVIEW_REQUIRED", tc.planCode)
			result, err := fixture.service.RequestVideoReplacement(context.Background(), fixture.input("request"))
			if err != nil {
				t.Fatal(err)
			}
			var replacement database.VideoReplacement
			if err := fixture.db.Take(&replacement).Error; err != nil {
				t.Fatal(err)
			}
			if err := fixture.db.Transaction(func(tx *gorm.DB) error {
				return fixture.service.ExpireVideoReplacementTx(tx, fixture.tenantID, replacement.ID)
			}); err != nil {
				t.Fatal(err)
			}
			if got := fixture.inspection(t).Status; got != "REPLACEMENT_PENDING" {
				t.Fatalf("expired before the deadline: %s", got)
			}
			*fixture.now = fixture.now.Add(49 * time.Hour)
			if err := fixture.db.Transaction(func(tx *gorm.DB) error {
				return fixture.service.ExpireVideoReplacementTx(tx, fixture.tenantID, replacement.ID)
			}); err != nil {
				t.Fatal(err)
			}
			inspection := fixture.inspection(t)
			if inspection.Status != tc.want || inspection.StateReason != "Nova gravação não enviada no prazo" {
				t.Fatalf("unexpected state after expiry: %+v", inspection)
			}
			var invitation database.Invitation
			var draft database.CaptureDraft
			if err := fixture.db.Where("id=?", result.InvitationID).Take(&invitation).Error; err != nil || invitation.Status != "REVOKED" {
				t.Fatalf("invitation must be revoked: %+v %v", invitation, err)
			}
			if err := fixture.db.Where("responsibility_id=?", result.ResponsibilityID).Take(&draft).Error; err != nil || draft.Status != "EXPIRED" {
				t.Fatalf("draft must expire: %+v %v", draft, err)
			}
			if status, _ := fixture.planStatus(t); status != "BLOCKED" {
				t.Fatalf("blocked plan must stay actionable, got %s", status)
			}
			if err := fixture.db.Transaction(func(tx *gorm.DB) error {
				return fixture.service.ExpireVideoReplacementTx(tx, fixture.tenantID, replacement.ID)
			}); err != nil {
				t.Fatalf("expiry must be idempotent: %v", err)
			}
		})
	}
}
