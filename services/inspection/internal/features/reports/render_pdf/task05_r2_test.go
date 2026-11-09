package render_pdf

import (
	"context"
	"strings"
	"testing"

	"inspection/services/inspection/internal/features/reports/core"
	customerreport "inspection/services/inspection/internal/features/reports/customer/core"
	"inspection/services/inspection/internal/platform/pdf"
)

// Item 8 (review round 2): the customer PDF labels a paired image it cannot
// show with the same rule (customerreport.PairAvailability) the customer
// GraphQL view uses; the internal PDF does not hide it.
func TestTask05R2CustomerPDFAndGraphQLShareThePairAvailability(t *testing.T) {
	snapshot := completeV3Snapshot()
	for i := range snapshot.Evidence {
		if snapshot.Evidence[i].ID == "baseline-photo" {
			snapshot.Evidence[i].Flags = append(snapshot.Evidence[i].Flags, "SENSITIVE_DETECTION")
		}
	}
	items := []core.ComparisonItem{{ItemIdentityID: "item-sofa", Room: "Sala", TypeCode: "SOFA", Name: "Sofá", Outcome: core.ComparisonUnchanged, BaselineCondition: "GOOD", CurrentCondition: "GOOD", Pairs: []core.ComparisonPair{{BaselineEvidenceID: "baseline-photo", CurrentEvidenceID: "current-photo"}}}}
	snapshot.Comparison = &core.ComparisonSection{Baseline: core.ComparisonBaseline{VersionID: "baseline-version", VersionNumber: 2, Purpose: "MOVE_IN", ActivatedAt: "2026-10-01T00:00:00Z"}, Status: "COMPLETED", Items: items, Counts: core.CountComparison(items), OutsideBaseline: []core.ComparisonObservation{}}
	if err := core.Validate(snapshot); err != nil {
		t.Fatal(err)
	}
	view, err := customerreport.ProjectComparison(snapshot, "")
	if err != nil {
		t.Fatal(err)
	}
	if pair := view.Items[0].Pairs[0]; pair.BaselineAvailability != "UNAVAILABLE" || pair.CurrentAvailability != "AVAILABLE" {
		t.Fatalf("customer GraphQL pair = %+v", pair)
	}
	for _, audience := range []string{"PDF_CUSTOMER", "PDF"} {
		projected, internal, err := projectForAudience(snapshot, audience)
		if err != nil {
			t.Fatal(err)
		}
		data, err := (MarotoRenderer{}).Render(context.Background(), PDFDocument{SnapshotID: "t05-r2", Version: 1, Snapshot: projected, Internal: internal}, pdf.AssetBundle{})
		if err != nil {
			t.Fatal(err)
		}
		text := legacyPDFDump(t, data)
		labeled := strings.Contains(text, "Imagem da referência indisponível")
		if audience == "PDF_CUSTOMER" && !labeled {
			t.Fatalf("customer PDF does not label the blocked baseline image:\n%s", text)
		}
		if audience == "PDF" && labeled {
			t.Fatal("the internal PDF hid an image staff may see")
		}
	}
}
