package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// comparisonScenario is a completed comparative inspection pinned to a
// one-environment baseline whose sink has one photo of evidence.
type comparisonScenario struct {
	db                                        *gorm.DB
	tenant, inspection, version               identity.ID
	envIdentity, itemIdentity, originEvidence identity.ID
}

func newComparisonScenario(t *testing.T) comparisonScenario {
	t.Helper()
	db := inventoryTestDB(t)
	s := comparisonScenario{db: db, tenant: identity.NewID(), inspection: identity.NewID(), version: identity.NewID(), envIdentity: identity.NewID(), itemIdentity: identity.NewID(), originEvidence: identity.NewID()}
	assetID, originID, envID, itemID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	state := "READY"
	rows := []any{
		&database.Origin{ID: originID, TenantID: s.tenant, AssetID: assetID, ActiveVersionID: &s.version, Version: 1},
		&database.OriginVersion{ID: s.version, TenantID: s.tenant, OriginID: originID, VersionNumber: 1, Source: "CAPTURE", HasInventory: true, DraftState: &state, Status: "ACTIVE", Purpose: stringPointer("MOVE_IN"), IdempotencyKey: "comparison-" + s.version.String()},
		&database.InventoryEnvironment{ID: envID, TenantID: s.tenant, OriginVersionID: s.version, IdentityID: s.envIdentity, TypeCode: "KITCHEN", Name: "Cozinha", Decision: "ACCEPTED", Position: 0, Version: 1},
		&database.InventoryItem{ID: itemID, TenantID: s.tenant, OriginVersionID: s.version, EnvironmentID: envID, IdentityID: s.itemIdentity, TypeCode: "SINK", Name: "Pia", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "ACCEPTED", Version: 1},
		&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: s.tenant, ItemID: itemID, Kind: "PHOTO", MediaID: s.originEvidence, Position: 0},
	}
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO inspections.inspections (id,tenant_id,status,asset_id) VALUES (?,?,?,?)", []any{s.inspection, s.tenant, "COMPLETED", assetID}},
		{"INSERT INTO inspections.reference_snapshots (id,tenant_id,inspection_id,reference_version_id) VALUES (?,?,?,?)", []any{identity.NewID(), s.tenant, s.inspection, s.version}},
	} {
		if err := db.Exec(statement.sql, statement.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func (s comparisonScenario) job(t *testing.T, key string, generation int, env identity.ID) identity.ID {
	t.Helper()
	id := identity.NewID()
	if err := s.db.Exec("INSERT INTO analysis.inventory_jobs (id,tenant_id,inspection_id,origin_version_id,kind,status,requirement_key,generation,environment_identity_id) VALUES (?,?,?,?,?,?,?,?,?)", id, s.tenant, s.inspection, s.version, "COMPARE", "SUCCEEDED", key, generation, env).Error; err != nil {
		t.Fatal(err)
	}
	return id
}

func (s comparisonScenario) itemResult(t *testing.T, job identity.ID, status, after string) {
	t.Helper()
	if err := s.db.Exec("INSERT INTO analysis.inventory_item_results (id,tenant_id,job_id,inspection_id,item_identity_id,status,condition_after) VALUES (?,?,?,?,?,?,?)", identity.NewID(), s.tenant, job, s.inspection, s.itemIdentity, status, after).Error; err != nil {
		t.Fatal(err)
	}
}

func (s comparisonScenario) outside(t *testing.T, job identity.ID, name string) {
	t.Helper()
	if err := s.db.Exec("INSERT INTO analysis.inventory_item_results (id,tenant_id,job_id,inspection_id,status,outside_type_code,outside_name) VALUES (?,?,?,?,?,?,?)", identity.NewID(), s.tenant, job, s.inspection, "OUTSIDE_INVENTORY", "CHAIR", name).Error; err != nil {
		t.Fatal(err)
	}
}

func (s comparisonScenario) pair(t *testing.T, job, current identity.ID, confirmed bool) {
	t.Helper()
	if err := s.db.Create(&database.InventoryEvidencePair{ID: identity.NewID(), TenantID: s.tenant, JobID: job, ItemIdentityID: s.itemIdentity, OriginKind: "PHOTO", OriginMediaID: s.originEvidence, CurrentKind: "PHOTO", CurrentMediaID: current, Decision: "SAME_VIEW", Confirmed: confirmed}).Error; err != nil {
		t.Fatal(err)
	}
}

func (s comparisonScenario) start(t *testing.T) (database.OriginVersion, []database.InventoryItem) {
	t.Helper()
	draft, err := testService(s.db).StartFromInspection(context.Background(), s.tenant, s.inspection, identity.NewID())
	if err != nil {
		t.Fatal(err)
	}
	var items []database.InventoryItem
	if err := s.db.Where("tenant_id=? AND origin_version_id=?", s.tenant, draft.ID).Order("name").Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	return draft, items
}

func (s comparisonScenario) copiedSink(t *testing.T, items []database.InventoryItem) database.InventoryItem {
	t.Helper()
	for _, item := range items {
		if item.IdentityID == s.itemIdentity {
			return item
		}
	}
	t.Fatalf("copied sink missing from %+v", items)
	return database.InventoryItem{}
}

func TestStartFromInspectionImportsOnlyTheCurrentGeneration(t *testing.T) {
	s := newComparisonScenario(t)
	key := "env:" + s.envIdentity.String()
	stale := s.job(t, key, 1, s.envIdentity)
	s.itemResult(t, stale, "CONDITION_CHANGED", "FAIR")
	s.outside(t, stale, "Cadeira descartada")
	current := s.job(t, key, 2, s.envIdentity)
	s.itemResult(t, current, "PRESENT_UNCHANGED", "GOOD")
	_, items := s.start(t)
	if len(items) != 1 {
		t.Fatalf("draft items = %+v, want only the baseline sink", items)
	}
	if sink := s.copiedSink(t, items); sink.Decision != "ACCEPTED" || len(sink.Suggested) != 0 {
		t.Fatalf("stale condition change leaked into the draft: %+v", sink)
	}
}

func TestStartFromInspectionAttachesConfirmedCurrentEvidence(t *testing.T) {
	s := newComparisonScenario(t)
	job := s.job(t, "env:"+s.envIdentity.String(), 1, s.envIdentity)
	s.itemResult(t, job, "CONDITION_CHANGED", "FAIR")
	confirmedPhoto, uncertainPhoto := identity.NewID(), identity.NewID()
	s.pair(t, job, confirmedPhoto, true)
	s.pair(t, job, uncertainPhoto, false)
	_, items := s.start(t)
	sink := s.copiedSink(t, items)
	if sink.Decision != "PENDING" || !strings.Contains(string(sink.Suggested), `"condition":"FAIR"`) {
		t.Fatalf("condition suggestion missing: %+v", sink)
	}
	var evidence []database.InventoryItemEvidence
	if err := s.db.Where("tenant_id=? AND item_id=?", s.tenant, sink.ID).Order("position").Find(&evidence).Error; err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 2 || evidence[0].MediaID != confirmedPhoto || evidence[1].MediaID != s.originEvidence || evidence[0].Position >= evidence[1].Position {
		t.Fatalf("evidence = %+v, want the confirmed current photo first and the historical photo kept", evidence)
	}
}

func TestStartFromInspectionSuggestsObservedImprovement(t *testing.T) {
	s := newComparisonScenario(t)
	job := s.job(t, "env:"+s.envIdentity.String(), 1, s.envIdentity)
	s.itemResult(t, job, "IMPROVED", "NEW")
	_, items := s.start(t)
	sink := s.copiedSink(t, items)
	if sink.Condition != "GOOD" || sink.Decision != "PENDING" || !strings.Contains(string(sink.Suggested), `"condition":"NEW"`) {
		t.Fatalf("improvement not sent to review: %+v", sink)
	}
}

func TestStartFromInspectionCreatesEnvironmentAddedDuringCapture(t *testing.T) {
	s := newComparisonScenario(t)
	added := identity.NewID()
	key := "env:new:" + added.String()
	responsibility := identity.NewID()
	if err := s.db.Exec("INSERT INTO inspections.responsibilities (id,tenant_id,inspection_id) VALUES (?,?,?)", responsibility, s.tenant, s.inspection).Error; err != nil {
		t.Fatal(err)
	}
	requirements, _ := json.Marshal([]capturecore.Requirement{{Key: key, Label: "Ambiente novo", EnvironmentIdentityID: added.String(), EnvironmentTypeCode: "OFFICE", EnvironmentName: "Escritório", AddedDuringCapture: true}})
	if err := s.db.Exec("INSERT INTO capture.capture_drafts (id,tenant_id,responsibility_id,reference_payload,policy_payload,requirements,created_at) VALUES (?,?,?,?,?,?,CURRENT_TIMESTAMP)", identity.NewID(), s.tenant, responsibility, []byte("{}"), []byte("{}"), requirements).Error; err != nil {
		t.Fatal(err)
	}
	s.outside(t, s.job(t, key, 1, added), "Mesa de trabalho")
	draft, items := s.start(t)
	if len(items) != 2 {
		t.Fatalf("draft items = %+v, want the sink and the new environment's item", items)
	}
	var env database.InventoryEnvironment
	if err := s.db.Where("tenant_id=? AND origin_version_id=? AND identity_id=?", s.tenant, draft.ID, added).Take(&env).Error; err != nil {
		t.Fatalf("added environment missing: %v", err)
	}
	if env.Name != "Escritório" || env.TypeCode != "OFFICE" || !env.AddedDuringCapture || env.Decision != "PENDING" {
		t.Fatalf("added environment = %+v", env)
	}
	for _, item := range items {
		if item.Name == "Mesa de trabalho" && item.EnvironmentID != env.ID {
			t.Fatalf("outside item not placed in the added environment: %+v", item)
		}
	}
}
