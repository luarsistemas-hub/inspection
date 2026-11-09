package generate_snapshot

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	report "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// comparisonInput is a v3 move-out report whose sofa is either paired with a
// current view (unchanged) or, after a pairing correction, without any
// equivalent current view (not observed).
func comparisonInput(tenant, inspection identity.ID, corrected bool) Input {
	input := publicationV3(automaticInput(tenant, inspection, "MANUAL", 1, "prompt-v3"))
	baseline, current := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	input.Evidence = []report.Evidence{{ID: baseline, RequirementKey: "env:sala", Role: "REFERENCE", Availability: "AVAILABLE"}, {ID: current, RequirementKey: "env:sala", Role: "CURRENT", Availability: "AVAILABLE"}}
	item := report.ComparisonItem{ItemIdentityID: "item-sofa", Room: "Sala", TypeCode: "SOFA", Name: "Sofá", Outcome: report.ComparisonUnchanged, BaselineCondition: "GOOD", CurrentCondition: "GOOD", Pairs: []report.ComparisonPair{{BaselineEvidenceID: baseline, CurrentEvidenceID: current}}}
	if corrected {
		item = report.ComparisonItem{ItemIdentityID: "item-sofa", Room: "Sala", TypeCode: "SOFA", Name: "Sofá", Outcome: report.ComparisonNotObserved, BaselineCondition: "GOOD", Limitations: []string{report.LimitationNoCurrentView}, PairingRevised: true}
	}
	items := []report.ComparisonItem{item}
	input.Comparison = &report.ComparisonSection{Baseline: report.ComparisonBaseline{VersionID: "baseline-version", VersionNumber: 1, Purpose: "MOVE_IN", ActivatedAt: "2026-10-01T00:00:00Z"}, Status: "COMPLETED", Items: items, Counts: report.CountComparison(items), OutsideBaseline: []report.ComparisonObservation{}}
	return input
}

// UT-077 (ordering): correcting a pairing after issue creates a new report
// version; the old conclusion remains available byte for byte, and replaying
// the corrected facts does not mint a third version.
func TestUT077PairingCorrectionCreatesNewVersionAndKeepsOldConclusion(t *testing.T) {
	db := publicationDB(t)
	tenant, inspection := identity.NewID(), identity.NewID()
	ctx := context.Background()
	var first Result
	if err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		first, err = CreateInTx(ctx, tx, comparisonInput(tenant, inspection, false), time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var v1 database.ReportSnapshot
	if err := db.First(&v1, "id=?", first.SnapshotID).Error; err != nil {
		t.Fatal(err)
	}
	corrected := comparisonInput(tenant, inspection, true)
	var second, replay Result
	for _, target := range []*Result{&second, &replay} {
		if err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			*target, err = CreateInTx(ctx, tx, corrected, time.Now())
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if first.Version != 1 || second.Version != 2 || replay.Version != 2 || replay.SnapshotID != second.SnapshotID {
		t.Fatalf("versions = %d, %d, replay %d (%s/%s); want 1, 2 and the same v2", first.Version, second.Version, replay.Version, second.SnapshotID, replay.SnapshotID)
	}
	var after database.ReportSnapshot
	if err := db.First(&after, "id=?", first.SnapshotID).Error; err != nil {
		t.Fatal(err)
	}
	if string(after.CanonicalJSON) != string(v1.CanonicalJSON) || after.JSONDigest != v1.JSONDigest || after.HTMLDigest != v1.HTMLDigest {
		t.Fatal("version 1 changed after the pairing correction")
	}
	var old, current report.Snapshot
	if err := json.Unmarshal(after.CanonicalJSON, &old); err != nil {
		t.Fatal(err)
	}
	var v2 database.ReportSnapshot
	if err := db.First(&v2, "id=?", second.SnapshotID).Error; err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(v2.CanonicalJSON, &current); err != nil {
		t.Fatal(err)
	}
	if old.Comparison.Items[0].Outcome != report.ComparisonUnchanged || current.Comparison.Items[0].Outcome != report.ComparisonNotObserved || !current.Comparison.Items[0].PairingRevised || current.ReportID != old.ReportID {
		t.Fatalf("v1=%+v v2=%+v; the old conclusion stays and v2 carries the correction under the same report number", old.Comparison.Items[0], current.Comparison.Items[0])
	}
}
