//go:build integration

package update_prompt

import (
	"context"
	"os"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/prompt"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestIT040UpdatePromptCommandChangesOnlyRequestedTask(t *testing.T) {
	url := os.Getenv("INSPECTION_TEST_PROMPT_DATABASE_URL")
	runtimeURL := os.Getenv("INSPECTION_TEST_PROMPT_RUNTIME_DATABASE_URL")
	if url == "" || runtimeURL == "" {
		t.Skip("fresh disposable prompt admin and runtime database URLs are required")
	}
	db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	runtimeDB, err := gorm.Open(postgres.Open(runtimeURL), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var exists *string
	if err := db.Raw("SELECT to_regclass('platform.schema_migrations')").Scan(&exists).Error; err != nil {
		t.Fatal(err)
	}
	if exists != nil {
		t.Fatal("prompt integration database must be fresh")
	}
	if err := (database.Migrator{DB: db}).Migrate(context.Background()); err != nil {
		t.Fatalf("migrate disposable prompt database: %v", err)
	}
	tenantID, actorID := identity.NewID(), identity.NewID()
	bus := mediator.New()
	const issuer, subject = "https://issuer.integration.test", "integration-admin"
	if err := Setup(Dependencies{DB: runtimeDB, Bus: bus, Authorizer: auth.Authorizer{}, SuperAdminIssuer: issuer, SuperAdminSubject: subject}); err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: actorID, TenantID: tenantID, Issuer: issuer, Subject: subject, Roles: []string{auth.TenantAdmin}, ProductEntitlements: []string{auth.AdminProduct}}})
	var findingsBefore database.AnalysisPrompt
	if err := db.Where("analysis_type=? AND task=?", prompt.RealEstate, prompt.Findings).First(&findingsBefore).Error; err != nil {
		t.Fatal(err)
	}
	var compareBefore database.AnalysisPrompt
	if err := db.Where("analysis_type=? AND task=?", prompt.RealEstate, prompt.InventoryCompare).First(&compareBefore).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Send(ctx, Command{TenantID: tenantID, AnalysisType: prompt.RealEstate, Task: string(prompt.InventoryCompare), SystemPrompt: "Atualize a comparação.", ExpectedRevision: compareBefore.Revision, ClientMutationID: "it040-update-compare"}); err != nil {
		t.Fatalf("updateAnalysisPrompt command: %v", err)
	}
	var findingsAfter, compareAfter database.AnalysisPrompt
	if err := db.Where("analysis_type=? AND task=?", prompt.RealEstate, prompt.Findings).First(&findingsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("analysis_type=? AND task=?", prompt.RealEstate, prompt.InventoryCompare).First(&compareAfter).Error; err != nil {
		t.Fatal(err)
	}
	if findingsAfter.Revision != findingsBefore.Revision || findingsAfter.CanonicalDigest != findingsBefore.CanonicalDigest {
		t.Fatalf("FINDINGS changed after INVENTORY_COMPARE update: before=%+v after=%+v", findingsBefore, findingsAfter)
	}
	if compareAfter.Revision != compareBefore.Revision+1 || compareAfter.CanonicalDigest == compareBefore.CanonicalDigest {
		t.Fatalf("INVENTORY_COMPARE was not updated: before=%+v after=%+v", compareBefore, compareAfter)
	}
}
