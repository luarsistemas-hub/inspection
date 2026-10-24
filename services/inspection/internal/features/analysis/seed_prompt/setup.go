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
	DB      *gorm.DB
	Now     func() time.Time
	Replace bool
	Task    string
}

// Run inserts REAL_ESTATE once and never overwrites an administrator edit.
func Run(ctx context.Context, d Dependencies) error {
	if d.DB == nil {
		return fmt.Errorf("slice analysis/seed_prompt: missing database")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	task := d.Task
	if task == "" {
		task = string(prompt.Findings)
	}
	if !prompt.IsKnownTask(task) {
		return fmt.Errorf("unknown analysis task")
	}
	definition, digest, err := prompt.CanonicalizeTask(task, prompt.DefaultDefinitionForTask(task))
	if err != nil {
		return err
	}
	now := d.Now().UTC()
	row := database.AnalysisPrompt{ID: identity.NewID(), AnalysisType: prompt.RealEstate, Task: task, DefinitionJSON: definition, CanonicalDigest: digest, Revision: 1, UpdatedBy: identity.ID{}, CreatedAt: now, UpdatedAt: now}
	if !d.Replace {
		return d.DB.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "analysis_type"}, {Name: "task"}}, DoNothing: true}).Create(&row).Error
	}
	var current database.AnalysisPrompt
	if err := d.DB.WithContext(ctx).Where("analysis_type=? AND task=?", prompt.RealEstate, task).First(&current).Error; err == gorm.ErrRecordNotFound {
		return d.DB.WithContext(ctx).Create(&row).Error
	} else if err != nil {
		return err
	} else if current.CanonicalDigest == digest {
		return nil
	}
	return d.DB.WithContext(ctx).Model(&database.AnalysisPrompt{}).Where("analysis_type=? AND task=?", prompt.RealEstate, task).Updates(map[string]any{
		"definition_json": definition, "canonical_digest": digest, "revision": current.Revision + 1, "updated_by": identity.ID{}, "updated_at": now,
	}).Error
}
