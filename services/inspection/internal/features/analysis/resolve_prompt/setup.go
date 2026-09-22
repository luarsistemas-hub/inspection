// Package resolve_prompt resolves and pins the effective global configuration.
package resolve_prompt

import (
	"context"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/prompt"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Result struct {
	Prompt   database.AnalysisPrompt
	Snapshot database.AnalysisPromptSnapshot
}

// Resolve materializes (or reuses) the immutable snapshot for an effective prompt.
// The caller supplies its existing transaction so inspection creation and snapshot
// pinning are atomic.
func Resolve(ctx context.Context, tx *gorm.DB, analysisType string, now time.Time) (Result, error) {
	if tx == nil || !prompt.IsKnownType(analysisType) {
		return Result{}, apperror.New(apperror.InvalidInput, "analysisType", "unknown analysis type")
	}
	var current database.AnalysisPrompt
	if err := tx.WithContext(ctx).Where("analysis_type=?", analysisType).First(&current).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return Result{}, apperror.New(apperror.InvalidState, "analysisType", "analysis prompt configuration is required")
		}
		return Result{}, err
	}
	if _, err := prompt.Parse(current.DefinitionJSON); err != nil {
		return Result{}, fmt.Errorf("analysis prompt configuration is invalid: %w", err)
	}
	candidate := database.AnalysisPromptSnapshot{ID: identity.NewID(), AnalysisType: analysisType, DefinitionJSON: current.DefinitionJSON, CanonicalDigest: current.CanonicalDigest, CreatedAt: now.UTC()}
	if err := tx.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "analysis_type"}, {Name: "canonical_digest"}}, DoNothing: true}).Create(&candidate).Error; err != nil {
		return Result{}, err
	}
	// Always load a fresh value. PostgreSQL does not hydrate an ignored INSERT,
	// and reusing candidate would make GORM add its unused generated ID to the
	// lookup predicate.
	var snapshot database.AnalysisPromptSnapshot
	if err := tx.WithContext(ctx).Where("analysis_type=? AND canonical_digest=?", analysisType, current.CanonicalDigest).First(&snapshot).Error; err != nil {
		return Result{}, err
	}
	return Result{Prompt: current, Snapshot: snapshot}, nil
}
