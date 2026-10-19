package promote_inspection_video

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSetupValidatesDependenciesBeforeRegistration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name string
		deps Dependencies
	}{
		{name: "database", deps: Dependencies{Bus: mediator.New()}},
		{name: "mediator", deps: Dependencies{DB: db}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := Setup(check.deps); err == nil {
				t.Fatal("Setup() succeeded with a missing required dependency")
			}
		})
	}

	bus := mediator.New()
	if err := Setup(Dependencies{DB: db, Bus: bus}); err != nil {
		t.Fatalf("Setup() with required dependencies: %v", err)
	}
	if _, err := bus.Send(context.Background(), Command{TenantID: identity.NewID(), InspectionID: identity.NewID(), ClientMutationID: "setup-registration"}); err == nil {
		t.Fatal("Setup() did not dispatch through its registered command handler")
	}
}

func TestPromotedExpiryExtendsFromFirstReadiness(t *testing.T) {
	firstReady := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	current := firstReady.Add(2 * 24 * time.Hour)
	got := PromotedExpiry(firstReady, current, 30)
	want := firstReady.Add(30 * 24 * time.Hour)
	if !got.Equal(want) {
		t.Fatalf("PromotedExpiry() = %s, want %s", got, want)
	}
}

func TestPromotedExpiryNeverShortensExistingDeadline(t *testing.T) {
	firstReady := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	current := firstReady.Add(40 * 24 * time.Hour)
	got := PromotedExpiry(firstReady, current, 30)
	if !got.Equal(current) {
		t.Fatalf("PromotedExpiry() = %s, want existing deadline %s", got, current)
	}
}
