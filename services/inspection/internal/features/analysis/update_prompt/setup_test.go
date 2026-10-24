package update_prompt

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/prompt"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestUpdateTaskPrompt_UT172(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec("ATTACH DATABASE ':memory:' AS analysis").Error)
	require.NoError(t, db.Exec(`CREATE TABLE analysis.prompts (
		id TEXT PRIMARY KEY, analysis_type TEXT NOT NULL, task TEXT NOT NULL,
		definition_json BLOB NOT NULL, canonical_digest TEXT NOT NULL, revision INTEGER NOT NULL,
		updated_by TEXT NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL
	)`).Error)

	now := time.Now().UTC()
	findings, findingsDigest, err := prompt.CanonicalizeTask(string(prompt.Findings), prompt.DefaultDefinitionForTask(string(prompt.Findings)))
	require.NoError(t, err)
	oldCompareJSON, oldCompareDigest, err := prompt.CanonicalizeTask(string(prompt.InventoryCompare), prompt.DefaultDefinitionForTask(string(prompt.InventoryCompare)))
	require.NoError(t, err)
	compare := prompt.DefaultDefinitionForTask(string(prompt.InventoryCompare))
	compare.SystemPrompt += " Atualize a avaliação."
	compareJSON, compareDigest, err := prompt.CanonicalizeTask(string(prompt.InventoryCompare), compare)
	require.NoError(t, err)
	actor := identity.NewID()
	require.NoError(t, db.Create(&database.AnalysisPrompt{ID: identity.NewID(), AnalysisType: prompt.RealEstate, Task: string(prompt.Findings), DefinitionJSON: findings, CanonicalDigest: findingsDigest, Revision: 1, UpdatedBy: actor, CreatedAt: now, UpdatedAt: now}).Error)
	require.NoError(t, db.Create(&database.AnalysisPrompt{ID: identity.NewID(), AnalysisType: prompt.RealEstate, Task: string(prompt.InventoryCompare), DefinitionJSON: oldCompareJSON, CanonicalDigest: oldCompareDigest, Revision: 1, UpdatedBy: actor, CreatedAt: now, UpdatedAt: now}).Error)

	_, _, err = updateTaskPrompt(context.Background(), db, prompt.RealEstate, string(prompt.InventoryCompare), 1, compareJSON, compareDigest, actor, now.Add(time.Second))
	require.NoError(t, err)
	var persistedFindings, persistedCompare database.AnalysisPrompt
	require.NoError(t, db.Where("task=?", string(prompt.Findings)).First(&persistedFindings).Error)
	require.NoError(t, db.Where("task=?", string(prompt.InventoryCompare)).First(&persistedCompare).Error)
	require.Equal(t, int64(1), persistedFindings.Revision)
	require.Equal(t, findingsDigest, persistedFindings.CanonicalDigest)
	require.Equal(t, int64(2), persistedCompare.Revision)
	require.Equal(t, compareDigest, persistedCompare.CanonicalDigest)
}

func TestUpdatePromptCommandUpdatesOnlyRequestedTaskUT172(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec("ATTACH DATABASE ':memory:' AS analysis").Error)
	require.NoError(t, db.Exec("ATTACH DATABASE ':memory:' AS audit").Error)
	require.NoError(t, db.Exec(`CREATE TABLE analysis.prompts (id TEXT PRIMARY KEY, analysis_type TEXT NOT NULL, task TEXT NOT NULL, definition_json BLOB NOT NULL, canonical_digest TEXT NOT NULL, revision INTEGER NOT NULL, updated_by TEXT NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, UNIQUE(analysis_type,task))`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE audit.events (id TEXT PRIMARY KEY,tenant_id TEXT NOT NULL,actor_id TEXT NOT NULL,action TEXT NOT NULL,target_type TEXT NOT NULL,target_id TEXT NOT NULL,outcome TEXT NOT NULL,reason TEXT,correlation_id TEXT NOT NULL,occurred_at DATETIME NOT NULL)`).Error)
	now := time.Now().UTC()
	findingsJSON, findingsDigest, err := prompt.CanonicalizeTask(string(prompt.Findings), prompt.DefaultDefinitionForTask(string(prompt.Findings)))
	require.NoError(t, err)
	compareJSON, compareDigest, err := prompt.CanonicalizeTask(string(prompt.InventoryCompare), prompt.DefaultDefinitionForTask(string(prompt.InventoryCompare)))
	require.NoError(t, err)
	actorID := identity.NewID()
	for _, row := range []database.AnalysisPrompt{
		{ID: identity.NewID(), AnalysisType: prompt.RealEstate, Task: string(prompt.Findings), DefinitionJSON: findingsJSON, CanonicalDigest: findingsDigest, Revision: 1, UpdatedBy: actorID, CreatedAt: now, UpdatedAt: now},
		{ID: identity.NewID(), AnalysisType: prompt.RealEstate, Task: string(prompt.InventoryCompare), DefinitionJSON: compareJSON, CanonicalDigest: compareDigest, Revision: 1, UpdatedBy: actorID, CreatedAt: now, UpdatedAt: now},
	} {
		require.NoError(t, db.Create(&row).Error)
	}
	tenantID := identity.NewID()
	bus := mediator.New()
	require.NoError(t, Setup(Dependencies{DB: db, Bus: bus, Authorizer: auth.Authorizer{}, SuperAdminIssuer: "https://issuer.test", SuperAdminSubject: "admin", Now: func() time.Time { return now.Add(time.Second) }, Within: func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(db) }}))
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: actorID, TenantID: tenantID, Issuer: "https://issuer.test", Subject: "admin", Roles: []string{auth.TenantAdmin}, ProductEntitlements: []string{auth.AdminProduct}}})
	_, err = bus.Send(ctx, Command{TenantID: tenantID, AnalysisType: prompt.RealEstate, Task: string(prompt.InventoryCompare), SystemPrompt: "Atualize a comparação.", ExpectedRevision: 1, ClientMutationID: "update-compare"})
	require.NoError(t, err)
	var persistedFindings, persistedCompare database.AnalysisPrompt
	require.NoError(t, db.Where("task=?", string(prompt.Findings)).First(&persistedFindings).Error)
	require.NoError(t, db.Where("task=?", string(prompt.InventoryCompare)).First(&persistedCompare).Error)
	require.Equal(t, int64(1), persistedFindings.Revision)
	require.Equal(t, findingsDigest, persistedFindings.CanonicalDigest)
	require.Equal(t, int64(2), persistedCompare.Revision)
	require.NotEqual(t, compareDigest, persistedCompare.CanonicalDigest)
}

func TestUpdatePromptCommandRejectsUnknownTaskUT173(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	tenantID, actorID := identity.NewID(), identity.NewID()
	bus := mediator.New()
	require.NoError(t, Setup(Dependencies{DB: db, Bus: bus, Authorizer: auth.Authorizer{}, SuperAdminIssuer: "https://issuer.test", SuperAdminSubject: "admin"}))
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: actorID, TenantID: tenantID, Issuer: "https://issuer.test", Subject: "admin", Roles: []string{auth.TenantAdmin}, ProductEntitlements: []string{auth.AdminProduct}}})
	_, err = bus.Send(ctx, Command{TenantID: tenantID, AnalysisType: prompt.RealEstate, Task: "SUMMARY", SystemPrompt: "prompt", ExpectedRevision: 1, ClientMutationID: "invalid-task"})
	code, field, _ := apperror.Public(err)
	require.Equal(t, apperror.InvalidInput, code)
	require.Equal(t, "task", field)
}
