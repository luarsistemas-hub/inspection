package core

import (
	"encoding/json"
	"html"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	reportcore "inspection/services/inspection/internal/features/reports/core"
)

func TestTask07CustomerComparisonHTMLMatchesSharedPresentationGolden(t *testing.T) {
	golden, err := os.ReadFile("testdata/task07_comparison_v3.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected struct {
		Counts     int      `json:"counts"`
		Outcomes   []string `json:"outcomes"`
		Pair       string   `json:"pair"`
		Disclaimer string   `json:"disclaimer"`
	}
	if err := json.Unmarshal(golden, &expected); err != nil {
		t.Fatal(err)
	}
	items := []reportcore.ComparisonItem{
		{Room: "Sala", RoomKey: "env:sala", Name: "Sofá", Outcome: reportcore.ComparisonUnchanged, BaselineCondition: "GOOD", CurrentCondition: "GOOD", Pairs: []reportcore.ComparisonPair{{BaselineEvidenceID: "ref-1", CurrentEvidenceID: "cur-2"}}},
		{Room: "Sala", RoomKey: "env:sala", Name: "Mesa", Outcome: reportcore.ComparisonChanged, BaselineCondition: "GOOD", CurrentCondition: "FAIR", Pairs: []reportcore.ComparisonPair{{BaselineEvidenceID: "ref-1", CurrentEvidenceID: "cur-1"}}},
		{Room: "Quarto", RoomKey: "env:quarto", Name: "Cama", Outcome: reportcore.ComparisonImproved, BaselineCondition: "FAIR", CurrentCondition: "GOOD", Pairs: []reportcore.ComparisonPair{{BaselineEvidenceID: "ref-2", CurrentEvidenceID: "cur-3"}}},
		{Room: "Cozinha", RoomKey: "env:cozinha", Name: "Cadeira", Outcome: reportcore.ComparisonMissing, BaselineCondition: "GOOD", Pairs: []reportcore.ComparisonPair{{BaselineEvidenceID: "ref-2", CurrentEvidenceID: "cur-4"}}},
		{Room: "Quarto", RoomKey: "env:quarto", Name: "Cômoda", Outcome: reportcore.ComparisonNotObserved, BaselineCondition: "GOOD", Limitations: []string{reportcore.LimitationNoCurrentView}},
		{Room: "Cozinha", RoomKey: "env:cozinha", Name: "Armário", Outcome: reportcore.ComparisonNoBasis, Limitations: []string{reportcore.LimitationNoBaselineView}},
	}
	evidence := []reportcore.Evidence{
		{ID: "ref-1", RequirementKey: "env:sala", Role: "REFERENCE", Availability: "AVAILABLE"},
		{ID: "cur-1", RequirementKey: "env:sala", Role: "CURRENT", Availability: "AVAILABLE"},
		{ID: "cur-2", RequirementKey: "env:sala", Role: "CURRENT", Availability: "AVAILABLE"},
		{ID: "ref-2", RequirementKey: "env:quarto", Role: "REFERENCE", Availability: "AVAILABLE"},
		{ID: "cur-3", RequirementKey: "env:quarto", Role: "CURRENT", Availability: "AVAILABLE"},
		{ID: "cur-4", RequirementKey: "env:cozinha", Role: "CURRENT", Availability: "AVAILABLE"},
	}
	snapshot := reportcore.Snapshot{SchemaVersion: 3, ReportID: "report-task07", InspectionID: "inspection-task07", TemplateVersionID: "template-task07", ReferenceVersionID: "baseline-task07", PromptDigest: "prompt-task07", Mode: "HISTORICAL", Classification: "ATTENTION", Advisory: reportcore.ComparisonDisclaimer, Context: reportcore.Context{Asset: reportcore.AssetContext{ID: "asset-task07", Name: "Imóvel QA", Address: "Rua QA"}, Participant: reportcore.ParticipantContext{ID: "participant-task07", Name: "Pessoa QA"}, Template: reportcore.TemplateContext{ID: "template-task07", Name: "Modelo QA", Version: 3}, Inspection: reportcore.InspectionContext{GeneratedAt: "2026-10-02T12:00:00Z"}}, Evidence: evidence, Checklist: &reportcore.ChecklistSection{Rooms: []reportcore.ChecklistRoom{}, Coverage: reportcore.ChecklistCoverage{UnknownScope: true}}, Processing: &reportcore.ProcessingState{Status: "COMPLETED"}, Scope: &reportcore.ScopeSnapshot{Unknown: true}, Provenance: &reportcore.Provenance{PromptRevision: "prompt-v1"}, Handover: &reportcore.HandoverSection{Entries: []reportcore.HandoverEntry{}}, Comparison: &reportcore.ComparisonSection{Baseline: reportcore.ComparisonBaseline{VersionID: "baseline-task07", VersionNumber: 4, Purpose: "MOVE_IN", ActivatedAt: "2026-10-01T12:00:00Z"}, Status: "COMPLETED", Items: items, Counts: reportcore.CountComparison(items), OutsideBaseline: []reportcore.ComparisonObservation{}}}
	rendered, err := reportcore.HTMLForPDF(snapshot, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	visible := html.UnescapeString(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(string(rendered), " "))
	for _, want := range append(append([]string{strconv.Itoa(expected.Counts) + " itens da referência", "1 de " + strconv.Itoa(expected.Counts) + " itens", expected.Pair, expected.Disclaimer}, expected.Outcomes...), "Sem base para comparar") {
		if !strings.Contains(visible, want) {
			t.Errorf("backend customer HTML omits shared comparison text %q; HTML text: %s", want, visible)
		}
	}
	for _, forbidden := range []string{"UUID", "env:", "origin:", "culpa atribuída", "responsabilidade do inquilino", "R$"} {
		if strings.Contains(visible, forbidden) {
			t.Errorf("backend customer HTML contains forbidden text %q", forbidden)
		}
	}
}
