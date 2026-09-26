package list_reports

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSetupRequiresDatabase(t *testing.T) {
	if _, err := Setup(Dependencies{}); err == nil {
		t.Fatal("expected missing database error")
	}
}

func TestCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, time.September, 26, 15, 4, 5, 123, time.FixedZone("BRT", -3*60*60))
	id := identity.NewID()
	encoded := encodeCursor(at, id)
	gotTime, gotID, err := decodeCursor(encoded)
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if !gotTime.Equal(at) || gotID != id {
		t.Fatalf("round trip = (%s, %s), want (%s, %s)", gotTime, gotID, at, id)
	}
}

func TestOperationRejectsInvalidInputsBeforeQuery(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := Setup(Dependencies{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	base := Input{TenantID: identity.NewID(), TenantAdmin: true}
	base.After = "not-a-cursor"
	if _, err := operation(context.Background(), base); err == nil {
		t.Fatal("expected invalid cursor error")
	}
	base.After = ""
	base.Classification = "UNKNOWN"
	if _, err := operation(context.Background(), base); err == nil {
		t.Fatal("expected invalid classification error")
	}
}

func TestListReturnsLatestSnapshotsWithSearchScopeAndCursor(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"reports", "inspections"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.AutoMigrate(&database.ReportSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE inspections.inspections (id blob primary key, tenant_id blob not null, business_unit_id blob not null, asset_id blob not null, project_id blob)").Error; err != nil {
		t.Fatal(err)
	}
	tenant, otherTenant, unit, otherUnit := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	assetA, assetB, otherAsset, projectB := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	inspectionA, inspectionB, inspectionOther := identity.NewID(), identity.NewID(), identity.NewID()
	for _, row := range []struct {
		id, tenant, unit, asset identity.ID
		project                 *identity.ID
	}{
		{id: inspectionA, tenant: tenant, unit: unit, asset: assetA},
		{id: inspectionB, tenant: tenant, unit: unit, asset: assetB, project: &projectB},
		{id: inspectionOther, tenant: otherTenant, unit: otherUnit, asset: otherAsset},
	} {
		if err := db.Exec("INSERT INTO inspections.inspections (id, tenant_id, business_unit_id, asset_id, project_id) VALUES (?, ?, ?, ?, ?)", row.id, row.tenant, row.unit, row.asset, row.project).Error; err != nil {
			t.Fatal(err)
		}
	}
	baseTime := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	createSnapshot := func(id, inspection, rowTenant identity.ID, version int, classification, asset string, at time.Time) {
		t.Helper()
		canonical, _ := json.Marshal(map[string]any{"context": map[string]any{"asset": map[string]string{"name": asset, "address": "Rua Central", "externalKey": asset + "-01"}, "participant": map[string]string{"name": "Maria Silva"}}})
		if err := db.Create(&database.ReportSnapshot{ID: id, TenantID: rowTenant, InspectionID: inspection, VersionNumber: version, Classification: classification, CanonicalJSON: canonical, HTML: json.RawMessage(`""`), CreatedAt: at}).Error; err != nil {
			t.Fatal(err)
		}
	}
	createSnapshot(identity.NewID(), inspectionA, tenant, 1, "NORMAL", "Casa antiga", baseTime)
	createSnapshot(identity.NewID(), inspectionA, tenant, 2, "CRITICAL", "Casa atual", baseTime.Add(time.Minute))
	createSnapshot(identity.NewID(), inspectionB, tenant, 1, "ATTENTION", "Apartamento", baseTime.Add(2*time.Minute))
	createSnapshot(identity.NewID(), inspectionOther, otherTenant, 1, "CRITICAL", "Outro tenant", baseTime.Add(3*time.Minute))
	operation, err := Setup(Dependencies{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	input := Input{TenantID: tenant, Scopes: []identity.ID{unit}}
	page, err := operation(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].AssetName != "Apartamento" || page.Items[1].AssetName != "Casa atual" || page.Items[1].Version != 2 {
		t.Fatalf("unexpected scoped latest reports: %#v", page.Items)
	}
	input.Search = "maria silva"
	input.Classification = "CRITICAL"
	input.First = 1
	page, err = operation(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].InspectionID != inspectionA || page.EndCursor == "" {
		t.Fatalf("unexpected filtered first page: %#v", page)
	}
	input.Search = ""
	input.Classification = ""
	input.After = ""
	page, err = operation(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].InspectionID != inspectionB || !page.HasNextPage {
		t.Fatalf("unexpected first unfiltered page: %#v", page)
	}
	input.After = page.EndCursor
	page, err = operation(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].InspectionID != inspectionA || page.HasNextPage {
		t.Fatalf("unexpected second page: %#v", page)
	}
	for name, scoped := range map[string]Input{
		"asset":      {TenantID: tenant, AssetScopes: []identity.ID{assetA}},
		"project":    {TenantID: tenant, ProjectScopes: []identity.ID{projectB}},
		"inspection": {TenantID: tenant, InspectionScopes: []identity.ID{inspectionA}},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := operation(context.Background(), scoped)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Items) != 1 {
				t.Fatalf("expected a single scoped report, got %#v", result.Items)
			}
			want := inspectionA
			if name == "project" {
				want = inspectionB
			}
			if result.Items[0].InspectionID != want {
				t.Fatalf("scope returned inspection %s, want %s", result.Items[0].InspectionID, want)
			}
		})
	}
}
