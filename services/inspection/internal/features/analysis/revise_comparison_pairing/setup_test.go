package revise_comparison_pairing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	processinventoryjob "inspection/services/inspection/internal/features/analysis/process_inventory_job"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"

	"gorm.io/gorm"
)

type fixture struct {
	seed     *inventorytest.Seed
	baseline inventorytest.Baseline
	current  []identity.ID
	kitchen  identity.ID
	job      database.InventoryJob
	revise   func(context.Context, Command) (Result, error)
}

// newFixture issues report v1 of a strict photo move-out comparison whose
// first generation is terminal.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	seed := inventorytest.New(t, inventorytest.InspectionOptions{})
	// SQLite allows ten attached schemas; the unused inventory schema makes
	// room for the report versions this operation reads.
	if err := seed.DB.Exec("DETACH DATABASE inventory").Error; err != nil {
		t.Fatal(err)
	}
	if err := seed.DB.Exec("ATTACH DATABASE ':memory:' AS reports").Error; err != nil {
		t.Fatal(err)
	}
	if err := seed.DB.AutoMigrate(&database.ReportSnapshot{}); err != nil {
		t.Fatal(err)
	}
	baseline := seed.PinBaseline(t,
		inventorytest.BaselineEnvironment{Name: "Sala", TypeCode: "LIVING_ROOM", Items: []inventorytest.BaselineItem{{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1}, {Name: "Rejeitado", Condition: "GOOD", Decision: "REJECTED", Evidence: 1}}},
		inventorytest.BaselineEnvironment{Name: "Cozinha", TypeCode: "KITCHEN", Items: []inventorytest.BaselineItem{{Name: "Pia", TypeCode: "SINK", Condition: "GOOD", Evidence: 1}}},
	)
	f := &fixture{seed: seed, baseline: baseline}
	requirements := []any{}
	rooms := []inventorytest.ScopeRoom{}
	for _, env := range baseline.Environments {
		requirements = append(requirements, inventorytest.EnvironmentRequirement(inventorytest.EnvironmentKey(env), env.TypeCode, env.Name, false))
		rooms = append(rooms, inventorytest.ScopeRoom{Key: env.IdentityID.String(), Name: env.Name, TypeCode: env.TypeCode, Components: map[string][]string{"geral": {"wide"}}})
	}
	raw, _ := json.Marshal(requirements)
	if err := seed.DB.Model(&database.CaptureDraft{}).Where("id=?", seed.CaptureDraft).Update("requirements", raw).Error; err != nil {
		t.Fatal(err)
	}
	seed.PinScope(t, rooms...)
	living, kitchen := baseline.Environments[0], baseline.Environments[1]
	f.current = []identity.ID{seed.Photo(t, inventorytest.EnvironmentKey(living)), seed.Photo(t, inventorytest.EnvironmentKey(living))}
	seed.Answer(t, inventorytest.EnvironmentKey(living), f.current, "")
	f.kitchen = seed.Photo(t, inventorytest.EnvironmentKey(kitchen))
	seed.Answer(t, inventorytest.EnvironmentKey(kitchen), []identity.ID{f.kitchen}, "")
	f.job = seed.Job(t, "COMPARE", inventorytest.EnvironmentKey(living), baseline.Version, &living.IdentityID)
	kitchenJob := seed.Job(t, "COMPARE", inventorytest.EnvironmentKey(kitchen), baseline.Version, &kitchen.IdentityID)
	answerRefFor := func(key string) string {
		t.Helper()
		var answer database.RequirementAnswer
		err := capturecore.LatestSubmittedAnswer(context.Background(), seed.DB, seed.Tenant, seed.Inspection, key, &answer)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "none"
		}
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%s:%d", answer.ID, answer.Version)
	}
	for _, job := range []database.InventoryJob{f.job, kitchenJob} {
		if err := seed.DB.Model(&database.InventoryJob{}).Where("id=?", job.ID).Updates(map[string]any{"status": "SUCCEEDED", "capture_answer_ref": answerRefFor(job.RequirementKey)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := seed.DB.Create(&database.ReportSnapshot{ID: identity.NewID(), TenantID: seed.Tenant, InspectionID: seed.Inspection, Mode: "HISTORICAL", Classification: "NORMAL", JSONDigest: "digest-v1", HTMLDigest: "html-v1", SchemaVersion: 3, VersionNumber: 1, PDFRenderVersion: 1, CanonicalJSON: json.RawMessage(`{}`), HTML: json.RawMessage(`""`), CreatedAt: seed.Now}).Error; err != nil {
		t.Fatal(err)
	}
	revise, err := Setup(Dependencies{DB: seed.DB, Within: seed.Within, Now: func() time.Time { return seed.Now }})
	if err != nil {
		t.Fatal(err)
	}
	f.revise = revise
	return f
}

func (f *fixture) item(name string) database.InventoryItem {
	for _, item := range f.baseline.Items {
		if item.Name == name {
			return item
		}
	}
	panic(name)
}

func (f *fixture) origin(item database.InventoryItem) identity.ID {
	for _, evidence := range f.baseline.Evidence {
		if evidence.ItemID == item.ID {
			return evidence.MediaID
		}
	}
	return identity.ID{}
}

func (f *fixture) command(name string, pairs ...Pair) Command {
	return Command{TenantID: f.seed.Tenant, ActorID: identity.NewID(), InspectionID: f.seed.Inspection, ItemIdentityID: f.item(name).IdentityID, Pairs: pairs, ExpectedVersion: 1, ClientMutationID: "revise-" + identity.NewID().String()}
}

func code(err error) apperror.Code {
	value, _, _ := apperror.Public(err)
	return value
}

func TestSetupRequiresDatabase(t *testing.T) {
	if operation, err := Setup(Dependencies{}); err == nil || operation != nil {
		t.Fatalf("Setup(empty) = %v, %v; want dependency error", operation != nil, err)
	}
}

// Happy path (5.5): the decision is recorded for the next generation, the
// processor judges the item with the staff pair instead of the matcher, and a
// reissue is queued; the earlier generation stays untouched.
func TestRevisedPairingOpensNewGenerationJudgedWithTheStaffPair(t *testing.T) {
	f := newFixture(t)
	sofa := f.item("Sofá")
	command := f.command("Sofá", Pair{BaselineEvidenceID: f.origin(sofa), CurrentEvidenceID: f.current[1]})
	result, err := f.revise(context.Background(), command)
	if err != nil || result.Report.VersionNumber != 1 || result.Replayed {
		t.Fatalf("revise = %+v, %v", result, err)
	}
	var next database.InventoryJob
	if err := f.seed.DB.Where("requirement_key=? AND generation=2", f.job.RequirementKey).Take(&next).Error; err != nil || next.Status != "PENDING" {
		t.Fatalf("next generation = %+v, %v", next, err)
	}
	var staff []database.InventoryEvidencePair
	if err := f.seed.DB.Where("job_id=? AND source='STAFF'", next.ID).Find(&staff).Error; err != nil || len(staff) != 1 || !staff[0].Confirmed || staff[0].CurrentMediaID != f.current[1] || staff[0].ItemIdentityID != sofa.IdentityID {
		t.Fatalf("staff pairs = %+v, %v", staff, err)
	}
	var older int64
	f.seed.DB.Model(&database.InventoryEvidencePair{}).Where("job_id=?", f.job.ID).Count(&older)
	if older != 0 {
		t.Fatal("the earlier generation was modified")
	}
	var queued int64
	f.seed.DB.Model(&database.AuditEvent{}).Where("action='report.reissue_requested' AND outcome='QUEUED'").Count(&queued)
	var outbox int64
	f.seed.DB.Model(&database.OutboxIntent{}).Where("type='analysis.comparison_completed.v1'").Count(&outbox)
	if queued != 1 || outbox != 1 {
		t.Fatalf("queued reissues=%d outbox=%d; want one each", queued, outbox)
	}
	// Retrying the same request returns the same version and queues nothing.
	if replay, err := f.revise(context.Background(), command); err != nil || !replay.Replayed {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	gateway := &inventorytest.Gateway{}
	matched := false
	gateway.Respond = func(request llm.StructuredRequest) (string, error) {
		if request.Mode == "INVENTORY_MATCH" {
			matched = true
			return `{"pairs":[]}`, nil
		}
		raw, _ := json.Marshal(map[string]any{"items": []map[string]any{{"itemIdentityId": sofa.IdentityID.String(), "status": "CONDITION_CHANGED", "conditionAfter": "FAIR", "reasonCode": nil, "description": nil, "outsideTypeCode": nil, "outsideName": nil}}})
		return string(raw), nil
	}
	processor, err := processinventoryjob.Setup(processinventoryjob.Dependencies{DB: f.seed.DB, Store: f.seed.Store.Store(), Gateway: gateway, Now: func() time.Time { return f.seed.Now }, Within: f.seed.Within})
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(context.Background(), next, func(write func(*gorm.DB) error) error { return f.seed.DB.Transaction(write) }); err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Fatal("the matcher was asked to re-pair an item decided by staff")
	}
	var row database.InventoryItemResult
	if err := f.seed.DB.Where("job_id=? AND item_identity_id=?", next.ID, sofa.IdentityID).Take(&row).Error; err != nil || row.Status != "CONDITION_CHANGED" {
		t.Fatalf("result = %+v, %v; want a conclusion supported by the staff pair", row, err)
	}
	if err := f.seed.DB.Where("job_id=? AND source='STAFF' AND confirmed", next.ID).Find(&staff).Error; err != nil || len(staff) != 1 {
		t.Fatalf("staff pair not kept with the job results: %+v %v", staff, err)
	}
	// A later reanalysis keeps the staff decision in force.
	if err := f.seed.DB.Model(&database.InventoryJob{}).Where("id=?", next.ID).Update("status", "SUCCEEDED").Error; err != nil {
		t.Fatal(err)
	}
	third := next
	third.ID, third.Generation, third.Status = identity.NewID(), 3, "PENDING"
	if err := f.seed.DB.Create(&third).Error; err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(context.Background(), third, func(write func(*gorm.DB) error) error { return f.seed.DB.Transaction(write) }); err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Fatal("a later generation re-paired an item decided by staff")
	}
	var later database.InventoryItemResult
	if err := f.seed.DB.Where("job_id=? AND item_identity_id=?", third.ID, sofa.IdentityID).Take(&later).Error; err != nil || later.Status != "CONDITION_CHANGED" {
		t.Fatalf("third generation result = %+v, %v", later, err)
	}
}

// Requirement 1: identities are validated across room, item and evidence.
func TestRevisionRejectsForeignRoomItemAndEvidence(t *testing.T) {
	f := newFixture(t)
	sofa := f.item("Sofá")
	cases := map[string]Command{
		"current evidence of another room":  f.command("Sofá", Pair{BaselineEvidenceID: f.origin(sofa), CurrentEvidenceID: f.kitchen}),
		"baseline evidence of another item": f.command("Sofá", Pair{BaselineEvidenceID: f.origin(f.item("Pia")), CurrentEvidenceID: f.current[0]}),
		"rejected baseline suggestion":      f.command("Rejeitado", Pair{BaselineEvidenceID: f.origin(f.item("Rejeitado")), CurrentEvidenceID: f.current[0]}),
		"unknown item":                      {TenantID: f.seed.Tenant, InspectionID: f.seed.Inspection, ItemIdentityID: identity.NewID(), ExpectedVersion: 1, ClientMutationID: "x"},
		"duplicate baseline evidence":       f.command("Sofá", Pair{BaselineEvidenceID: f.origin(sofa), CurrentEvidenceID: f.current[0]}, Pair{BaselineEvidenceID: f.origin(sofa), CurrentEvidenceID: f.current[1]}),
	}
	for name, command := range cases {
		if _, err := f.revise(context.Background(), command); code(err) != apperror.InvalidInput {
			t.Fatalf("%s: err=%v; want a Portuguese invalid-input error", name, err)
		}
	}
	var jobs int64
	f.seed.DB.Model(&database.InventoryJob{}).Where("generation>1").Count(&jobs)
	if jobs != 0 {
		t.Fatal("a rejected revision opened a new generation")
	}
}

func TestRevisionRejectsStaleVersionMissingInputAndLegacyComparison(t *testing.T) {
	f := newFixture(t)
	sofa := f.item("Sofá")
	stale := f.command("Sofá", Pair{BaselineEvidenceID: f.origin(sofa), CurrentEvidenceID: f.current[0]})
	stale.ExpectedVersion = 7
	if _, err := f.revise(context.Background(), stale); code(err) != apperror.Conflict {
		t.Fatalf("stale version err=%v; want conflict", err)
	}
	missing := f.command("Sofá")
	missing.ClientMutationID = " "
	if _, err := f.revise(context.Background(), missing); code(err) != apperror.InvalidInput {
		t.Fatalf("missing client mutation id err=%v", err)
	}
	if err := f.seed.DB.Where("inspection_id=?", f.seed.Inspection).Delete(&database.InspectionScopeSnapshot{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.revise(context.Background(), f.command("Sofá", Pair{BaselineEvidenceID: f.origin(sofa), CurrentEvidenceID: f.current[0]})); code(err) != apperror.InvalidState {
		t.Fatalf("legacy comparison err=%v; want invalid state", err)
	}
}

// An empty decision records that staff found no equivalent current view; the
// item is then never judged as unchanged.
func TestEmptyRevisionRecordsNoEquivalentView(t *testing.T) {
	f := newFixture(t)
	sofa := f.item("Sofá")
	if _, err := f.revise(context.Background(), f.command("Sofá")); err != nil {
		t.Fatal(err)
	}
	var staff []database.InventoryEvidencePair
	if err := f.seed.DB.Where("source='STAFF' AND item_identity_id=?", sofa.IdentityID).Find(&staff).Error; err != nil || len(staff) != 1 || staff[0].Confirmed || staff[0].Decision != "NOT_COMPARABLE" {
		t.Fatalf("staff decision = %+v, %v", staff, err)
	}
}

func TestRevisionErrorsUseLaudoVocabulary(t *testing.T) {
	f := newFixture(t)
	missing := f.command("Sofá")
	missing.InspectionID = identity.NewID()
	_, err := f.revise(context.Background(), missing)
	if _, _, message := apperror.Public(err); message != "Laudo não encontrado." {
		t.Fatalf("missing report message = %q; want Laudo vocabulary", message)
	}
	stale := f.command("Sofá")
	stale.ExpectedVersion = 2
	_, err = f.revise(context.Background(), stale)
	if _, _, message := apperror.Public(err); message != "O laudo foi atualizado. Recarregue a versão mais recente e tente novamente." {
		t.Fatalf("stale report message = %q; want Laudo vocabulary", message)
	}
}

func TestRevisionPropagatesDependencyFailure(t *testing.T) {
	failure := errors.New("database unavailable")
	revise, err := Setup(Dependencies{DB: &gorm.DB{}, Within: func(context.Context, identity.ID, func(*gorm.DB) error) error { return failure }})
	if err != nil {
		t.Fatal(err)
	}
	command := Command{TenantID: identity.NewID(), InspectionID: identity.NewID(), ItemIdentityID: identity.NewID(), ExpectedVersion: 1, ClientMutationID: "x"}
	if _, err := revise(context.Background(), command); !errors.Is(err, failure) {
		t.Fatalf("err=%v; want the dependency failure", err)
	}
}
