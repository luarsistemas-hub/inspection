package purge_data

import (
	"testing"

	"inspection/libs/identity"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPurgeInventoryAnalysisRemovesInspectionRowsAndKeepsBaseline(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"analysis", "origins"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`CREATE TABLE analysis.inventory_jobs (id text, tenant_id text, inspection_id text)`,
		`CREATE TABLE analysis.inventory_evidence_pairs (id text, tenant_id text, job_id text)`,
		`CREATE TABLE analysis.inventory_item_results (id text, tenant_id text, job_id text, inspection_id text)`,
		`CREATE TABLE analysis.findings (id text, tenant_id text, inventory_job_id text, comparison_job_id text)`,
		`CREATE TABLE origins.origin_versions (id text, tenant_id text, source_inspection_id text)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, purged, other := identity.NewID(), identity.NewID(), identity.NewID()
	seed := func(inspectionID identity.ID) {
		jobID := identity.NewID()
		for _, insert := range []struct {
			sql  string
			args []any
		}{
			{"INSERT INTO analysis.inventory_jobs VALUES (?,?,?)", []any{jobID, tenantID, inspectionID}},
			{"INSERT INTO analysis.inventory_evidence_pairs VALUES (?,?,?)", []any{identity.NewID(), tenantID, jobID}},
			{"INSERT INTO analysis.inventory_item_results VALUES (?,?,?,?)", []any{identity.NewID(), tenantID, jobID, inspectionID}},
			{"INSERT INTO analysis.findings VALUES (?,?,?,NULL)", []any{identity.NewID(), tenantID, jobID}},
		} {
			if err := db.Exec(insert.sql, insert.args...).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	seed(purged)
	seed(other)
	comparisonFinding, baselineID := identity.NewID(), identity.NewID()
	if err := db.Exec("INSERT INTO analysis.findings VALUES (?,?,NULL,?)", comparisonFinding, tenantID, identity.NewID()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO origins.origin_versions VALUES (?,?,?)", baselineID, tenantID, purged).Error; err != nil {
		t.Fatal(err)
	}

	if err := purgeInventoryAnalysis(db, tenantID, purged); err != nil {
		t.Fatal(err)
	}

	for table, want := range map[string]int64{"analysis.inventory_jobs": 1, "analysis.inventory_evidence_pairs": 1, "analysis.inventory_item_results": 1, "analysis.findings": 2} {
		var remaining int64
		if err := db.Raw("SELECT count(*) FROM " + table).Scan(&remaining).Error; err != nil {
			t.Fatal(err)
		}
		if remaining != want {
			t.Fatalf("%s rows=%d, want %d", table, remaining, want)
		}
	}
	var otherJobs int64
	if err := db.Raw("SELECT count(*) FROM analysis.inventory_jobs WHERE inspection_id=?", other).Scan(&otherJobs).Error; err != nil || otherJobs != 1 {
		t.Fatalf("other inspection job count=%d err=%v", otherJobs, err)
	}
	var baselines, detached int64
	if err := db.Raw("SELECT count(*), count(source_inspection_id) FROM origins.origin_versions WHERE id=?", baselineID).Row().Scan(&baselines, &detached); err != nil {
		t.Fatal(err)
	}
	if baselines != 1 || detached != 0 {
		t.Fatalf("baseline rows=%d with source inspection=%d, want kept and detached", baselines, detached)
	}
}
