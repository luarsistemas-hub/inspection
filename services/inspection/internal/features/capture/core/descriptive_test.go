package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestUT015EvaluateCoverageDistinguishesComponentStates(t *testing.T) {
	room := catalog.ExpectedRoom{Key: "kitchen", Name: "Cozinha", Components: []catalog.ExpectedComponent{{Key: "floor", Label: "Piso", Views: []string{"geral", "detalhe"}}, {Key: "wall", Label: "Parede", Views: []string{"geral"}}, {Key: "window", Label: "Janela", Views: []string{"geral"}}, {Key: "ceiling", Label: "Teto", Views: []string{"geral"}}, {Key: "door", Label: "Porta", Views: []string{"geral"}}}}
	answers := []ComponentCoverageAnswer{{"env:kitchen", "floor", "geral", "OBSERVED"}, {"env:kitchen", "wall", "", "PARTIAL"}, {"env:kitchen", "window", "", "NOT_OBSERVED"}, {"env:kitchen", "ceiling", "", "NOT_APPLICABLE"}, {"env:kitchen", "door", "", "IMPOSSIBLE"}}
	got := EvaluateCoverage([]catalog.ExpectedRoom{room}, answers)
	if got.Expected != 5 || got.FullyAssessed != 0 || got.PartiallyAssessed != 2 || got.NotObserved != 1 || got.NotApplicable != 1 || got.Impossible != 1 {
		t.Fatalf("coverage=%+v", got)
	}
	if len(got.Suggestions) != 3 {
		t.Fatalf("suggestions=%+v", got.Suggestions)
	}
}

func TestUT015JustifiedViewDoesNotReduceComponentCoverage(t *testing.T) {
	room := catalog.ExpectedRoom{Key: "living-room", Name: "Sala", Components: []catalog.ExpectedComponent{{Key: "floor", Label: "Piso", Views: []string{"geral", "detalhe"}}}}
	answers := []ComponentCoverageAnswer{{RequirementKey: "env:living-room", ComponentKey: "floor", ViewKey: "geral", State: "OBSERVED"}, {RequirementKey: "env:living-room", ComponentKey: "floor", ViewKey: "detalhe", State: "NOT_APPLICABLE"}}
	got := EvaluateCoverage([]catalog.ExpectedRoom{room}, answers)
	if got.Expected != 1 || got.FullyAssessed != 1 || got.PartiallyAssessed != 0 || len(got.Suggestions) != 0 {
		t.Fatalf("justified view changed component coverage: %+v", got)
	}
}

func TestCaptureTimeConflictUsesSafeToleranceAndDeviceSource(t *testing.T) {
	received := time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC)
	threeHoursEarlier := received.Add(-3 * time.Hour)
	fifteenHoursEarlier := received.Add(-15 * time.Hour)
	sevenMinutesEarlier := received.Add(-7 * time.Minute)
	if captureTimeConflict("GALLERY", "EXIF", &received, &threeHoursEarlier, []string{"CAPTURE_TIME_DEVICE_LOCAL"}) {
		t.Fatal("device-local EXIF offset was incorrectly flagged as a conflict")
	}
	if !captureTimeConflict("GALLERY", "EXIF", &received, &fifteenHoursEarlier, []string{"CAPTURE_TIME_DEVICE_LOCAL"}) {
		t.Fatal("device-local EXIF with a difference beyond 14 hours was not flagged")
	}
	if !captureTimeConflict("FILE", "EXIF", &received, &sevenMinutesEarlier, nil) {
		t.Fatal("ordinary EXIF conflict was not flagged")
	}
	if captureTimeConflict("GALLERY", "DEVICE", &received, &fifteenHoursEarlier, []string{"CAPTURE_TIME_DEVICE_LOCAL"}) {
		t.Fatal("device-provided capture time was incorrectly compared to receipt time")
	}
}

func TestUT016ComponentExceptionStatesRequirePortugueseReason(t *testing.T) {
	for _, state := range []string{"NOT_APPLICABLE", "IMPOSSIBLE"} {
		if err := ValidateComponentAnswer(state, "", 0); !apperror.Is(err, apperror.InvalidInput) || !containsText(err.Error(), "motivo") {
			t.Fatalf("state %s error=%v", state, err)
		}
	}
}

