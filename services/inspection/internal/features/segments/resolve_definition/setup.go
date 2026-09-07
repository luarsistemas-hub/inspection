package resolve_definition

import (
	"context"
	"encoding/json"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/segments/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/gorm"
)

type Query struct{ TenantID, VersionID identity.ID }
type ValidateAttributesQuery struct {
	TenantID, VersionID identity.ID
	Attributes          map[string]any
}
type Result struct {
	Definition database.SegmentDefinition
	Version    database.SegmentDefinitionVersion
}
type Dependencies struct {
	DB  *gorm.DB
	Bus *mediator.Bus
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice segments/resolve_definition: missing dependency")
	}
	s := core.Service{DB: d.DB}
	if err := d.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		q := raw.(Query)
		view, err := s.Resolve(ctx, q.TenantID, q.VersionID)
		return Result{Definition: view.Definition, Version: view.Version}, err
	}); err != nil {
		return err
	}
	return d.Bus.RegisterQuery(ValidateAttributesQuery{}, func(ctx context.Context, raw any) (any, error) {
		q := raw.(ValidateAttributesQuery)
		view, err := s.Resolve(ctx, q.TenantID, q.VersionID)
		if err != nil {
			return nil, err
		}
		var schema core.Schema
		if err := json.Unmarshal(view.Version.SchemaJSON, &schema); err != nil {
			return nil, fmt.Errorf("decode segment schema: %w", err)
		}
		if err := core.ValidateAttributes(schema, q.Attributes); err != nil {
			return nil, apperror.New(apperror.InvalidInput, "attributes", err.Error())
		}
		return true, nil
	})
}
