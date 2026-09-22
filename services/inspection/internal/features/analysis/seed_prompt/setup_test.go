package seed_prompt

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/prompt"
	"inspection/services/inspection/internal/platform/database"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestReplaceIsExplicitAndIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec("ATTACH DATABASE ':memory:' AS analysis").Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE analysis.prompts (
			id TEXT PRIMARY KEY,
			analysis_type TEXT NOT NULL,
			definition_json BLOB NOT NULL,
			canonical_digest TEXT NOT NULL,
			revision INTEGER NOT NULL,
			updated_by TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);
		CREATE UNIQUE INDEX analysis.idx_analysis_prompts_analysis_type ON prompts (analysis_type);
	`).Error)

	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	require.NoError(t, Run(context.Background(), Dependencies{DB: db, Now: clock}))

	var current database.AnalysisPrompt
	require.NoError(t, db.Where("analysis_type=?", prompt.RealEstate).First(&current).Error)
	originalRevision := current.Revision
	current.DefinitionJSON = []byte(`{"administrator":"edited"}`)
	current.CanonicalDigest = "admin-edit"
	require.NoError(t, db.Save(&current).Error)
	require.NoError(t, Run(context.Background(), Dependencies{DB: db, Now: clock}))
	var preserved database.AnalysisPrompt
	require.NoError(t, db.Where("analysis_type=?", prompt.RealEstate).First(&preserved).Error)
	require.Equal(t, "admin-edit", preserved.CanonicalDigest)

	require.NoError(t, Run(context.Background(), Dependencies{DB: db, Now: clock, Replace: true}))
	var replaced database.AnalysisPrompt
	require.NoError(t, db.Where("analysis_type=?", prompt.RealEstate).First(&replaced).Error)
	require.Equal(t, originalRevision+1, replaced.Revision)
	require.NotEqual(t, "admin-edit", replaced.CanonicalDigest)
	require.NoError(t, Run(context.Background(), Dependencies{DB: db, Now: clock, Replace: true}))
	var idempotent database.AnalysisPrompt
	require.NoError(t, db.Where("analysis_type=?", prompt.RealEstate).First(&idempotent).Error)
	require.Equal(t, replaced.Revision, idempotent.Revision)
}
