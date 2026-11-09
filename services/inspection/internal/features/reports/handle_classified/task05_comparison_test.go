package handle_classified

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	processinventoryjob "inspection/services/inspection/internal/features/analysis/process_inventory_job"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	customerreport "inspection/services/inspection/internal/features/reports/customer/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"

	"gorm.io/gorm"
)

// t05Fixture is a photo move-out inspection with a pinned descriptive scope
// (report v3) compared against an approved baseline, processed by the real
// INVENTORY_COMPARE processor over SQLite and a scripted gateway.
type t05Fixture struct {
	seed      *inventorytest.Seed
	baseline  inventorytest.Baseline
	gateway   *inventorytest.Gateway
	processor *processinventoryjob.Processor
}

type t05MatchPair struct {
	OriginEvidenceID  string `json:"originEvidenceId"`
	CurrentEvidenceID string `json:"currentEvidenceId"`
	Decision          string `json:"decision"`
	Reason            string `json:"reason"`
}

type t05MatchRequest struct {
	Stage              string         `json:"stage"`
	OriginEvidenceID   string         `json:"originEvidenceId"`
	CurrentEvidenceIDs []string       `json:"currentEvidenceIds"`
	ProposedPairs      []t05MatchPair `json:"proposedPairs"`
}

func newT05Fixture(t *testing.T, environments ...inventorytest.BaselineEnvironment) *t05Fixture {
	t.Helper()
	seed := inventorytest.New(t, inventorytest.InspectionOptions{})
	baseline := seed.PinBaseline(t, environments...)
	requirements := make([]any, 0, len(baseline.Environments))
	rooms := make([]inventorytest.ScopeRoom, 0, len(baseline.Environments))
	for _, env := range baseline.Environments {
		requirements = append(requirements, inventorytest.EnvironmentRequirement(inventorytest.EnvironmentKey(env), env.TypeCode, env.Name, false))
		rooms = append(rooms, inventorytest.ScopeRoom{Key: env.IdentityID.String(), Name: env.Name, TypeCode: env.TypeCode, Components: map[string][]string{"geral": {"wide"}}})
	}
	raw, _ := json.Marshal(requirements)
	if err := seed.DB.Model(&database.CaptureDraft{}).Where("id=?", seed.CaptureDraft).Update("requirements", raw).Error; err != nil {
		t.Fatal(err)
	}
	seed.PinScope(t, rooms...)
	gateway := &inventorytest.Gateway{}
	processor, err := processinventoryjob.Setup(processinventoryjob.Dependencies{DB: seed.DB, Store: seed.Store.Store(), Gateway: gateway, Now: func() time.Time { return seed.Now }, Within: seed.Within})
	if err != nil {
		t.Fatal(err)
	}
	return &t05Fixture{seed: seed, baseline: baseline, gateway: gateway, processor: processor}
}

func (f *t05Fixture) item(name string) database.InventoryItem {
	for _, item := range f.baseline.Items {
		if item.Name == name {
			return item
		}
	}
	panic("unknown item " + name)
}

func (f *t05Fixture) originOf(item database.InventoryItem) identity.ID {
	for _, evidence := range f.baseline.Evidence {
		if evidence.ItemID == item.ID {
			return evidence.MediaID
		}
	}
	return identity.ID{}
}

// capture answers room index with n current photos; covered records every
// expected view of the room as observed.
func (f *t05Fixture) capture(t *testing.T, index, photos int, covered bool) []identity.ID {
	t.Helper()
	key := inventorytest.EnvironmentKey(f.baseline.Environments[index])
	ids := make([]identity.ID, 0, photos)
	for i := 0; i < photos; i++ {
		ids = append(ids, f.seed.Photo(t, key))
	}
	f.seed.Answer(t, key, ids, "")
	state := "PARTIAL"
	if covered {
		state = "OBSERVED"
	}
	f.seed.ComponentAnswer(t, "env:"+f.baseline.Environments[index].IdentityID.String(), "geral", "wide", state, ids)
	return ids
}

