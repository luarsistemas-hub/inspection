package core

import (
	"bytes"
	"encoding/json"
	"html"
	"os"
	"regexp"
	"strings"
	"testing"

	reportcore "inspection/services/inspection/internal/features/reports/core"
)

var customerHTMLTags = regexp.MustCompile(`<[^>]+>`)

func TestTask07CustomerChecklistProjectionMatchesSharedGolden(t *testing.T) {
	snapshot := reportcore.Snapshot{
		SchemaVersion:      3,
		ReportID:           "report-task07",
		InspectionID:       "inspection-task07",
		TemplateVersionID:  "template-task07",
		ReferenceVersionID: "reference-task07",
		PromptDigest:       "prompt-task07",
		Mode:               "HISTORICAL",
		Classification:     "NORMAL",
		Advisory:           "Laudo descritivo.",
		Context: reportcore.Context{
			Asset:       reportcore.AssetContext{ID: "asset", Name: "Imóvel QA", Address: "Rua QA"},
			Participant: reportcore.ParticipantContext{ID: "participant", Name: "Pessoa QA"},
			Template:    reportcore.TemplateContext{ID: "template", Name: "Modelo", Version: 3},
			Inspection:  reportcore.InspectionContext{GeneratedAt: "2026-10-01T00:00:00Z"},
		},
		Scope:      &reportcore.ScopeSnapshot{Digest: "scope-task07", Version: 1, Profile: "PROPERTY_COMPONENTS"},
		Provenance: &reportcore.Provenance{PromptRevision: "prompt-v1", ModelRevision: "model-v1", ScopeDigest: "scope-task07"},
		Checklist: &reportcore.ChecklistSection{
			Coverage: reportcore.ChecklistCoverage{Expected: 1, FullyAssessed: 1},
			Rooms: []reportcore.ChecklistRoom{{Key: "kitchen", Name: "Cozinha", Components: []reportcore.ChecklistComponent{{
				Label: "Armários", Status: "OBSERVED", Description: "Armários íntegros nas vistas disponíveis.", Condition: "GOOD", ReviewStatus: "NOT_REVIEWED",
				EvidenceIDs: []string{"current-photo"}, Limitations: []string{"A parte superior não foi observada."},
			}}}},
		},
		Processing: &reportcore.ProcessingState{Status: "COMPLETED"},
		Handover:   &reportcore.HandoverSection{Entries: []reportcore.HandoverEntry{}},
		Evidence:   []reportcore.Evidence{{ID: "current-photo", RequirementKey: "room:kitchen", Role: "CURRENT", Availability: "AVAILABLE"}},
	}
	view := ProjectChecklist(snapshot)
	actual, err := json.MarshalIndent(struct {
		Coverage ChecklistCoverageView `json:"coverage"`
		Rooms    []ChecklistRoomView   `json:"rooms"`
	}{Coverage: view.Coverage, Rooms: view.Rooms}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	actual = append(actual, '\n')
	golden, err := os.ReadFile("testdata/customer_report_v3.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, golden) {
		t.Fatalf("customer checklist projection differs from shared golden\nactual:\n%s", actual)
	}

	rendered, err := reportcore.HTMLForPDF(snapshot, nil, false)
	if err != nil {
		t.Fatalf("render customer HTML for shared presentation golden: %v", err)
	}
	visibleHTML := html.UnescapeString(customerHTMLTags.ReplaceAllString(string(rendered), " "))
	for _, expectedText := range []string{
		"Observado",
		"Armários íntegros nas vistas disponíveis.",
		"Bom",
		"Não revisado",
		"E001",
		"A parte superior não foi observada.",
	} {
		if !strings.Contains(visibleHTML, expectedText) {
			t.Errorf("customer HTML for the same snapshot omits presentation golden text %q", expectedText)
		}
	}
}
