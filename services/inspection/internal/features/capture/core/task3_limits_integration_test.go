//go:build integration

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestIT041CaptureEnvironmentAndPhotoLimits(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	h, err := harness.NewFromEnv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	fixture, err := h.SeedTask06Fixture(context.Background(), harness.Task06FixtureOptions{SchemaVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	service := Service{DB: h.DB}
	if err := h.WithinTenant(context.Background(), fixture.TenantID, func(tx *gorm.DB) error {
		requirements := make([]Requirement, 60)
		for i := range requirements {
			id := identity.NewID()
			name := fmt.Sprintf("Quarto %02d", i+1)
			requirements[i] = Requirement{Key: "env:" + id.String(), EvidenceKind: "PHOTO", Required: true, Expected: true, MinimumMedia: 1, MaximumMedia: 20, EnvironmentIdentityID: id.String(), EnvironmentTypeCode: "BEDROOM", EnvironmentName: name}
		}
		encoded, err := json.Marshal(requirements)
		if err != nil {
			return err
		}
		return tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fixture.TenantID, fixture.ResponsibilityID).Updates(map[string]any{"status": "OPEN", "media_kind": "PHOTO", "requirements": encoded}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeclareEnvironment(context.Background(), DeclareEnvironmentInput{TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, TypeCode: "BEDROOM", Name: "Quarto extra", ClientMutationID: "it041-sixty-first"}); !apperror.Is(err, apperror.InvalidInput) || err.Error() != "LIMIT_ENVIRONMENTS" {
		t.Fatalf("61st environment error=%v; want LIMIT_ENVIRONMENTS", err)
	}
	if _, err := service.DeclareEnvironment(context.Background(), DeclareEnvironmentInput{TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, TypeCode: "BEDROOM", Name: " quarto   01 ", ClientMutationID: "it041-duplicate"}); !apperror.Is(err, apperror.Conflict) || err.Error() != "DUPLICATE_ENVIRONMENT" {
		t.Fatalf("duplicate environment error=%v; want DUPLICATE_ENVIRONMENT", err)
	}

	mediaFixture, err := h.SeedTask06Fixture(context.Background(), harness.Task06FixtureOptions{SchemaVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	requirementKey := "env:" + identity.NewID().String()
	var twentyFirst identity.ID
	if err := h.WithinTenant(context.Background(), mediaFixture.TenantID, func(tx *gorm.DB) error {
		requirements, _ := json.Marshal([]Requirement{{Key: requirementKey, EvidenceKind: "PHOTO", Required: true, Expected: true, MinimumMedia: 1, MaximumMedia: 20}})
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", mediaFixture.TenantID, mediaFixture.ResponsibilityID).Updates(map[string]any{"status": "OPEN", "media_kind": "PHOTO", "requirements": requirements}).Error; err != nil {
			return err
		}
		for i := 0; i < 21; i++ {
			id := identity.NewID()
			if i == 20 {
				twentyFirst = id
			}
			if err := tx.Create(&database.MediaObject{ID: id, TenantID: mediaFixture.TenantID, ResponsibilityID: mediaFixture.ResponsibilityID, ObjectKey: "it041/" + id.String(), ContentType: "image/jpeg", SHA256: fmt.Sprintf("%064x", i+1), SizeBytes: 10, Status: "READY", MediaKind: "PHOTO", RequirementKey: requirementKey, IdempotencyKey: "it041-photo-" + id.String(), Flags: json.RawMessage(`[]`), AttentionItems: json.RawMessage(`[]`), CreatedAt: time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err = service.SaveMetadata(context.Background(), MetadataInput{TenantID: mediaFixture.TenantID, ResponsibilityID: mediaFixture.ResponsibilityID, MediaID: twentyFirst, RequirementKey: requirementKey, CaptureSource: "CAMERA"})
	if !apperror.Is(err, apperror.InvalidInput) || err.Error() != "LIMIT_MEDIA_PER_ENVIRONMENT" {
		t.Fatalf("21st photo metadata error=%v; want LIMIT_MEDIA_PER_ENVIRONMENT", err)
	}
}
