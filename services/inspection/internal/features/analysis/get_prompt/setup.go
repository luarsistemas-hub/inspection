package get_prompt

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/prompt"
	access "inspection/services/inspection/internal/features/analysis/prompt_access"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/gorm"
)

type Query struct {
	TenantID     identity.ID
	AnalysisType string
}
type Dependencies struct {
	DB                                  *gorm.DB
	Bus                                 *mediator.Bus
	Authorizer                          auth.Authorizer
	SuperAdminIssuer, SuperAdminSubject string
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice analysis/get_prompt: missing dependency")
	}
	return d.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		q := raw.(Query)
		if _, err := access.Authorize(ctx, d.Authorizer, q.TenantID, d.SuperAdminIssuer, d.SuperAdminSubject, false); err != nil {
			return nil, err
		}
		if !prompt.IsKnownType(q.AnalysisType) {
			return nil, fmt.Errorf("unknown analysis type")
		}
		var row database.AnalysisPrompt
		if err := d.DB.WithContext(ctx).Where("analysis_type=?", q.AnalysisType).First(&row).Error; err != nil {
			return nil, err
		}
		return row, nil
	})
}
