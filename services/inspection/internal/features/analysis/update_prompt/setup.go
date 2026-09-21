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
	ExpectedRevision           int64
	ClientMutationID           string
}
type Dependencies struct {
	DB                                  *gorm.DB
	Bus                                 *mediator.Bus
	Authorizer                          auth.Authorizer
	SuperAdminIssuer, SuperAdminSubject string
	Now                                 func() time.Time
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
		definition := prompt.DefaultDefinition()
		definition.SystemPrompt = strings.TrimSpace(c.SystemPrompt)
		canonical, digest, err := prompt.Canonicalize(definition)
		if err != nil {
			return nil, apperror.New(apperror.InvalidInput, "systemPrompt", "invalid system prompt")
		}
		var out database.AnalysisPrompt
		err = (tenanttx.Runner{DB: d.DB}).Within(ctx, c.TenantID, func(tx *gorm.DB) error {
			var current database.AnalysisPrompt
			if err := tx.WithContext(ctx).Where("analysis_type=?", c.AnalysisType).First(&current).Error; err != nil {
				return err
			}
			if current.Revision != c.ExpectedRevision {
				return apperror.New(apperror.Conflict, "expectedRevision", "stale analysis prompt revision")
			}
			meta, _ := requestctx.FromContext(ctx)
			result := tx.WithContext(ctx).Model(&database.AnalysisPrompt{}).Where("analysis_type=? AND revision=?", c.AnalysisType, c.ExpectedRevision).Updates(map[string]any{"definition_json": canonical, "canonical_digest": digest, "revision": c.ExpectedRevision + 1, "updated_by": principal.IdentityID, "updated_at": d.Now().UTC()})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return apperror.New(apperror.Conflict, "expectedRevision", "stale analysis prompt revision")
			}
			out = database.AnalysisPrompt{ID: current.ID, AnalysisType: c.AnalysisType, DefinitionJSON: canonical, CanonicalDigest: digest, Revision: c.ExpectedRevision + 1, UpdatedBy: principal.IdentityID, CreatedAt: current.CreatedAt, UpdatedAt: d.Now().UTC()}
			event := database.AuditEvent{ID: identity.NewID(), TenantID: c.TenantID, ActorID: meta.Principal.IdentityID, Action: "GLOBAL_ANALYSIS_PROMPT_UPDATED", TargetType: "ANALYSIS_PROMPT", TargetID: c.AnalysisType, Outcome: "SUCCESS", Reason: fmt.Sprintf("digest:%s->%s", current.CanonicalDigest, digest), CorrelationID: requestctx.IdempotencyKey(ctx, c.ClientMutationID), OccurredAt: d.Now().UTC()}
			return tx.Create(&event).Error
		})
		return out, err
	})
}
