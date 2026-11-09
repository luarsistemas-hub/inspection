package core

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	reportcore "inspection/services/inspection/internal/features/reports/core"
)

// t05Snapshot is an issued v3 move-out report with one item of every outcome.
func t05Snapshot() reportcore.Snapshot {
	evidence := []reportcore.Evidence{
		{ID: "11111111-1111-4111-8111-111111111111", RequirementKey: "env:sala", Role: "REFERENCE", Availability: "AVAILABLE"},
		{ID: "22222222-2222-4222-8222-222222222222", RequirementKey: "env:sala", Role: "CURRENT", Availability: "AVAILABLE"},
		{ID: "33333333-3333-4333-8333-333333333333", RequirementKey: "env:sala", Role: "REFERENCE", Availability: "AVAILABLE"},
		{ID: "44444444-4444-4444-8444-444444444444", RequirementKey: "env:sala", Role: "CURRENT", Availability: "AVAILABLE"},
	}
	items := []reportcore.ComparisonItem{
		{ItemIdentityID: "item-sofa", Room: "Sala", TypeCode: "SOFA", Name: "Sofá", Outcome: reportcore.ComparisonChanged, BaselineCondition: "GOOD", CurrentCondition: "FAIR", Description: "Rasgo novo no assento.", Pairs: []reportcore.ComparisonPair{{BaselineEvidenceID: evidence[0].ID, CurrentEvidenceID: evidence[1].ID}}, PairingRevised: true},
		{ItemIdentityID: "item-mesa", Room: "Sala", TypeCode: "TABLE", Name: "Mesa", Outcome: reportcore.ComparisonUnchanged, BaselineCondition: "POOR", CurrentCondition: "POOR", Pairs: []reportcore.ComparisonPair{{BaselineEvidenceID: evidence[2].ID, CurrentEvidenceID: evidence[3].ID}}},
		{ItemIdentityID: "item-cortina", Room: "Sala", TypeCode: "CURTAIN_BLIND", Name: "Cortina", Outcome: reportcore.ComparisonNotObserved, BaselineCondition: "GOOD", Limitations: []string{reportcore.LimitationRoomCoverage}},
		{ItemIdentityID: "item-quadro", Room: "Sala", TypeCode: "OTHER", Name: "Quadro", Outcome: reportcore.ComparisonNoBasis, BaselineCondition: "GOOD", Limitations: []string{reportcore.LimitationNoBaselineView}},
	}
	section := &reportcore.ComparisonSection{Baseline: reportcore.ComparisonBaseline{VersionID: "9a6f0d0e-0000-4000-8000-000000000001", VersionNumber: 2, Purpose: "MOVE_IN", ActivatedAt: "2026-09-30T10:00:00Z"}, Status: "COMPLETED", Items: items, Counts: reportcore.CountComparison(items), OutsideBaseline: []reportcore.ComparisonObservation{{Room: "Sala", TypeCode: "OTHER", Name: "Luminária", Description: "Luminária de chão."}}}
	return reportcore.Snapshot{SchemaVersion: 3, ReportID: "RPT-T05", InspectionID: "inspection", TemplateVersionID: "template", ReferenceVersionID: section.Baseline.VersionID, PromptDigest: "digest", Mode: "HISTORICAL", Classification: "ATTENTION", Advisory: "Esta triagem interna descreve alterações observadas e não atribui culpa, custo, responsabilidade ou consequência automática.", Context: reportcore.Context{Asset: reportcore.AssetContext{ID: "asset", Name: "Imóvel", Address: "Rua Exemplo"}, Participant: reportcore.ParticipantContext{ID: "participant", Name: "Participante"}, Template: reportcore.TemplateContext{ID: "template", Name: "Modelo", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: "2026-11-05T00:00:00Z"}}, Evidence: evidence, Processing: &reportcore.ProcessingState{Status: "COMPLETED"}, Checklist: &reportcore.ChecklistSection{Rooms: []reportcore.ChecklistRoom{}, Coverage: reportcore.ChecklistCoverage{Expected: 1, FullyAssessed: 1}}, Scope: &reportcore.ScopeSnapshot{Digest: "scope", Version: 1, Profile: "PROPERTY_COMPONENTS"}, Handover: &reportcore.HandoverSection{Entries: []reportcore.HandoverEntry{}}, Provenance: &reportcore.Provenance{PromptRevision: "prompt"}, Comparison: section}
}

