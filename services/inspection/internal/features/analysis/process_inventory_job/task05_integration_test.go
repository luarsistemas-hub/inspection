//go:build integration

package process_inventory_job

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	classifyinspection "inspection/services/inspection/internal/features/analysis/classify_inspection"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	revisepairing "inspection/services/inspection/internal/features/analysis/revise_comparison_pairing"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	customerreport "inspection/services/inspection/internal/features/reports/customer/core"
	generatesnapshot "inspection/services/inspection/internal/features/reports/generate_snapshot"
	handleclassified "inspection/services/inspection/internal/features/reports/handle_classified"
	"inspection/services/inspection/internal/features/reports/render_pdf"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"

	"gorm.io/gorm"
)

// strictScope pins a descriptive scope (report v3) whose rooms are the pinned
// baseline environments, each expecting one general view. covered lists the
// rooms whose general view was observed; the others were only partly seen.
func (f *flow) strictScope(p pinned, covered map[string]bool) {
	f.t.Helper()
	type component struct {
		Key      string   `json:"key"`
		TypeCode string   `json:"typeCode"`
		Label    string   `json:"label"`
		Views    []string `json:"views"`
	}
	type room struct {
		Key        string      `json:"key"`
		TypeCode   string      `json:"typeCode"`
		Name       string      `json:"name"`
		Components []component `json:"components"`
	}
	rooms := make([]room, 0, len(p.environments))
	requirements := make([]capturecore.Requirement, 0, len(p.environments))
	for _, env := range p.environments {
		rooms = append(rooms, room{Key: env.IdentityID.String(), TypeCode: env.TypeCode, Name: env.Name, Components: []component{{Key: "geral", TypeCode: "OTHER", Label: "Visão geral", Views: []string{"wide"}}}})
		requirements = append(requirements, inventorytest.EnvironmentRequirement(inventorytest.EnvironmentKey(env), env.TypeCode, env.Name, false))
	}
	f.requirements(requirements...)
	document, _ := json.Marshal(map[string]any{"rooms": rooms, "handoverRecords": []any{}})
	// Every component answer cites the room photo it was recorded with: an
	// observation without ready evidence observes nothing (capture rule).
	evidence := map[string]json.RawMessage{}
	for _, env := range p.environments {
		ids, _ := json.Marshal([]identity.ID{f.renderablePhoto(inventorytest.EnvironmentKey(env), 120)})
		evidence[env.Name] = ids
	}
	now := f.now()
	f.within(func(tx *gorm.DB) error {
		if err := tx.Create(&database.InspectionScopeSnapshot{ID: identity.NewID(), TenantID: f.seed.TenantID, InspectionID: f.seed.InspectionID, TemplateVersionID: f.seed.TemplateVersionID, Profile: "PROPERTY_COMPONENTS", Document: document, Digest: "t05-scope-" + f.seed.InspectionID.String(), Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		for _, env := range p.environments {
			state := "PARTIAL"
			if covered[env.Name] {
				state = "OBSERVED"
			}
			if err := tx.Create(&database.ComponentAnswer{ID: identity.NewID(), TenantID: f.seed.TenantID, DraftID: f.seed.DraftID, RequirementKey: inventorytest.EnvironmentKey(env), ComponentKey: "geral", ViewKey: "wide", State: state, EvidenceIDs: evidence[env.Name], Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// strictScript answers the matcher with pick (verify agrees) and the
// comparison with compare.
func (f *flow) strictScript(pick func(matchRequest) (string, string), compare func(llm.StructuredRequest) string) {
	f.gateway.Respond = func(request llm.StructuredRequest) (string, error) {
		switch request.Mode {
		case "INVENTORY_MATCH":
			var in matchRequest
			if err := json.Unmarshal([]byte(request.UserPrompt), &in); err != nil {
				return "", err
			}
			if in.Stage == "verify" {
				return mustJSON(f.t, matchOutput{Pairs: in.ProposedPairs}), nil
			}
			current, decision := pick(in)
			return mustJSON(f.t, matchOutput{Pairs: []matchPair{{OriginEvidenceID: in.OriginEvidenceID, CurrentEvidenceID: current, Decision: decision, Reason: "vista"}}}), nil
		case "INVENTORY_COMPARE":
			return compare(request), nil
		}
		return "", errors.New("unexpected mode")
	}
}

func firstSameView(in matchRequest) (string, string) { return in.CurrentEvidenceIDs[0], "SAME_VIEW" }

// classifyAndReportV3 runs classification and the classified-event handler
// with the transactional snapshot adapter production wires for v3 reports.
func (f *flow) classifyAndReportV3() (database.ClassificationRun, database.ReportSnapshot) {
	f.t.Helper()
	f.clock.Add(int64(time.Second))
	classify, err := classifyinspection.Setup(classifyinspection.Dependencies{Now: f.now})
	if err != nil {
		f.t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"inspectionId": f.seed.InspectionID})
	f.within(func(tx *gorm.DB) error {
		return classify(f.ctx, tx, events.RawEnvelope{ID: identity.NewID(), Type: "analysis.comparison_completed.v1", TenantID: f.seed.TenantID, CorrelationID: f.seed.InspectionID.String(), Payload: payload})
	})
	var run database.ClassificationRun
	f.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, f.seed.InspectionID).Order("created_at DESC").First(&run).Error
	})
	handler, err := handleclassified.Setup(handleclassified.Dependencies{Notifications: silentNotifications{}, CreateSnapshotInTx: func(ctx context.Context, tx *gorm.DB, input generatesnapshot.Input) (generatesnapshot.Result, error) {
		return generatesnapshot.CreateInTx(ctx, tx, input, f.now())
	}, Now: f.now})
	if err != nil {
		f.t.Fatal(err)
	}
	raw, _ := json.Marshal(events.InspectionClassifiedPayload{InspectionID: f.seed.InspectionID, ClassificationRunID: run.ID})
	f.within(func(tx *gorm.DB) error {
		return handler(f.ctx, tx, events.RawEnvelope{ID: identity.NewID(), Type: "inspection.classified.v1", SchemaVersion: 1, OccurredAt: f.now(), TenantID: f.seed.TenantID, AggregateID: f.seed.InspectionID, CorrelationID: f.seed.InspectionID.String(), Payload: raw})
	})
	var snapshot database.ReportSnapshot
	if err := f.h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, f.seed.InspectionID).Order("version_number DESC").First(&snapshot).Error; err != nil {
		f.t.Fatal(err)
	}
	return run, snapshot
}

func decodeSnapshot(t *testing.T, row database.ReportSnapshot) reportcore.Snapshot {
	t.Helper()
	var snapshot reportcore.Snapshot
	if err := json.Unmarshal(row.CanonicalJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.SchemaVersion != 3 || snapshot.Comparison == nil {
		t.Fatalf("snapshot v%d comparison=%v; want a v3 comparison", snapshot.SchemaVersion, snapshot.Comparison != nil)
	}
	return snapshot
}

func comparisonItem(t *testing.T, snapshot reportcore.Snapshot, name string) reportcore.ComparisonItem {
	t.Helper()
	for _, item := range snapshot.Comparison.Items {
		if item.Name == name {
			return item
		}
	}
	t.Fatalf("%s missing from comparison %+v", name, snapshot.Comparison.Items)
	return reportcore.ComparisonItem{}
}

func (f *flow) confirmedPairs(item database.InventoryItem) []database.InventoryEvidencePair {
	f.t.Helper()
	var pairs []database.InventoryEvidencePair
	f.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND item_identity_id=? AND confirmed", f.seed.TenantID, item.IdentityID).Find(&pairs).Error
	})
	return pairs
}

func strictLiving(items ...inventorytest.BaselineItem) inventorytest.BaselineEnvironment {
	return inventorytest.BaselineEnvironment{Name: "Sala", TypeCode: "LIVING_ROOM", Items: items}
}

// IT-020: INVENTORY_COMPARE with a confirmed same-room pair returns the
// expected result for that one baseline item.
func TestIT020ConfirmedSameRoomPairReturnsExpectedResult(t *testing.T) {
	f := newFlow(t, "PHOTO")
	p := f.pin(strictLiving(inventorytest.BaselineItem{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1}))
	key := inventorytest.EnvironmentKey(p.environments[0])
	f.strictScope(p, map[string]bool{"Sala": true})
	current := f.photo(key)
	f.answer(key, []identity.ID{current}, "")
	f.strictScript(firstSameView, func(llm.StructuredRequest) string {
		return compareJSON(t, claim(p.items["Sofá"], "PRESENT_UNCHANGED", "conditionAfter", "GOOD"))
	})
	f.submitPhotos()
	f.drain()
	row := f.results()[p.items["Sofá"].IdentityID.String()]
	pairs := f.confirmedPairs(p.items["Sofá"])
	if row.Status != "PRESENT_UNCHANGED" || value(row.ConditionAfter) != "GOOD" || len(pairs) != 1 || pairs[0].CurrentMediaID != current || pairs[0].Source != "MATCHER" {
		t.Fatalf("Sofá = %+v pairs = %+v; want PRESENT_UNCHANGED with its same-room pair", row, pairs)
	}
	calls := f.gateway.Calls("INVENTORY_COMPARE")
	if len(calls) != 1 {
		t.Fatalf("compare calls = %d", len(calls))
	}
	declared := userData(t, calls[0])["confirmedPairs"].([]any)
	if len(declared) != 1 || declared[0].(map[string]any)["currentEvidenceId"] != current.String() {
		t.Fatalf("declared pairs = %v", declared)
	}
}

// IT-021: a movable item absent only from its former location, with
// incomplete room coverage, remains NOT_OBSERVED.
func TestIT021MovableAbsenceWithIncompleteRoomCoverageStaysNotObserved(t *testing.T) {
	f := newFlow(t, "PHOTO")
	p := f.pin(strictLiving(inventorytest.BaselineItem{Name: "Poltrona", TypeCode: "FURNITURE_OTHER", Condition: "GOOD", Evidence: 1}))
	key := inventorytest.EnvironmentKey(p.environments[0])
	f.strictScope(p, map[string]bool{})
	f.answer(key, []identity.ID{f.photo(key)}, "")
	f.strictScript(firstSameView, func(llm.StructuredRequest) string {
		return compareJSON(t, claim(p.items["Poltrona"], "MISSING"))
	})
	f.submitPhotos()
	f.drain()
	row := f.results()[p.items["Poltrona"].IdentityID.String()]
	if row.Status != "NOT_OBSERVED" || value(row.ReasonCode) != reasonRoomCoverage {
		t.Fatalf("Poltrona = %+v; a vacant former location alone is not proof of absence", row)
	}
	var findings int64
	f.within(func(tx *gorm.DB) error {
		return tx.Model(&database.FindingRecord{}).Where("tenant_id=? AND inventory_job_id IS NOT NULL", f.seed.TenantID).Count(&findings).Error
	})
	if findings != 0 {
		t.Fatalf("an unproven absence produced %d findings", findings)
	}
}

// IT-049: INVENTORY_COMPARE with a pair from another room rejects the pair
// and records a limitation.
func TestIT049PairFromAnotherRoomIsRejectedWithLimitation(t *testing.T) {
	f := newFlow(t, "PHOTO")
	p := f.pin(strictLiving(inventorytest.BaselineItem{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1}), inventorytest.BaselineEnvironment{Name: "Cozinha", TypeCode: "KITCHEN", Items: []inventorytest.BaselineItem{{Name: "Pia", TypeCode: "SINK", Condition: "GOOD", Evidence: 1}}})
	living, kitchen := inventorytest.EnvironmentKey(p.environments[0]), inventorytest.EnvironmentKey(p.environments[1])
	f.strictScope(p, map[string]bool{"Sala": true, "Cozinha": true})
	f.answer(living, []identity.ID{f.photo(living)}, "")
	kitchenPhoto := f.photo(kitchen)
	f.answer(kitchen, []identity.ID{kitchenPhoto}, "")
	f.completeFindingsComparison()
	f.strictScript(func(in matchRequest) (string, string) {
		for _, id := range in.CurrentEvidenceIDs {
			if id == kitchenPhoto.String() {
				return id, "SAME_VIEW"
			}
		}
		// The living-room matcher names the kitchen photo.
		return kitchenPhoto.String(), "SAME_VIEW"
	}, func(request llm.StructuredRequest) string {
		return compareJSON(t, claim(p.items["Sofá"], "PRESENT_UNCHANGED", "conditionAfter", "GOOD"), claim(p.items["Pia"], "PRESENT_UNCHANGED", "conditionAfter", "GOOD"))
	})
	f.submitPhotos()
	f.drain()
	row := f.results()[p.items["Sofá"].IdentityID.String()]
	if row.Status != "NOT_OBSERVED" || value(row.ReasonCode) != reasonPairRejected || len(f.confirmedPairs(p.items["Sofá"])) != 0 {
		t.Fatalf("Sofá = %+v; want the cross-room pair rejected", row)
	}
	_, snapshot := f.classifyAndReportV3()
	item := comparisonItem(t, decodeSnapshot(t, snapshot), "Sofá")
	if item.Outcome != reportcore.ComparisonNotObserved || len(item.Limitations) != 1 || item.Limitations[0] != reportcore.LimitationPairRejected || len(item.Pairs) != 0 {
		t.Fatalf("report item = %+v; want the rejected-pair limitation", item)
	}
}

// IT-050: INVENTORY_COMPARE with no equivalent current view returns
// NOT_OBSERVED for that baseline item.
func TestIT050NoEquivalentCurrentViewReturnsNotObserved(t *testing.T) {
	f := newFlow(t, "PHOTO")
	p := f.pin(strictLiving(inventorytest.BaselineItem{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1}))
	key := inventorytest.EnvironmentKey(p.environments[0])
	f.strictScope(p, map[string]bool{"Sala": true})
	f.answer(key, []identity.ID{f.photo(key)}, "")
	f.strictScript(func(in matchRequest) (string, string) { return in.CurrentEvidenceIDs[0], "NOT_COMPARABLE" }, func(llm.StructuredRequest) string {
		return compareJSON(t, claim(p.items["Sofá"], "PRESENT_UNCHANGED", "conditionAfter", "GOOD"))
	})
	f.submitPhotos()
	f.drain()
	row := f.results()[p.items["Sofá"].IdentityID.String()]
	if row.Status != "NOT_OBSERVED" || value(row.ReasonCode) != "NO_CORRESPONDING_VIEW" || len(f.confirmedPairs(p.items["Sofá"])) != 0 {
		t.Fatalf("Sofá = %+v; without an equivalent current view the item is not observed", row)
	}
}

// IT-051: a movable item with confirmed whole-room coverage and an empty
// location yields MISSING with a supported pair.
func TestIT051MovableAbsenceWithWholeRoomCoverageIsMissingWithPair(t *testing.T) {
	f := newFlow(t, "PHOTO")
	p := f.pin(strictLiving(inventorytest.BaselineItem{Name: "Poltrona", TypeCode: "FURNITURE_OTHER", Condition: "GOOD", Evidence: 1}))
	key := inventorytest.EnvironmentKey(p.environments[0])
	f.strictScope(p, map[string]bool{"Sala": true})
	current := f.photo(key)
	f.answer(key, []identity.ID{current}, "")
	f.strictScript(firstSameView, func(llm.StructuredRequest) string {
		return compareJSON(t, claim(p.items["Poltrona"], "MISSING"))
	})
	f.submitPhotos()
	f.drain()
	row := f.results()[p.items["Poltrona"].IdentityID.String()]
	pairs := f.confirmedPairs(p.items["Poltrona"])
	if row.Status != "MISSING" || len(pairs) != 1 || pairs[0].CurrentMediaID != current {
		t.Fatalf("Poltrona = %+v pairs = %+v; want MISSING with its supported pair", row, pairs)
	}
}

func sixItemBaseline() inventorytest.BaselineEnvironment {
	return strictLiving(
		inventorytest.BaselineItem{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1},
		inventorytest.BaselineItem{Name: "Mesa", TypeCode: "TABLE", Condition: "GOOD", Evidence: 1},
		inventorytest.BaselineItem{Name: "Cadeira", TypeCode: "CHAIR", Condition: "POOR", Evidence: 1},
		inventorytest.BaselineItem{Name: "Televisor", TypeCode: "APPLIANCE_OTHER", Condition: "GOOD", Evidence: 1},
		inventorytest.BaselineItem{Name: "Cortina", TypeCode: "CURTAIN_BLIND", Condition: "GOOD", Evidence: 1},
		inventorytest.BaselineItem{Name: "Quadro", TypeCode: "OTHER", Condition: "GOOD", Declared: true},
	)
}

// IT-022: the comparison v3 report projects the six outcome counts and the
// original baseline version for a six-item fixture, for staff and customers.
func TestIT022ComparisonV3ReportProjectsSixOutcomeCountsAndBaselineVersion(t *testing.T) {
	f := newFlow(t, "PHOTO")
	p := f.pin(sixItemBaseline())
	key := inventorytest.EnvironmentKey(p.environments[0])
	f.strictScope(p, map[string]bool{"Sala": true})
	f.answer(key, []identity.ID{f.photo(key)}, "")
	f.completeFindingsComparison()
	f.strictScript(firstSameView, func(llm.StructuredRequest) string {
		return compareJSON(t,
			claim(p.items["Sofá"], "PRESENT_UNCHANGED", "conditionAfter", "GOOD"),
			claim(p.items["Mesa"], "CONDITION_CHANGED", "conditionAfter", "FAIR"),
			claim(p.items["Cadeira"], "CONDITION_CHANGED", "conditionAfter", "GOOD"),
			claim(p.items["Televisor"], "MISSING"),
			claim(p.items["Cortina"], "NOT_OBSERVED", "reasonCode", "OBSTRUCTED"),
			claim(p.items["Quadro"], "PRESENT_UNCHANGED"),
		)
	})
	f.submitPhotos()
	f.drain()
	_, row := f.classifyAndReportV3()
	snapshot := decodeSnapshot(t, row)
	want := reportcore.ComparisonCounts{Total: 6, Unchanged: 1, Changed: 1, Improved: 1, Missing: 1, NotObserved: 1, NoBasis: 1}
	if snapshot.Comparison.Counts != want || snapshot.Comparison.Status != "COMPLETED" {
		t.Fatalf("comparison counts = %+v status %s; want one of each outcome", snapshot.Comparison.Counts, snapshot.Comparison.Status)
	}
	if snapshot.Comparison.Baseline.VersionID != p.version.String() || snapshot.Comparison.Baseline.VersionNumber != 1 || snapshot.Comparison.Baseline.Purpose != "MOVE_IN" {
		t.Fatalf("baseline = %+v; want the pinned version 1", snapshot.Comparison.Baseline)
	}
	for name, outcome := range map[string]string{"Sofá": reportcore.ComparisonUnchanged, "Mesa": reportcore.ComparisonChanged, "Cadeira": reportcore.ComparisonImproved, "Televisor": reportcore.ComparisonMissing, "Cortina": reportcore.ComparisonNotObserved, "Quadro": reportcore.ComparisonNoBasis} {
		if item := comparisonItem(t, snapshot, name); item.Outcome != outcome {
			t.Fatalf("%s = %+v; want %s", name, item, outcome)
		}
	}
	if item := comparisonItem(t, snapshot, "Cadeira"); item.BaselineCondition != "POOR" || item.CurrentCondition != "GOOD" {
		t.Fatalf("improved item conditions = %+v", item)
	}
	if !strings.Contains(string(row.HTML), "Referência aprovada: versão 1") || !strings.Contains(string(row.HTML), "Sem base para comparar · 1 de 6 itens") {
		t.Fatal("issued HTML misses the baseline version or the outcome groups")
	}
	view, err := customerreport.ProjectComparison(snapshot, "")
	if err != nil {
		t.Fatal(err)
	}
	if view.Counts != want || view.BaselineVersionNumber != 1 || view.Displayed != 6 {
		t.Fatalf("customer comparison = %+v; want the same counts and version", view)
	}
	filtered, err := customerreport.ProjectComparison(snapshot, reportcore.ComparisonMissing)
	if err != nil || filtered.Displayed != 1 || filtered.Counts.Total != 6 || filtered.Items[0].Name != "Televisor" {
		t.Fatalf("missing filter = %+v, %v", filtered, err)
	}
}

// renderablePhoto stores a READY photo whose display derivative is a real
// JPEG with matching digest and size, so the PDF executor embeds it.
func (f *flow) renderablePhoto(key string, shade uint8) identity.ID {
	f.t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 32, 24))
	for x := 0; x < 32; x++ {
		for y := 0; y < 24; y++ {
			canvas.Set(x, y, color.RGBA{R: shade, G: uint8(x * 7), B: uint8(y * 9), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, canvas, nil); err != nil {
		f.t.Fatal(err)
	}
	data := encoded.Bytes()
	id, now := identity.NewID(), f.now()
	object := "tenant/" + f.seed.TenantID.String() + "/display/" + id.String()
	if err := f.h.Store.Client.Put(f.ctx, f.h.Store.Bucket, object, bytes.NewReader(data), int64(len(data)), "image/jpeg"); err != nil {
		f.t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	f.within(func(tx *gorm.DB) error {
		if err := tx.Create(&database.MediaObject{ID: id, TenantID: f.seed.TenantID, ResponsibilityID: f.seed.ResponsibilityID, ObjectKey: object, ContentType: "image/jpeg", SHA256: digest, SizeBytes: int64(len(data)), Status: "READY", MediaKind: "PHOTO", IdempotencyKey: "t05-photo-" + id.String(), RequirementKey: key, Description: "Evidência", CaptureSource: "CAMERA", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.MediaDerivative{ID: identity.NewID(), TenantID: f.seed.TenantID, MediaID: id, ObjectKey: object, Kind: "DISPLAY", SHA256: digest, ContentType: "image/jpeg", Width: 32, Height: 24, SizeBytes: int64(len(data)), CreatedAt: now}).Error
	})
	return id
}

// pinRenderable pins an ACTIVE one-room baseline whose items cite renderable
// origin photos.
func (f *flow) pinRenderable(env inventorytest.BaselineEnvironment) pinned {
	f.t.Helper()
	p := pinned{version: identity.NewID(), items: map[string]database.InventoryItem{}}
	now, purpose := f.now(), "MOVE_IN"
	evidence := map[string]identity.ID{}
	for i, item := range env.Items {
		evidence[item.Name] = f.renderablePhoto("origin", uint8(200-20*i))
	}
	f.within(func(tx *gorm.DB) error {
		origin := database.Origin{ID: identity.NewID(), TenantID: f.seed.TenantID, AssetID: f.seed.AssetID, ActiveVersionID: &p.version, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&origin).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.OriginVersion{ID: p.version, TenantID: f.seed.TenantID, OriginID: origin.ID, VersionNumber: 1, Purpose: &purpose, Source: "CAPTURE", HasInventory: true, Status: "ACTIVE", IdempotencyKey: "t05-baseline-" + p.version.String(), CreatedAt: now, ActivatedAt: &now}).Error; err != nil {
			return err
		}
		room := database.InventoryEnvironment{ID: identity.NewID(), TenantID: f.seed.TenantID, OriginVersionID: p.version, IdentityID: identity.NewID(), TypeCode: env.TypeCode, Name: env.Name, Expected: true, Decision: "ACCEPTED", Position: 0, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&room).Error; err != nil {
			return err
		}
		p.environments = append(p.environments, room)
		for _, spec := range env.Items {
			item := database.InventoryItem{ID: identity.NewID(), TenantID: f.seed.TenantID, OriginVersionID: p.version, EnvironmentID: room.ID, IdentityID: identity.NewID(), TypeCode: spec.TypeCode, Name: spec.Name, Condition: spec.Condition, Observation: "OBSERVED", Origin: "AI", Decision: "ACCEPTED", Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
			p.items[spec.Name] = item
			if err := tx.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: f.seed.TenantID, ItemID: item.ID, Kind: "PHOTO", MediaID: evidence[spec.Name], Position: 0, CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", f.seed.TenantID, f.seed.InspectionID).Update("inventory_prompt_snapshot_id", f.compare).Error
	})
	f.replaceReference(p.version)
	return p
}

func artifactDigest(t *testing.T, f *flow, artifact database.ReportArtifact) string {
	t.Helper()
	reader, err := f.h.Store.Client.Get(f.ctx, f.h.Store.Bucket, artifact.ObjectKey)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("%PDF")) {
		t.Fatalf("artifact %s is not a PDF", artifact.ObjectKey)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// renderPDFs queues and renders both PDF audiences of one snapshot with the
// real executor and returns the artifacts by audience.
func (f *flow) renderPDFs(snapshot database.ReportSnapshot) map[string]database.ReportArtifact {
	f.t.Helper()
	if err := f.h.AdminDB.Model(&database.ReportPDFJob{}).Where("status IN ('QUEUED','PROCESSING') AND tenant_id<>?", f.seed.TenantID).Updates(map[string]any{"next_attempt_at": time.Now().Add(24 * time.Hour), "lease_expires_at": time.Now().Add(24 * time.Hour)}).Error; err != nil {
		f.t.Fatal(err)
	}
	queue, err := render_pdf.Setup(render_pdf.Dependencies{})
	if err != nil {
		f.t.Fatal(err)
	}
	created := events.RawEnvelope{ID: identity.NewID(), Type: "report.snapshot_created.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: f.seed.TenantID, AggregateID: f.seed.InspectionID, CorrelationID: "t05-pdf", Payload: json.RawMessage(`{"inspectionId":"` + f.seed.InspectionID.String() + `","snapshotId":"` + snapshot.ID.String() + `","version":` + fmt.Sprint(snapshot.VersionNumber) + `}`)}
	f.within(func(tx *gorm.DB) error { return queue(f.ctx, tx, created) })
	executor, err := render_pdf.SetupExecutor(f.h.AdminDB, f.h.Store, render_pdf.DefaultLimits(), time.Now, "")
	if err != nil {
		f.t.Fatal(err)
	}
	for range 10 {
		worked, err := executor.RunDue(f.ctx)
		if err != nil {
			f.t.Fatalf("PDF executor: %v", err)
		}
		if !worked {
			break
		}
	}
	var jobs []database.ReportPDFJob
	if err := f.h.AdminDB.Where("tenant_id=? AND snapshot_id=?", f.seed.TenantID, snapshot.ID).Find(&jobs).Error; err != nil || len(jobs) != 2 {
		f.t.Fatalf("PDF jobs = %+v, %v", jobs, err)
	}
	out := map[string]database.ReportArtifact{}
	for _, job := range jobs {
		if job.Status != "READY" || job.ArtifactID == nil {
			f.t.Fatalf("%s PDF job = %+v; want READY", job.Audience, job)
		}
		var artifact database.ReportArtifact
		if err := f.h.AdminDB.Where("tenant_id=? AND id=?", f.seed.TenantID, *job.ArtifactID).Take(&artifact).Error; err != nil {
			f.t.Fatal(err)
		}
		out[job.Audience] = artifact
	}
	return out
}

// IT-023: correcting a published pairing creates v2 while the v1 canonical
// JSON, HTML and PDF digests remain unchanged.
func TestIT023CorrectedPairingCreatesV2AndKeepsV1Digests(t *testing.T) {
	f := newFlow(t, "PHOTO")
	p := f.pinRenderable(strictLiving(inventorytest.BaselineItem{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1}))
	key := inventorytest.EnvironmentKey(p.environments[0])
	f.strictScope(p, map[string]bool{"Sala": true})
	first, second := f.renderablePhoto(key, 60), f.renderablePhoto(key, 90)
	f.answer(key, []identity.ID{first, second}, "")
	f.completeFindingsComparison()
	status := "PRESENT_UNCHANGED"
	f.strictScript(firstSameView, func(llm.StructuredRequest) string {
		return compareJSON(t, claim(p.items["Sofá"], status, "conditionAfter", map[string]string{"PRESENT_UNCHANGED": "GOOD", "CONDITION_CHANGED": "FAIR"}[status]))
	})
	f.submitPhotos()
	f.drain()
	_, v1 := f.classifyAndReportV3()
	old := decodeSnapshot(t, v1)
	if item := comparisonItem(t, old, "Sofá"); item.Outcome != reportcore.ComparisonUnchanged || len(item.Pairs) != 1 || item.Pairs[0].CurrentEvidenceID != first.String() || item.PairingRevised {
		t.Fatalf("v1 Sofá = %+v; want UNCHANGED paired with the first photo", item)
	}
	v1Artifacts := f.renderPDFs(v1)
	v1PDF := map[string]string{}
	for audience, artifact := range v1Artifacts {
		v1PDF[audience] = artifactDigest(t, f, artifact)
	}

	// Staff correct the pairing of the published v1: the sofa matches the
	// second photo, where the model sees a worse condition.
	revise, err := revisepairing.Setup(revisepairing.Dependencies{DB: f.h.DB, Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	var origin identity.ID
	f.within(func(tx *gorm.DB) error {
		var evidence database.InventoryItemEvidence
		err := tx.Where("tenant_id=? AND item_id=?", f.seed.TenantID, p.items["Sofá"].ID).Take(&evidence).Error
		origin = evidence.MediaID
		return err
	})
	result, err := revise(f.ctx, revisepairing.Command{TenantID: f.seed.TenantID, ActorID: f.seed.MembershipID, InspectionID: f.seed.InspectionID, ItemIdentityID: p.items["Sofá"].IdentityID, Pairs: []revisepairing.Pair{{BaselineEvidenceID: origin, CurrentEvidenceID: second}}, ExpectedVersion: v1.VersionNumber, ClientMutationID: "it-023-revise"})
	if err != nil || result.Report.ID != v1.ID {
		t.Fatalf("revise = %+v, %v", result, err)
	}
	status = "CONDITION_CHANGED"
	f.clock.Add(int64(time.Second))
	f.drain()
	_, v2 := f.classifyAndReportV3()
	if v2.VersionNumber != v1.VersionNumber+1 {
		t.Fatalf("versions v1=%d v2=%d; the correction must issue the next version", v1.VersionNumber, v2.VersionNumber)
	}
	current := decodeSnapshot(t, v2)
	item := comparisonItem(t, current, "Sofá")
	if item.Outcome != reportcore.ComparisonChanged || !item.PairingRevised || len(item.Pairs) != 1 || item.Pairs[0].CurrentEvidenceID != second.String() || current.ReportID != old.ReportID {
		t.Fatalf("v2 Sofá = %+v report %s/%s; want the corrected pair", item, current.ReportID, old.ReportID)
	}
	var after database.ReportSnapshot
	if err := f.h.AdminDB.Where("tenant_id=? AND id=?", f.seed.TenantID, v1.ID).Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after.CanonicalJSON, v1.CanonicalJSON) || after.JSONDigest != v1.JSONDigest || after.HTMLDigest != v1.HTMLDigest || !bytes.Equal(after.HTML, v1.HTML) {
		t.Fatal("v1 canonical JSON or HTML changed after the pairing correction")
	}
	for audience, artifact := range v1Artifacts {
		var reloaded database.ReportArtifact
		if err := f.h.AdminDB.Where("tenant_id=? AND id=?", f.seed.TenantID, artifact.ID).Take(&reloaded).Error; err != nil {
			t.Fatal(err)
		}
		if reloaded.ObjectKey != artifact.ObjectKey || artifactDigest(t, f, reloaded) != v1PDF[audience] {
			t.Fatalf("v1 %s PDF changed after the correction", audience)
		}
	}
	v2Artifacts := f.renderPDFs(v2)
	for audience, artifact := range v2Artifacts {
		if artifact.ObjectKey == v1Artifacts[audience].ObjectKey {
			t.Fatalf("v2 %s PDF overwrote the v1 artifact", audience)
		}
	}
	var outcome database.AuditEvent
	if err := f.h.AdminDB.Where("tenant_id=? AND action='report.reissue_requested' AND target_id=?", f.seed.TenantID, f.seed.InspectionID.String()).Take(&outcome).Error; err != nil || outcome.Outcome != "COMPLETED" {
		t.Fatalf("reissue request = %+v, %v; want COMPLETED", outcome, err)
	}
}