// respond scripts the matcher (propose names the current evidence chosen by
// pick, verify agrees) and the comparison.
func (f *t05Fixture) respond(t *testing.T, pick func(t05MatchRequest) (string, string), compare func(llm.StructuredRequest) string) {
	f.gateway.Respond = func(request llm.StructuredRequest) (string, error) {
		switch request.Mode {
		case "INVENTORY_MATCH":
			var in t05MatchRequest
			if err := json.Unmarshal([]byte(request.UserPrompt), &in); err != nil {
				return "", err
			}
			if in.Stage == "verify" {
				raw, _ := json.Marshal(map[string]any{"pairs": in.ProposedPairs})
				return string(raw), nil
			}
			current, decision := pick(in)
			raw, _ := json.Marshal(map[string]any{"pairs": []t05MatchPair{{OriginEvidenceID: in.OriginEvidenceID, CurrentEvidenceID: current, Decision: decision, Reason: "vista"}}})
			return string(raw), nil
		case "INVENTORY_COMPARE":
			return compare(request), nil
		}
		return "", errors.New("unexpected mode")
	}
}

func firstCurrent(in t05MatchRequest) (string, string) { return in.CurrentEvidenceIDs[0], "SAME_VIEW" }

func t05Compare(rows ...map[string]any) func(llm.StructuredRequest) string {
	return func(llm.StructuredRequest) string {
		full := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			item := map[string]any{"itemIdentityId": nil, "status": "PRESENT_UNCHANGED", "conditionAfter": nil, "reasonCode": nil, "description": nil, "outsideTypeCode": nil, "outsideName": nil}
			for k, v := range row {
				item[k] = v
			}
			full = append(full, item)
		}
		raw, _ := json.Marshal(map[string]any{"items": full})
		return string(raw)
	}
}

func t05Claim(item database.InventoryItem, status string, extra ...any) map[string]any {
	row := map[string]any{"itemIdentityId": item.IdentityID.String(), "status": status}
	for i := 0; i+1 < len(extra); i += 2 {
		row[extra[i].(string)] = extra[i+1]
	}
	return row
}

// run processes the COMPARE job of one room and marks it SUCCEEDED, as the
// executor would.
func (f *t05Fixture) run(t *testing.T, index int) database.InventoryJob {
	t.Helper()
	env := f.baseline.Environments[index]
	job := f.seed.Job(t, "COMPARE", inventorytest.EnvironmentKey(env), f.baseline.Version, &env.IdentityID)
	if err := f.processor.Process(context.Background(), job, func(write func(*gorm.DB) error) error { return f.seed.DB.Transaction(write) }); err != nil {
		t.Fatalf("process %s: %v", env.Name, err)
	}
	if err := f.seed.DB.Model(&database.InventoryJob{}).Where("id=?", job.ID).Update("status", "SUCCEEDED").Error; err != nil {
		t.Fatal(err)
	}
	return job
}

func (f *t05Fixture) section(t *testing.T) (*reportcore.ComparisonSection, []reportcore.Evidence) {
	t.Helper()
	var inspection database.Inspection
	var reference database.ReferenceSnapshot
	if err := f.seed.DB.Where("id=?", f.seed.Inspection).Take(&inspection).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.seed.DB.Where("inspection_id=?", f.seed.Inspection).Take(&reference).Error; err != nil {
		t.Fatal(err)
	}
	evidence := []reportcore.Evidence{}
	section, err := buildComparisonReport(context.Background(), f.seed.DB, f.seed.Tenant, inspection, reference, &evidence)
	if err != nil {
		t.Fatal(err)
	}
	if section == nil {
		t.Fatal("strict photo comparison produced no comparison section")
	}
	return section, evidence
}

