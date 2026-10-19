//go:build integration

package video_admission

import (
	"context"
	"os"
	"testing"
	"time"

	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestAdmissionReadLockSerializesWithDisableIntegration(t *testing.T) {
	adminDSN, runtimeDSN := os.Getenv("INSPECTION_TEST_DATABASE_URL"), os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL")
	if adminDSN == "" || runtimeDSN == "" {
		t.Skip("video admission locking integration requires INSPECTION_TEST_DATABASE_URL and INSPECTION_TEST_RUNTIME_DATABASE_URL")
	}
	adminDB, err := gorm.Open(postgres.Open(adminDSN), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := (database.Migrator{DB: adminDB}).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	runtimeDB, err := gorm.Open(postgres.Open(runtimeDSN), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var original database.VideoAdmission
	if err := adminDB.First(&original, "id=1").Error; err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := adminDB.Model(&database.VideoAdmission{}).Where("id=1").Updates(map[string]any{"enabled": original.Enabled, "version": original.Version, "changed_by": original.ChangedBy, "changed_at": original.ChangedAt, "reason": original.Reason, "mutation_id": original.MutationID, "payload_hash": original.PayloadHash}).Error; err != nil {
			t.Errorf("restore video admission fixture: %v", err)
		}
	}()
	if err := adminDB.Model(&database.VideoAdmission{}).Where("id=1").Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}

	lockAcquired := make(chan error, 1)
	allowCommit := make(chan struct{})
	createDone := make(chan error, 1)
	go func() {
		createDone <- runtimeDB.WithContext(context.Background()).Transaction(func(tx *gorm.DB) error {
			if err := RequireEnabled(tx); err != nil {
				return err
			}
			lockAcquired <- nil
			<-allowCommit
			return nil
		})
	}()
	if err := <-lockAcquired; err != nil {
		t.Fatal(err)
	}

	updateStarted := make(chan struct{})
	updateDone := make(chan error, 1)
	go func() {
		close(updateStarted)
		updateDone <- adminDB.Model(&database.VideoAdmission{}).Where("id=1").Update("enabled", false).Error
	}()
	<-updateStarted
	select {
	case err := <-updateDone:
		t.Fatalf("disable committed while an admitted creation held FOR SHARE: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(allowCommit)
	if err := <-createDone; err != nil {
		t.Fatalf("creation that acquired admission first failed: %v", err)
	}
	if err := <-updateDone; err != nil {
		t.Fatalf("disable did not commit after creation: %v", err)
	}
	if err := runtimeDB.Transaction(func(tx *gorm.DB) error { return RequireEnabled(tx) }); apperrorCode(err) != apperror.IntegrationDisabled {
		t.Fatalf("creation after disable commit error=%v, want INTEGRATION_DISABLED", err)
	}
}
