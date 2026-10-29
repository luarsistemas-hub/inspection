package core

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestUT030CompletenessAcceptsEvidenceAndPermittedImpossibility(t *testing.T) {
	requirements := []Requirement{{Key: "photo", Required: true, MinimumMedia: 1}, {Key: "reason", Required: true, ImpossibilityAllowed: true, MinimumMedia: 1}}
	result, err := Evaluate(requirements, []Answer{{RequirementKey: "photo", ReadyMedia: 1}, {RequirementKey: "reason", Impossibility: "sem acesso"}}, false)
	if err != nil || !result.Complete || result.Satisfied != 2 || result.Total != 2 {
		t.Fatalf("complete evidence rejected: %+v %v", result, err)
	}
}

func TestValidateDescriptionAllowsBlankDeclaredRequirementDescription(t *testing.T) {
	if err := ValidateDescription("   ", false); err != nil {
		t.Fatalf("blank optional description rejected: %v", err)
	}
	if err := ValidateDescription("<script>alert(1)</script>", false); err == nil {
		t.Fatal("unsafe optional description accepted")
	}
}

func TestUT031CompletenessNamesMissingAndBlockedRequirements(t *testing.T) {
	requirements := []Requirement{{Key: "photo", Required: true, MinimumMedia: 1}, {Key: "blocked", Required: true, MinimumMedia: 1}, {Key: "reason", Required: true, ImpossibilityAllowed: true, MinimumMedia: 1}}
	result, err := Evaluate(requirements, []Answer{{RequirementKey: "photo", ReadyMedia: 1}, {RequirementKey: "blocked", FailedMedia: 1}, {RequirementKey: "reason", Impossibility: "sem acesso"}}, false)
	if err == nil || result.Complete || len(result.Blocked) != 1 || result.Satisfied != 2 {
		t.Fatalf("unexpected completeness: %+v %v", result, err)
	}
	if result.Blocked[0] != "blocked" {
		t.Fatalf("blocked requirement not named: %+v", result)
	}
}

func TestCompletenessIgnoresOptionalRequirements(t *testing.T) {
	result, err := Evaluate([]Requirement{{Key: "required", Required: true, MinimumMedia: 1}, {Key: "optional", Required: false, MinimumMedia: 1}}, []Answer{{RequirementKey: "required", ReadyMedia: 1}}, false)
	if err != nil || !result.Complete || result.Total != 1 || result.Satisfied != 1 {
		t.Fatalf("optional requirement changed completeness: %+v %v", result, err)
	}
}

func TestEnvironmentCompletenessUT111UT112UT113UT114UT124(t *testing.T) {
	requirements := []Requirement{
		{Key: "env:expected", Required: true, Expected: true, ImpossibilityAllowed: true, MinimumMedia: 1},
		{Key: "env:added", Required: true, Expected: false, ImpossibilityAllowed: true, MinimumMedia: 1},
	}
	result, err := Evaluate(requirements, nil, false)
	if !apperror.Is(err, apperror.InvalidState) || result.Complete || len(result.PendingKeys) != 2 || result.PendingKeys[0] != "env:expected" {
		t.Fatalf("empty environment requirements = %+v, %v", result, err)
	}
	result, err = Evaluate(requirements, nil, true)
	if !apperror.Is(err, apperror.InvalidState) || result.Complete || len(result.PendingKeys) != 2 {
		t.Fatalf("incomplete confirmation bypassed expected environments: %+v, %v", result, err)
	}
	addedOnly := []Requirement{{Key: "env:new", Required: true, Expected: false, AddedDuringCapture: true, ImpossibilityAllowed: true, MinimumMedia: 1}}
	result, err = Evaluate(addedOnly, nil, true)
	if err != nil || result.Complete || len(result.PendingKeys) != 1 || result.PendingKeys[0] != "env:new" {
		t.Fatalf("confirmIncomplete incorrectly blocked only a newly added environment: %+v, %v", result, err)
	}
	result, err = Evaluate(requirements, []Answer{{RequirementKey: "env:expected", ReadyMedia: 1}, {RequirementKey: "env:added", Impossibility: "UNSAFE"}}, false)
	if err != nil || !result.Complete || result.Satisfied != 2 {
		t.Fatalf("captured and impossible environments = %+v, %v", result, err)
	}
	result, err = Evaluate([]Requirement{
		{Key: "env:retained", Required: true, Expected: true, MinimumMedia: 1},
		{Key: "env:removed", Required: true, AddedDuringCapture: true, Deleted: true, MinimumMedia: 1},
	}, []Answer{{RequirementKey: "env:retained", ReadyMedia: 1}}, false)
	if err != nil || !result.Complete || result.Total != 1 {
		t.Fatalf("removed environment affected completeness = %+v, %v", result, err)
	}
	result, err = Evaluate(nil, nil, false)
	if !apperror.Is(err, apperror.InvalidState) || len(result.PendingKeys) != 0 || err.Error() != "capture has no applicable requirements" {
		t.Fatalf("empty environment list = %+v, %v", result, err)
	}
	for _, ready := range []int{0, 1} {
		result, err = Evaluate([]Requirement{{Key: "env:photo", Required: true, ImpossibilityAllowed: true, MinimumMedia: 1}}, []Answer{{RequirementKey: "env:photo", ReadyMedia: ready}}, false)
		if ready == 0 && (err == nil || result.Complete || len(result.PendingKeys) != 1) {
			t.Fatalf("zero-photo environment = %+v, %v", result, err)
		}
		if ready == 1 && (err != nil || !result.Complete) {
			t.Fatalf("one-photo environment = %+v, %v", result, err)
		}
	}
	result, err = Evaluate([]Requirement{{Key: "env:locked", Required: true, ImpossibilityAllowed: true, MinimumMedia: 1}}, []Answer{{RequirementKey: "env:locked", Impossibility: "LOCKED"}}, false)
	if err != nil || !result.Complete {
		t.Fatalf("all-impossible environments = %+v, %v", result, err)
	}
}

