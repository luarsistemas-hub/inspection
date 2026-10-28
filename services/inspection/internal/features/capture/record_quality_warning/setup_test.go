package record_quality_warning

import (
	"testing"

	"inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSetupValidatesDependenciesB19(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Setup(Dependencies{}); err == nil {
		t.Fatal("Setup accepted missing dependencies")
	}
	if err := Setup(Dependencies{Bus: mediator.New()}); err == nil {
		t.Fatal("Setup accepted a service without a database")
	}
	if err := Setup(Dependencies{Bus: mediator.New(), Service: core.Service{DB: db}}); err != nil {
		t.Fatalf("Setup rejected valid dependencies: %v", err)
	}
}
