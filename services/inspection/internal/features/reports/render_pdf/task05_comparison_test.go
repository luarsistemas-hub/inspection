package render_pdf

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestTask07SharedComparisonGoldenMatchesHTMLAndExtractedCustomerPDF(t *testing.T) {
	golden, err := os.ReadFile("../customer/core/testdata/task07_comparison_v3.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected struct {
		Counts     int      `json:"counts"`
		Outcomes   []string `json:"outcomes"`
		Disclaimer string   `json:"disclaimer"`
	}
	if err := json.Unmarshal(golden, &expected); err != nil {
		t.Fatal(err)
	}
	snapshot := task05ComparisonSnapshot(1)
	markup, err := core.HTMLForPDF(snapshot, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	visibleHTML := html.UnescapeString(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(string(markup), " "))
	pairPattern := regexp.MustCompile(`Referência E[0-9]{3} ↔ vistoria atual E[0-9]{3}`)
	pair := pairPattern.FindString(visibleHTML)
	if pair == "" {
		t.Fatalf("customer HTML did not render the paired evidence references: %s", visibleHTML)
	}
	shared := append([]string{fmt.Sprintf("%d itens da referência", expected.Counts), fmt.Sprintf("1 de %d itens", expected.Counts), pair, expected.Disclaimer}, expected.Outcomes...)
	for _, item := range shared {
		if !strings.Contains(visibleHTML, item) {
			t.Errorf("HTML from backend snapshot omits shared golden text %q", item)
		}
	}
	customerSnapshot, _, err := projectForAudience(snapshot, "PDF_CUSTOMER")
	if err != nil {
		t.Fatal(err)
	}
	pdfData, err := (MarotoRenderer{}).Render(context.Background(), PDFDocument{SnapshotID: "task07-shared-golden", Version: 1, Snapshot: customerSnapshot}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pdfText := assertV3PDFTextAndPages(t, pdfData, 1, shared)
	for _, forbidden := range []string{"culpa do inquilino", "culpa do proprietário", "env:", "origin:", "UUID"} {
		if strings.Contains(visibleHTML, forbidden) || strings.Contains(pdfText, forbidden) {
			t.Errorf("customer presentation contains forbidden text %q", forbidden)
		}
	}
}

// task05ComparisonSnapshot is a "complete" v3 move-out report: checklist,
// handover, the historical inventory section and a comparison of a large
// baseline with every outcome. Paired images are unavailable at render time,
// so the documents stay text-only.
func task05ComparisonSnapshot(unchanged int) core.Snapshot {
	snapshot := task04PDFSnapshot(0)
	snapshot.ReferenceVersionID = identity.NewID().String()
	snapshot.Handover.Entries[0].EvidenceIDs = nil
	var sofaPair core.ComparisonPair
	add := func(n int) []core.ComparisonPair {
		baseline, current := identity.NewID().String(), identity.NewID().String()
		if n == 900 {
			sofaPair = core.ComparisonPair{BaselineEvidenceID: baseline, CurrentEvidenceID: current}
		}
		snapshot.Evidence = append(snapshot.Evidence, core.Evidence{ID: baseline, RequirementKey: "env:sala", Role: "REFERENCE", Description: "Referência", Availability: "MISSING"}, core.Evidence{ID: current, RequirementKey: "env:sala", Role: "CURRENT", Description: "Vistoria atual", Availability: "MISSING"})
		return []core.ComparisonPair{{BaselineEvidenceID: baseline, CurrentEvidenceID: current}}
	}
	items := []core.ComparisonItem{}
	for n := 0; n < unchanged; n++ {
		items = append(items, core.ComparisonItem{ItemIdentityID: identity.NewID().String(), Room: "Sala", TypeCode: "CHAIR", Name: fmt.Sprintf("Cadeira %02d", n), Outcome: core.ComparisonUnchanged, BaselineCondition: "GOOD", CurrentCondition: "GOOD", Pairs: add(n)})
	}
	items = append(items,
		core.ComparisonItem{ItemIdentityID: identity.NewID().String(), Room: "Sala", TypeCode: "SOFA", Name: "Sofá", Outcome: core.ComparisonChanged, BaselineCondition: "GOOD", CurrentCondition: "FAIR", Description: "Rasgo novo no assento.", Pairs: add(900), PairingRevised: true},
		core.ComparisonItem{ItemIdentityID: identity.NewID().String(), Room: "Sala", TypeCode: "TABLE", Name: "Mesa", Outcome: core.ComparisonImproved, BaselineCondition: "POOR", CurrentCondition: "GOOD", Pairs: add(901)},
		core.ComparisonItem{ItemIdentityID: identity.NewID().String(), Room: "Sala", TypeCode: "APPLIANCE_OTHER", Name: "Micro-ondas", Outcome: core.ComparisonMissing, BaselineCondition: "GOOD", Pairs: add(902)},
		core.ComparisonItem{ItemIdentityID: identity.NewID().String(), Room: "Sala", TypeCode: "CURTAIN_BLIND", Name: "Cortina", Outcome: core.ComparisonNotObserved, BaselineCondition: "GOOD", Limitations: []string{core.LimitationRoomCoverage}},
		core.ComparisonItem{ItemIdentityID: identity.NewID().String(), Room: "Sala", TypeCode: "OTHER", Name: "Quadro", Outcome: core.ComparisonNoBasis, BaselineCondition: "GOOD", Limitations: []string{core.LimitationNoBaselineView}},
	)
	snapshot.Comparison = &core.ComparisonSection{Baseline: core.ComparisonBaseline{VersionID: snapshot.ReferenceVersionID, VersionNumber: 4, Purpose: "MOVE_IN", ActivatedAt: "2026-10-01T00:00:00Z"}, Status: "COMPLETED", Items: items, Counts: core.CountComparison(items), OutsideBaseline: []core.ComparisonObservation{{Room: "Sala", TypeCode: "OTHER", Name: "Luminária", Description: "Luminária de chão."}}}
	snapshot.Inventory = &core.InventorySection{Reference: core.InventoryReference{VersionID: snapshot.ReferenceVersionID, VersionNumber: 4, Purpose: "MOVE_IN", ActivatedAt: "2026-10-01T00:00:00Z"}, Coverage: &core.InventoryCoverage{ExpectedItems: len(items), AssessedItems: len(items) - 2, NotObserved: 2, EnvironmentsNotCaptured: []string{}, EnvironmentsAddedDuringCapture: []string{}}, Problems: []core.InventoryEntry{{ItemIdentityID: identity.NewID().String(), Environment: "Sala", Type: "SOFA", Name: "Sofá", Status: "CONDITION_CHANGED", ConditionBefore: "GOOD", ConditionAfter: "FAIR", Pairs: []core.InventoryEvidencePair{{Origin: sofaPair.BaselineEvidenceID, Current: sofaPair.CurrentEvidenceID, Decision: "SAME_VIEW"}}}}, Informational: []core.InventoryEntry{}}
	return snapshot
}

// UT-081 (boundary): a large baseline shows the same total and displayed
// counts across the customer filters, the staff PDF and the customer PDF,
// rendered by the real executor from the same issued snapshot.
func TestUT081LargeBaselineCountsAgreeAcrossFiltersAndPDFSections(t *testing.T) {
	env := newTestEnv(t)
	snapshot := task05ComparisonSnapshot(34)
	if err := core.Validate(snapshot); err != nil {
		t.Fatalf("complete comparison snapshot rejected: %v", err)
	}
	canonical, digest, err := core.CanonicalJSON(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.db.Model(&database.ReportSnapshot{}).Where("id=?", env.snapshot.ID).Updates(map[string]any{"canonical_json": canonical, "json_digest": digest}).Error; err != nil {
		t.Fatal(err)
	}
	groups := []string{"Sem alteração · 34 de 39 itens", "Alterado · 1 de 39 itens", "Melhorou · 1 de 39 itens", "Ausência comprovada · 1 de 39 itens", "Não observado · 1 de 39 itens", "Sem base para comparar · 1 de 39 itens", "39 itens da referência · 34 sem alteração · 1 alterados · 1 melhoraram · 1 com ausência comprovada · 1 não observados · 1 sem base para comparar", "Referência aprovada: versão 4"}
	texts := map[string]string{}
	for _, audience := range []string{"PDF", "PDF_CUSTOMER"} {
		job := env.addJob(t, audience, "QUEUED", 0)
		if worked, err := env.executor.RunDue(context.Background()); err != nil || !worked {
			t.Fatalf("%s worked=%v err=%v", audience, worked, err)
		}
		completed := env.job(t, job.ID)
		if completed.Status != "READY" || completed.ArtifactID == nil {
			t.Fatalf("%s PDF of a complete comparison was not published: %+v", audience, completed)
		}
		var artifact database.ReportArtifact
		if err := env.db.Where("tenant_id=? AND id=?", env.tenantID, *completed.ArtifactID).Take(&artifact).Error; err != nil {
			t.Fatal(err)
		}
		texts[audience] = assertV3PDFTextAndPages(t, env.client.objects[artifact.ObjectKey], 1, groups)
	}
	if !strings.Contains(texts["PDF"], "Rasgo novo no assento.") {
		t.Fatal("the staff PDF must keep the internal comparison description")
	}
	customer := texts["PDF_CUSTOMER"]
	for _, secret := range []string{"Rasgo novo", snapshot.ReferenceVersionID, "Luminária de chão", "Situação não reconhecida"} {
		if strings.Contains(customer, secret) {
			t.Fatalf("customer PDF shows %q", secret)
		}
	}
	// The customer screen shows the same denominator under every filter.
	shown := 0
	for _, outcome := range core.ComparisonOutcomes {
		view, err := customerreport.ProjectComparison(snapshot, outcome)
		if err != nil {
			t.Fatal(err)
		}
		if view.Counts.Total != 39 || view.Displayed != view.Counts.ComparisonCount(outcome) {
			t.Fatalf("%s filter shows %d of %d (counts %+v)", outcome, view.Displayed, view.Counts.Total, view.Counts)
		}
		shown += view.Displayed
	}
	if shown != 39 {
		t.Fatalf("filters display %d items; want 39", shown)
	}
}

// The customer projection of a complete snapshot keeps every field the
// customer validation requires and removes only what the allowlist removes.
func TestTask05CustomerProjectionOfCompleteComparisonSnapshotValidates(t *testing.T) {
	snapshot := task05ComparisonSnapshot(3)
	customer, internal, err := projectForAudience(snapshot, "PDF_CUSTOMER")
	if err != nil || internal {
		t.Fatalf("customer projection err=%v internal=%v", err, internal)
	}
	if err := core.ValidateCustomerSnapshot(customer); err != nil {
		t.Fatalf("customer projection rejected: %v", err)
	}
	if customer.Comparison == nil || customer.Comparison.Counts != snapshot.Comparison.Counts || len(customer.Comparison.Items) != len(snapshot.Comparison.Items) || customer.Comparison.Baseline.VersionNumber != 4 {
		t.Fatalf("customer comparison = %+v", customer.Comparison)
	}
	staff, internal, err := projectForAudience(snapshot, "PDF")
	if err != nil || !internal || staff.Comparison.Items[len(staff.Comparison.Items)-5].Description == "" {
		t.Fatalf("staff projection err=%v internal=%v", err, internal)
	}
}
