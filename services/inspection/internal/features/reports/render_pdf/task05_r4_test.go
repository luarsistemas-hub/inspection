package render_pdf

import (
	"context"
	"encoding/json"
	"html"
	"os"
	"regexp"
	"strings"
	"testing"

	"inspection/libs/identity"
	core "inspection/services/inspection/internal/features/reports/core"
	customerreport "inspection/services/inspection/internal/features/reports/customer/core"
	"inspection/services/inspection/internal/platform/database"
)

// rawReportTokens are internal identifiers a reader must never see: capture
// requirement keys and UUIDs.
var rawReportTokens = regexp.MustCompile(`(?i)env:|origin:|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

var htmlTags = regexp.MustCompile(`(?s)<style.*?</style>|<[^>]+>`)

// visibleHTMLText is the text a reader sees in a rendered HTML report.
func visibleHTMLText(document []byte) string {
	return html.UnescapeString(htmlTags.ReplaceAllString(string(document), " "))
}

// task05RawKeySnapshot is a complete v3 comparison whose evidence carries the
// requirement keys real reports carry (scope keys, "env:<identity>" and
// "origin:<uuid>"), with no description: capture photos, baseline photos of
// the pairs and unavailable media have none.
func task05RawKeySnapshot() core.Snapshot {
	snapshot := task05ComparisonSnapshot(6)
	identityKey, originKey := "env:"+identity.NewID().String(), "origin:"+identity.NewID().String()
	// Paired evidence carries the room key of its item.
	for i := range snapshot.Evidence {
		snapshot.Evidence[i].Description = ""
		snapshot.Evidence[i].RequirementKey = identityKey
	}
	// Promoted origin photos and evidence no item cites still never show a key.
	snapshot.Evidence = append(snapshot.Evidence,
		core.Evidence{ID: identity.NewID().String(), RequirementKey: originKey, Role: "REFERENCE", Availability: "MISSING"},
		core.Evidence{ID: identity.NewID().String(), RequirementKey: identityKey, Role: "CURRENT", Availability: "MISSING"},
	)
	for i := range snapshot.Comparison.Items {
		snapshot.Comparison.Items[i].RoomKey = identityKey
	}
	return snapshot
}

// Item 1 (round 4): the v3 evidence index of the staff and customer PDFs and
// the staff and customer HTML never print a requirement key or a UUID; an
// evidence without description is named by its role and room.
func TestTask05R4EvidenceIndexNeverPrintsRequirementKeys(t *testing.T) {
	env := newTestEnv(t)
	snapshot := task05RawKeySnapshot()
	if len(snapshot.Evidence) <= 10 {
		t.Fatalf("fixture has %d evidence; the index needs more than 10", len(snapshot.Evidence))
	}
	if err := core.Validate(snapshot); err != nil {
		t.Fatalf("validate: %v", err)
	}
	canonical, digest, err := core.CanonicalJSON(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.db.Model(&database.ReportSnapshot{}).Where("id=?", env.snapshot.ID).Updates(map[string]any{"canonical_json": canonical, "json_digest": digest}).Error; err != nil {
		t.Fatal(err)
	}
	for _, audience := range []string{"PDF", "PDF_CUSTOMER"} {
		job := env.addJob(t, audience, "QUEUED", 0)
		if worked, err := env.executor.RunDue(context.Background()); err != nil || !worked {
			t.Fatalf("%s worked=%v err=%v", audience, worked, err)
		}
		completed := env.job(t, job.ID)
		if completed.Status != "READY" || completed.ArtifactID == nil {
			t.Fatalf("%s job = %+v", audience, completed)
		}
		var artifact database.ReportArtifact
		if err := env.db.Where("tenant_id=? AND id=?", env.tenantID, *completed.ArtifactID).Take(&artifact).Error; err != nil {
			t.Fatal(err)
		}
		text := assertV3PDFTextAndPages(t, env.client.objects[artifact.ObjectKey], 1, []string{"Anexo de evidências · índice", "E001 · Referência · Sala", "E002 · Vistoria atual · Sala"})
		if raw := rawReportTokens.FindString(text); raw != "" {
			index := strings.Index(text, raw)
			t.Fatalf("%s prints an internal identifier %q in %q", audience, raw, text[max(0, index-40):min(len(text), index+60)])
		}
	}
	for _, internal := range []bool{true, false} {
		// The stored HTML (core.HTML) is the internal rendering; the
		// non-internal rendering is the audience-neutral copy.
		rendered, err := core.HTMLForPDF(snapshot, nil, internal)
		if err != nil {
			t.Fatal(err)
		}
		text := visibleHTMLText(rendered)
		if raw := rawReportTokens.FindString(text); raw != "" {
			index := strings.Index(text, raw)
			t.Fatalf("HTML internal=%v prints an internal identifier %q in %q", internal, raw, text[max(0, index-40):min(len(text), index+60)])
		}
	}
	// The label never falls back to the key, whatever the evidence cites.
	for _, evidence := range snapshot.Evidence {
		if label := core.EvidenceIndexLabel(snapshot, evidence); rawReportTokens.MatchString(label) || label == "" {
			t.Fatalf("index label of %+v = %q", evidence, label)
		}
	}
}

// Item 4 (round 4): one report v3 names each of the six item outcomes with
// the same words on every surface: the comparison (HTML and PDF groups), the
// inventory section (HTML and PDF headings), the GraphQL statusLabel the
// Dashboard shows, and the shared golden file the Dashboard tests read.
// Reports v2 keep their historical inventory labels.
func TestTask05R4OutcomeLabelsAgreeAcrossSurfaces(t *testing.T) {
	raw, err := os.ReadFile("../core/testdata/comparison-outcome-labels.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]string
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	outcomes := map[string]string{"PRESENT_UNCHANGED": core.ComparisonUnchanged, "CONDITION_CHANGED": core.ComparisonChanged, "IMPROVED": core.ComparisonImproved, "MISSING": core.ComparisonMissing, "NOT_OBSERVED": core.ComparisonNotObserved, core.InventoryNoBasis: core.ComparisonNoBasis}
	if len(golden) != len(outcomes) || len(core.ComparisonOutcomes) != len(outcomes) {
		t.Fatalf("golden %v, outcomes %v", golden, core.ComparisonOutcomes)
	}
	for status, outcome := range outcomes {
		want := golden[status]
		if want == "" || core.ComparisonOutcomeLabel(outcome) != want || core.InventoryStatusLabel(status) != want {
			t.Fatalf("%s: golden %q comparison %q inventory %q", status, want, core.ComparisonOutcomeLabel(outcome), core.InventoryStatusLabel(status))
		}
		entry := core.InventoryEntry{Environment: "Sala", Type: "SOFA", Name: "Sofá", Status: status}
		v3 := task05ComparisonSnapshot(1)
		v3.Inventory.Problems = []core.InventoryEntry{entry}
		if heading := presentationFor(v3).inventoryProblemHeading(entry); heading != want+" · Sala · Sofá" {
			t.Fatalf("%s: v3 PDF heading %q", status, heading)
		}
		// The GraphQL statusLabel the Dashboard shows.
		view := customerreport.MapInventory(v3)
		if len(view.Problems) != 1 || view.Problems[0].StatusLabel == nil || *view.Problems[0].StatusLabel != want {
			t.Fatalf("%s: GraphQL statusLabel %+v", status, view.Problems)
		}
		// The v3 HTML inventory section (shown without a comparison).
		withoutComparison := v3
		withoutComparison.Comparison = nil
		rendered, err := core.HTMLForPDF(withoutComparison, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(visibleHTMLText(rendered), "SOFA · "+want) {
			t.Fatalf("%s: v3 HTML inventory does not say %q", status, want)
		}
		// Reports v2 keep their historical labels and carry no statusLabel.
		v2 := v3
		v2.SchemaVersion = 2
		if view := customerreport.MapInventory(v2); view.Problems[0].StatusLabel != nil {
			t.Fatalf("%s: v2 entry got statusLabel %q", status, *view.Problems[0].StatusLabel)
		}
		if heading := presentationFor(v2).inventoryProblemHeading(entry); heading != legacyPDF.inventoryProblemHeading(entry) || heading != inventoryProblemHeading(entry) {
			t.Fatalf("%s: v2 PDF heading changed: %q", status, heading)
		}
	}
	// The comparison groups of the HTML and the PDF use the same labels.
	snapshot := task05ComparisonSnapshot(1)
	rendered, err := core.HTMLForPDF(snapshot, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	html := visibleHTMLText(rendered)
	for _, group := range core.GroupComparison(*snapshot.Comparison) {
		if group.Count > 0 && !strings.Contains(html, group.Label+" · ") {
			t.Fatalf("HTML comparison lacks group %q", group.Label)
		}
	}
	// v2 historical wording is unchanged byte for byte.
	if legacyPDF.inventoryProblemHeading(core.InventoryEntry{Environment: "Sala", Name: "Sofá", Status: "MISSING"}) != "Itens ausentes · Sala · Sofá" || label("CONDITION_CHANGED") != "Condição alterada" || label("IMPROVED") != "Condição melhorada" {
		t.Fatal("v2 PDF vocabulary changed")
	}
}
