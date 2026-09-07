package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"
)

type Service struct {
	DB         *gorm.DB
	Authorizer auth.Authorizer
}
type View struct {
	Definition database.SegmentDefinition
	Version    database.SegmentDefinitionVersion
}
type Schema struct {
	Type                 string              `json:"type"`
	Properties           map[string]Property `json:"properties"`
	Required             []string            `json:"required"`
	AdditionalProperties *bool               `json:"additionalProperties,omitempty"`
}
type Property struct {
	Type      string   `json:"type"`
	MaxLength int      `json:"maxLength,omitempty"`
	Enum      []string `json:"enum,omitempty"`
}

func ValidateSchema(payload []byte) ([]byte, string, Schema, error) {
	if len(payload) == 0 || len(payload) > catalog.MaxTemplateBytes {
		return nil, "", Schema{}, fmt.Errorf("schema size out of range")
	}
	var schema Schema
	if err := json.Unmarshal(payload, &schema); err != nil {
		return nil, "", Schema{}, fmt.Errorf("invalid JSON Schema: %w", err)
	}
	if schema.Type != "object" || len(schema.Properties) == 0 {
		return nil, "", Schema{}, fmt.Errorf("schema must describe an object")
	}
	for _, name := range schema.Required {
		if _, ok := schema.Properties[name]; !ok {
			return nil, "", Schema{}, fmt.Errorf("required field %s is unknown", name)
		}
	}
	for name, p := range schema.Properties {
		if !catalog.ValidName(name) {
			return nil, "", Schema{}, fmt.Errorf("field name limit exceeded")
		}
		if p.Type != "string" && p.Type != "number" && p.Type != "integer" && p.Type != "boolean" {
			return nil, "", Schema{}, fmt.Errorf("unsupported field type")
		}
		if p.MaxLength > catalog.MaxTextCodePoints {
			return nil, "", Schema{}, fmt.Errorf("field limit exceeds platform cap")
		}
	}
	canonical, digest, err := catalog.CanonicalJSON(schema)
	return canonical, digest, schema, err
}

func ValidateAttributes(schema Schema, attributes map[string]any) error {
	for _, required := range schema.Required {
		value, ok := attributes[required]
		if !ok || value == nil || value == "" {
			return fmt.Errorf("required attribute %s is missing", required)
		}
	}
	for key, value := range attributes {
		p, ok := schema.Properties[key]
		if !ok {
			return fmt.Errorf("unknown attribute %s", key)
		}
		switch p.Type {
		case "string":
			v, ok := value.(string)
			if !ok {
				return fmt.Errorf("attribute %s must be string", key)
			}
			if p.MaxLength > 0 && len([]rune(v)) > p.MaxLength {
				return fmt.Errorf("attribute %s exceeds limit", key)
			}
		case "number", "integer":
			if _, ok := value.(float64); !ok {
				return fmt.Errorf("attribute %s must be numeric", key)
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("attribute %s must be boolean", key)
			}
		}
	}
	return nil
}

func (s Service) Publish(ctx context.Context, tenantID identity.ID, key, name, idempotencyKey string, schemaJSON, uiJSON []byte) (View, error) {
	if _, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin}, nil, true); err != nil {
		return View{}, err
	}
	key, name = strings.TrimSpace(key), strings.TrimSpace(name)
	if key == "" || !catalog.ValidName(name) || idempotencyKey == "" {
		return View{}, apperror.New(apperror.InvalidInput, "definition", "key, valid name and idempotency key are required")
	}
	canonical, digest, _, err := ValidateSchema(schemaJSON)
	if err != nil {
		return View{}, apperror.New(apperror.InvalidInput, "schema", err.Error())
	}
	if len(uiJSON) == 0 {
		uiJSON = []byte(`{}`)
	}
	var ui any
	if json.Unmarshal(uiJSON, &ui) != nil {
		return View{}, apperror.New(apperror.InvalidInput, "uiSchema", "invalid UI schema")
	}
	uiCanonical, _, _ := catalog.CanonicalJSON(ui)
	var out View
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		var existing database.SegmentDefinitionVersion
		if err := tx.Where("tenant_id=? AND idempotency_key=?", tenantID, idempotencyKey).First(&existing).Error; err == nil {
			out.Version = existing
			return tx.Where("tenant_id=? AND id=?", tenantID, existing.DefinitionID).First(&out.Definition).Error
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		var def database.SegmentDefinition
		err := tx.Where("tenant_id=? AND key=?", tenantID, key).First(&def).Error
		now := time.Now().UTC()
		if err == gorm.ErrRecordNotFound {
			def = database.SegmentDefinition{ID: identity.NewID(), TenantID: tenantID, Key: key, Name: name, Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&def).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&database.SegmentDefinitionVersion{}).Where("tenant_id=? AND definition_id=?", tenantID, def.ID).Count(&count).Error; err != nil {
			return err
		}
		meta, _ := metadataActor(ctx)
		v := database.SegmentDefinitionVersion{ID: identity.NewID(), TenantID: tenantID, DefinitionID: def.ID, VersionNumber: int(count) + 1, SchemaVersion: 1, SchemaJSON: canonical, UISchemaJSON: uiCanonical, CanonicalDigest: digest, Status: "PUBLISHED", IdempotencyKey: idempotencyKey, PublishedAt: now, CreatedBy: meta}
		if err := tx.Create(&v).Error; err != nil {
			return err
		}
		out = View{Definition: def, Version: v}
		return nil
	})
	return out, err
}

