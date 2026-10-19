package video_admission

import (
	"testing"

	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRequireEnabledMapsMissingAndDisabledRows(t *testing.T) {
	db := videoAdmissionTestDB(t)
	if err := RequireEnabled(db); apperrorCode(err) != apperror.IntegrationDisabled {
		t.Fatalf("missing admission row error=%v, want INTEGRATION_DISABLED", err)
	}
	if err := db.Create(&database.VideoAdmission{ID: 1, Enabled: false, Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := RequireEnabled(db); apperrorCode(err) != apperror.IntegrationDisabled {
		t.Fatalf("disabled admission error=%v, want INTEGRATION_DISABLED", err)
	}
	if err := db.Model(&database.VideoAdmission{}).Where("id=1").Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := RequireEnabled(db); err != nil {
		t.Fatalf("enabled admission rejected: %v", err)
	}
}

func videoAdmissionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS platform").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.VideoAdmission{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func apperrorCode(err error) apperror.Code {
	code, _, _ := apperror.Public(err)
	return code
}