func viewItem(t *testing.T, view *ComparisonView, name string) ComparisonItemView {
	t.Helper()
	for _, item := range view.Items {
		if item.Name == name {
			return item
		}
	}
	t.Fatalf("item %s missing from %+v", name, view.Items)
	return ComparisonItemView{}
}

// UT-078 (happy): a reader of an authorized comparison sees, for a changed
// item, the baseline version, current and prior evidence, status and limits.
func TestUT078ChangedItemShowsBaselineVersionEvidenceStatusAndLimitation(t *testing.T) {
	snapshot := t05Snapshot()
	if err := reportcore.Validate(snapshot); err != nil {
		t.Fatalf("issued snapshot rejected: %v", err)
	}
	view, err := ProjectComparison(snapshot, "")
	if err != nil || view == nil {
		t.Fatalf("ProjectComparison = %+v, %v", view, err)
	}
	if view.BaselineVersionNumber != 2 || !strings.Contains(view.BaselineLine, "Referência aprovada: versão 2 · Entrada no imóvel · aprovada em 30/09/2026") {
		t.Fatalf("baseline = %d %q", view.BaselineVersionNumber, view.BaselineLine)
	}
	sofa := viewItem(t, view, "Sofá")
	if sofa.OutcomeLabel != "Alterado" || sofa.BaselineCondition != "Bom" || sofa.CurrentCondition != "Regular" || len(sofa.Pairs) != 1 || sofa.Pairs[0].BaselineEvidence != "E001" || sofa.Pairs[0].CurrentEvidence != "E002" || sofa.Pairs[0].BaselineAvailability != "AVAILABLE" {
		t.Fatalf("changed item = %+v", sofa)
	}
	if curtain := viewItem(t, view, "Cortina"); len(curtain.Limitations) != 1 || curtain.Limitations[0] != reportcore.LimitationRoomCoverage {
		t.Fatalf("not observed item limitations = %+v", curtain.Limitations)
	}
}

// UT-079 (error): an invalid comparison filter yields a safe error and no data.
func TestUT079InvalidComparisonFilterReturnsNoData(t *testing.T) {
	view, err := ProjectComparison(t05Snapshot(), "ALL_GOOD")
	if !errors.Is(err, reportcore.ErrInvalidComparisonFilter) || view != nil {
		t.Fatalf("invalid filter = %+v, %v; want an error without data", view, err)
	}
	legacy := t05Snapshot()
	legacy.Comparison = nil
	if view, err := ProjectComparison(legacy, "ALL_GOOD"); !errors.Is(err, reportcore.ErrInvalidComparisonFilter) || view != nil {
		t.Fatalf("invalid filter on a report without comparison = %+v, %v", view, err)
	}
	filtered, err := ProjectComparison(t05Snapshot(), reportcore.ComparisonNoBasis)
	if err != nil || filtered.Displayed != 1 || filtered.Counts.Total != 4 || filtered.Items[0].Name != "Quadro" {
		t.Fatalf("valid filter = %+v, %v", filtered, err)
	}
}

// UT-080 (boundary): a comparison without supported change shows coverage and
// unresolved items instead of "everything unchanged".
func TestUT080NoSupportedChangeShowsUnresolvedItemsNotEverythingUnchanged(t *testing.T) {
	snapshot := t05Snapshot()
	snapshot.Classification = "NORMAL"
	items := []reportcore.ComparisonItem{snapshot.Comparison.Items[2], snapshot.Comparison.Items[3]}
	snapshot.Comparison.Items, snapshot.Comparison.Counts = items, reportcore.CountComparison(items)
	snapshot.Evidence = snapshot.Evidence[:1]
	if err := reportcore.Validate(snapshot); err != nil {
		t.Fatal(err)
	}
	view, err := ProjectComparison(snapshot, "")
	if err != nil {
		t.Fatal(err)
	}
	if view.Counts.Unchanged != 0 || view.Counts.NotObserved != 1 || view.Counts.NoBasis != 1 || !strings.Contains(view.CountsLine, "1 não observados · 1 sem base para comparar") {
		t.Fatalf("view = %+v", view)
	}
	if label := reportcore.ClassificationLabelForSnapshot(snapshot); label == reportcore.ClassificationLabel("NORMAL") {
		t.Fatalf("classification label = %q; unresolved items cannot read as no alerts", label)
	}
	for _, item := range view.Items {
		if item.Outcome == reportcore.ComparisonUnchanged || item.OutcomeLabel == "Sem alteração" {
			t.Fatalf("item %+v presented as unchanged", item)
		}
	}
}