func TestUT017EmptyRoomMediaRemainsNotObserved(t *testing.T) {
	room := catalog.ExpectedRoom{Key: "kitchen", Name: "Cozinha", Components: []catalog.ExpectedComponent{{Key: "floor", Label: "Piso", Views: []string{"geral", "detalhe"}}}}
	got := EvaluateCoverage([]catalog.ExpectedRoom{room}, nil)
	if got.NotObserved != 1 || got.FullyAssessed != 0 || len(got.Suggestions) != 2 {
		t.Fatalf("empty room coverage=%+v", got)
	}
}

func TestUT018ValidateMediaLimitPreservesCurrentAcceptedMedia(t *testing.T) {
	if err := ValidateMediaLimit(3, 1, 4); err != nil {
		t.Fatalf("last accepted media rejected: %v", err)
	}
	if err := ValidateMediaLimit(4, 1, 4); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("over-limit media error=%v", err)
	}
}

func TestUT021SubmitKeepsSupplementalMediaOutOfIssuedVersion(t *testing.T) {
	db, service, tenantID, responsibilityID := environmentFixture(t, "OPEN", json.RawMessage(`[{"key":"room","required":false,"minimumMedia":1}]`))
	var draft database.CaptureDraft
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.TemplateVersion{}).Where("tenant_id=? AND id=?", tenantID, draft.TemplateVersionID).Update("schema_version", 1).Error; err != nil {
		t.Fatal(err)
	}
	submission, err := service.Submit(context.Background(), tenantID, responsibilityID, false)
	if err != nil {
		t.Fatal(err)
	}
	media := database.MediaObject{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, ObjectKey: "supplemental", ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 1, Status: "READY", MediaKind: "PHOTO", RequirementKey: "extra:supplemental", Flags: json.RawMessage(`[]`), AttentionItems: json.RawMessage(`[]`)}
	if err := db.Create(&media).Error; err != nil {
		t.Fatal(err)
	}
	_, err = service.SaveMetadata(context.Background(), MetadataInput{TenantID: tenantID, ResponsibilityID: responsibilityID, MediaID: media.ID, RequirementKey: media.RequirementKey, CaptureSource: "CAMERA"})
	if !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("post-submission supplemental metadata error=%v", err)
	}
	var count int64
	if err := db.Model(&database.SubmissionVersion{}).Where("tenant_id=? AND draft_id=?", tenantID, draft.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("issued submission count=%d err=%v", count, err)
	}
	var stored database.SubmissionVersion
	if err := db.Where("id=?", submission.ID).Take(&stored).Error; err != nil || stored.PayloadDigest != submission.PayloadDigest {
		t.Fatalf("issued submission changed: %+v err=%v", stored, err)
	}
}

func TestUT022AndUT023HandoverPayloadValidation(t *testing.T) {
	if err := validateHandoverPayload("KEY", map[string]any{"quantity": float64(2), "keyType": "Chave da porta"}); err != nil {
		t.Fatal(err)
	}
	if err := validateHandoverPayload("METER", map[string]any{"meterType": "ENERGY", "identifier": "A123", "reading": 123.4, "unit": "kWh", "date": "2026-11-03"}); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []map[string]any{{"keyType": "door", "quantity": -1}, {"keyType": "door", "quantity": "2"}, {"keyType": "door", "quantity": 1.5}, {"meterType": "ENERGY", "identifier": "A123", "reading": "abc", "unit": "kWh", "date": "2026-11-03"}, {"meterType": "ENERGY", "identifier": "A123", "reading": "0x1p2", "unit": "kWh", "date": "2026-11-03"}, {"meterType": "ENERGY", "identifier": "A123", "reading": -1, "unit": "kWh", "date": "2026-11-03"}} {
		kind := "KEY"
		if _, ok := payload["reading"]; ok {
			kind = "METER"
		}
		if err := validateHandoverPayload(kind, payload); !apperror.Is(err, apperror.InvalidInput) {
			t.Fatalf("%s payload accepted: %v", kind, err)
		}
	}
}

