//go:build integration

package core

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestIT025IT053DescriptiveTablesEnforceTenantRLS(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	a, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	answer := database.ComponentAnswer{ID: identity.NewID(), TenantID: b.TenantID, DraftID: b.DraftID, RequirementKey: "env:kitchen", ComponentKey: "floor", State: "NOT_OBSERVED", EvidenceIDs: []byte("[]"), Version: 1, CreatedAt: now, UpdatedAt: now}
	record := database.HandoverRecord{ID: identity.NewID(), TenantID: b.TenantID, DraftID: b.DraftID, RecordKey: "meter", Kind: "METER", Payload: []byte(`{"reading":123.4,"unit":"kWh","date":"2026-11-03"}`), MediaIDs: []byte("[]"), Declarant: "Participante", Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := h.WithinTenant(ctx, b.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&answer).Error; err != nil {
			return err
		}
		return tx.Create(&record).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, a.TenantID, func(tx *gorm.DB) error {
		var answers, records int64
		if err := tx.Model(&database.ComponentAnswer{}).Where("tenant_id=? AND id=?", b.TenantID, answer.ID).Count(&answers).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.HandoverRecord{}).Where("tenant_id=? AND id=?", b.TenantID, record.ID).Count(&records).Error; err != nil {
			return err
		}
		if answers != 0 || records != 0 {
			t.Fatalf("tenant A read tenant B rows: answers=%d records=%d", answers, records)
		}
		answerUpdate := tx.Model(&database.ComponentAnswer{}).Where("tenant_id=? AND id=?", b.TenantID, answer.ID).Update("state", "OBSERVED")
		if answerUpdate.Error != nil {
			return answerUpdate.Error
		}
		recordUpdate := tx.Model(&database.HandoverRecord{}).Where("tenant_id=? AND id=?", b.TenantID, record.ID).Update("declarant", "Alterado")
		if recordUpdate.Error != nil {
			return recordUpdate.Error
		}
		if answerUpdate.RowsAffected != 0 || recordUpdate.RowsAffected != 0 {
			t.Fatalf("tenant A changed tenant B rows: answers=%d records=%d", answerUpdate.RowsAffected, recordUpdate.RowsAffected)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestUT017UT026CoverageRevalidatesEvidenceAndRoomImpossibility(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture := seedDescriptiveCaptureFixture(t, ctx, h, false)
	service := Service{DB: h.DB, Within: h.WithinTenant}
	now := time.Now().UTC()
	abortedID := identity.NewID()
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.MediaObject{ID: abortedID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "ut017-aborted/" + abortedID.String(), IdempotencyKey: "ut017-" + identity.NewID().String(), ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 1, Status: "ABORTED", MediaKind: "PHOTO", RequirementKey: "env:kitchen", Flags: []byte("[]"), AttentionItems: []byte("[]"), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.ComponentAnswer{ID: identity.NewID(), TenantID: fixture.TenantID, DraftID: fixture.DraftID, RequirementKey: "env:kitchen", ComponentKey: "floor", ViewKey: "geral", State: "OBSERVED", EvidenceIDs: mustJSON([]identity.ID{abortedID}), Version: 1, CreatedAt: now, UpdatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	coverage, err := service.Coverage(ctx, fixture.TenantID, fixture.ResponsibilityID)
	if err != nil || coverage.NotObserved != 1 || coverage.FullyAssessed != 0 {
		t.Fatalf("UT-017 counted aborted evidence: %+v err=%v", coverage, err)
	}
	if err := service.DeclareImpossibility(ctx, fixture.TenantID, fixture.ResponsibilityID, "env:kitchen", "Acesso bloqueado", 1); err != nil {
		t.Fatal(err)
	}
	coverage, err = service.Coverage(ctx, fixture.TenantID, fixture.ResponsibilityID)
	if err != nil || coverage.Impossible != 1 || len(coverage.Suggestions) != 0 {
		t.Fatalf("room impossibility was not reflected in component coverage: %+v err=%v", coverage, err)
	}

	other := seedDescriptiveCaptureFixture(t, ctx, h, false)
	if err := h.WithinTenant(ctx, other.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.InspectionScopeSnapshot{}).Where("tenant_id=? AND inspection_id=?", other.TenantID, other.InspectionID).Update("document", []byte(`{"rooms":[],"handoverRecords":[{"key":"foreign-meter","kind":"METER","required":true}]}`)).Error
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpsertHandoverRecord(ctx, HandoverInput{TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, RecordKey: "foreign-meter", Kind: "METER", Payload: json.RawMessage(`{"meterType":"ENERGY","identifier":"x","reading":1,"unit":"kWh","date":"2026-11-03"}`)}); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("UT-026 accepted a record key belonging to another capture: %v", err)
	}
	legacy := seedDescriptiveCaptureFixture(t, ctx, h, false)
	if err := h.WithinTenant(ctx, legacy.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND inspection_id=?", legacy.TenantID, legacy.InspectionID).Delete(&database.InspectionScopeSnapshot{}).Error
	}); err != nil {
		t.Fatal(err)
	}
	unknown, err := service.Coverage(ctx, legacy.TenantID, legacy.ResponsibilityID)
	if err != nil || !unknown.UnknownScope || unknown.Expected != 0 {
		t.Fatalf("UT-109 legacy inspection coverage=%+v err=%v", unknown, err)
	}
}

func TestIT004IT005IT006IT007IT033IT034IT035IT036IT037IT038IT026IT054DescriptiveCapture(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture := seedDescriptiveCaptureFixture(t, ctx, h, false)
	service := Service{DB: h.DB, Within: h.WithinTenant}
	answer, err := service.RecordComponentAnswer(ctx, ComponentAnswerInput{TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, RequirementKey: "env:kitchen", ComponentKey: "floor", State: "NOT_OBSERVED"})
	if err != nil || answer.Version != 1 {
		t.Fatalf("IT-005 initial answer=%+v err=%v", answer, err)
	}
	retry, err := service.RecordComponentAnswer(ctx, ComponentAnswerInput{TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, RequirementKey: "env:kitchen", ComponentKey: "floor", State: "NOT_OBSERVED"})
	if err != nil || retry.ID != answer.ID || retry.Version != answer.Version {
		t.Fatalf("IT-005 idempotent retry=%+v err=%v", retry, err)
	}
	answer, err = service.RecordComponentAnswer(ctx, ComponentAnswerInput{TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, RequirementKey: "env:kitchen", ComponentKey: "floor", State: "NOT_OBSERVED", Reason: "Sem visibilidade", ExpectedVersion: 1})
	if err != nil || answer.Version != 2 {
		t.Fatalf("IT-005 versioned answer=%+v err=%v", answer, err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var events int64
		if err := tx.Model(&database.AuditEvent{}).Where("tenant_id=? AND action=? AND target_id=?", fixture.TenantID, "capture.component_answer_recorded", answer.ID.String()).Count(&events).Error; err != nil {
			return err
		}
		if events != 2 {
			t.Fatalf("component answer emitted %d audit events, want one for each revision", events)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecordComponentAnswer(ctx, ComponentAnswerInput{TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, RequirementKey: "env:kitchen", ComponentKey: "floor", State: "NOT_APPLICABLE", Reason: "Componente ausente", ExpectedVersion: 1}); !apperror.Is(err, apperror.Conflict) {
		t.Fatalf("IT-034 stale answer error=%v", err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var foreign database.MediaObject
		foreign = database.MediaObject{ID: identity.NewID(), TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "it033-foreign-room/" + fixture.TenantID.String(), IdempotencyKey: "it033-" + identity.NewID().String(), ContentType: "image/png", SHA256: strings.Repeat("a", 64), SizeBytes: 1, Status: "READY", MediaKind: "PHOTO", RequirementKey: "env:hall", Flags: []byte("[]"), AttentionItems: []byte("[]"), CreatedAt: time.Now().UTC()}
		return tx.Create(&foreign).Error
	}); err != nil {
		t.Fatal(err)
	}
	var foreignRoomMedia database.MediaObject
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND responsibility_id=? AND requirement_key='env:hall'", fixture.TenantID, fixture.ResponsibilityID).Take(&foreignRoomMedia).Error
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecordComponentAnswer(ctx, ComponentAnswerInput{TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, RequirementKey: "env:kitchen", ComponentKey: "floor", State: "OBSERVED", EvidenceIDs: []identity.ID{foreignRoomMedia.ID}, ExpectedVersion: 2}); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("IT-033 foreign-room evidence error=%v", err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error { return tx.Delete(&foreignRoomMedia).Error }); err != nil {
		t.Fatal(err)
	}
	boot, err := service.Load(ctx, fixture.TenantID, fixture.ResponsibilityID)
	if err != nil || len(boot.ComponentAnswers) != 1 || boot.ComponentAnswers[0].Version != 2 || len(boot.Scope) == 0 || boot.Coverage.Expected != 1 || boot.Coverage.UnknownScope {
		t.Fatalf("IT-004 bootstrap lost pinned scope/answer: %+v err=%v", boot, err)
	}
	screenedMediaID := identity.NewID()
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		screened := database.MediaObject{ID: screenedMediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "it-screened-coverage/" + screenedMediaID.String(), IdempotencyKey: "it-screened-" + screenedMediaID.String(), ContentType: "image/png", SHA256: strings.Repeat("c", 64), SizeBytes: 1, Status: "SCREENED", MediaKind: "PHOTO", RequirementKey: "env:kitchen", Flags: []byte("[]"), AttentionItems: []byte("[]"), CreatedAt: time.Now().UTC()}
		if err := tx.Create(&screened).Error; err != nil {
			return err
		}
		return tx.Model(&database.ComponentAnswer{}).Where("tenant_id=? AND draft_id=? AND requirement_key=? AND component_key=?", fixture.TenantID, fixture.DraftID, "env:kitchen", "floor").Updates(map[string]any{"state": "OBSERVED", "evidence_ids": mustJSON([]identity.ID{screened.ID})}).Error
	}); err != nil {
		t.Fatal(err)
	}
	screenedCoverage, err := service.Coverage(ctx, fixture.TenantID, fixture.ResponsibilityID)
	if err != nil || screenedCoverage.FullyAssessed != 0 || screenedCoverage.NotObserved != 1 {
		t.Fatalf("SCREENED media counted as coverage: %+v err=%v", screenedCoverage, err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error { return tx.Delete(&database.MediaObject{}, "id=?", screenedMediaID).Error }); err != nil {
		t.Fatal(err)
	}
	var pinned struct {
		Rooms []struct {
			Key        string `json:"key"`
			Components []struct {
				Key   string   `json:"key"`
				Views []string `json:"views"`
			} `json:"components"`
		} `json:"rooms"`
		HandoverRecords []struct {
			Key string `json:"key"`
		} `json:"handoverRecords"`
	}
	if err := json.Unmarshal(boot.Scope, &pinned); err != nil || len(pinned.Rooms) != 1 || pinned.Rooms[0].Key != "kitchen" || len(pinned.Rooms[0].Components) != 1 || pinned.Rooms[0].Components[0].Key != "floor" || strings.Join(pinned.Rooms[0].Components[0].Views, ",") != "geral,detalhe" || len(pinned.HandoverRecords) != 1 || pinned.HandoverRecords[0].Key != "meter" {
		t.Fatalf("IT-004 returned scope differs from pinned scope: %+v err=%v", pinned, err)
	}
	secondEvidence := database.MediaObject{ID: identity.NewID(), TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "it035-second-evidence/" + fixture.TenantID.String(), IdempotencyKey: "it035-" + identity.NewID().String(), ContentType: "image/png", SHA256: strings.Repeat("f", 64), SizeBytes: 1, Status: "READY", MediaKind: "PHOTO", RequirementKey: "env:kitchen", Flags: []byte("[]"), AttentionItems: []byte("[]"), CreatedAt: time.Now().UTC()}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error { return tx.Create(&secondEvidence).Error }); err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.RequirementAnswer{}).Where("tenant_id=? AND draft_id=? AND requirement_key=?", fixture.TenantID, fixture.DraftID, "env:kitchen").Update("media_ids", mustJSON([]identity.ID{fixture.MediaID, secondEvidence.ID})).Error
	}); err != nil {
		t.Fatal(err)
	}
	answer, err = service.RecordComponentAnswer(ctx, ComponentAnswerInput{TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, RequirementKey: "env:kitchen", ComponentKey: "floor", State: "OBSERVED", EvidenceIDs: []identity.ID{fixture.MediaID, secondEvidence.ID}, ExpectedVersion: 2})
	if err != nil || answer.Version != 3 {
		t.Fatalf("IT-035 observed answer=%+v err=%v", answer, err)
	}
	answerRetry, err := service.RecordComponentAnswer(ctx, ComponentAnswerInput{TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, RequirementKey: "env:kitchen", ComponentKey: "floor", State: "OBSERVED", EvidenceIDs: []identity.ID{fixture.MediaID, secondEvidence.ID}, ExpectedVersion: 2})
	if err != nil || answerRetry.ID != answer.ID || answerRetry.Version != answer.Version {
		t.Fatalf("IT-035 JSONB answer retry=%+v err=%v", answerRetry, err)
	}
	if _, err := service.RecordComponentAnswer(ctx, ComponentAnswerInput{TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, RequirementKey: "env:kitchen", ComponentKey: "floor", State: "NOT_OBSERVED", Reason: "A vista ainda não foi registrada", ExpectedVersion: 3}); err != nil {
		t.Fatal(err)
	}
	meter := HandoverInput{TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, RecordKey: "meter", Kind: "METER", Declarant: "Participante", Payload: json.RawMessage(`{"meterType":"ENERGY","identifier":"A123","reading":123.4,"unit":"kWh","date":"2026-11-03"}`), MediaIDs: []identity.ID{fixture.MediaID}}
	meter.MediaIDs = []identity.ID{fixture.MediaID, secondEvidence.ID}
	record, err := service.UpsertHandoverRecord(ctx, meter)
	if err != nil || record.Version != 1 || record.Declarant != "Participante" {
		t.Fatalf("IT-006 handover record=%+v err=%v", record, err)
	}
	recordRetry, err := service.UpsertHandoverRecord(ctx, meter)
	if err != nil || recordRetry.ID != record.ID || recordRetry.Version != record.Version {
		t.Fatalf("IT-035 JSONB handover retry=%+v err=%v", recordRetry, err)
	}
	if len(record.MediaIDs) == 0 || !strings.Contains(string(record.Payload), "unit") {
		t.Fatalf("IT-006 omitted reading unit or linked evidence: %+v", record)
	}
	invalidMeter := meter
	invalidMeter.Payload = json.RawMessage(`{"meterType":"ENERGY","identifier":"A123","reading":-1,"unit":"kWh","date":"2026-11-03"}`)
	if _, err := service.UpsertHandoverRecord(ctx, invalidMeter); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("IT-035 negative meter reading error=%v", err)
	}
	var unchanged database.HandoverRecord
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error { return tx.Where("id=?", record.ID).Take(&unchanged).Error }); err != nil || string(unchanged.Payload) != string(record.Payload) {
		var payload map[string]any
		if err == nil {
			_ = json.Unmarshal(unchanged.Payload, &payload)
		}
		if err != nil || unchanged.Version != 1 || payload["reading"] != 123.4 {
			t.Fatalf("IT-035 invalid reading changed prior record: %+v err=%v", unchanged, err)
		}
	}
	foreignResponsibilityID := identity.NewID()
	foreignMedia := database.MediaObject{ID: identity.NewID(), TenantID: fixture.TenantID, ResponsibilityID: foreignResponsibilityID, ObjectKey: "it036-other-inspection/" + fixture.TenantID.String(), IdempotencyKey: "it036-" + identity.NewID().String(), ContentType: "image/png", SHA256: strings.Repeat("b", 64), SizeBytes: 1, Status: "READY", MediaKind: "PHOTO", RequirementKey: "env:kitchen", Flags: []byte("[]"), AttentionItems: []byte("[]"), CreatedAt: time.Now().UTC()}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.Responsibility{ID: foreignResponsibilityID, TenantID: fixture.TenantID, InspectionID: identity.NewID(), Status: "PENDING", Version: 1, CreatedAt: time.Now().UTC()}).Error; err != nil {
			return err
		}
		return tx.Create(&foreignMedia).Error
	}); err != nil {
		t.Fatal(err)
	}
	foreignRecord := meter
	foreignRecord.Payload = json.RawMessage(`{"meterType":"ENERGY","identifier":"A123","reading":130,"unit":"kWh","date":"2026-11-03"}`)
	foreignRecord.MediaIDs = []identity.ID{foreignMedia.ID}
	if _, err := service.UpsertHandoverRecord(ctx, foreignRecord); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("IT-036 foreign-inspection media error=%v", err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.RequirementAnswer{}).Where("tenant_id=? AND draft_id=? AND requirement_key=?", fixture.TenantID, fixture.DraftID, "env:kitchen").Update("media_ids", mustJSON([]identity.ID{fixture.MediaID})).Error; err != nil {
			return err
		}
		return tx.Delete(&secondEvidence).Error
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Submit(ctx, fixture.TenantID, fixture.ResponsibilityID, false); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("IT-037 unconfirmed coverage error=%v", err)
	}
	submission, err := service.Submit(ctx, fixture.TenantID, fixture.ResponsibilityID, true)
	if err != nil || submission.Complete || !submission.RequiresAttention {
		t.Fatalf("IT-007 confirmed incomplete submission=%+v err=%v", submission, err)
	}
	var submittedPayload struct {
		Coverage CoverageSummary `json:"coverage"`
	}
	if err := json.Unmarshal(submission.Payload, &submittedPayload); err != nil || submittedPayload.Coverage.Expected != 1 || submittedPayload.Coverage.NotObserved != 1 || submittedPayload.Coverage.FullyAssessed != 0 || len(submittedPayload.Coverage.Suggestions) != 2 || submittedPayload.Coverage.Suggestions[0].MissingView == "" {
		t.Fatalf("IT-007 missing coverage gap from immutable payload: %+v err=%v payload=%s", submittedPayload, err, submission.Payload)
	}
	issued := database.ReportSnapshot{ID: identity.NewID(), TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, Mode: "CURRENT", Classification: "OK", JSONDigest: "it-handover-before", HTMLDigest: "it-handover-before", SchemaVersion: 2, VersionNumber: 1, CanonicalJSON: json.RawMessage(`{"handover":{"meter":{"reading":123.4,"unit":"kWh"}}}`), HTML: json.RawMessage(`{}`), CreatedAt: time.Now().UTC()}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error { return tx.Create(&issued).Error }); err != nil {
		t.Fatal(err)
	}
	correction := meter
	correction.ExpectedVersion = 1
	correction.MediaIDs = []identity.ID{fixture.MediaID}
	correction.Payload = json.RawMessage(`{"meterType":"ENERGY","identifier":"A123","reading":124,"unit":"kWh","date":"2026-11-03"}`)
	corrected, err := service.UpsertHandoverRecord(ctx, correction)
	if err != nil || corrected.Version != 2 || corrected.ID == record.ID {
		t.Fatalf("UT-028 post-issue handover correction=%+v err=%v", corrected, err)
	}
	var prior database.HandoverRecord
	var preserved database.ReportSnapshot
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("id=?", record.ID).Take(&prior).Error; err != nil {
			return err
		}
		return tx.Where("id=?", issued.ID).Take(&preserved).Error
	}); err != nil {
		t.Fatal(err)
	}
	var priorPayload map[string]any
	if err := json.Unmarshal(prior.Payload, &priorPayload); err != nil {
		t.Fatal(err)
	}
	var priorSnapshot map[string]any
	if err := json.Unmarshal(preserved.CanonicalJSON, &priorSnapshot); err != nil {
		t.Fatal(err)
	}
	handover, _ := priorSnapshot["handover"].(map[string]any)
	meterSnapshot, _ := handover["meter"].(map[string]any)
	if prior.Version != 1 || priorPayload["reading"] != 123.4 || meterSnapshot["reading"] != 123.4 {
		t.Fatalf("UT-028 changed prior declaration or issued snapshot: record=%+v snapshot=%s", prior, preserved.CanonicalJSON)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.HandoverRecord{}).Where("tenant_id=? AND id=?", fixture.TenantID, record.ID).Update("declarant", "Alterado").Error
	}); err == nil {
		t.Fatal("issued handover revision accepted an in-place database update")
	}

	failedFixture := seedDescriptiveCaptureFixture(t, ctx, h, true)
	if _, err := (Service{DB: h.DB, Within: h.WithinTenant}).Submit(ctx, failedFixture.TenantID, failedFixture.ResponsibilityID, true); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("IT-038 confirmed submit accepted failed attached media: %v", err)
	}

	provenanceFixture := seedDescriptiveCaptureFixture(t, ctx, h, false)
	provenanceService := Service{DB: h.DB, Within: h.WithinTenant}
	now := time.Now().UTC().Truncate(time.Microsecond)
	fallbackMedia := database.MediaObject{ID: identity.NewID(), TenantID: provenanceFixture.TenantID, ResponsibilityID: provenanceFixture.ResponsibilityID, ObjectKey: "it054-fallback/" + provenanceFixture.TenantID.String(), IdempotencyKey: "it054-" + identity.NewID().String(), ContentType: "image/jpeg", SHA256: strings.Repeat("d", 64), SizeBytes: 1, Status: "READY", MediaKind: "PHOTO", CapturedAtSource: "SERVER_FALLBACK", Flags: []byte("[]"), AttentionItems: []byte("[]"), CreatedAt: now}
	if err := h.WithinTenant(ctx, provenanceFixture.TenantID, func(tx *gorm.DB) error { return tx.Create(&fallbackMedia).Error }); err != nil {
		t.Fatal(err)
	}
	fallbackSaved, err := provenanceService.SaveMetadata(ctx, MetadataInput{TenantID: provenanceFixture.TenantID, ResponsibilityID: provenanceFixture.ResponsibilityID, MediaID: fallbackMedia.ID, RequirementKey: "env:kitchen", CaptureSource: "CAMERA"})
	if err != nil || fallbackSaved.CapturedAtSource != "SERVER_FALLBACK" || fallbackSaved.CapturedAt == nil {
		t.Fatalf("IT-054 fallback provenance=%+v err=%v", fallbackSaved, err)
	}
	deviceTime := now.Add(-2 * time.Minute)
	deviceMedia := database.MediaObject{ID: identity.NewID(), TenantID: provenanceFixture.TenantID, ResponsibilityID: provenanceFixture.ResponsibilityID, ObjectKey: "it-device-time/" + provenanceFixture.TenantID.String(), IdempotencyKey: "it-device-time-" + identity.NewID().String(), ContentType: "image/jpeg", SHA256: strings.Repeat("e", 64), SizeBytes: 1, Status: "READY", MediaKind: "PHOTO", Flags: []byte("[]"), AttentionItems: []byte("[]"), CreatedAt: now}
	if err := h.WithinTenant(ctx, provenanceFixture.TenantID, func(tx *gorm.DB) error { return tx.Create(&deviceMedia).Error }); err != nil {
		t.Fatal(err)
	}
	deviceSaved, err := provenanceService.SaveMetadata(ctx, MetadataInput{TenantID: provenanceFixture.TenantID, ResponsibilityID: provenanceFixture.ResponsibilityID, MediaID: deviceMedia.ID, RequirementKey: "env:kitchen", CaptureSource: "CAMERA", CapturedAt: deviceTime})
	if err != nil || deviceSaved.CapturedAtSource != "DEVICE" || deviceSaved.CapturedAt == nil || !deviceSaved.CapturedAt.Equal(deviceTime) {
		t.Fatalf("device capture timestamp provenance=%+v err=%v", deviceSaved, err)
	}
}