// UT-083 (boundary): temporarily unavailable prior media is labeled
// unavailable while its historical conclusion and version remain.
func TestUT083UnavailablePriorMediaKeepsConclusionAndVersion(t *testing.T) {
	snapshot := t05Snapshot()
	before, err := ProjectComparison(snapshot, "")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Evidence[0].Availability = "MISSING"
	after, err := ProjectComparison(snapshot, "")
	if err != nil {
		t.Fatal(err)
	}
	sofaBefore, sofaAfter := viewItem(t, before, "Sofá"), viewItem(t, after, "Sofá")
	if sofaAfter.Pairs[0].BaselineAvailability != "UNAVAILABLE" || sofaAfter.Pairs[0].CurrentAvailability != "AVAILABLE" {
		t.Fatalf("pair availability = %+v; the prior image must be labeled unavailable", sofaAfter.Pairs[0])
	}
	if sofaAfter.Outcome != sofaBefore.Outcome || sofaAfter.CurrentCondition != sofaBefore.CurrentCondition || after.BaselineVersionNumber != before.BaselineVersionNumber || after.Counts != before.Counts {
		t.Fatalf("conclusion or version changed with media availability: before=%+v after=%+v", sofaBefore, sofaAfter)
	}
	encoded, _ := json.Marshal(after)
	if strings.Contains(string(encoded), "http") || strings.Contains(string(encoded), snapshot.Evidence[0].ID) {
		t.Fatalf("customer view exposes a URL or raw evidence id: %s", encoded)
	}
}

// UT-082 (error): a customer cannot see internal confidence, staff backstage
// or evidence outside the authorized report.
func TestUT082CustomerComparisonOmitsInternalFieldsAndForeignEvidence(t *testing.T) {
	snapshot := t05Snapshot()
	filtered := FilterComparison(snapshot)
	customer := snapshot
	customer.Comparison, customer.Scope, customer.Provenance, customer.Findings = filtered, nil, nil, nil
	if err := reportcore.ValidateCustomerSnapshot(customer); err != nil {
		t.Fatalf("customer copy rejected: %v", err)
	}
	for _, item := range filtered.Items {
		if item.ItemIdentityID != "" || item.TypeCode != "" || item.Description != "" || item.PairingRevised {
			t.Fatalf("customer item keeps internal fields: %+v", item)
		}
	}
	if filtered.Baseline.VersionID != "" || filtered.OutsideBaseline[0].TypeCode != "" || filtered.OutsideBaseline[0].Description != "" {
		t.Fatalf("customer comparison keeps internal fields: %+v", filtered)
	}
	view, _ := ProjectComparison(snapshot, "")
	encoded, _ := json.Marshal(view)
	for _, secret := range []string{"item-sofa", "9a6f0d0e", "Rasgo novo", "SOFA", "Luminária de chão", "PairingRevised\":true"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("customer view leaks %q: %s", secret, encoded)
		}
	}
	// A pair citing evidence outside the issued report is rejected outright.
	foreign := snapshot
	foreign.Comparison = FilterComparison(snapshot)
	foreign.Comparison.Items[0].Pairs = []reportcore.ComparisonPair{{BaselineEvidenceID: "55555555-5555-4555-8555-555555555555", CurrentEvidenceID: snapshot.Evidence[1].ID}}
	foreign.Scope, foreign.Provenance = nil, nil
	if err := reportcore.ValidateCustomerSnapshot(foreign); err == nil {
		t.Fatal("a customer comparison citing evidence outside the report was accepted")
	}
}

// blameProbePhrases are the sentences the review round 2 probe showed the
// former filter accepting.
var blameProbePhrases = []string{
	"O morador é responsável pelo dano", "deve ser cobrado do ocupante", "Dano causado por mau uso", "O conserto custará cerca de 300 reais",
	"Reparo estimado em 300 reais", "preço do reparo", "o proprietário deve pagar", "Desgaste esperado pelo tempo de uso", "Deterioração natural do material",
	"normal wear", "The occupant is responsible for the damage", "repair will cost about 300", "damage caused by misuse", "uso indevido pelo morador",
	"Valor do conserto: R$ 300", "o inquilino pagou", "cobrança ao locatário", "tenant must pay", "charge the occupant", "price of the repair",
}