func t05Snapshot(section *reportcore.ComparisonSection, evidence []reportcore.Evidence) reportcore.Snapshot {
	return reportcore.Snapshot{SchemaVersion: 3, ReportID: "RPT-T05", InspectionID: "inspection", TemplateVersionID: "template", ReferenceVersionID: section.Baseline.VersionID, PromptDigest: "digest", Mode: "HISTORICAL", Classification: "NORMAL", Advisory: "Esta triagem interna descreve alterações observadas e não atribui culpa, custo, responsabilidade ou consequência automática.", Context: reportcore.Context{Asset: reportcore.AssetContext{ID: "asset", Name: "Imóvel", Address: "Rua Exemplo"}, Participant: reportcore.ParticipantContext{ID: "participant", Name: "Participante"}, Template: reportcore.TemplateContext{ID: "template", Name: "Modelo", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: "2026-11-05T00:00:00Z"}}, Evidence: evidence, Processing: &reportcore.ProcessingState{Status: "COMPLETED"}, Checklist: &reportcore.ChecklistSection{Rooms: []reportcore.ChecklistRoom{}, Coverage: reportcore.ChecklistCoverage{Expected: 1, FullyAssessed: 1}}, Scope: &reportcore.ScopeSnapshot{Digest: "scope", Version: 1, Profile: "PROPERTY_COMPONENTS"}, Handover: &reportcore.HandoverSection{Entries: []reportcore.HandoverEntry{}}, Provenance: &reportcore.Provenance{PromptRevision: "prompt"}, Comparison: section}
}

func itemNamed(t *testing.T, section *reportcore.ComparisonSection, name string) reportcore.ComparisonItem {
	t.Helper()
	for _, item := range section.Items {
		if item.Name == name {
			return item
		}
	}
	t.Fatalf("item %s missing from comparison: %+v", name, section.Items)
	return reportcore.ComparisonItem{}
}

func evidenceRole(evidence []reportcore.Evidence, id string) string {
	for _, item := range evidence {
		if item.ID == id {
			return item.Role
		}
	}
	return ""
}

var t05Living = inventorytest.BaselineEnvironment{Name: "Sala", TypeCode: "LIVING_ROOM", Items: []inventorytest.BaselineItem{{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1}}}

