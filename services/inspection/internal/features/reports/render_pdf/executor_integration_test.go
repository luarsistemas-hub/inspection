//go:build integration

package render_pdf

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Two executors race for the same queue on PostgreSQL: SKIP LOCKED must hand
// every job to exactly one of them, and each (snapshot, audience) publishes once.
func TestPostgresConcurrentExecutorsProcessEachJobOnce(t *testing.T) {
	dsn := os.Getenv("INSPECTION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("INSPECTION_TEST_DATABASE_URL not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := (database.Migrator{DB: db}).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	tenantID := identity.NewID()
	value := validPDFSnapshot()
	canonical, digest, err := reportcore.CanonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var jobIDs []identity.ID
	for range 4 {
		inspection := database.Inspection{ID: identity.NewID(), TenantID: tenantID, BusinessUnitID: identity.NewID(), AssetID: identity.NewID(), ParticipantID: identity.NewID(), TemplateID: identity.NewID(), TemplateVersionID: identity.NewID(), AnalysisPromptSnapshotID: identity.NewID(), Source: "MANUAL", SourceKey: identity.NewID().String(), Status: "COMPLETED", ReminderInstants: json.RawMessage("[]"), ContextSnapshot: json.RawMessage("{}")}
		if err := db.Create(&inspection).Error; err != nil {
			t.Fatal(err)
		}
		snapshot := database.ReportSnapshot{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspection.ID, VersionNumber: 1, PDFRenderVersion: 1, Mode: "HISTORICAL", Classification: "ATTENTION", JSONDigest: digest, HTMLDigest: "x", CanonicalJSON: json.RawMessage(canonical), HTML: json.RawMessage(`""`), CreatedAt: now}
		if err := db.Create(&snapshot).Error; err != nil {
			t.Fatal(err)
		}
		for _, audience := range []string{"PDF", "PDF_CUSTOMER"} {
			job := database.ReportPDFJob{ID: identity.NewID(), TenantID: tenantID, SnapshotID: snapshot.ID, Audience: audience, RendererVersion: rendererVersion, Status: "QUEUED", NextAttemptAt: now.Add(-time.Second), CreatedAt: now, UpdatedAt: now}
			if err := db.Create(&job).Error; err != nil {
				t.Fatal(err)
			}
			jobIDs = append(jobIDs, job.ID)
		}
	}
	client := &memoryClient{objects: map[string][]byte{}}
	store := objectstore.Store{Bucket: "private", Client: client}
	var group sync.WaitGroup
	for range 3 {
		executor, err := SetupExecutor(db, store, DefaultLimits(), time.Now)
		if err != nil {
			t.Fatal(err)
		}
		group.Add(1)
		go func() {
			defer group.Done()
			for {
				worked, err := executor.RunDue(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				if !worked {
					return
				}
			}
		}()
	}
	group.Wait()
	var jobs []database.ReportPDFJob
	if err := db.Where("id IN ?", jobIDs).Find(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if len(jobs) != len(jobIDs) {
		t.Fatalf("jobs=%d want %d", len(jobs), len(jobIDs))
	}
	for _, job := range jobs {
		if job.Status != "READY" || job.Attempts != 1 || job.ArtifactID == nil || job.PendingObjectKey != "" {
			t.Fatalf("job processed incorrectly: %+v", job)
		}
	}
	var artifacts int64
	if err := db.Model(&database.ReportArtifact{}).Where("tenant_id=?", tenantID).Count(&artifacts).Error; err != nil || artifacts != int64(len(jobIDs)) || client.count() != len(jobIDs) {
		t.Fatalf("artifacts=%d objects=%d err=%v", artifacts, client.count(), err)
	}
	var forced bool
	if err := db.Raw(`SELECT c.relforcerowsecurity FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='reports' AND c.relname='report_pdf_jobs'`).Row().Scan(&forced); err != nil || !forced {
		t.Fatalf("report_pdf_jobs RLS forced=%v err=%v", forced, err)
	}
}
