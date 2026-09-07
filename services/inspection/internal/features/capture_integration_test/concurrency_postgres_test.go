package capture_integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	mediacore "inspection/services/inspection/internal/features/media/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func postgresSubmission(t *testing.T) submissionFixture {
	t.Helper()
	dsn, runtimeDSN := os.Getenv("INSPECTION_TEST_DATABASE_URL"), os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL")
	if dsn == "" || runtimeDSN == "" {
		t.Skip("PostgreSQL admin and runtime DSNs are required")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := (database.Migrator{DB: admin}).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	runtimeDB, err := gorm.Open(postgres.Open(runtimeDSN), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, db := range []*gorm.DB{admin, runtimeDB} {
		pool, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		pool.SetMaxOpenConns(16)
		t.Cleanup(func() { _ = pool.Close() })
	}
	f := submissionFixtureWithDB(t, admin)
	f.service.DB = runtimeDB
	f.service.Within = (tenanttx.Runner{DB: runtimeDB}).Within
	return f
}

func TestIT185PostgresConcurrentSubmitCommitsOneVersionAndEvent(t *testing.T) {
	f := postgresSubmission(t)
	f.addMedia(t, "READY", "room", "room overview", true)
	const callers = 8
	results := make(chan database.SubmissionVersion, callers)
	errors := make(chan error, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := f.submit(false)
			results <- result
			errors <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id identity.ID
	for result := range results {
		if id == (identity.ID{}) {
			id = result.ID
		}
		if result.ID != id || !result.Complete {
			t.Fatalf("concurrent outcome diverged: %+v", result)
		}
	}
	for _, model := range []any{&database.SubmissionVersion{}, &database.OutboxIntent{}} {
		var count int64
		if err := f.db.Model(model).Where("tenant_id=?", f.draft.TenantID).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("duplicate %T: %d %v", model, count, err)
		}
	}
}

func TestIT165PostgresConcurrentMetadataPreservesBothPhotos(t *testing.T) {
	f := postgresSubmission(t)
	media := []database.MediaObject{f.addMedia(t, "READY", "", "", false), f.addMedia(t, "READY", "", "", false)}
	start := make(chan struct{})
	errors := make(chan error, len(media))
	for _, item := range media {
		go func() {
			<-start
			_, err := f.service.SaveMetadata(context.Background(), capturecore.MetadataInput{TenantID: f.draft.TenantID, ResponsibilityID: f.draft.ResponsibilityID, MediaID: item.ID, RequirementKey: "room", Description: "room overview", CaptureSource: "CAMERA", WindowStartedAt: f.now})
			errors <- err
		}()
	}
	close(start)
	for range media {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	var answer database.RequirementAnswer
	if err := f.db.Where("draft_id=?", f.draft.ID).First(&answer).Error; err != nil {
		t.Fatal(err)
	}
	var ids []identity.ID
	if err := json.Unmarshal(answer.MediaIDs, &ids); err != nil || len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("concurrent association lost photo: %s %v", answer.MediaIDs, err)
	}
}

func TestIT173PostgresConcurrentAdmissionNeverExceeds200(t *testing.T) {
	f := postgresSubmission(t)
	within := (tenanttx.Runner{DB: f.service.DB}).Within
	service := mediacore.Service{DB: f.service.DB, Within: within, Store: objectstore.Store{Bucket: "private", Client: &objectClient{}}, Now: f.service.Now}
	const callers = 205
	start := make(chan struct{})
	errors := make(chan error, callers)
	for index := range callers {
		go func() {
			<-start
			_, err := service.CreateWithKey(context.Background(), f.draft.TenantID, f.draft.ResponsibilityID, "image/jpeg", strings.Repeat("a", 64), 100, fmt.Sprintf("%s-%d", f.draft.ID, index))
			errors <- err
		}()
	}
	close(start)
	accepted, rejected := 0, 0
	for range callers {
		if err := <-errors; err == nil {
			accepted++
		} else if code(err) == apperror.InvalidState {
			rejected++
		} else {
			t.Errorf("unexpected admission error: %v", err)
		}
	}
	if accepted != 200 || rejected != 5 {
		t.Fatalf("admitted=%d rejected=%d", accepted, rejected)
	}
	var count int64
	if err := f.db.Model(&database.MediaObject{}).Where("tenant_id=?", f.draft.TenantID).Count(&count).Error; err != nil || count != 200 {
		t.Fatalf("persisted=%d err=%v", count, err)
	}
}