// UT-071 (happy): equivalent baseline and current views produce a conclusion
// that opens both linked evidence views and identifies the baseline version.
func TestUT071EquivalentViewsConclusionLinksBothEvidenceAndBaselineVersion(t *testing.T) {
	f := newT05Fixture(t, t05Living)
	current := f.capture(t, 0, 1, true)
	sofa := f.item("Sofá")
	f.respond(t, firstCurrent, t05Compare(t05Claim(sofa, "PRESENT_UNCHANGED", "conditionAfter", "GOOD")))
	f.run(t, 0)
	section, evidence := f.section(t)
	item := itemNamed(t, section, "Sofá")
	origin := f.originOf(sofa).String()
	if item.Outcome != reportcore.ComparisonUnchanged || len(item.Pairs) != 1 || item.Pairs[0].BaselineEvidenceID != origin || item.Pairs[0].CurrentEvidenceID != current[0].String() {
		t.Fatalf("Sofá = %+v; want UNCHANGED paired with origin %s and current %s", item, origin, current[0])
	}
	if evidenceRole(evidence, origin) != "REFERENCE" || evidenceRole(evidence, current[0].String()) != "CURRENT" {
		t.Fatalf("paired evidence roles = %q/%q; want REFERENCE/CURRENT", evidenceRole(evidence, origin), evidenceRole(evidence, current[0].String()))
	}
	if section.Baseline.VersionID != f.baseline.Version.String() || section.Baseline.VersionNumber != 1 || section.Baseline.Purpose != "MOVE_IN" || section.Baseline.ActivatedAt == "" {
		t.Fatalf("baseline = %+v; want the pinned approved version", section.Baseline)
	}
	snapshot := t05Snapshot(section, evidence)
	html, err := reportcore.HTML(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	pairLine := reportcore.ComparisonPairLine(snapshot, item.Pairs[0])
	if !strings.Contains(string(html), "Referência aprovada: versão 1") || !strings.Contains(string(html), pairLine) || strings.Contains(pairLine, "sem índice") {
		t.Fatalf("HTML must identify the baseline version and both evidence views (%q):\n%s", pairLine, html)
	}
}

// UT-072 (error): an AI comparison citing another room's item, or a pair with
// another room's evidence, cannot support a conclusion.
func TestUT072ForeignRoomItemOrEvidenceCannotSupportConclusion(t *testing.T) {
	kitchen := inventorytest.BaselineEnvironment{Name: "Cozinha", TypeCode: "KITCHEN", Items: []inventorytest.BaselineItem{{Name: "Pia", TypeCode: "SINK", Condition: "GOOD", Evidence: 1}}}
	f := newT05Fixture(t, t05Living, kitchen)
	f.capture(t, 0, 1, true)
	kitchenPhotos := f.capture(t, 1, 1, true)
	sofa, sink := f.item("Sofá"), f.item("Pia")
	// The living-room matcher names the kitchen photo; the living-room
	// comparison also claims the kitchen sink is missing.
	f.respond(t, func(t05MatchRequest) (string, string) { return kitchenPhotos[0].String(), "SAME_VIEW" }, t05Compare(t05Claim(sofa, "PRESENT_UNCHANGED"), t05Claim(sink, "MISSING")))
	livingJob := f.run(t, 0)
	f.respond(t, firstCurrent, t05Compare(t05Claim(sink, "PRESENT_UNCHANGED", "conditionAfter", "GOOD")))
	f.run(t, 1)
	var foreign int64
	if err := f.seed.DB.Model(&database.InventoryEvidencePair{}).Where("job_id=? AND current_media_id=?", livingJob.ID, kitchenPhotos[0]).Count(&foreign).Error; err != nil || foreign != 0 {
		t.Fatalf("a pair with another room's evidence was stored (%d, %v)", foreign, err)
	}
	section, _ := f.section(t)
	sofaItem, sinkItem := itemNamed(t, section, "Sofá"), itemNamed(t, section, "Pia")
	if sofaItem.Outcome != reportcore.ComparisonNotObserved || len(sofaItem.Pairs) != 0 || len(sofaItem.Limitations) != 1 || sofaItem.Limitations[0] != reportcore.LimitationPairRejected {
		t.Fatalf("Sofá = %+v; want NOT_OBSERVED with the rejected-pair limitation", sofaItem)
	}
	if sinkItem.Outcome != reportcore.ComparisonUnchanged {
		t.Fatalf("Pia = %+v; another room's MISSING claim must not decide it", sinkItem)
	}
}

// UT-073 (boundary): an item without an equivalent baseline view is "no basis
// to compare", is never sent to the model, and stays distinct from "not observed".
func TestUT073ItemWithoutBaselineViewIsNoBasisToCompare(t *testing.T) {
	env := inventorytest.BaselineEnvironment{Name: "Sala", TypeCode: "LIVING_ROOM", Items: []inventorytest.BaselineItem{{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1}, {Name: "Quadro", TypeCode: "OTHER", Condition: "GOOD", Declared: true}}}
	f := newT05Fixture(t, env)
	f.capture(t, 0, 1, true)
	sofa, frame := f.item("Sofá"), f.item("Quadro")
	f.respond(t, firstCurrent, t05Compare(t05Claim(sofa, "PRESENT_UNCHANGED", "conditionAfter", "GOOD"), t05Claim(frame, "PRESENT_UNCHANGED", "conditionAfter", "GOOD")))
	f.run(t, 0)
	for _, call := range f.gateway.Calls("INVENTORY_COMPARE") {
		if strings.Contains(call.UserPrompt, frame.IdentityID.String()) {
			t.Fatal("an item without baseline view was sent to the comparison model")
		}
	}
	section, evidence := f.section(t)
	item := itemNamed(t, section, "Quadro")
	if item.Outcome != reportcore.ComparisonNoBasis || len(item.Pairs) != 0 || len(item.Limitations) != 1 || item.Limitations[0] != reportcore.LimitationNoBaselineView {
		t.Fatalf("Quadro = %+v; want NO_BASIS with its limitation", item)
	}
	if section.Counts.NoBasis != 1 || section.Counts.NotObserved != 0 {
		t.Fatalf("counts = %+v; no basis must not be counted as not observed", section.Counts)
	}
	html, err := reportcore.HTML(t05Snapshot(section, evidence))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "Sem base para comparar · 1 de 2 itens") {
		t.Fatalf("HTML must label the item as no basis to compare:\n%s", html)
	}
}