func (s Service) Activate(ctx context.Context, tenantID, versionID identity.ID, expectedDefinitionVersion int64) (View, error) {
	if _, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin}, nil, true); err != nil {
		return View{}, err
	}
	var out View
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=? AND status='PUBLISHED'", tenantID, versionID).First(&out.Version).Error; err != nil {
			return apperror.New(apperror.InvalidState, "versionId", "published definition version required")
		}
		var current database.SegmentDefinition
		if err := tx.Where("tenant_id=? AND id=?", tenantID, out.Version.DefinitionID).First(&current).Error; err != nil {
			return err
		}
		if current.ActiveVersionID != nil && *current.ActiveVersionID == versionID {
			out.Definition = current
			return nil
		}
		r := tx.Model(&database.SegmentDefinition{}).Where("tenant_id=? AND id=? AND version=?", tenantID, out.Version.DefinitionID, expectedDefinitionVersion).Updates(map[string]any{"active_version_id": versionID, "version": expectedDefinitionVersion + 1, "updated_at": time.Now().UTC()})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "stale definition version")
		}
		return tx.Where("tenant_id=? AND id=?", tenantID, out.Version.DefinitionID).First(&out.Definition).Error
	})
	return out, err
}

func (s Service) Resolve(ctx context.Context, tenantID, versionID identity.ID) (View, error) {
	var out View
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=? AND status='PUBLISHED'", tenantID, versionID).First(&out.Version).Error; err != nil {
			return apperror.New(apperror.NotFound, "segmentVersionId", "segment version not found")
		}
		return tx.Where("tenant_id=? AND id=?", tenantID, out.Version.DefinitionID).First(&out.Definition).Error
	})
	return out, err
}
func (s Service) List(ctx context.Context, tenantID identity.ID, search string, first int, after string) ([]View, string, bool, error) {
	if _, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin, auth.Manager, auth.Employee, auth.Viewer}, nil, false); err != nil {
		return nil, "", false, err
	}
	if first <= 0 {
		first = 25
	}
	if first > 100 {
		return nil, "", false, apperror.New(apperror.InvalidInput, "first", "page size exceeds 100")
	}
	var defs []database.SegmentDefinition
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		q := tx.Where("tenant_id=?", tenantID)
		if search != "" {
			q = q.Where("name ILIKE ?", "%"+search+"%")
		}
		if after != "" {
			q = q.Where("id > ?", after)
		}
		return q.Order("id").Limit(first + 1).Find(&defs).Error
	})
	if err != nil {
		return nil, "", false, err
	}
	has := len(defs) > first
	if has {
		defs = defs[:first]
	}
	out := make([]View, len(defs))
	for i, d := range defs {
		out[i].Definition = d
	}
	end := ""
	if len(defs) > 0 {
		end = defs[len(defs)-1].ID.String()
	}
	return out, end, has, nil
}

func metadataActor(ctx context.Context) (identity.ID, bool) {
	metadata, ok := requestctx.FromContext(ctx)
	return metadata.Principal.IdentityID, ok
}
