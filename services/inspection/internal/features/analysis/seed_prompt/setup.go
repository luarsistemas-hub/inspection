package seed_prompt

import (
	"context"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/prompt"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Dependencies struct {
	DB  *gorm.DB
	Now func() time.Time
}

// Run inserts REAL_ESTATE once and never overwrites an administrator edit.
func Run(ctx context.Context, d Dependencies) error {
	if d.DB == nil {
		return fmt.Errorf("slice analysis/seed_prompt: missing database")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	definition, digest, err := prompt.Canonicalize(prompt.DefaultDefinition())
	if err != nil {
		return err
	}
	now := d.Now().UTC()
	row := database.AnalysisPrompt{ID: identity.NewID(), AnalysisType: prompt.RealEstate, DefinitionJSON: definition, CanonicalDigest: digest, Revision: 1, UpdatedBy: identity.ID{}, CreatedAt: now, UpdatedAt: now}
	return d.DB.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "analysis_type"}}, DoNothing: true}).Create(&row).Error
}