// UT-118 (error): no comparison output states tenant responsibility, normal
// wear or repair cost.
func TestUT118ComparisonOutputNeverStatesResponsibilityWearOrCost(t *testing.T) {
	blame := []string{"Dano causado pelo inquilino.", "Desgaste natural de uso.", "Custo de reparo estimado em R$ 300.", "Responsabilidade do locatário."}
	// Every phrase the review round 2 probe showed passing, in PT and EN,
	// plus case and accent variants: the report text is generated from
	// controlled vocabulary, and this filter is the validation backstop.
	for _, text := range blameProbePhrases {
		blame = append(blame, text, strings.ToUpper(text), strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ç", "c", "ã", "a").Replace(text))
	}
	for _, text := range blame {
		if reportcore.ComparisonTextAllowed(text) {
			t.Fatalf("%q accepted as neutral comparison text", text)
		}
		snapshot := t05Snapshot()
		snapshot.Comparison.Items[0].Description = text
		if err := reportcore.Validate(snapshot); err == nil {
			t.Fatalf("a comparison stating %q was accepted", text)
		}
	}
	snapshot := t05Snapshot()
	view, err := ProjectComparison(snapshot, "")
	if err != nil {
		t.Fatal(err)
	}
	texts := []string{view.BaselineLine, view.CountsLine, view.StatusLabel}
	for _, item := range view.Items {
		texts = append(texts, item.Room, item.Name, item.OutcomeLabel, item.BaselineCondition, item.CurrentCondition)
		texts = append(texts, item.Limitations...)
	}
	for _, text := range texts {
		if !reportcore.ComparisonTextAllowed(text) {
			t.Fatalf("customer comparison states %q", text)
		}
	}
	html, err := reportcore.HTML(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(html), `<section id="comparison">`)
	section := strings.Replace(string(html)[start:], reportcore.ComparisonDisclaimer, "", 1)
	if !reportcore.ComparisonTextAllowed(section) {
		t.Fatalf("comparison HTML states responsibility, wear or cost: %s", section)
	}
	if !strings.Contains(string(html), reportcore.ComparisonDisclaimer) {
		t.Fatal("comparison HTML must keep the neutral disclaimer")
	}
}

// Item 11 (review round 2, UT-082): the internal pairing detail "a pair of
// another room was rejected" and an incoherent analysis answer never reach a
// customer; the reader sees the neutral text of the same gap.
func TestTask05R2CustomerSeesNeutralLimitationForInternalPairingDetails(t *testing.T) {
	snapshot := t05Snapshot()
	snapshot.Comparison.Items[2].Limitations = []string{reportcore.LimitationPairRejected}
	snapshot.Comparison.Items[3].Outcome, snapshot.Comparison.Items[3].Limitations = reportcore.ComparisonNotObserved, []string{reportcore.LimitationInconsistentClaim}
	snapshot.Comparison.Counts = reportcore.CountComparison(snapshot.Comparison.Items)
	view, err := ProjectComparison(snapshot, "")
	if err != nil {
		t.Fatal(err)
	}
	filtered := FilterComparison(snapshot)
	encoded, _ := json.Marshal(struct {
		View     *ComparisonView
		Filtered *reportcore.ComparisonSection
	}{view, filtered})
	for _, internal := range []string{reportcore.LimitationPairRejected, reportcore.LimitationInconsistentClaim, "outro ambiente", "coerente"} {
		if strings.Contains(string(encoded), internal) {
			t.Fatalf("customer comparison shows internal detail %q: %s", internal, encoded)
		}
	}
	if got := viewItem(t, view, "Cortina").Limitations; len(got) != 1 || got[0] != reportcore.LimitationNoCurrentView {
		t.Fatalf("rejected pair limitation = %v; want the neutral no-current-view text", got)
	}
	if got := viewItem(t, view, "Quadro").Limitations; len(got) != 1 || got[0] != reportcore.LimitationComparisonNoResult {
		t.Fatalf("inconsistent claim limitation = %v; want the neutral no-result text", got)
	}
	// The internal copy keeps the precise limitation for staff.
	if snapshot.Comparison.Items[2].Limitations[0] != reportcore.LimitationPairRejected {
		t.Fatal("the staff limitation was changed")
	}
}