// UT-076 (boundary): failed comparison processing is shown as failure, not as
// "no changes".
func TestUT076FailedComparisonIsShownAsFailureNotNoChanges(t *testing.T) {
	f := newT05Fixture(t, inventorytest.BaselineEnvironment{Name: "Sala", TypeCode: "LIVING_ROOM", Items: []inventorytest.BaselineItem{{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1}, {Name: "Mesa", TypeCode: "TABLE", Condition: "GOOD", Evidence: 1}}})
	f.capture(t, 0, 1, true)
	env := f.baseline.Environments[0]
	job := f.seed.Job(t, "COMPARE", inventorytest.EnvironmentKey(env), f.baseline.Version, &env.IdentityID)
	if err := f.seed.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.InventoryJob{}).Where("id=?", job.ID).Update("status", "FAILED").Error; err != nil {
			return err
		}
		job.Status = "FAILED"
		return f.processor.Failed(context.Background(), tx, job, "gateway_unavailable")
	}); err != nil {
		t.Fatal(err)
	}
	section, evidence := f.section(t)
	if section.Status != "FAILED" || section.Counts.Unchanged != 0 || section.Counts.NotObserved != 2 {
		t.Fatalf("section = %+v; want FAILED with every item not observed", section)
	}
	for _, item := range section.Items {
		if item.Outcome != reportcore.ComparisonNotObserved || len(item.Limitations) != 1 || item.Limitations[0] != reportcore.LimitationComparisonFailed {
			t.Fatalf("item = %+v; want the comparison failure limitation", item)
		}
	}
	snapshot := t05Snapshot(section, evidence)
	if !reportcore.ClassificationLimitedForSnapshot(snapshot) || reportcore.ClassificationLabelForSnapshot(snapshot) == reportcore.ClassificationLabel("NORMAL") {
		t.Fatal("a failed comparison must not read as a report without alerts")
	}
	html, err := reportcore.HTML(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "Estado: Falha na comparação") || strings.Contains(string(html), "Sem alteração ·") {
		t.Fatalf("HTML must show the failure and no unchanged group:\n%s", html)
	}
}

// UT-117 (state): a prior damage still present keeps the current condition
// and the historical change as separate fields.
func TestUT117PriorDamageStillPresentKeepsConditionAndChangeSeparate(t *testing.T) {
	f := newT05Fixture(t, inventorytest.BaselineEnvironment{Name: "Sala", TypeCode: "LIVING_ROOM", Items: []inventorytest.BaselineItem{{Name: "Mesa", TypeCode: "TABLE", Condition: "POOR", Evidence: 1}}})
	f.capture(t, 0, 1, true)
	table := f.item("Mesa")
	f.respond(t, firstCurrent, t05Compare(t05Claim(table, "PRESENT_UNCHANGED", "conditionAfter", "POOR")))
	f.run(t, 0)
	section, evidence := f.section(t)
	item := itemNamed(t, section, "Mesa")
	if item.Outcome != reportcore.ComparisonUnchanged || item.BaselineCondition != "POOR" || item.CurrentCondition != "POOR" {
		t.Fatalf("Mesa = %+v; want UNCHANGED outcome with baseline POOR and current POOR kept apart", item)
	}
	html, err := reportcore.HTML(t05Snapshot(section, evidence))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "Sala · Mesa · Sem alteração · Condição na referência: Ruim · Condição atual: Ruim") {
		t.Fatalf("HTML must show the outcome and both conditions separately:\n%s", html)
	}
}

