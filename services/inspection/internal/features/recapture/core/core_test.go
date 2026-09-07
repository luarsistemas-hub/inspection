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
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type recaptureFixture struct {
	db                       *gorm.DB
	service                  Service
	tenantID, inspectionID   identity.ID
	originalResponsibilityID identity.ID
	now                      time.Time
	requirements             []capturecore.Requirement
}

func TestRecaptureRequestContractsIT221ToIT230(t *testing.T) {
	t.Run("IT-221 unknown item or blank reason", func(t *testing.T) {
		fixture := newRecaptureFixture(t, "SUBMITTED", 2)
		input := fixture.input("unknown", "reason", "unknown")
		if _, err := fixture.service.Request(context.Background(), input); errorCode(err) != apperror.InvalidInput {
			t.Fatalf("unknown requirement accepted: %v", err)
		}
		input = fixture.input("requirement-1", " ", "blank")
		if _, err := fixture.service.Request(context.Background(), input); errorCode(err) != apperror.InvalidInput {
			t.Fatalf("blank reason accepted: %v", err)
		}
	})
	t.Run("IT-222 no selected items", func(t *testing.T) {
		fixture := newRecaptureFixture(t, "SUBMITTED", 1)
		input := fixture.input("requirement-1", "reason", "empty")
		input.Items = nil
		if _, err := fixture.service.Request(context.Background(), input); errorCode(err) != apperror.InvalidInput {
			t.Fatalf("empty request accepted: %v", err)
		}
	})
	t.Run("IT-223 item and cycle limits", func(t *testing.T) {
		fixture := newRecaptureFixture(t, "SUBMITTED", 1)
		input := fixture.input("requirement-1", "reason", "oversized")
		input.Items = make([]Item, capturecore.MaxActivePhotos+1)
		if _, err := fixture.service.Request(context.Background(), input); errorCode(err) != apperror.InvalidInput {
			t.Fatalf("oversized request accepted: %v", err)
		}
		for cycle := 0; cycle < MaxRecaptureCycles; cycle++ {
			row := database.RecaptureRequest{ID: identity.NewID(), TenantID: fixture.tenantID, InspectionID: fixture.inspectionID, ResponsibilityID: identity.NewID(), Status: "SUBMITTED", DeadlineAt: fixture.now, IdempotencyKey: "prior-" + string(rune('a'+cycle)), PayloadDigest: strings.Repeat("a", 64), CreatedAt: fixture.now, UpdatedAt: fixture.now}
			if err := fixture.db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
		}
		if _, err := fixture.service.Request(context.Background(), fixture.input("requirement-1", "reason", "cycle-limit")); errorCode(err) != apperror.InvalidState {
			t.Fatalf("cycle limit not enforced: %v", err)
		}
	})
	t.Run("IT-224 viewer and wrong scope denied", func(t *testing.T) {
		fixture := newRecaptureFixture(t, "SUBMITTED", 1)
		principal := requestctx.Principal{IdentityID: identity.NewID(), TenantID: fixture.tenantID, Roles: []string{auth.Viewer}, Scopes: []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: identity.NewID()}}}
		ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fixture.tenantID, Principal: principal})
		if _, err := (auth.Authorizer{}).Authorize(ctx, fixture.tenantID, []string{auth.TenantAdmin, auth.Manager, auth.Employee}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: identity.NewID()}, true); errorCode(err) != apperror.Forbidden {
			t.Fatalf("viewer mutation accepted: %v", err)
		}
	})
	t.Run("IT-225 concurrent reasons merge into one active requirement", func(t *testing.T) {
		fixture := newRecaptureFixture(t, "SUBMITTED", 1)
		first, err := fixture.service.Request(context.Background(), fixture.input("requirement-1", "automatic blur", "automatic"))
		if err != nil {
			t.Fatal(err)
		}
		second, err := fixture.service.Request(context.Background(), fixture.input("requirement-1", "reviewer obstruction", "reviewer"))
		if err != nil || second.RequestID != first.RequestID {
			t.Fatalf("active request was duplicated: %+v %v", second, err)
		}
		var rows []database.RecaptureRequirement
		if err := fixture.db.Where("request_id=?", first.RequestID).Find(&rows).Error; err != nil || len(rows) != 1 || !strings.Contains(rows[0].Reason, "automatic blur") || !strings.Contains(rows[0].Reason, "reviewer obstruction") {
			t.Fatalf("reasons were not consolidated: %+v %v", rows, err)
		}
		draft := loadDraft(t, fixture.db, first.ResponsibilityID)
		if draft.Version != 2 || !strings.Contains(string(draft.Requirements), "reviewer obstruction") {
			t.Fatalf("merged reason missing from participant instructions: %+v", draft)
		}
	})
	t.Run("IT-226 delivery failure does not cancel active request", func(t *testing.T) {
		fixture := newRecaptureFixture(t, "SUBMITTED", 1)
		created, err := fixture.service.Request(context.Background(), fixture.input("requirement-1", "blur", "delivery-failure"))
		if err != nil {
			t.Fatal(err)
		}
		var row database.RecaptureRequest
		if err := fixture.db.First(&row, "id=?", created.RequestID).Error; err != nil || row.Status != "REQUESTED" {
			t.Fatalf("request was not durable before delivery: %+v %v", row, err)
		}
	})
	t.Run("IT-227 identical retry creates one request", func(t *testing.T) {
		fixture := newRecaptureFixture(t, "SUBMITTED", 1)
		input := fixture.input("requirement-1", "blur", "same")
		first, err := fixture.service.Request(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		second, err := fixture.service.Request(context.Background(), input)
		if err != nil || second.RequestID != first.RequestID || second.LinkToken != "" {
			t.Fatalf("retry was not idempotent: %+v %v", second, err)
		}
		var count int64
		fixture.db.Model(&database.RecaptureRequest{}).Count(&count)
		if count != 1 {
			t.Fatalf("request count=%d", count)
		}
	})
	t.Run("IT-228 initial capture must be submitted", func(t *testing.T) {
		fixture := newRecaptureFixture(t, "IN_PROGRESS", 1)
		if _, err := fixture.service.Request(context.Background(), fixture.input("requirement-1", "blur", "early")); errorCode(err) != apperror.InvalidState {
			t.Fatalf("early recapture accepted: %v", err)
		}
	})
	t.Run("IT-229 terminal inspection rejects recapture", func(t *testing.T) {
		for _, status := range []string{"CANCELED", "INVALIDATED", "COMPLETED"} {
			fixture := newRecaptureFixture(t, status, 1)
			if _, err := fixture.service.Request(context.Background(), fixture.input("requirement-1", "blur", "terminal-"+status)); errorCode(err) != apperror.InvalidState {
				t.Fatalf("%s recapture accepted: %v", status, err)
			}
		}
		fixture, _ := requestedFixture(t, 1)
		fixture.service.Now = func() time.Time { return fixture.now.Add(2 * time.Hour) }
		late := fixture.input("requirement-1", "late", "late")
		late.DeadlineAt = fixture.now.Add(3 * time.Hour)
		if _, err := fixture.service.Request(context.Background(), late); errorCode(err) != apperror.InvalidState {
			t.Fatalf("late active recapture accepted: %v", err)
		}
	})
	t.Run("IT-230 many item instructions remain item specific", func(t *testing.T) {
		fixture := newRecaptureFixture(t, "SUBMITTED", 50)
		input := fixture.input("requirement-1", "reason-1", "many")
		input.Items = make([]Item, 50)
		for index := range input.Items {
			input.Items[index] = Item{RequirementKey: fixture.requirements[index].Key, Reason: "reason-" + fixture.requirements[index].Key}
		}
		created, err := fixture.service.Request(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		draft := loadDraft(t, fixture.db, created.ResponsibilityID)
		var focused []capturecore.Requirement
		if err := json.Unmarshal(draft.Requirements, &focused); err != nil || len(focused) != 50 || focused[49].Instructions != "instructions-50\nRecapture: reason-requirement-50" {
			t.Fatalf("focused requirements lost detail: %+v %v", focused, err)
		}
	})
}

func TestRecaptureCompletionContractsIT231ToIT240(t *testing.T) {
	t.Run("IT-231 invalid replacement keeps requirement open", func(t *testing.T) {
		fixture, created := requestedFixture(t, 1)
		if _, err := fixture.service.Finalize(context.Background(), fixture.tenantID, created.RequestID, true); errorCode(err) != apperror.InvalidState {
			t.Fatalf("missing replacement accepted: %v", err)
		}
		assertRequirementStatus(t, fixture.db, created.RequestID, "OPEN")
	})
	t.Run("IT-232 no correction remains pending before expiry", func(t *testing.T) {
		fixture, created := requestedFixture(t, 1)
		if _, err := fixture.service.Finalize(context.Background(), fixture.tenantID, created.RequestID, false); errorCode(err) != apperror.InvalidState {
			t.Fatalf("premature expiry accepted: %v", err)
		}
		assertRequestStatus(t, fixture.db, created.RequestID, "REQUESTED")
	})
	t.Run("IT-233 replacement limit preserves prior evidence", func(t *testing.T) {
		fixture, created := requestedFixture(t, 1)
		draft := loadDraft(t, fixture.db, created.ResponsibilityID)
		var focused []capturecore.Requirement
		if err := json.Unmarshal(draft.Requirements, &focused); err != nil {
			t.Fatal(err)
		}
		if len(focused) != 1 || focused[0].MaximumMedia != 2 {
			t.Fatalf("original item limit was not retained: %+v", focused)
		}
		var originals int64
		fixture.db.Model(&database.MediaObject{}).Where("responsibility_id=?", fixture.originalResponsibilityID).Count(&originals)
		if originals != 1 {
			t.Fatalf("original evidence changed: %d", originals)
		}
	})
	t.Run("IT-234 unrelated request identity is denied", func(t *testing.T) {
		fixture, _ := requestedFixture(t, 1)
		if _, err := fixture.service.Finalize(context.Background(), fixture.tenantID, identity.NewID(), true); errorCode(err) != apperror.NotFound {
			t.Fatalf("unrelated request accepted: %v", err)
		}
	})
	t.Run("IT-235 deadline is deterministic", func(t *testing.T) {
		fixture, created := requestedFixture(t, 1)
		fixture.service.Now = func() time.Time { return fixture.now.Add(time.Hour) }
		if _, err := fixture.service.Finalize(context.Background(), fixture.tenantID, created.RequestID, true); errorCode(err) != apperror.InvalidState {
			t.Fatalf("correction at cutoff accepted: %v", err)
		}
		result, err := fixture.service.Finalize(context.Background(), fixture.tenantID, created.RequestID, false)
		if err != nil || result.Status != "EXPIRED" {
			t.Fatalf("expiry at cutoff failed: %+v %v", result, err)
		}
	})
	t.Run("IT-236 interrupted progress remains resumable until deadline", func(t *testing.T) {
		fixture, created := requestedFixture(t, 1)
		draft := loadDraft(t, fixture.db, created.ResponsibilityID)
		var invite database.Invitation
		if err := fixture.db.Where("responsibility_id=?", created.ResponsibilityID).First(&invite).Error; err != nil || draft.Status != "OPEN" || !invite.ExpiresAt.Equal(fixture.now.Add(time.Hour)) {
			t.Fatalf("progress validity mismatch: draft=%+v invite=%+v err=%v", draft, invite, err)
		}
	})
	t.Run("IT-237 corrected finalization is idempotent", func(t *testing.T) {
		fixture, created := requestedFixture(t, 1)
		seedReplacement(t, fixture, created, "requirement-1")
		first, err := fixture.service.Finalize(context.Background(), fixture.tenantID, created.RequestID, true)
		if err != nil {
			t.Fatal(err)
		}
		second, err := fixture.service.Finalize(context.Background(), fixture.tenantID, created.RequestID, true)
		if err != nil || first.Status != "SUBMITTED" || second.Status != first.Status {
			t.Fatalf("finalization replay failed: %+v %+v %v", first, second, err)
		}
	})
	t.Run("IT-238 pending replacement is named", func(t *testing.T) {
		fixture, created := requestedFixture(t, 1)
		_, err := fixture.service.Finalize(context.Background(), fixture.tenantID, created.RequestID, true)
		code, field, _ := apperror.Public(err)
		if code != apperror.InvalidState || field != "requirement-1" {
			t.Fatalf("pending item not identified: code=%s field=%s err=%v", code, field, err)
		}
	})
	t.Run("IT-239 terminal request blocks later correction", func(t *testing.T) {
		fixture, created := requestedFixture(t, 1)
		fixture.service.Now = func() time.Time { return fixture.now.Add(2 * time.Hour) }
		if _, err := fixture.service.Finalize(context.Background(), fixture.tenantID, created.RequestID, false); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.service.Finalize(context.Background(), fixture.tenantID, created.RequestID, true); errorCode(err) != apperror.InvalidState {
			t.Fatalf("expired request accepted correction: %v", err)
		}
	})
	t.Run("IT-240 focused draft excludes unrelated evidence", func(t *testing.T) {
		fixture := newRecaptureFixture(t, "SUBMITTED", 100)
		input := fixture.input("requirement-37", "blur", "focused")
		created, err := fixture.service.Request(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		draft := loadDraft(t, fixture.db, created.ResponsibilityID)
		var focused []capturecore.Requirement
		if err := json.Unmarshal(draft.Requirements, &focused); err != nil || len(focused) != 1 || focused[0].Key != "requirement-37" {
			t.Fatalf("unrelated requirements leaked: %+v %v", focused, err)
		}
	})
}

func newRecaptureFixture(t *testing.T, status string, requirementCount int) recaptureFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"capture", "recapture", "inspections", "media", "invitations", "messaging"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	models := []any{&database.CaptureDraft{}, &database.RecaptureRequest{}, &database.RecaptureRequirement{}, &database.Inspection{}, &database.Responsibility{}, &database.MediaObject{}, &database.Invitation{}, &database.ExternalSession{}, &database.OutboxIntent{}}
	for _, model := range models {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	tenantID, inspectionID, responsibilityID := identity.NewID(), identity.NewID(), identity.NewID()
	requirements := make([]capturecore.Requirement, requirementCount)
	for index := range requirements {
		requirements[index] = capturecore.Requirement{Key: "requirement-" + itoa(index+1), Section: "section-" + itoa(index/10+1), Label: "label-" + itoa(index+1), Instructions: "instructions-" + itoa(index+1), EvidenceKind: "PHOTO", Required: true, MinimumMedia: 1, MaximumMedia: 2, DescriptionRequired: true, CaptureSourcePolicy: "CAMERA_DEFAULT", ComparisonTarget: "FIXED_ORIGIN"}
	}
	inspection := database.Inspection{ID: inspectionID, TenantID: tenantID, BusinessUnitID: identity.NewID(), AssetID: identity.NewID(), ParticipantID: identity.NewID(), TemplateID: identity.NewID(), TemplateVersionID: identity.NewID(), AnalysisProfileVersionID: identity.NewID(), Source: "MANUAL", SourceKey: identity.NewID().String(), Status: status, DueAt: now, DeadlineAt: now.Add(time.Hour), ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{}`), Version: 1, CreatedAt: now, UpdatedAt: now}
	responsibility := database.Responsibility{ID: responsibilityID, TenantID: tenantID, InspectionID: inspectionID, ParticipantID: inspection.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}
	draft := database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, Kind: "INSPECTION", TemplateVersionID: inspection.TemplateVersionID, ReferencePayload: json.RawMessage(`{"fixed":true}`), PolicyPayload: json.RawMessage(`{"gpsRequired":true}`), Requirements: mustMarshal(requirements), Status: "SUBMITTED", Version: 2, CreatedAt: now, UpdatedAt: now}
	original := database.MediaObject{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, ObjectKey: "original/1", ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 100, Status: "READY", RequirementKey: "requirement-1", Description: "original", CaptureSource: "CAMERA", Flags: json.RawMessage(`[]`), CreatedAt: now}
	for _, row := range []any{&inspection, &responsibility, &draft, &original} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
	return recaptureFixture{db: db, service: Service{DB: db, Within: within, Now: func() time.Time { return now }}, tenantID: tenantID, inspectionID: inspectionID, originalResponsibilityID: responsibilityID, now: now, requirements: requirements}
}

func (f recaptureFixture) input(key, reason, idempotency string) RequestInput {
	return RequestInput{TenantID: f.tenantID, InspectionID: f.inspectionID, Items: []Item{{RequirementKey: key, Reason: reason}}, Delivery: []invitationcore.DeliveryIntent{{Channel: "EMAIL", Destination: "owner@example.com"}}, DeadlineAt: f.now.Add(time.Hour), IdempotencyKey: idempotency}
}

func requestedFixture(t *testing.T, requirements int) (recaptureFixture, Result) {
	t.Helper()
	fixture := newRecaptureFixture(t, "SUBMITTED", requirements)
	created, err := fixture.service.Request(context.Background(), fixture.input("requirement-1", "blur", "request"))
	if err != nil {
		t.Fatal(err)
	}
	return fixture, created
}

func seedReplacement(t *testing.T, fixture recaptureFixture, created Result, requirementKey string) {
	t.Helper()
	row := database.MediaObject{ID: identity.NewID(), TenantID: fixture.tenantID, ResponsibilityID: created.ResponsibilityID, ObjectKey: "replacement/" + identity.NewID().String(), ContentType: "image/jpeg", SHA256: strings.Repeat("b", 64), SizeBytes: 100, Status: "READY", RequirementKey: requirementKey, Description: "corrected", CaptureSource: "CAMERA", Flags: json.RawMessage(`[]`), CreatedAt: fixture.now}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
}

func assertRequestStatus(t *testing.T, db *gorm.DB, id identity.ID, wanted string) {
	t.Helper()
	var row database.RecaptureRequest
	if err := db.First(&row, "id=?", id).Error; err != nil || row.Status != wanted {
		t.Fatalf("request status=%s want=%s err=%v", row.Status, wanted, err)
	}
}

func assertRequirementStatus(t *testing.T, db *gorm.DB, requestID identity.ID, wanted string) {
	t.Helper()
	var row database.RecaptureRequirement
	if err := db.Where("request_id=?", requestID).First(&row).Error; err != nil || row.Status != wanted {
		t.Fatalf("requirement status=%s want=%s err=%v", row.Status, wanted, err)
	}
}

func loadDraft(t *testing.T, db *gorm.DB, responsibilityID identity.ID) database.CaptureDraft {
	t.Helper()
	var row database.CaptureDraft
	if err := db.Where("responsibility_id=?", responsibilityID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

func errorCode(err error) apperror.Code {
	code, _, _ := apperror.Public(err)
	return code
}

func mustMarshal(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