func seedDescriptiveCaptureFixture(t *testing.T, ctx context.Context, h *harness.Harness, attachFailed bool) harness.Task06Fixture {
	t.Helper()
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{SchemaVersion: 3, RequirementKey: "env:kitchen", CaptureDraftStatus: "OPEN", CapturePolicyPayload: []byte(`{"allowGallery":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		document := []byte(`{"rooms":[{"key":"kitchen","typeCode":"KITCHEN","name":"Cozinha","components":[{"key":"floor","typeCode":"FLOOR","label":"Piso","views":["geral","detalhe"]}]}],"handoverRecords":[{"key":"meter","kind":"METER","required":true}]}`)
		if err := tx.Create(&database.InspectionScopeSnapshot{ID: identity.NewID(), TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, TemplateVersionID: fixture.TemplateVersionID, Profile: "GENERAL", Document: document, Digest: "it-descriptive-scope", Version: 1, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ProcessingAcceptance{ID: identity.NewID(), TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, DisclosureVersion: "photo-v1", PhotoProcessing: true, AIAnalysis: true, GPSUse: true, AcceptedAt: now}).Error; err != nil {
			return err
		}
		if attachFailed {
			failed := database.MediaObject{ID: identity.NewID(), TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "it038-failed/" + fixture.TenantID.String(), IdempotencyKey: "it038-" + identity.NewID().String(), ContentType: "image/png", SHA256: strings.Repeat("e", 64), SizeBytes: 1, Status: "FAILED", MediaKind: "PHOTO", RequirementKey: "env:kitchen", Description: "Foto com falha", Flags: []byte("[]"), AttentionItems: []byte("[]"), CreatedAt: now}
			return tx.Create(&failed).Error
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return fixture
}
