package handle_classified

import (
	"context"
	"encoding/json"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"
)

type reportFixture struct {
	seed     *inventorytest.Seed
	baseline inventorytest.Baseline
	section  *core.InventorySection
	evidence []core.Evidence
	frame    identity.ID
	origin   identity.ID
}

func item(baseline inventorytest.Baseline, name string) database.InventoryItem {
	for _, row := range baseline.Items {
		if row.Name == name {
			return row
		}
	}
	panic("unknown item " + name)
}

// newReportFixture seeds a comparative inspection with one result of each kind.
func newReportFixture(t *testing.T) reportFixture {
	t.Helper()
	seed := inventorytest.New(t, inventorytest.InspectionOptions{})
	baseline := seed.PinBaseline(t,
		inventorytest.BaselineEnvironment{Name: "Sala", TypeCode: "LIVING_ROOM", Items: []inventorytest.BaselineItem{
			{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1},
			{Name: "Mesa", TypeCode: "TABLE", Condition: "GOOD", Evidence: 1},
			{Name: "Quadro", Condition: "GOOD", Declared: true},
			{Name: "Tapete", Condition: "POOR", Evidence: 1},
			{Name: "Cadeira", TypeCode: "CHAIR", Condition: "GOOD", Evidence: 1},
		}},
		inventorytest.BaselineEnvironment{Name: "Garagem", TypeCode: "GARAGE", Items: []inventorytest.BaselineItem{{Name: "Portão", Condition: "GOOD"}}},
	)
	sala, garagem := baseline.Environments[0], baseline.Environments[1]
	addedKey := "env:new:" + identity.NewID().String()
	requirements := []capturecore.Requirement{
		inventorytest.EnvironmentRequirement(inventorytest.EnvironmentKey(sala), "LIVING_ROOM", "Sala", false),
		inventorytest.EnvironmentRequirement(inventorytest.EnvironmentKey(garagem), "GARAGE", "Garagem", false),
		inventorytest.EnvironmentRequirement(addedKey, "OFFICE", "Escritório", true),
	}
	raw, _ := json.Marshal(requirements)
	seed.DB.Model(&database.CaptureDraft{}).Where("id=?", seed.CaptureDraft).Update("requirements", raw)
	current := seed.Photo(t, inventorytest.EnvironmentKey(sala))
	clip, _, frames := seed.Clip(t, inventorytest.EnvironmentKey(sala), "READY", 2)
	expiry := seed.Now
	if err := seed.DB.Create(&database.VideoSource{ID: identity.NewID(), TenantID: seed.Tenant, MediaID: clip, Status: "EXPIRED", RetentionAnchor: "FIXED", SourceExpiryAt: &expiry, CreatedAt: seed.Now, UpdatedAt: seed.Now}).Error; err != nil {
		t.Fatal(err)
	}
	seed.Answer(t, inventorytest.EnvironmentKey(sala), []identity.ID{current, clip}, "")
	seed.Answer(t, inventorytest.EnvironmentKey(garagem), nil, "LOCKED")
	seed.Answer(t, addedKey, []identity.ID{seed.Photo(t, addedKey)}, "")
	salaJob := seed.Job(t, "COMPARE", inventorytest.EnvironmentKey(sala), baseline.Version, &sala.IdentityID)
	garagemJob := seed.Job(t, "COMPARE", inventorytest.EnvironmentKey(garagem), baseline.Version, &garagem.IdentityID)
	addedIdentity := identity.NewID()
	addedJob := seed.Job(t, "COMPARE", addedKey, baseline.Version, &addedIdentity)
	originOf := func(name string) database.InventoryItemEvidence {
		for _, evidence := range baseline.Evidence {
			if evidence.ItemID == item(baseline, name).ID {
				return evidence
			}
		}
		t.Fatalf("no evidence for %s", name)
		return database.InventoryItemEvidence{}
	}
	pair := func(name string, currentKind string, currentMedia identity.ID, frame *identity.ID) database.InventoryEvidencePair {
		origin := originOf(name)
		return database.InventoryEvidencePair{ID: identity.NewID(), TenantID: seed.Tenant, JobID: salaJob.ID, ItemIdentityID: item(baseline, name).IdentityID, OriginKind: "PHOTO", OriginMediaID: origin.MediaID, CurrentKind: currentKind, CurrentMediaID: currentMedia, CurrentFrameID: frame, Decision: "SAME_VIEW", Confirmed: true, CreatedAt: seed.Now}
	}
	frame := frames[0]
	for _, row := range []database.InventoryEvidencePair{pair("Sofá", "PHOTO", current, nil), pair("Mesa", "VIDEO_FRAME", clip, &frame), pair("Tapete", "PHOTO", current, nil), pair("Cadeira", "PHOTO", current, nil)} {
		if err := seed.DB.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	outsideName, outsideType := "Mesa de trabalho", "TABLE"
	results := []struct {
		job database.InventoryJob
		row database.InventoryItemResult
	}{
		{salaJob, itemResult(item(baseline, "Sofá"), "MISSING", "", "")},
		{salaJob, itemResult(item(baseline, "Mesa"), "CONDITION_CHANGED", "FAIR", "")},
		{salaJob, itemResult(item(baseline, "Quadro"), "NOT_OBSERVED", "", "OBSTRUCTED")},
		{salaJob, itemResult(item(baseline, "Tapete"), "IMPROVED", "GOOD", "")},
		{salaJob, itemResult(item(baseline, "Cadeira"), "PRESENT_UNCHANGED", "", "")},
		{garagemJob, itemResult(item(baseline, "Portão"), "NOT_OBSERVED", "", "ENVIRONMENT_NOT_CAPTURED")},
		{addedJob, database.InventoryItemResult{Status: "OUTSIDE_INVENTORY", OutsideName: &outsideName, OutsideTypeCode: &outsideType}},
	}
	for _, r := range results {
		row := r.row
		row.ID, row.TenantID, row.JobID, row.InspectionID, row.CreatedAt = identity.NewID(), seed.Tenant, r.job.ID, seed.Inspection, seed.Now
		if err := seed.DB.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, job := range []database.InventoryJob{salaJob, garagemJob, addedJob} {
		seed.DB.Model(&database.InventoryJob{}).Where("id=?", job.ID).Update("status", "SUCCEEDED")
	}
	f := reportFixture{seed: seed, baseline: baseline, frame: frame, origin: originOf("Mesa").MediaID}
	f.section, f.evidence = build(t, seed)
	return f
}

func itemResult(row database.InventoryItem, status, after, reason string) database.InventoryItemResult {
	id, before := row.IdentityID, row.Condition
	result := database.InventoryItemResult{ItemIdentityID: &id, Status: status, ConditionBefore: &before}
	if after != "" {
		result.ConditionAfter = &after
	}
	if reason != "" {
		result.ReasonCode = &reason
	}
	return result
}

func build(t *testing.T, seed *inventorytest.Seed) (*core.InventorySection, []core.Evidence) {
	t.Helper()
	var inspection database.Inspection
	var reference database.ReferenceSnapshot
	seed.DB.Where("id=?", seed.Inspection).Take(&inspection)
	seed.DB.Where("inspection_id=?", seed.Inspection).Take(&reference)
	var evidence []core.Evidence
	section, err := buildInventoryReport(context.Background(), seed.DB, seed.Tenant, inspection, reference, &evidence)
	if err != nil {
		t.Fatal(err)
	}
	return section, evidence
}

func TestUT261CoverageCountsBaselineAndListsAddedEnvironments(t *testing.T) {
	f := newReportFixture(t)
	coverage := f.section.Coverage
	if coverage == nil || coverage.ExpectedItems != 6 || coverage.AssessedItems != 4 || coverage.NotObserved != 2 {
		t.Fatalf("coverage = %+v, want expected 6 / assessed 4 / not observed 2", coverage)
	}
	if len(coverage.EnvironmentsAddedDuringCapture) != 1 || coverage.EnvironmentsAddedDuringCapture[0] != "Escritório" {
		t.Fatalf("added environments = %v", coverage.EnvironmentsAddedDuringCapture)
	}
	if len(coverage.EnvironmentsNotCaptured) != 1 || coverage.EnvironmentsNotCaptured[0] != "Garagem" {
		t.Fatalf("not captured = %v, want display names only", coverage.EnvironmentsNotCaptured)
	}
	if f.section.Reference.VersionID != f.baseline.Version.String() || f.section.Reference.Purpose != "MOVE_IN" || f.section.Reference.ActivatedAt == "" {
		t.Fatalf("reference = %+v (UT-260)", f.section.Reference)
	}
}

func TestUT263ProblemsFollowSeverityOrder(t *testing.T) {
	f := newReportFixture(t)
	statuses := []string{}
	for _, problem := range f.section.Problems {
		statuses = append(statuses, problem.Status+":"+problem.Name)
	}
	want := []string{"MISSING:Sofá", "CONDITION_CHANGED:Mesa", "NOT_OBSERVED:Quadro", "ENVIRONMENT_NOT_CAPTURED:Garagem"}
	if len(statuses) != len(want) {
		t.Fatalf("problems = %v, want %v", statuses, want)
	}
	for i := range want {
		if statuses[i] != want[i] {
			t.Fatalf("problems = %v, want %v", statuses, want)
		}
	}
}

func TestUT264InformationalHoldsOutsideAndImprovedOnly(t *testing.T) {
	f := newReportFixture(t)
	got := map[string]string{}
	for _, entry := range f.section.Informational {
		got[entry.Name] = entry.Status + "@" + entry.Environment
	}
	if len(got) != 2 || got["Tapete"] != "IMPROVED@Sala" || got["Mesa de trabalho"] != "OUTSIDE_INVENTORY@Escritório" {
		t.Fatalf("informational = %v", got)
	}
}

func TestUT265And270ProblemsCarryPairsWithFrameTimestampsAndExpiredSource(t *testing.T) {
	f := newReportFixture(t)
	var mesa core.InventoryEntry
	for _, problem := range f.section.Problems {
		if problem.Name == "Mesa" {
			mesa = problem
		}
	}
	if len(mesa.Pairs) != 1 || mesa.Pairs[0].Decision != "SAME_VIEW" || mesa.Pairs[0].TimestampMS == nil || *mesa.Pairs[0].TimestampMS != 1000 || mesa.Pairs[0].Current != f.frame.String() || mesa.Pairs[0].Origin != f.origin.String() {
		t.Fatalf("Mesa pairs = %+v", mesa.Pairs)
	}
	byID := map[string]core.Evidence{}
	for _, evidence := range f.evidence {
		if _, duplicate := byID[evidence.ID]; duplicate {
			t.Fatalf("evidence %s listed twice", evidence.ID)
		}
		byID[evidence.ID] = evidence
	}
	frame, ok := byID[f.frame.String()]
	if !ok || frame.SourceAvailability != "EXPIRED" || frame.FrameID != f.frame.String() || frame.Role != "CURRENT" {
		t.Fatalf("frame evidence = %+v, want the frame referenced with an expired source", frame)
	}
	for _, entry := range f.section.Informational {
		if len(entry.Pairs) != 0 {
			t.Fatal("informational entries carry no pair evidence")
		}
	}
}

func TestUT272DeclaredItemProblemHasNoPairs(t *testing.T) {
	f := newReportFixture(t)
	for _, problem := range f.section.Problems {
		if problem.Name == "Quadro" {
			if len(problem.Pairs) != 0 || problem.Reason == "" {
				t.Fatalf("declared problem = %+v", problem)
			}
			return
		}
	}
	t.Fatal("declared item problem missing")
}

func TestUT262CatalogingInspectionHasNoCoverage(t *testing.T) {
	seed := inventorytest.New(t, inventorytest.InspectionOptions{})
	origin := database.Origin{ID: identity.NewID(), TenantID: seed.Tenant, AssetID: seed.Asset, Version: 1, CreatedAt: seed.Now, UpdatedAt: seed.Now}
	seed.DB.Create(&origin)
	ready := "READY"
	draft := database.OriginVersion{ID: identity.NewID(), TenantID: seed.Tenant, OriginID: origin.ID, VersionNumber: 1, Source: "CAPTURE", SourceInspectionID: &seed.Inspection, HasInventory: true, Status: "DRAFT", DraftState: &ready, IdempotencyKey: "draft", CreatedAt: seed.Now}
	if err := seed.DB.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	section, _ := build(t, seed)
	if section == nil || section.Coverage != nil || section.Reference.VersionID != draft.ID.String() || len(section.Problems) != 0 {
		t.Fatalf("cataloging section = %+v", section)
	}
}
