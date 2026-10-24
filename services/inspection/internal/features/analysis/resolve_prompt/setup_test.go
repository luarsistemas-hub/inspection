package resolve_prompt

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/prompt"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestResolveReusesExistingSnapshot(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec("ATTACH DATABASE ':memory:' AS analysis").Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE analysis.prompts (
			id TEXT PRIMARY KEY,
			analysis_type TEXT NOT NULL,
			task TEXT NOT NULL DEFAULT 'FINDINGS',
			definition_json BLOB NOT NULL,
			canonical_digest TEXT NOT NULL,
			revision INTEGER NOT NULL,
			updated_by TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);
		CREATE UNIQUE INDEX analysis.uq_analysis_prompt_task ON prompts (analysis_type,task);
		CREATE TABLE analysis.prompt_snapshots (
			id TEXT PRIMARY KEY,
			analysis_type TEXT NOT NULL,
			task TEXT NOT NULL DEFAULT 'FINDINGS',
			definition_json BLOB NOT NULL,
			canonical_digest TEXT NOT NULL,
			created_at DATETIME NOT NULL
		);
		CREATE UNIQUE INDEX analysis.uq_analysis_prompt_snapshot_task_digest ON prompt_snapshots (analysis_type,task,canonical_digest);
	`).Error)

	definition, digest, err := prompt.Canonicalize(prompt.DefaultDefinition())
	require.NoError(t, err)
	now := time.Now().UTC()
	require.NoError(t, db.Create(&database.AnalysisPrompt{
		ID: identity.NewID(), AnalysisType: prompt.RealEstate, DefinitionJSON: definition,
		CanonicalDigest: digest, Revision: 1, UpdatedBy: identity.NewID(), CreatedAt: now, UpdatedAt: now,
	}).Error)
	existingID := identity.NewID()
	require.NoError(t, db.Create(&database.AnalysisPromptSnapshot{
		ID: existingID, AnalysisType: prompt.RealEstate, DefinitionJSON: definition,
		CanonicalDigest: digest, CreatedAt: now,
	}).Error)

	result, err := Resolve(context.Background(), db, prompt.RealEstate, now)
	require.NoError(t, err)
	require.Equal(t, existingID, result.Snapshot.ID)

	var count int64
	require.NoError(t, db.Model(&database.AnalysisPromptSnapshot{}).Where("analysis_type=? AND canonical_digest=?", prompt.RealEstate, digest).Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestResolveInventoryTask_UT170(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec("ATTACH DATABASE ':memory:' AS analysis").Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE analysis.prompts (id TEXT PRIMARY KEY, analysis_type TEXT NOT NULL, task TEXT NOT NULL, definition_json BLOB NOT NULL, canonical_digest TEXT NOT NULL, revision INTEGER NOT NULL, updated_by TEXT NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL);
		CREATE TABLE analysis.prompt_snapshots (id TEXT PRIMARY KEY, analysis_type TEXT NOT NULL, task TEXT NOT NULL, definition_json BLOB NOT NULL, canonical_digest TEXT NOT NULL, created_at DATETIME NOT NULL);
		CREATE UNIQUE INDEX analysis.uq_prompt_task_digest ON prompt_snapshots (analysis_type,task,canonical_digest);
	`).Error)
	definition, digest, err := prompt.CanonicalizeTask(string(prompt.InventoryCatalog), prompt.DefaultDefinitionForTask(string(prompt.InventoryCatalog)))
	require.NoError(t, err)
	now := time.Now().UTC()
	require.NoError(t, db.Create(&database.AnalysisPrompt{ID: identity.NewID(), AnalysisType: prompt.RealEstate, Task: string(prompt.InventoryCatalog), DefinitionJSON: definition, CanonicalDigest: digest, Revision: 1, UpdatedBy: identity.NewID(), CreatedAt: now, UpdatedAt: now}).Error)

	result, err := ResolveTask(context.Background(), db, prompt.RealEstate, string(prompt.InventoryCatalog), now)
	require.NoError(t, err)
	require.Equal(t, string(prompt.InventoryCatalog), result.Snapshot.Task)
	require.Equal(t, digest, result.Snapshot.CanonicalDigest)
}

func TestResolveUnknownTask_UT173(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	_, err = ResolveTask(context.Background(), db, prompt.RealEstate, "SUMMARY", time.Now())
	code, field, _ := apperror.Public(err)
	require.Equal(t, apperror.InvalidInput, code)
	require.Equal(t, "task", field)
}