// UT-082 (error, builder side): rejected and pending baseline suggestions never
// reach the comparison, and the customer copy carries no internal field.
func TestUT082RejectedSuggestionsAndInternalFieldsNeverReachCustomerComparison(t *testing.T) {
	env := inventorytest.BaselineEnvironment{Name: "Sala", TypeCode: "LIVING_ROOM", Items: []inventorytest.BaselineItem{{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1}, {Name: "Sugestão rejeitada", TypeCode: "OTHER", Condition: "GOOD", Decision: "REJECTED", Evidence: 1}, {Name: "Sugestão pendente", TypeCode: "OTHER", Condition: "GOOD", Decision: "PENDING", Evidence: 1}}}
	f := newT05Fixture(t, env)
	f.capture(t, 0, 1, true)
	f.respond(t, firstCurrent, t05Compare(t05Claim(f.item("Sofá"), "CONDITION_CHANGED", "conditionAfter", "FAIR", "description", "Rasgo novo no assento.")))
	f.run(t, 0)
	section, evidence := f.section(t)
	if len(section.Items) != 1 || section.Items[0].Name != "Sofá" || section.Counts.Total != 1 {
		t.Fatalf("items = %+v; only included baseline items are compared", section.Items)
	}
	snapshot := t05Snapshot(section, evidence)
	customer := customerreport.FilterComparison(snapshot)
	if customer.Baseline.VersionID != "" || customer.Items[0].ItemIdentityID != "" || customer.Items[0].TypeCode != "" || customer.Items[0].Description != "" {
		t.Fatalf("customer comparison = %+v; internal fields must be removed", customer)
	}
	view, err := customerreport.ProjectComparison(snapshot, "")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(view)
	for _, secret := range []string{f.baseline.Version.String(), f.item("Sofá").IdentityID.String(), "Rasgo novo", "SOFA"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("customer view leaks %q: %s", secret, encoded)
		}
	}
	for _, id := range []string{f.originOf(f.item("Sofá")).String()} {
		if strings.Contains(string(encoded), id) {
			t.Fatalf("customer view exposes the raw evidence id %s", id)
		}
	}
}

// UT-084 (state): a later baseline version does not change the baseline number
// or text of the issued comparison.
func TestUT084LaterBaselineVersionDoesNotChangeIssuedComparison(t *testing.T) {
	f := newT05Fixture(t, t05Living)
	f.capture(t, 0, 1, true)
	f.respond(t, firstCurrent, t05Compare(t05Claim(f.item("Sofá"), "PRESENT_UNCHANGED", "conditionAfter", "GOOD")))
	f.run(t, 0)
	section, evidence := f.section(t)
	issued, _, err := reportcore.CanonicalJSON(t05Snapshot(section, evidence))
	if err != nil {
		t.Fatal(err)
	}
	// A newer baseline is activated for the asset after the report was issued.
	var version database.OriginVersion
	if err := f.seed.DB.Where("id=?", f.baseline.Version).Take(&version).Error; err != nil {
		t.Fatal(err)
	}
	newer, purpose, now := identity.NewID(), "INVENTORY_UPDATE", f.seed.Now.Add(time.Hour)
	if err := f.seed.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.OriginVersion{}).Where("id=?", version.ID).Update("status", "SUPERSEDED").Error; err != nil {
			return err
		}
		if err := tx.Create(&database.OriginVersion{ID: newer, TenantID: f.seed.Tenant, OriginID: version.OriginID, VersionNumber: 2, Purpose: &purpose, Source: "CAPTURE", HasInventory: true, Status: "ACTIVE", IdempotencyKey: "t05-newer-" + newer.String(), CreatedAt: now, ActivatedAt: &now}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Origin{}).Where("id=?", version.OriginID).Update("active_version_id", newer).Error
	}); err != nil {
		t.Fatal(err)
	}
	var stored reportcore.Snapshot
	if err := json.Unmarshal(issued, &stored); err != nil {
		t.Fatal(err)
	}
	view, err := customerreport.ProjectComparison(stored, "")
	if err != nil {
		t.Fatal(err)
	}
	if view.BaselineVersionNumber != 1 || view.Purpose != "Entrada no imóvel" || !strings.Contains(view.BaselineLine, "versão 1") {
		t.Fatalf("issued comparison view = %+v; want baseline version 1 as issued", view)
	}
	// A later report of the same inspection keeps the pinned version too.
	again, _ := f.section(t)
	if again.Baseline.VersionNumber != 1 || again.Baseline.VersionID != f.baseline.Version.String() {
		t.Fatalf("rebuilt comparison baseline = %+v; the pinned version must not switch", again.Baseline)
	}
}

