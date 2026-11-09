package resolvers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"inspection/libs/identity"
	inspectionscope "inspection/services/inspection/internal/features/inspections/scope"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

func confirmInspectionScope(ctx context.Context, resolver *Resolver, input graphql1.ConfirmInspectionScopeInput) (*graphql1.InspectionScopePayload, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	inspectionID, err := identity.ParseID(input.InspectionID)
	if err != nil {
		return nil, invalidID("inspectionId")
	}
	override, err := parseScopeOverride(input.ScopeOverride)
	if err != nil {
		return nil, err
	}
	if override == nil {
		return nil, validationError("scopeOverride", "Informe o escopo da vistoria.")
	}
	snapshot, err := resolver.InspectionService.ConfirmScope(ctx, meta.TenantID, inspectionID, *override, input.ExpectedVersion)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(snapshot.Document, &value); err != nil {
		return nil, fmt.Errorf("decode inspection scope: %w", err)
	}
	return &graphql1.InspectionScopePayload{Scope: value, UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}, nil
}

func inspectionEffectiveScope(ctx context.Context, resolver *Resolver, obj *graphql1.Inspection) (*graphql1.InspectionScope, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	inspectionID, err := identity.ParseID(obj.ID)
	if err != nil {
		return nil, invalidID("id")
	}
	var snapshot database.InspectionScopeSnapshot
	err = (tenanttx.Runner{DB: resolver.DB}).Within(ctx, meta.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND inspection_id=?", meta.TenantID, inspectionID).Take(&snapshot).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			undefined, resolveErr := inspectionscope.Resolve(catalog.TemplateDocument{SchemaVersion: catalog.LegacySchemaVersion}, obj.TemplateVersionID, nil)
			if resolveErr != nil {
				return nil, resolveErr
			}
			return mapInspectionScope(undefined, undefined.Version, undefined.Digest), nil
		}
		return nil, err
	}
	var effective inspectionscope.Effective
	if err := json.Unmarshal(snapshot.Document, &effective); err != nil {
		return nil, fmt.Errorf("decode inspection scope: %w", err)
	}
	return mapInspectionScope(effective, snapshot.Version, snapshot.Digest), nil
}

func mapInspectionScope(effective inspectionscope.Effective, version int, digest string) *graphql1.InspectionScope {
	rooms := make([]*graphql1.InspectionScopeRoom, 0, len(effective.Rooms))
	for _, room := range effective.Rooms {
		components := make([]*graphql1.InspectionScopeComponent, 0, len(room.Components))
		for _, component := range room.Components {
			components = append(components, &graphql1.InspectionScopeComponent{Key: component.Key, TypeCode: component.TypeCode, Label: component.Label, Views: append([]string(nil), component.Views...)})
		}
		rooms = append(rooms, &graphql1.InspectionScopeRoom{Key: room.Key, TypeCode: room.TypeCode, Name: room.Name, Components: components})
	}
	records := make([]*graphql1.InspectionScopeHandoverRecord, 0, len(effective.HandoverRecords))
	for _, record := range effective.HandoverRecords {
		records = append(records, &graphql1.InspectionScopeHandoverRecord{Key: record.Key, Kind: record.Kind, Required: record.Required})
	}
	return &graphql1.InspectionScope{Profile: effective.Profile, Rooms: rooms, HandoverRecords: records, Version: version, Digest: digest}
}