func TestUT024AndUT025RequiredHandoverPendingAndNoteLimit(t *testing.T) {
	if err := validateHandoverPayload("NOTE", map[string]any{"note": string(make([]rune, 2001))}); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("overlong note error=%v", err)
	}
	if err := validateHandoverPayload("NOTE", map[string]any{"note": strings.Repeat("á", 2000)}); err != nil {
		t.Fatalf("2,000 accented characters rejected: %v", err)
	}
	if err := validateHandoverPayload("NOTE", map[string]any{"note": strings.Repeat("á", 2001)}); !apperror.Is(err, apperror.InvalidInput) || !strings.Contains(err.Error(), "2.000 caracteres") {
		t.Fatalf("overlong accented note error=%v", err)
	}
	if err := validateHandoverPayload("KEY", map[string]any{}); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("missing quantity error=%v", err)
	}
	for _, tc := range []struct {
		kind    string
		payload map[string]any
	}{{"NOTE", map[string]any{}}, {"METER", map[string]any{"meterType": "ENERGY", "identifier": "A1", "reading": 1.0, "date": "2026-11-03"}}, {"KEY", map[string]any{"keyType": "door", "quantity": 1, "internal": "forbidden"}}} {
		if err := validateHandoverPayload(tc.kind, tc.payload); !apperror.Is(err, apperror.InvalidInput) {
			t.Errorf("invalid %s payload accepted: %v", tc.kind, err)
		}
	}
}

func TestDescriptiveSlicesReturnDependencyFailure(t *testing.T) {
	service := Service{}
	tenant, responsibility := identity.NewID(), identity.NewID()
	if _, err := service.RecordComponentAnswer(context.Background(), ComponentAnswerInput{TenantID: tenant, ResponsibilityID: responsibility, State: "NOT_OBSERVED"}); err == nil {
		t.Fatal("component answer succeeded without database")
	}
	if _, err := service.UpsertHandoverRecord(context.Background(), HandoverInput{TenantID: tenant, ResponsibilityID: responsibility, Kind: "NOTE", Payload: json.RawMessage(`{"note":"ok"}`)}); err == nil {
		t.Fatal("handover upsert succeeded without database")
	}
}

func TestUT024UT026UT027UT028HandoverRevisionsRetriesAndIssuedSnapshot(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"capture", "inspections", "reports"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.CaptureDraft{}, &database.HandoverRecord{}, &database.Responsibility{}, &database.InspectionScopeSnapshot{}, &database.ReportSnapshot{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS capture.component_answers (id text primary key, tenant_id text not null, draft_id text not null, requirement_key text not null, component_key text not null, view_key text not null default '', state text not null, reason text not null default '', evidence_ids blob not null, version integer not null, actor_id text, created_at datetime not null, updated_at datetime not null)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS capture.requirement_answers (id text primary key, tenant_id text not null, draft_id text not null, requirement_key text not null, impossibility_reason text not null default '')`).Error; err != nil {
		t.Fatal(err)
	}
	tenantID, responsibilityID, inspectionID, draftID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	if err := db.Create(&database.Responsibility{ID: responsibilityID, TenantID: tenantID, InspectionID: inspectionID, Status: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.CaptureDraft{ID: draftID, TenantID: tenantID, ResponsibilityID: responsibilityID, Kind: "INSPECTION", MediaKind: "PHOTO", ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[]`), Status: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	scope := json.RawMessage(`{"rooms":[],"handoverRecords":[{"key":"meter","kind":"METER","required":true}]}`)
	if err := db.Create(&database.InspectionScopeSnapshot{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, TemplateVersionID: identity.NewID(), Profile: "GENERAL", Document: scope, Digest: "scope", Version: 1, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	service := Service{DB: db, Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(db) }}
	input := HandoverInput{TenantID: tenantID, ResponsibilityID: responsibilityID, RecordKey: "meter", Kind: "METER", Payload: json.RawMessage(`{"meterType":"ENERGY","identifier":"A123","reading":123.4,"unit":"kWh","date":"2026-11-03"}`)}
	pending, err := service.Coverage(context.Background(), tenantID, responsibilityID)
	if err != nil || len(pending.PendingRecords) != 1 || pending.PendingRecords[0] != "meter" {
		t.Fatalf("required meter was not pending: %+v %v", pending, err)
	}
	first, err := service.UpsertHandoverRecord(context.Background(), input)
	if err != nil || first.Version != 1 {
		t.Fatalf("first handover save=%+v err=%v", first, err)
	}
	retry, err := service.UpsertHandoverRecord(context.Background(), input)
	if err != nil || retry.ID != first.ID || retry.Version != first.Version {
		t.Fatalf("idempotent retry=%+v err=%v", retry, err)
	}
	oldReport := database.ReportSnapshot{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, Mode: "CURRENT", Classification: "OK", JSONDigest: "previous-digest", HTMLDigest: "", SchemaVersion: 3, VersionNumber: 1, CanonicalJSON: json.RawMessage(`{"handover":{"meter":{"reading":123.4,"unit":"kWh"}}}`), HTML: json.RawMessage(`{}`), CreatedAt: now}
	if err := db.Create(&oldReport).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.CaptureDraft{}).Where("id=?", draftID).Update("status", "SUBMITTED").Error; err != nil {
		t.Fatal(err)
	}
	input.ExpectedVersion = 1
	input.Payload = json.RawMessage(`{"meterType":"ENERGY","identifier":"A123","reading":124,"unit":"kWh","date":"2026-11-03"}`)
	second, err := service.UpsertHandoverRecord(context.Background(), input)
	if err != nil || second.Version != 2 || second.ID == first.ID {
		t.Fatalf("versioned update=%+v err=%v", second, err)
	}
	input.Payload = json.RawMessage(`{"meterType":"ENERGY","identifier":"A123","reading":125,"unit":"kWh","date":"2026-11-03"}`)
	if _, err := service.UpsertHandoverRecord(context.Background(), input); !apperror.Is(err, apperror.Conflict) {
		t.Fatalf("stale revision error=%v", err)
	}
	var saved database.HandoverRecord
	if err := db.Where("id=?", first.ID).Take(&saved).Error; err != nil || saved.Version != 1 || string(saved.Payload) != string(first.Payload) {
		t.Fatalf("stale update changed saved declaration: %+v err=%v", saved, err)
	}
	var issued database.ReportSnapshot
	if err := db.Where("id=?", oldReport.ID).Take(&issued).Error; err != nil || !strings.Contains(string(issued.CanonicalJSON), `"reading":123.4`) || issued.JSONDigest != oldReport.JSONDigest {
		t.Fatalf("UT-028 correction changed issued report snapshot: %+v err=%v", issued, err)
	}
	oldDigest, newDigest := sha256.Sum256(first.Payload), sha256.Sum256(second.Payload)
	if oldDigest == newDigest || second.Version != 2 {
		t.Fatal("UT-028 corrected handover revision was not distinguishable for canonical report digesting")
	}
	covered, err := service.Coverage(context.Background(), tenantID, responsibilityID)
	if err != nil || len(covered.PendingRecords) != 0 {
		t.Fatalf("saved required record stayed pending: %+v %v", covered, err)
	}
}