func TestUT105DeclareEnvironmentAddsStableExpectedFields(t *testing.T) {
	db, service, tenant, responsibility := environmentFixture(t, "OPEN", nil)
	_ = db
	created, err := service.DeclareEnvironment(context.Background(), DeclareEnvironmentInput{TenantID: tenant, ResponsibilityID: responsibility, TypeCode: " bedroom ", Name: "  Quarto   2 ", ClientMutationID: "declare-105"})
	if err != nil || !strings.HasPrefix(created.Key, "env:new:") || !created.AddedDuringCapture || created.Expected || created.EnvironmentTypeCode != "BEDROOM" || created.EnvironmentName != "Quarto 2" {
		t.Fatalf("declared requirement = %+v, %v", created, err)
	}
}

func TestDeclareEnvironmentUsesPinnedTemplatePhotoLimits(t *testing.T) {
	db, service, tenant, responsibility := environmentFixture(t, "OPEN", nil)
	var draft database.CaptureDraft
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	definition := json.RawMessage(`{"schemaVersion":2,"captureLayout":"ENVIRONMENTS","environmentCapture":{"photo":{"minimum":3,"maximum":5},"video":{"maximumClips":2}}}`)
	if err := db.Model(&database.TemplateVersion{}).Where("tenant_id=? AND id=?", tenant, draft.TemplateVersionID).Update("definition_json", definition).Error; err != nil {
		t.Fatal(err)
	}
	created, err := service.DeclareEnvironment(context.Background(), DeclareEnvironmentInput{TenantID: tenant, ResponsibilityID: responsibility, TypeCode: "KITCHEN", Name: "Cozinha", ClientMutationID: "custom-capture-limits"})
	if err != nil {
		t.Fatal(err)
	}
	if created.MinimumMedia != 3 || created.MaximumMedia != 5 {
		t.Fatalf("declared requirement limits = %d..%d; want 3..5", created.MinimumMedia, created.MaximumMedia)
	}
	completeness, err := Evaluate([]Requirement{created}, []Answer{{RequirementKey: created.Key, ReadyMedia: 1}}, false)
	if err == nil || completeness.Complete || len(completeness.PendingKeys) != 1 || completeness.PendingKeys[0] != created.Key {
		t.Fatalf("one photo satisfied minimum of three: completeness=%+v err=%v", completeness, err)
	}
	for i := 1; i <= 6; i++ {
		media := database.MediaObject{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, ObjectKey: "qa/custom-limit-" + identity.NewID().String(), MediaKind: "PHOTO", RequirementKey: created.Key, ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 10, Status: "READY", Flags: json.RawMessage(`[]`), AttentionItems: json.RawMessage(`[]`)}
		if err := db.Create(&media).Error; err != nil {
			t.Fatal(err)
		}
		_, err := service.SaveMetadata(context.Background(), MetadataInput{TenantID: tenant, ResponsibilityID: responsibility, MediaID: media.ID, RequirementKey: created.Key, CaptureSource: "CAMERA"})
		if i <= 5 && err != nil {
			t.Fatalf("photo %d within custom maximum rejected: %v", i, err)
		}
		if i == 6 && (!apperror.Is(err, apperror.InvalidInput) || !strings.Contains(err.Error(), "LIMIT_MEDIA_PER_ENVIRONMENT")) {
			t.Fatalf("sixth photo = %v; want LIMIT_MEDIA_PER_ENVIRONMENT", err)
		}
	}
}

func TestB14DeclareEnvironmentReplayAtSixtyRequirementsIsIdempotent(t *testing.T) {
	db, service, tenant, responsibility := environmentFixture(t, "OPEN", nil)
	input := DeclareEnvironmentInput{TenantID: tenant, ResponsibilityID: responsibility, TypeCode: "BEDROOM", Name: "Quarto de cima", ClientMutationID: "replay-at-limit"}
	created, err := service.DeclareEnvironment(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	var draft database.CaptureDraft
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	var requirements []Requirement
	if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
		t.Fatal(err)
	}
	for len(requirements) < 60 {
		requirements = append(requirements, Requirement{Key: "env:" + identity.NewID().String(), EnvironmentTypeCode: "OTHER", EnvironmentName: "Extra " + strconv.Itoa(len(requirements))})
	}
	encoded, _ := json.Marshal(requirements)
	if err := db.Model(&draft).Update("requirements", encoded).Error; err != nil {
		t.Fatal(err)
	}
	replay, err := service.DeclareEnvironment(context.Background(), input)
	if err != nil || replay.Key != created.Key || replay.EnvironmentName != created.EnvironmentName {
		t.Fatalf("idempotent retry at limit returned %+v, want %+v: %v", replay, created, err)
	}
}

