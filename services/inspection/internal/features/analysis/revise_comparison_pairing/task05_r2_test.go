package revise_comparison_pairing

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	processinventoryjob "inspection/services/inspection/internal/features/analysis/process_inventory_job"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"

	"gorm.io/gorm"
)

func message(err error) string {
	_, _, text := apperror.Public(err)
	return text
}

// Item 7 (review round 2): correcting one item re-analyses only that item's
// room and, inside it, only that item. Other rooms get no new generation and
// the other items of the room keep their previous result, pairs and the
// informational observations without a new model call about them.
func TestTask05R2RevisionReanalysesOnlyTheRevisedItem(t *testing.T) {
	f := newFixture(t)
	sofa := f.item("Sofá")
	// A second baseline item in the living room, judged by generation 1.
	var living database.InventoryEnvironment
	if err := f.seed.DB.Where("id=?", sofa.EnvironmentID).Take(&living).Error; err != nil {
		t.Fatal(err)
	}
	mesa := database.InventoryItem{ID: identity.NewID(), TenantID: f.seed.Tenant, OriginVersionID: f.baseline.Version, EnvironmentID: living.ID, IdentityID: identity.NewID(), TypeCode: "TABLE", Name: "Mesa", NotAssessed: []byte(`[]`), Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "ACCEPTED", Version: 1, CreatedAt: f.seed.Now, UpdatedAt: f.seed.Now}
	mesaOrigin := f.seed.Photo(t, "origin")
	for _, row := range []any{&mesa, &database.InventoryItemEvidence{ID: identity.NewID(), TenantID: f.seed.Tenant, ItemID: mesa.ID, Kind: "PHOTO", MediaID: mesaOrigin, CreatedAt: f.seed.Now}} {
		if err := f.seed.DB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	good, outsideName, outsideType := "GOOD", "Cadeira", "CHAIR"
	previous := []database.InventoryItemResult{
		{ID: identity.NewID(), TenantID: f.seed.Tenant, JobID: f.job.ID, InspectionID: f.seed.Inspection, ItemIdentityID: &sofa.IdentityID, Status: "NOT_OBSERVED", ConditionBefore: &good, ReasonCode: strPtr("NO_CORRESPONDING_VIEW"), CreatedAt: f.seed.Now},
		{ID: identity.NewID(), TenantID: f.seed.Tenant, JobID: f.job.ID, InspectionID: f.seed.Inspection, ItemIdentityID: &mesa.IdentityID, Status: "PRESENT_UNCHANGED", ConditionBefore: &good, ConditionAfter: &good, CreatedAt: f.seed.Now},
		{ID: identity.NewID(), TenantID: f.seed.Tenant, JobID: f.job.ID, InspectionID: f.seed.Inspection, Status: "OUTSIDE_INVENTORY", OutsideName: &outsideName, OutsideTypeCode: &outsideType, CreatedAt: f.seed.Now},
	}
	mesaPair := database.InventoryEvidencePair{ID: identity.NewID(), TenantID: f.seed.Tenant, JobID: f.job.ID, ItemIdentityID: mesa.IdentityID, OriginKind: "PHOTO", OriginMediaID: mesaOrigin, CurrentKind: "PHOTO", CurrentMediaID: f.current[0], Decision: "SAME_VIEW", Confirmed: true, Source: "MATCHER", CreatedAt: f.seed.Now}
	for _, row := range []any{&previous, &mesaPair} {
		if err := f.seed.DB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.revise(context.Background(), f.command("Sofá", Pair{BaselineEvidenceID: f.origin(sofa), CurrentEvidenceID: f.current[1]})); err != nil {
		t.Fatal(err)
	}
	var generations []database.InventoryJob
	if err := f.seed.DB.Where("generation>1").Find(&generations).Error; err != nil {
		t.Fatal(err)
	}
	if len(generations) != 1 || generations[0].RequirementKey != f.job.RequirementKey {
		t.Fatalf("new generations = %+v; want only the living room", generations)
	}
	next := generations[0]
	gateway := &inventorytest.Gateway{}
	gateway.Respond = func(request llm.StructuredRequest) (string, error) {
		if request.Mode == "INVENTORY_MATCH" {
			t.Fatal("the matcher was called for items nobody revised")
		}
		raw, _ := json.Marshal(map[string]any{"items": []map[string]any{
			{"itemIdentityId": sofa.IdentityID.String(), "status": "PRESENT_UNCHANGED", "conditionAfter": "GOOD", "reasonCode": nil, "description": nil, "outsideTypeCode": nil, "outsideName": nil},
			// A claim about an item nobody revised is ignored.
			{"itemIdentityId": mesa.IdentityID.String(), "status": "CONDITION_CHANGED", "conditionAfter": "POOR", "reasonCode": nil, "description": nil, "outsideTypeCode": nil, "outsideName": nil},
			{"itemIdentityId": nil, "status": "OUTSIDE_INVENTORY", "conditionAfter": nil, "reasonCode": nil, "description": nil, "outsideTypeCode": "SOFA", "outsideName": "Poltrona"},
		}})
		return string(raw), nil
	}
	processor, err := processinventoryjob.Setup(processinventoryjob.Dependencies{DB: f.seed.DB, Store: f.seed.Store.Store(), Gateway: gateway, Now: func() time.Time { return f.seed.Now.Add(time.Minute) }, Within: f.seed.Within})
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(context.Background(), next, func(write func(*gorm.DB) error) error { return f.seed.DB.Transaction(write) }); err != nil {
		t.Fatal(err)
	}
	calls := gateway.Calls("INVENTORY_COMPARE")
	if len(calls) != 1 || !strings.Contains(calls[0].UserPrompt, sofa.IdentityID.String()) || strings.Contains(calls[0].UserPrompt, mesa.IdentityID.String()) {
		t.Fatalf("compare requests = %d; want one request about the revised item only", len(calls))
	}
	var rows []database.InventoryItemResult
	if err := f.seed.DB.Where("job_id=?", next.ID).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	byItem, outside := map[identity.ID]database.InventoryItemResult{}, []string{}
	for _, row := range rows {
		if row.ItemIdentityID == nil {
			outside = append(outside, *row.OutsideName)
			continue
		}
		byItem[*row.ItemIdentityID] = row
	}
	if row := byItem[sofa.IdentityID]; row.Status != "PRESENT_UNCHANGED" {
		t.Fatalf("revised sofa = %+v; want judged with the staff pair", row)
	}
	if row := byItem[mesa.IdentityID]; row.Status != "PRESENT_UNCHANGED" || row.ConditionAfter == nil || *row.ConditionAfter != "GOOD" || row.ReasonCode != nil {
		t.Fatalf("untouched table = %+v; want the previous generation's result", row)
	}
	if len(rows) != 3 || len(outside) != 1 || outside[0] != outsideName {
		t.Fatalf("results = %d outside = %v; want the previous informational observation only", len(rows), outside)
	}
	var carried []database.InventoryEvidencePair
	if err := f.seed.DB.Where("job_id=? AND item_identity_id=? AND confirmed", next.ID, mesa.IdentityID).Find(&carried).Error; err != nil || len(carried) != 1 || carried[0].CurrentMediaID != f.current[0] {
		t.Fatalf("table pairs = %+v, %v; want the previous pair carried over", carried, err)
	}
}

// Item 7: a provider failure of the revised room fails only the revised item;
// the room's other results stay the previous generation's.
func TestTask05R2FailedRevisionKeepsUntouchedResults(t *testing.T) {
	f := newFixture(t)
	sofa := f.item("Sofá")
	var living database.InventoryEnvironment
	if err := f.seed.DB.Where("id=?", sofa.EnvironmentID).Take(&living).Error; err != nil {
		t.Fatal(err)
	}
	mesa := database.InventoryItem{ID: identity.NewID(), TenantID: f.seed.Tenant, OriginVersionID: f.baseline.Version, EnvironmentID: living.ID, IdentityID: identity.NewID(), TypeCode: "TABLE", Name: "Mesa", NotAssessed: []byte(`[]`), Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "ACCEPTED", Version: 1, CreatedAt: f.seed.Now, UpdatedAt: f.seed.Now}
	good := "GOOD"
	if err := f.seed.DB.Create(&mesa).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.seed.DB.Create(&database.InventoryItemResult{ID: identity.NewID(), TenantID: f.seed.Tenant, JobID: f.job.ID, InspectionID: f.seed.Inspection, ItemIdentityID: &mesa.IdentityID, Status: "PRESENT_UNCHANGED", ConditionBefore: &good, ConditionAfter: &good, CreatedAt: f.seed.Now}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.revise(context.Background(), f.command("Sofá", Pair{BaselineEvidenceID: f.origin(sofa), CurrentEvidenceID: f.current[1]})); err != nil {
		t.Fatal(err)
	}
	var next database.InventoryJob
	if err := f.seed.DB.Where("requirement_key=? AND generation=2", f.job.RequirementKey).Take(&next).Error; err != nil {
		t.Fatal(err)
	}
	processor, err := processinventoryjob.Setup(processinventoryjob.Dependencies{DB: f.seed.DB, Store: f.seed.Store.Store(), Gateway: &inventorytest.Gateway{}, Now: func() time.Time { return f.seed.Now }, Within: f.seed.Within})
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.Failed(context.Background(), f.seed.DB, next, "provider unavailable"); err != nil {
		t.Fatal(err)
	}
	var rows []database.InventoryItemResult
	if err := f.seed.DB.Where("job_id=?", next.ID).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	statuses := map[identity.ID]string{}
	for _, row := range rows {
		statuses[*row.ItemIdentityID] = row.Status + ":" + value(row.ReasonCode)
	}
	if statuses[sofa.IdentityID] != "NOT_OBSERVED:ANALYSIS_FAILED" || statuses[mesa.IdentityID] != "PRESENT_UNCHANGED:" || len(rows) != 2 {
		t.Fatalf("failed revision results = %v; want only the revised item failed", statuses)
	}
}

// Item 3b: a photo of a submission that a later submission of the room
// superseded, or of an open recapture, is refused with a Portuguese message.
func TestTask05R2RevisionRefusesSupersededAndOpenRecapturePhotos(t *testing.T) {
	f := newFixture(t)
	sofa := f.item("Sofá")
	key := f.job.RequirementKey
	draft := func(status string, at time.Time) identity.ID {
		t.Helper()
		var base database.CaptureDraft
		if err := f.seed.DB.Where("id=?", f.seed.CaptureDraft).Take(&base).Error; err != nil {
			t.Fatal(err)
		}
		base.ID, base.Status, base.CreatedAt, base.UpdatedAt = identity.NewID(), status, at, at
		if err := f.seed.DB.Create(&base).Error; err != nil {
			t.Fatal(err)
		}
		photo := f.seed.Photo(t, key)
		ids, _ := json.Marshal([]identity.ID{photo})
		if err := f.seed.DB.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: f.seed.Tenant, DraftID: base.ID, RequirementKey: key, MediaIDs: ids, Flags: json.RawMessage(`[]`), Version: 1, CreatedAt: at, UpdatedAt: at}).Error; err != nil {
			t.Fatal(err)
		}
		return photo
	}
	superseded := draft("SUBMITTED", f.seed.Now.Add(-time.Hour))
	open := draft("OPEN", f.seed.Now.Add(time.Hour))
	cases := map[identity.ID]string{superseded: "substituída por um envio mais recente", open: "não pertence ao mesmo ambiente"}
	for photo, want := range cases {
		_, err := f.revise(context.Background(), f.command("Sofá", Pair{BaselineEvidenceID: f.origin(sofa), CurrentEvidenceID: photo}))
		if code(err) != apperror.InvalidInput || !strings.Contains(message(err), want) {
			t.Fatalf("photo %s: err=%v (%q); want invalid input %q", photo, err, message(err), want)
		}
	}
	if _, err := f.revise(context.Background(), f.command("Sofá", Pair{BaselineEvidenceID: f.origin(sofa), CurrentEvidenceID: f.current[0]})); err != nil {
		t.Fatalf("a photo of the newest submission was refused: %v", err)
	}
}

// Item 10: the idempotency key is bound to the inspection and the decision;
// the same key with another decision is a Portuguese conflict, and the same
// key on another inspection is a new request there.
func TestTask05R2IdempotencyIsBoundToInspectionAndContent(t *testing.T) {
	f := newFixture(t)
	sofa := f.item("Sofá")
	first := f.command("Sofá", Pair{BaselineEvidenceID: f.origin(sofa), CurrentEvidenceID: f.current[0]})
	if _, err := f.revise(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if replay, err := f.revise(context.Background(), first); err != nil || !replay.Replayed {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	other := first
	other.Pairs = []Pair{{BaselineEvidenceID: f.origin(sofa), CurrentEvidenceID: f.current[1]}}
	if _, err := f.revise(context.Background(), other); code(err) != apperror.Conflict || !strings.Contains(message(err), "já foi usado com outra correção") {
		t.Fatalf("same key with another decision err=%v; want a Portuguese conflict", err)
	}

	// A second inspection of the same tenant that reuses the key is a new
	// request: it is not answered as a replay of the first one.
	second := cloneInspection(t, f)
	command := first
	command.InspectionID = second
	result, err := f.revise(context.Background(), command)
	if err != nil || result.Replayed || result.Report.InspectionID != second {
		t.Fatalf("other inspection = %+v, %v; want a new revision of that inspection", result, err)
	}
	var generations int64
	f.seed.DB.Model(&database.InventoryJob{}).Where("inspection_id=? AND generation=2", second).Count(&generations)
	if generations != 1 {
		t.Fatalf("other inspection generations = %d; want its room re-analysed", generations)
	}
}

// cloneInspection copies the fixture's move-out inspection (same baseline,
// scope, submitted capture and first comparison generation) under a new ID.
func cloneInspection(t *testing.T, f *fixture) identity.ID {
	t.Helper()
	db := f.seed.DB
	id, responsibility, draftID := identity.NewID(), identity.NewID(), identity.NewID()
	var inspection database.Inspection
	var reference database.ReferenceSnapshot
	var scope database.InspectionScopeSnapshot
	var report database.ReportSnapshot
	var draft database.CaptureDraft
	var responsibilityRow database.Responsibility
	var answers []database.RequirementAnswer
	var jobs []database.InventoryJob
	for _, load := range []error{
		db.Where("id=?", f.seed.Inspection).Take(&inspection).Error,
		db.Where("inspection_id=?", f.seed.Inspection).Take(&reference).Error,
		db.Where("inspection_id=?", f.seed.Inspection).Take(&scope).Error,
		db.Where("inspection_id=?", f.seed.Inspection).Take(&report).Error,
		db.Where("id=?", f.seed.CaptureDraft).Take(&draft).Error,
		db.Where("id=?", f.seed.Responsibility).Take(&responsibilityRow).Error,
		db.Where("draft_id=?", f.seed.CaptureDraft).Find(&answers).Error,
		db.Where("inspection_id=? AND generation=1", f.seed.Inspection).Find(&jobs).Error,
	} {
		if load != nil {
			t.Fatal(load)
		}
	}
	inspection.ID, inspection.SourceKey = id, "clone-"+id.String()
	reference.ID, reference.InspectionID = identity.NewID(), id
	scope.ID, scope.InspectionID = identity.NewID(), id
	report.ID, report.InspectionID = identity.NewID(), id
	responsibilityRow.ID, responsibilityRow.InspectionID = responsibility, id
	draft.ID, draft.ResponsibilityID = draftID, responsibility
	rows := []any{&inspection, &reference, &scope, &report, &responsibilityRow, &draft}
	for i := range answers {
		answers[i].ID, answers[i].DraftID = identity.NewID(), draftID
		rows = append(rows, &answers[i])
	}
	for i := range jobs {
		jobs[i].ID, jobs[i].InspectionID = identity.NewID(), id
		rows = append(rows, &jobs[i])
	}
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func strPtr(s string) *string { return &s }

func value(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
