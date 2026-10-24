package update_prompt

import (
	"context"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/prompt"
	access "inspection/services/inspection/internal/features/analysis/prompt_access"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

type Command struct {
	TenantID                   identity.ID
	AnalysisType, SystemPrompt string
	Task                       string
	ExpectedRevision           int64
	ClientMutationID           string
}
type Dependencies struct {
	DB                                  *gorm.DB
	Bus                                 *mediator.Bus
	Authorizer                          auth.Authorizer
	SuperAdminIssuer, SuperAdminSubject string
	Now                                 func() time.Time
	Within                              func(context.Context, identity.ID, func(*gorm.DB) error) error
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice analysis/update_prompt: missing dependency")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		c := raw.(Command)
		principal, err := access.Authorize(ctx, d.Authorizer, c.TenantID, d.SuperAdminIssuer, d.SuperAdminSubject, true)
		if err != nil {
			return nil, err
		}
		if !prompt.IsKnownType(c.AnalysisType) {
			return nil, apperror.New(apperror.InvalidInput, "analysisType", "unknown analysis type")
		}
		if c.Task == "" {
			c.Task = string(prompt.Findings)
		}
		if !prompt.IsKnownTask(c.Task) {
			return nil, apperror.New(apperror.InvalidInput, "task", "unknown analysis task")
		}
		definition := prompt.DefaultDefinitionForTask(c.Task)
		definition.SystemPrompt = strings.TrimSpace(c.SystemPrompt)
		canonical, digest, err := prompt.CanonicalizeTask(c.Task, definition)
		if err != nil {
			return nil, apperror.New(apperror.InvalidInput, "systemPrompt", "invalid system prompt")
		}
		var out database.AnalysisPrompt
		within := d.Within
		if within == nil {
			within = (tenanttx.Runner{DB: d.DB}).Within
		}
		err = within(ctx, c.TenantID, func(tx *gorm.DB) error {
			meta, _ := requestctx.FromContext(ctx)
			var oldDigest string
			var updateErr error
			out, oldDigest, updateErr = updateTaskPrompt(ctx, tx, c.AnalysisType, c.Task, c.ExpectedRevision, canonical, digest, principal.IdentityID, d.Now().UTC())
			if updateErr != nil {
				return updateErr
			}
			event := database.AuditEvent{ID: identity.NewID(), TenantID: c.TenantID, ActorID: meta.Principal.IdentityID, Action: "GLOBAL_ANALYSIS_PROMPT_UPDATED", TargetType: "ANALYSIS_PROMPT", TargetID: c.AnalysisType + ":" + c.Task, Outcome: "SUCCESS", Reason: fmt.Sprintf("digest:%s->%s", oldDigest, digest), CorrelationID: requestctx.IdempotencyKey(ctx, c.ClientMutationID), OccurredAt: d.Now().UTC()}
			return tx.Create(&event).Error
		})
		return out, err
	})
}

func updateTaskPrompt(ctx context.Context, tx *gorm.DB, analysisType, task string, expectedRevision int64, definition []byte, digest string, updatedBy identity.ID, now time.Time) (database.AnalysisPrompt, string, error) {
	var current database.AnalysisPrompt
	if err := tx.WithContext(ctx).Where("analysis_type=? AND task=?", analysisType, task).First(&current).Error; err != nil {
		return database.AnalysisPrompt{}, "", err
	}
	if current.Revision != expectedRevision {
		return database.AnalysisPrompt{}, "", apperror.New(apperror.Conflict, "expectedRevision", "stale analysis prompt revision")
	}
	result := tx.WithContext(ctx).Model(&database.AnalysisPrompt{}).Where("analysis_type=? AND task=? AND revision=?", analysisType, task, expectedRevision).Updates(map[string]any{"definition_json": definition, "canonical_digest": digest, "revision": expectedRevision + 1, "updated_by": updatedBy, "updated_at": now})
	if result.Error != nil {
		return database.AnalysisPrompt{}, "", result.Error
	}
	if result.RowsAffected != 1 {
		return database.AnalysisPrompt{}, "", apperror.New(apperror.Conflict, "expectedRevision", "stale analysis prompt revision")
	}
	return database.AnalysisPrompt{ID: current.ID, AnalysisType: analysisType, Task: task, DefinitionJSON: definition, CanonicalDigest: digest, Revision: expectedRevision + 1, UpdatedBy: updatedBy, CreatedAt: current.CreatedAt, UpdatedAt: now}, current.CanonicalDigest, nil
}
