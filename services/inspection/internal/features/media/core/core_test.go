package core

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/sensitivecontent"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestUT032SupportedBoundaries(t *testing.T) {
	for _, kind := range []string{"image/jpeg", "image/png", "image/webp", "image/heic", "image/heif"} {
		if err := ValidateAdmission(kind, objectstore.MaxOriginalBytes, 199); err != nil {
			t.Fatalf("%s boundary rejected: %v", kind, err)
		}
	}
}

func TestUT033RejectedMediaBoundaries(t *testing.T) {
	for _, tc := range []struct {
		kind   string
		size   int64
		active int
	}{{"text/plain", 1, 0}, {"image/jpeg", objectstore.MaxOriginalBytes + 1, 0}, {"image/jpeg", 1, 200}} {
		if ValidateAdmission(tc.kind, tc.size, tc.active) == nil {
			t.Fatalf("accepted invalid input: %+v", tc)
		}
	}
}

func TestIT193ScreeningAttemptLimitIsExplicit(t *testing.T) {
	if MaxScreeningAttempts != 3 {
		t.Fatalf("unexpected screening attempt limit: %d", MaxScreeningAttempts)
	}
}

func TestScreeningDetectorFailuresConsumeRetryBudget(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"media", "messaging"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.MediaObject{}, &database.ScreeningRun{}, &database.OutboxIntent{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenantID, mediaID := identity.NewID(), identity.NewID()
	if err := db.Create(&database.MediaObject{ID: mediaID, TenantID: tenantID, ResponsibilityID: identity.NewID(), ObjectKey: "private/original", ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 1, Status: "VERIFIED"}).Error; err != nil {
		t.Fatal(err)
	}
	service := Service{
		DB:       db,
		Detector: sensitivecontent.Detector{Model: failingModel{}},
		Now:      func() time.Time { return time.Unix(100, 0).UTC() },
		Within: func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
			return fn(db)
		},
	}
	for attempt := 0; attempt < MaxScreeningAttempts; attempt++ {
		if _, err := service.Screen(context.Background(), tenantID, mediaID, []byte("invalid")); err == nil || code(err) != apperror.DependencyUnavailable {
			t.Fatalf("attempt %d returned %v", attempt+1, err)
		}
	}
	if _, err := service.Screen(context.Background(), tenantID, mediaID, []byte("invalid")); code(err) != apperror.RateLimited {
		t.Fatalf("retry budget was not enforced: %v", err)
	}
	var attempts int64
	if err := db.Model(&database.ScreeningRun{}).Where("tenant_id=? AND media_id=?", tenantID, mediaID).Count(&attempts).Error; err != nil || attempts != MaxScreeningAttempts {
		t.Fatalf("persisted attempts=%d err=%v", attempts, err)
	}
}

type failingModel struct{}

func (failingModel) Detect(context.Context, image.Image) ([]sensitivecontent.Region, error) {
	return nil, errors.New("detector unavailable")
}

func (failingModel) Bytes() []byte { return []byte("failing-model") }

func code(err error) apperror.Code {
	value, _, _ := apperror.Public(err)
	return value
}