func TestUT109RecaptureSuggestionUsesPinnedMissingViews(t *testing.T) {
	room := catalog.ExpectedRoom{Key: "kitchen", Name: "Cozinha", Components: []catalog.ExpectedComponent{{Key: "floor", Label: "Piso", Views: []string{"geral", "detalhe"}}}}
	got := EvaluateCoverage([]catalog.ExpectedRoom{room}, []ComponentCoverageAnswer{{"env:kitchen", "floor", "geral", "OBSERVED"}})
	if got.PartiallyAssessed != 1 || len(got.Suggestions) != 1 || got.Suggestions[0].RoomKey != "kitchen" || got.Suggestions[0].ComponentKey != "floor" || got.Suggestions[0].MissingView != "detalhe" {
		t.Fatalf("suggestions=%+v coverage=%+v", got.Suggestions, got)
	}
}

func TestUT109UnknownScopeHasNoInventedDenominator(t *testing.T) {
	if got := EvaluateCoverage(nil, nil); !got.UnknownScope || got.Expected != 0 {
		t.Fatalf("unknown scope=%+v", got)
	}
}

func TestUT109NoViewComponentGetsGenericRecaptureAndNotApplicableDoesNot(t *testing.T) {
	room := catalog.ExpectedRoom{Key: "bedroom", Name: "Quarto", Components: []catalog.ExpectedComponent{{Key: "ceiling", Label: "Teto"}, {Key: "closet", Label: "Armário", Views: []string{"frente"}}}}
	got := EvaluateCoverage([]catalog.ExpectedRoom{room}, []ComponentCoverageAnswer{{RequirementKey: "env:bedroom", ComponentKey: "closet", State: "NOT_APPLICABLE"}})
	if got.NotObserved != 1 || got.NotApplicable != 1 || len(got.Suggestions) != 1 || got.Suggestions[0].ComponentKey != "ceiling" || got.Suggestions[0].MissingView != "general" {
		t.Fatalf("coverage with generic suggestion=%+v", got)
	}
}

func containsText(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