// A legacy (unscoped) inspection keeps its historical report shape: no
// comparison section and no extra evidence.
func TestTask05LegacyInspectionWithoutScopeHasNoComparisonSection(t *testing.T) {
	seed := inventorytest.New(t, inventorytest.InspectionOptions{})
	seed.PinBaseline(t, t05Living)
	var inspection database.Inspection
	var reference database.ReferenceSnapshot
	if err := seed.DB.Where("id=?", seed.Inspection).Take(&inspection).Error; err != nil {
		t.Fatal(err)
	}
	if err := seed.DB.Where("inspection_id=?", seed.Inspection).Take(&reference).Error; err != nil {
		t.Fatal(err)
	}
	evidence := []reportcore.Evidence{}
	section, err := buildComparisonReport(context.Background(), seed.DB, seed.Tenant, inspection, reference, &evidence)
	if err != nil || section != nil || len(evidence) != 0 {
		t.Fatalf("legacy comparison section = %+v err=%v evidence=%d; want none", section, err, len(evidence))
	}
}

func TestTask05ComparisonOutcomeMapsEveryStoredReason(t *testing.T) {
	for reason, want := range map[string]string{"NO_BASELINE_VIEW": reportcore.ComparisonNoBasis, "PAIR_REJECTED": reportcore.ComparisonNotObserved, "ROOM_COVERAGE_INSUFFICIENT": reportcore.ComparisonNotObserved, "ENVIRONMENT_NOT_CAPTURED": reportcore.ComparisonNotObserved, "ANALYSIS_FAILED": reportcore.ComparisonNotObserved, "OBSTRUCTED": reportcore.ComparisonNotObserved, "NO_CORRESPONDING_VIEW": reportcore.ComparisonNotObserved} {
		code := reason
		outcome, limitations := comparisonOutcome(database.InventoryItemResult{Status: "NOT_OBSERVED", ReasonCode: &code})
		if outcome != want || len(limitations) != 1 || strings.TrimSpace(limitations[0]) == "" || !reportcore.ComparisonTextAllowed(limitations[0]) {
			t.Fatalf("%s = %s %v; want %s with one neutral limitation", reason, outcome, limitations, want)
		}
	}
	for status, want := range map[string]string{"PRESENT_UNCHANGED": reportcore.ComparisonUnchanged, "CONDITION_CHANGED": reportcore.ComparisonChanged, "IMPROVED": reportcore.ComparisonImproved, "MISSING": reportcore.ComparisonMissing} {
		if outcome, limitations := comparisonOutcome(database.InventoryItemResult{Status: status}); outcome != want || limitations != nil {
			t.Fatalf("%s = %s %v", status, outcome, limitations)
		}
	}
}