func TestDeclareEnvironmentReplayAfterRemovalKeepsOneRequirement(t *testing.T) {
	db, service, tenant, responsibility := environmentFixture(t, "OPEN", nil)
	if err := db.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Update("media_kind", "VIDEO").Error; err != nil {
		t.Fatal(err)
	}
	input := DeclareEnvironmentInput{TenantID: tenant, ResponsibilityID: responsibility, TypeCode: "BEDROOM", Name: "Quarto", ClientMutationID: "replay-after-removal"}
	created, err := service.DeclareEnvironment(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ManageEnvironment(context.Background(), ManageEnvironmentInput{TenantID: tenant, ResponsibilityID: responsibility, RequirementKey: created.Key, Remove: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeclareEnvironment(context.Background(), input); !apperror.Is(err, apperror.Conflict) {
		t.Fatalf("replay after removal = %v, want Conflict", err)
	}
	var draft database.CaptureDraft
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	var requirements []Requirement
	if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
		t.Fatal(err)
	}
	matches := 0
	for _, requirement := range requirements {
		if requirement.Key == created.Key {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("requirements with key %s = %d, want 1", created.Key, matches)
	}
}

func TestEnvironmentDeclarationUsesPinnedTemplateVersionAfterNewActivation(t *testing.T) {
	db, service, tenant, responsibility := environmentFixture(t, "OPEN", nil)
	var pinned database.TemplateVersion
	if err := db.Where("tenant_id=?", tenant).Take(&pinned).Error; err != nil {
		t.Fatal(err)
	}
	newer := database.TemplateVersion{ID: identity.NewID(), TenantID: tenant, TemplateID: pinned.TemplateID, VersionNumber: 2, SchemaVersion: 1, DefinitionJSON: json.RawMessage(`{}`), CanonicalDigest: "newer", Status: "ACTIVE", IdempotencyKey: "env-new-version", PublishedAt: time.Now().UTC(), CreatedBy: identity.NewID()}
	if err := db.Create(&newer).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeclareEnvironment(context.Background(), DeclareEnvironmentInput{TenantID: tenant, ResponsibilityID: responsibility, TypeCode: "BEDROOM", Name: "Quarto"}); err != nil {
		t.Fatalf("draft did not keep its pinned v2 schema version: %v", err)
	}
	var draft database.CaptureDraft
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Take(&draft).Error; err != nil || draft.TemplateVersionID != pinned.ID {
		t.Fatalf("draft template pin changed: draft=%+v err=%v", draft, err)
	}
}

func TestUT106UT107DeclareEnvironmentRejectsInvalidNamesWithoutMutation(t *testing.T) {
	for _, name := range []string{"", "\x00", strings.Repeat("x", 81)} {
		t.Run("invalid-name", func(t *testing.T) {
			db, service, tenant, responsibility := environmentFixture(t, "OPEN", nil)
			if _, err := service.DeclareEnvironment(context.Background(), DeclareEnvironmentInput{TenantID: tenant, ResponsibilityID: responsibility, TypeCode: "OTHER", Name: name}); !apperror.Is(err, apperror.InvalidInput) {
				t.Fatalf("invalid name %q accepted: %v", name, err)
			}
			var draft database.CaptureDraft
			if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Take(&draft).Error; err != nil {
				t.Fatal(err)
			}
			var reqs []Requirement
			_ = json.Unmarshal(draft.Requirements, &reqs)
			if len(reqs) != 0 {
				t.Fatalf("invalid declaration mutated requirements: %+v", reqs)
			}
		})
	}
}

func TestUT108UT109UT110DeclareEnvironmentRejectsDuplicateLimitAndClosedDraft(t *testing.T) {
	t.Run("duplicate-normalized", func(t *testing.T) {
		requirements, _ := json.Marshal([]Requirement{{Key: "env:old", EnvironmentTypeCode: "BEDROOM", EnvironmentName: "Quarto 2"}})
		_, service, tenant, responsibility := environmentFixture(t, "OPEN", requirements)
		_, err := service.DeclareEnvironment(context.Background(), DeclareEnvironmentInput{TenantID: tenant, ResponsibilityID: responsibility, TypeCode: "bedroom", Name: " quarto   2 "})
		if !apperror.Is(err, apperror.Conflict) {
			t.Fatalf("normalized duplicate accepted: %v", err)
		}
	})
	t.Run("sixty-first", func(t *testing.T) {
		requirements := make([]Requirement, 60)
		for i := range requirements {
			requirements[i] = Requirement{Key: "env:" + identity.NewID().String(), EnvironmentTypeCode: "BEDROOM", EnvironmentName: "Quarto " + strconv.Itoa(i)}
		}
		encoded, _ := json.Marshal(requirements)
		_, service, tenant, responsibility := environmentFixture(t, "OPEN", encoded)
		if _, err := service.DeclareEnvironment(context.Background(), DeclareEnvironmentInput{TenantID: tenant, ResponsibilityID: responsibility, TypeCode: "OTHER", Name: "Depósito"}); !apperror.Is(err, apperror.InvalidInput) || !strings.Contains(err.Error(), "LIMIT_ENVIRONMENTS") {
			t.Fatalf("61st environment returned %v", err)
		}
	})
	t.Run("closed-draft", func(t *testing.T) {
		_, service, tenant, responsibility := environmentFixture(t, "SUBMITTED", nil)
		if _, err := service.DeclareEnvironment(context.Background(), DeclareEnvironmentInput{TenantID: tenant, ResponsibilityID: responsibility, TypeCode: "BEDROOM", Name: "Quarto"}); !apperror.Is(err, apperror.InvalidState) {
			t.Fatalf("closed draft accepted declaration: %v", err)
		}
	})
}

func TestUT112UT116UT124DeclareImpossibilityPersistsFallbackAndCompletes(t *testing.T) {
	for _, reason := range []string{"LOCKED", "NO_ACCESS", "DOES_NOT_EXIST", "UNSAFE"} {
		t.Run(reason, func(t *testing.T) {
			requirements, _ := json.Marshal([]Requirement{{Key: "env:expected", Required: true, Expected: true, ImpossibilityAllowed: true, MinimumMedia: 1}})
			db, service, tenant, responsibility := environmentFixture(t, "OPEN", requirements)
			if err := service.DeclareImpossibilityWithCode(context.Background(), tenant, responsibility, "env:expected", reason, "", 1); err != nil {
				t.Fatal(err)
			}
			var draft database.CaptureDraft
			if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Take(&draft).Error; err != nil {
				t.Fatal(err)
			}
			var answer database.RequirementAnswer
			if err := db.Where("tenant_id=? AND draft_id=? AND requirement_key=?", tenant, draft.ID, "env:expected").Take(&answer).Error; err != nil {
				t.Fatal(err)
			}
			if answer.ImpossibilityReason != reason {
				t.Fatalf("stored impossibility=%q, want fallback code %q", answer.ImpossibilityReason, reason)
			}
			result, err := Evaluate([]Requirement{{Key: "env:expected", Required: true, Expected: true, ImpossibilityAllowed: true, MinimumMedia: 1}}, []Answer{{RequirementKey: "env:expected", Impossibility: answer.ImpossibilityReason}}, false)
			if err != nil || !result.Complete {
				t.Fatalf("persisted impossibility did not complete environment: %+v, %v", result, err)
			}
		})
	}
	for _, invalid := range []struct{ code, text string }{{"OTHER", ""}, {"RAIN", ""}} {
		_, service, tenant, responsibility := environmentFixture(t, "OPEN", json.RawMessage(`[{"key":"env:x","required":true,"impossibilityAllowed":true,"minimumMedia":1}]`))
		err := service.DeclareImpossibilityWithCode(context.Background(), tenant, responsibility, "env:x", invalid.code, invalid.text, 1)
		if !apperror.Is(err, apperror.InvalidInput) {
			t.Fatalf("invalid impossibility %q/%q returned %v", invalid.code, invalid.text, err)
		}
	}
}

func TestChangingEnvironmentImpossibilityReplacesPreviousFlag(t *testing.T) {
	requirements, _ := json.Marshal([]Requirement{{Key: "env:room", Required: true, ImpossibilityAllowed: true, MinimumMedia: 1}})
	db, service, tenant, responsibility := environmentFixture(t, "OPEN", requirements)
	var draft database.CaptureDraft
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	flags, _ := json.Marshal([]string{"IMPOSSIBILITY:LOCKED", "QUALITY_WARNING_IGNORED:DARK"})
	answer := database.RequirementAnswer{ID: identity.NewID(), TenantID: tenant, DraftID: draft.ID, RequirementKey: "env:room", MediaIDs: json.RawMessage(`[]`), Flags: flags, ImpossibilityReason: "LOCKED", Version: 1}
	if err := db.Create(&answer).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.DeclareImpossibilityWithCode(context.Background(), tenant, responsibility, "env:room", "UNSAFE", "", 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("id=?", answer.ID).Take(&answer).Error; err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(answer.Flags, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !containsFlag(got, "IMPOSSIBILITY:UNSAFE") || containsFlag(got, "IMPOSSIBILITY:LOCKED") || !containsFlag(got, "QUALITY_WARNING_IGNORED:DARK") {
		t.Fatalf("impossibility flags after correction = %v", got)
	}
}

func TestDeclareImpossibilityKeepsLegacyValidationOrderAndFields(t *testing.T) {
	db, service, tenant, responsibility := environmentFixture(t, "SUBMITTED", json.RawMessage(`[]`))
	var draft database.CaptureDraft
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	var appErr *apperror.Error
	err := service.DeclareImpossibilityWithCode(context.Background(), tenant, responsibility, "not-allowed", "OTHER", "", 0)
	if !errors.As(err, &appErr) || appErr.Code != apperror.InvalidInput || appErr.Field != "reason" {
		t.Fatalf("legacy blank reason on a closed draft = %v; want INVALID_INPUT reason", err)
	}
	requirements, _ := json.Marshal([]Requirement{{Key: "room", Required: true, ImpossibilityAllowed: true, MinimumMedia: 1}})
	openDB, openService, openTenant, openResponsibility := environmentFixture(t, "OPEN", requirements)
	var openDraft database.CaptureDraft
	if err := openDB.Where("tenant_id=? AND responsibility_id=?", openTenant, openResponsibility).Take(&openDraft).Error; err != nil {
		t.Fatal(err)
	}
	if err := openDB.Model(&database.TemplateVersion{}).Where("id=?", openDraft.TemplateVersionID).Update("schema_version", 1).Error; err != nil {
		t.Fatal(err)
	}
	err = openService.DeclareImpossibilityWithCode(context.Background(), openTenant, openResponsibility, "room", "OTHER", strings.Repeat("x", 2001), 0)
	if !errors.As(err, &appErr) || appErr.Code != apperror.InvalidInput || appErr.Field != "description" || appErr.Safe != "description is too long" {
		t.Fatalf("legacy oversized reason = %v; want INVALID_INPUT description", err)
	}
}

func TestUT117SaveReadyPhotoClearsImpossibilityAndPreservesOtherFlags(t *testing.T) {
	requirements, _ := json.Marshal([]Requirement{{Key: "env:room", Required: true, Expected: true, ImpossibilityAllowed: true, MinimumMedia: 1, MaximumMedia: 20}})
	db, service, tenant, responsibility := environmentFixture(t, "OPEN", requirements)
	var draft database.CaptureDraft
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	flags, _ := json.Marshal([]string{"IMPOSSIBILITY:LOCKED", "QUALITY_WARNING_IGNORED:DARK", "UNRELATED"})
	answer := database.RequirementAnswer{ID: identity.NewID(), TenantID: tenant, DraftID: draft.ID, RequirementKey: "env:room", MediaIDs: json.RawMessage(`[]`), Flags: flags, ImpossibilityReason: "LOCKED", Version: 2}
	media := database.MediaObject{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, ObjectKey: "qa/photo.jpg", MediaKind: "PHOTO", RequirementKey: "env:room", ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 10, Status: "READY", Flags: json.RawMessage(`[]`), AttentionItems: json.RawMessage(`[]`)}
	if err := db.Create(&answer).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&media).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveMetadata(context.Background(), MetadataInput{TenantID: tenant, ResponsibilityID: responsibility, MediaID: media.ID, RequirementKey: "env:room", CaptureSource: "CAMERA"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("id=?", answer.ID).Take(&answer).Error; err != nil {
		t.Fatal(err)
	}
	var got []string
	_ = json.Unmarshal(answer.Flags, &got)
	if answer.ImpossibilityReason != "" || containsFlag(got, "IMPOSSIBILITY:LOCKED") || !containsFlag(got, "QUALITY_WARNING_IGNORED:DARK") || !containsFlag(got, "UNRELATED") {
		t.Fatalf("answer flags/reason after READY photo = %+v, reason=%q", got, answer.ImpossibilityReason)
	}
}

func TestUT118RecordQualityWarningReplayDoesNotDuplicateOrIncrementVersion(t *testing.T) {
	requirements, _ := json.Marshal([]Requirement{{Key: "env:room", Required: true, Expected: true, ImpossibilityAllowed: true, MinimumMedia: 1}})
	db, service, tenant, responsibility := environmentFixture(t, "OPEN", requirements)
	var draft database.CaptureDraft
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	answer := database.RequirementAnswer{ID: identity.NewID(), TenantID: tenant, DraftID: draft.ID, RequirementKey: "env:room", MediaIDs: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), Version: 1}
	if err := db.Create(&answer).Error; err != nil {
		t.Fatal(err)
	}
	input := RecordQualityWarningInput{TenantID: tenant, ResponsibilityID: responsibility, RequirementKey: "env:room", Code: "DARK", Ignored: true}
	for i := 0; i < 2; i++ {
		if _, err := service.RecordQualityWarning(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Where("id=?", answer.ID).Take(&answer).Error; err != nil {
		t.Fatal(err)
	}
	var got []string
	_ = json.Unmarshal(answer.Flags, &got)
	if answer.Version != 1 || len(got) != 1 || got[0] != "QUALITY_WARNING_IGNORED:DARK" {
		t.Fatalf("quality warning replay changed state: version=%d flags=%v", answer.Version, got)
	}
}

func TestV1SaveMetadataAndImpossibilityPreserveLegacyFlagsContract(t *testing.T) {
	requirements := json.RawMessage(`[{"key":"room","required":true,"impossibilityAllowed":true,"minimumMedia":1,"maximumMedia":20}]`)
	db, service, tenant, responsibility := environmentFixture(t, "OPEN", requirements)
	var draft database.CaptureDraft
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.TemplateVersion{}).Where("id=?", draft.TemplateVersionID).Update("schema_version", 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: tenant, DraftID: draft.ID, RequirementKey: "room", MediaIDs: json.RawMessage(`[]`), Flags: json.RawMessage(`[` + `"QUALITY_WARNING_IGNORED:DARK"` + `,` + `"UNRELATED"` + `]`), ImpossibilityReason: "legacy reason", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	media := database.MediaObject{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, ObjectKey: "qa/v1-photo.jpg", MediaKind: "PHOTO", RequirementKey: "room", ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 10, Status: "READY", Flags: json.RawMessage(`[]`), AttentionItems: json.RawMessage(`[]`)}
	if err := db.Create(&media).Error; err != nil {
		t.Fatal(err)
	}
	var appErr *apperror.Error
	if err := service.DeclareImpossibilityWithCode(context.Background(), tenant, responsibility, "room", "OTHER", "", 0); !errors.As(err, &appErr) || appErr.Field != "reason" {
		t.Fatalf("v1 blank impossibility reason error=%v; want field reason", err)
	}
	updated, err := service.SaveMetadata(context.Background(), MetadataInput{TenantID: tenant, ResponsibilityID: responsibility, MediaID: media.ID, RequirementKey: "room", CaptureSource: "CAMERA"})
	if err != nil {
		t.Fatal(err)
	}
	var answer database.RequirementAnswer
	if err := db.Where("tenant_id=? AND draft_id=? AND requirement_key=?", tenant, draft.ID, "room").Take(&answer).Error; err != nil {
		t.Fatal(err)
	}
	if answer.ImpossibilityReason != "legacy reason" || string(answer.Flags) != string(updated.Flags) {
		t.Fatalf("v1 answer contract changed: answer flags=%s reason=%q media flags=%s", answer.Flags, answer.ImpossibilityReason, updated.Flags)
	}
}

func TestUT119RecordQualityWarningRejectsUnknownCode(t *testing.T) {
	_, service, tenant, responsibility := environmentFixture(t, "OPEN", json.RawMessage(`[]`))
	_, err := service.RecordQualityWarning(context.Background(), RecordQualityWarningInput{TenantID: tenant, ResponsibilityID: responsibility, RequirementKey: "env:room", Code: "NOISY", Ignored: true})
	if !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("unknown quality warning code accepted: %v", err)
	}
}

func TestUT121SubmitReportsEnvironmentKeyForUploadingPhoto(t *testing.T) {
	requirements, _ := json.Marshal([]Requirement{{Key: "env:room", Required: true, Expected: true, ImpossibilityAllowed: true, MinimumMedia: 1, MaximumMedia: 20}})
	db, service, tenant, responsibility := environmentFixture(t, "OPEN", requirements)
	if err := db.Create(&database.MediaObject{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, ObjectKey: "qa/uploading.jpg", MediaKind: "PHOTO", RequirementKey: "env:room", ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 10, Status: "UPLOADING", Flags: json.RawMessage(`[]`), AttentionItems: json.RawMessage(`[]`)}).Error; err != nil {
		t.Fatal(err)
	}
	_, err := service.Submit(context.Background(), tenant, responsibility, true)
	if !apperror.Is(err, apperror.InvalidState) || !strings.Contains(err.Error(), "UPLOADS_IN_PROGRESS") {
		t.Fatalf("uploading photo submit returned %v", err)
	}
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Field != "env:room" {
		t.Fatalf("uploading photo error field=%+v", appErr)
	}
}

func TestUT125FinalizePhotoV2EmitsEnvironmentKeysAndFinalizes(t *testing.T) {
	requirements, _ := json.Marshal([]Requirement{{Key: "env:room", Required: true, Expected: true, ImpossibilityAllowed: true, MinimumMedia: 1, MaximumMedia: 20}})
	db, service, tenant, responsibility := environmentFixture(t, "OPEN", requirements)
	mediaID := identity.NewID()
	if err := db.Create(&database.MediaObject{ID: mediaID, TenantID: tenant, ResponsibilityID: responsibility, ObjectKey: "qa/ready.jpg", MediaKind: "PHOTO", RequirementKey: "env:room", ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 10, Status: "READY", Flags: json.RawMessage(`[]`), AttentionItems: json.RawMessage(`[]`)}).Error; err != nil {
		t.Fatal(err)
	}
	var draft database.CaptureDraft
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	mediaIDs, _ := json.Marshal([]identity.ID{mediaID})
	if err := db.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: tenant, DraftID: draft.ID, RequirementKey: "env:room", MediaIDs: mediaIDs, Flags: json.RawMessage(`[]`), Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	inspectionID := identity.NewID()
	if err := db.Create(&database.Responsibility{ID: responsibility, TenantID: tenant, InspectionID: inspectionID, ParticipantID: identity.NewID(), Status: "IN_PROGRESS", Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	finalized := false
	service.Finalizer = submissionFinalizerFunc(func(_ context.Context, _ *gorm.DB, _ database.CaptureDraft, submitted database.SubmissionVersion) error {
		finalized = submitted.Complete
		return nil
	})
	submission, err := service.Submit(context.Background(), tenant, responsibility, false)
	if err != nil || !finalized {
		t.Fatalf("v2 finalize submission=%+v finalized=%v err=%v", submission, finalized, err)
	}
	var outbox database.OutboxIntent
	if err := db.Where("tenant_id=? AND type='capture.submitted.v1'", tenant).Take(&outbox).Error; err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Payload struct {
			RequirementKeys []string `json:"requirementKeys"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(outbox.Payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Payload.RequirementKeys) != 1 || envelope.Payload.RequirementKeys[0] != "env:room" {
		t.Fatalf("v2 submitted event keys=%v", envelope.Payload.RequirementKeys)
	}
}

func TestV1PhotoSubmissionOmitsRequirementKeysFromOutbox(t *testing.T) {
	requirements, _ := json.Marshal([]Requirement{{Key: "overview", Required: true, MinimumMedia: 1, MaximumMedia: 2}})
	db, service, tenant, responsibility := environmentFixture(t, "OPEN", requirements)
	if err := db.Model(&database.TemplateVersion{}).Where("tenant_id=?", tenant).Update("schema_version", 1).Error; err != nil {
		t.Fatal(err)
	}
	var draft database.CaptureDraft
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	mediaID := identity.NewID()
	if err := db.Create(&database.MediaObject{ID: mediaID, TenantID: tenant, ResponsibilityID: responsibility, ObjectKey: "qa/v1.jpg", MediaKind: "PHOTO", RequirementKey: "overview", ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 10, Status: "READY", Flags: json.RawMessage(`[]`), AttentionItems: json.RawMessage(`[]`)}).Error; err != nil {
		t.Fatal(err)
	}
	mediaIDs, _ := json.Marshal([]identity.ID{mediaID})
	if err := db.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: tenant, DraftID: draft.ID, RequirementKey: "overview", MediaIDs: mediaIDs, Flags: json.RawMessage(`[]`), Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.Responsibility{ID: responsibility, TenantID: tenant, InspectionID: identity.NewID(), ParticipantID: identity.NewID(), Status: "IN_PROGRESS", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.Submit(context.Background(), tenant, responsibility, false); err != nil {
		t.Fatal(err)
	}
	var outbox database.OutboxIntent
	if err := db.Where("tenant_id=? AND type='capture.submitted.v1'", tenant).Take(&outbox).Error; err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Payload map[string]json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(outbox.Payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if _, exists := envelope.Payload["requirementKeys"]; exists {
		t.Fatal("v1 capture submission added requirementKeys")
	}
}

func TestUT115SaveAndSubmitTwentyEnvironmentPhotosAndRejectTwentyFirst(t *testing.T) {
	t.Run("20 photos save and submit", func(t *testing.T) {
		requirements, _ := json.Marshal([]Requirement{{Key: "env:room", Required: true, Expected: true, ImpossibilityAllowed: true, MinimumMedia: 1, MaximumMedia: 20}})
		db, service, tenant, responsibility := environmentFixture(t, "OPEN", requirements)
		if err := db.Create(&database.Responsibility{ID: responsibility, TenantID: tenant, InspectionID: identity.NewID(), ParticipantID: identity.NewID(), Status: "IN_PROGRESS", Version: 1}).Error; err != nil {
			t.Fatal(err)
		}
		for range 20 {
			media := database.MediaObject{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, ObjectKey: "qa/" + identity.NewID().String(), MediaKind: "PHOTO", RequirementKey: "env:room", ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 10, Status: "READY", Flags: json.RawMessage(`[]`), AttentionItems: json.RawMessage(`[]`)}
			if err := db.Create(&media).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := service.SaveMetadata(context.Background(), MetadataInput{TenantID: tenant, ResponsibilityID: responsibility, MediaID: media.ID, RequirementKey: "env:room", CaptureSource: "CAMERA"}); err != nil {
				t.Fatalf("photo %s at the inclusive limit was rejected: %v", media.ID, err)
			}
		}
		if _, err := service.Submit(context.Background(), tenant, responsibility, false); err != nil {
			t.Fatalf("20 ready photos did not submit: %v", err)
		}
	})
	t.Run("21st photo fails save and submit", func(t *testing.T) {
		requirements, _ := json.Marshal([]Requirement{{Key: "env:room", Required: true, Expected: true, ImpossibilityAllowed: true, MinimumMedia: 1, MaximumMedia: 20}})
		db, service, tenant, responsibility := environmentFixture(t, "OPEN", requirements)
		if err := db.Create(&database.Responsibility{ID: responsibility, TenantID: tenant, InspectionID: identity.NewID(), ParticipantID: identity.NewID(), Status: "IN_PROGRESS", Version: 1}).Error; err != nil {
			t.Fatal(err)
		}
		var ids []identity.ID
		for range 20 {
			media := database.MediaObject{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, ObjectKey: "qa/" + identity.NewID().String(), MediaKind: "PHOTO", RequirementKey: "env:room", ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 10, Status: "READY", Flags: json.RawMessage(`[]`), AttentionItems: json.RawMessage(`[]`)}
			if err := db.Create(&media).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := service.SaveMetadata(context.Background(), MetadataInput{TenantID: tenant, ResponsibilityID: responsibility, MediaID: media.ID, RequirementKey: "env:room", CaptureSource: "CAMERA"}); err != nil {
				t.Fatalf("photo within the limit was rejected: %v", err)
			}
			ids = append(ids, media.ID)
		}
		twentyFirst := database.MediaObject{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, ObjectKey: "qa/21st", MediaKind: "PHOTO", RequirementKey: "env:room", ContentType: "image/jpeg", SHA256: strings.Repeat("b", 64), SizeBytes: 10, Status: "READY", Flags: json.RawMessage(`[]`), AttentionItems: json.RawMessage(`[]`)}
		if err := db.Create(&twentyFirst).Error; err != nil {
			t.Fatal(err)
		}
		ids = append(ids, twentyFirst.ID)
		var draft database.CaptureDraft
		if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Take(&draft).Error; err != nil {
			t.Fatal(err)
		}
		mediaIDs, _ := json.Marshal(ids)
		answer := database.RequirementAnswer{ID: identity.NewID(), TenantID: tenant, DraftID: draft.ID, RequirementKey: "env:room", MediaIDs: mediaIDs, Flags: json.RawMessage(`[]`), Version: 1}
		if err := db.Create(&answer).Error; err != nil {
			t.Fatal(err)
		}
		_, saveErr := service.SaveMetadata(context.Background(), MetadataInput{TenantID: tenant, ResponsibilityID: responsibility, MediaID: twentyFirst.ID, RequirementKey: "env:room", CaptureSource: "CAMERA"})
		if !apperror.Is(saveErr, apperror.InvalidInput) || !strings.Contains(saveErr.Error(), "LIMIT_MEDIA_PER_ENVIRONMENT") {
			t.Fatalf("save at 21 photos returned %v", saveErr)
		}
		_, submitErr := service.Submit(context.Background(), tenant, responsibility, true)
		if !apperror.Is(submitErr, apperror.InvalidInput) || !strings.Contains(submitErr.Error(), "LIMIT_MEDIA_PER_ENVIRONMENT") {
			t.Fatalf("submit at 21 photos returned %v", submitErr)
		}
	})
}

func environmentFixture(t *testing.T, status string, requirements json.RawMessage) (*gorm.DB, Service, identity.ID, identity.ID) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"capture", "templates", "media", "invitations", "messaging", "inspections"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.CaptureDraft{}, &database.TemplateVersion{}, &database.RequirementAnswer{}, &database.SubmissionVersion{}, &database.MediaObject{}, &database.Invitation{}, &database.ExternalSession{}, &database.ProcessingAcceptance{}, &database.OutboxIntent{}, &database.Responsibility{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	if requirements == nil {
		requirements = json.RawMessage(`[]`)
	}
	tenant, responsibility, templateID := identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	if err := db.Create(&database.TemplateVersion{ID: templateID, TenantID: tenant, TemplateID: identity.NewID(), VersionNumber: 1, SchemaVersion: 2, DefinitionJSON: json.RawMessage(`{}`), CanonicalDigest: "digest", Status: "PUBLISHED", IdempotencyKey: "env-test", PublishedAt: now, CreatedBy: identity.NewID()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", MediaKind: "PHOTO", TemplateVersionID: templateID, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: requirements, Status: status, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.ProcessingAcceptance{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, DisclosureVersion: "v1", PhotoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	service := Service{DB: db, Now: func() time.Time { return now }, Within: func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(db) }}
	return db, service, tenant, responsibility
}

func containsFlag(flags []string, wanted string) bool {
	for _, flag := range flags {
		if flag == wanted {
			return true
		}
	}
	return false
}

func TestUT060GPSAcceptsExactAccuracyAndTimeBoundary(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	lat, lon := -27.59, -48.55
	decision, err := EvaluateGPS(GPSPolicy{Required: true, AssetLatitude: &lat, AssetLongitude: &lon, GeofenceMeters: 150}, &GPSReading{Latitude: lat, Longitude: lon, AccuracyMeters: 50, CapturedAt: now.Add(60 * time.Second)}, now)
	if err != nil || !decision.Accepted || decision.Blocked || decision.DistanceMeters != 0 {
		t.Fatalf("boundary rejected: %+v %v", decision, err)
	}
}

func TestUT061GPSFlagsInvalidReadingsWithoutBlockingOptionalPolicy(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	decision, _ := EvaluateGPS(GPSPolicy{Required: false}, nil, now)
	if !decision.Accepted || decision.Blocked || len(decision.Flags) == 0 {
		t.Fatalf("optional GPS blocked: %+v", decision)
	}
	for _, reading := range []*GPSReading{{Latitude: 91, CapturedAt: now}, {Latitude: 0, Longitude: 0, AccuracyMeters: 51, CapturedAt: now}, {Latitude: 0, Longitude: 0, AccuracyMeters: 1, CapturedAt: now.Add(61 * time.Second)}} {
		decision, err := EvaluateGPS(GPSPolicy{Required: true}, reading, now)
		if err != nil || !decision.Blocked || decision.Accepted || len(decision.Flags) != 1 {
			t.Fatalf("invalid required GPS was not flagged: %+v %v", decision, err)
		}
	}
}

func TestUT063AtomicAdmission(t *testing.T) {
	admission := NewAdmission(10, 3)
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	releases := make([]func(), 0, 3)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := admission.Reserve("tenant")
			if err == nil {
				mu.Lock()
				accepted++
				releases = append(releases, release)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if accepted != 3 {
		t.Fatalf("tenant limit overshot: %d", accepted)
	}
	for _, release := range releases {
		release()
		release()
	}
	if release, err := admission.Reserve("tenant"); err != nil {
		t.Fatalf("released capacity was not restored: %v", err)
	} else {
		release()
	}
}
