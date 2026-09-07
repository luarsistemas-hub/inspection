package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"inspection/libs/identity"
	segmentresolve "inspection/services/inspection/internal/features/segments/resolve_definition"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"
)

type Service struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
}
type View struct {
	Template database.Template
	Version  database.TemplateVersion
}
type ProfileDocument struct {
	SchemaVersion        int            `json:"schemaVersion"`
	ModelAlias           string         `json:"modelAlias"`
	PromptVersion        string         `json:"promptVersion"`
	OutputSchema         map[string]any `json:"outputSchema"`
	MinimumConfidenceBPS int            `json:"minimumConfidenceBps"`
}

type refs struct{ segment, profile string }

func (r refs) SegmentExists(v string) bool         { return v == r.segment }
func (r refs) AnalysisProfileExists(v string) bool { return v == r.profile }

func (s Service) PublishProfile(ctx context.Context, tenantID identity.ID, key, idempotencyKey string, payload []byte) (database.AnalysisProfileVersion, error) {
	if _, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin}, nil, true); err != nil {
		return database.AnalysisProfileVersion{}, err
	}
	var doc ProfileDocument
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil || doc.SchemaVersion != 1 || doc.ModelAlias == "" || doc.PromptVersion == "" || len(doc.OutputSchema) == 0 || doc.MinimumConfidenceBPS < 0 || doc.MinimumConfidenceBPS > 10000 {
		return database.AnalysisProfileVersion{}, apperror.New(apperror.InvalidInput, "profile", "invalid analysis profile")
	}
	canonical, digest, err := catalog.CanonicalJSON(doc)
	if err != nil {
		return database.AnalysisProfileVersion{}, err
	}
	var out database.AnalysisProfileVersion
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND idempotency_key=?", tenantID, idempotencyKey).First(&out).Error; err == nil {
			return nil
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		var root database.AnalysisProfile
		rootErr := tx.Where("tenant_id=? AND key=?", tenantID, key).First(&root).Error
		var count int64
		if err := tx.Model(&database.AnalysisProfileVersion{}).Where("tenant_id=? AND key=?", tenantID, key).Count(&count).Error; err != nil {
			return err
		}
		meta, _ := requestctx.FromContext(ctx)
		now := time.Now().UTC()
		out = database.AnalysisProfileVersion{ID: identity.NewID(), TenantID: tenantID, Key: key, VersionNumber: int(count) + 1, SchemaVersion: 1, DefinitionJSON: canonical, CanonicalDigest: digest, Status: "PUBLISHED", IdempotencyKey: idempotencyKey, PublishedAt: now, CreatedBy: meta.Principal.IdentityID}
		if rootErr == gorm.ErrRecordNotFound {
			root = database.AnalysisProfile{ID: identity.NewID(), TenantID: tenantID, Key: key, ActiveVersionID: out.ID, Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&root).Error; err != nil {
				return err
			}
		} else if rootErr != nil {
			return rootErr
		} else {
			if err := tx.Model(&root).Updates(map[string]any{"active_version_id": out.ID, "version": root.Version + 1, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		out.ProfileID = root.ID
		return tx.Create(&out).Error
	})
	return out, err
}

func (s Service) Publish(ctx context.Context, tenantID identity.ID, key, name, idempotencyKey string, payload []byte) (View, error) {
	if _, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin}, nil, true); err != nil {
		return View{}, err
	}
	if key == "" || !catalog.ValidName(name) || idempotencyKey == "" {
		return View{}, apperror.New(apperror.InvalidInput, "template", "key, valid name and idempotency key are required")
	}
	var raw struct {
		SegmentVersionID string `json:"segmentVersionId"`
		AnalysisProfile  string `json:"analysisProfile"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return View{}, apperror.New(apperror.InvalidInput, "definition", "invalid template")
	}
	segmentID, err := identity.ParseID(raw.SegmentVersionID)
	if err != nil {
		return View{}, apperror.New(apperror.InvalidInput, "segmentVersionId", "invalid segment version")
	}
	if s.Bus == nil {
		return View{}, fmt.Errorf("template publisher: missing mediator")
	}
	if _, err := s.Bus.Ask(ctx, segmentresolve.Query{TenantID: tenantID, VersionID: segmentID}); err != nil {
		return View{}, err
	}
	var profile database.AnalysisProfileVersion
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND (id::text=? OR key=?) AND status='PUBLISHED'", tenantID, raw.AnalysisProfile, raw.AnalysisProfile).Order("version_number DESC").First(&profile).Error
	})
	if err != nil {
		return View{}, apperror.New(apperror.InvalidInput, "analysisProfile", "unknown analysis profile")
	}
	compiled, err := catalog.Compile(payload, refs{segment: raw.SegmentVersionID, profile: raw.AnalysisProfile})
	if err != nil {
		return View{}, apperror.New(apperror.InvalidInput, "definition", err.Error())
	}
	var out View
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		var existing database.TemplateVersion
		if err := tx.Where("tenant_id=? AND idempotency_key=?", tenantID, idempotencyKey).First(&existing).Error; err == nil {
			out.Version = existing
			return tx.Where("tenant_id=? AND id=?", tenantID, existing.TemplateID).First(&out.Template).Error
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		var template database.Template
		err := tx.Where("tenant_id=? AND key=?", tenantID, key).First(&template).Error
		now := time.Now().UTC()
		if err == gorm.ErrRecordNotFound {
			template = database.Template{ID: identity.NewID(), TenantID: tenantID, Key: key, Name: name, SegmentVersionID: segmentID, Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&template).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&database.TemplateVersion{}).Where("tenant_id=? AND template_id=?", tenantID, template.ID).Count(&count).Error; err != nil {
			return err
		}
		meta, _ := requestctx.FromContext(ctx)
		version := database.TemplateVersion{ID: identity.NewID(), TenantID: tenantID, TemplateID: template.ID, VersionNumber: int(count) + 1, SchemaVersion: 1, DefinitionJSON: compiled.Canonical, CanonicalDigest: compiled.Digest, Status: "PUBLISHED", IdempotencyKey: idempotencyKey, PublishedAt: now, CreatedBy: meta.Principal.IdentityID}
		if err := tx.Create(&version).Error; err != nil {
			return err
		}
		out = View{Template: template, Version: version}
		return nil
	})
	return out, err
}

func (s Service) Activate(ctx context.Context, tenantID, versionID identity.ID, expectedVersion int64) (View, error) {
	if _, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin}, nil, true); err != nil {
		return View{}, err
	}
	var out View
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=? AND status IN ('PUBLISHED','ACTIVE')", tenantID, versionID).First(&out.Version).Error; err != nil {
			return apperror.New(apperror.InvalidState, "versionId", "published template version required")
		}
		if out.Version.Status == "ACTIVE" {
			if err := tx.Where("tenant_id=? AND id=? AND active_version_id=?", tenantID, out.Version.TemplateID, versionID).First(&out.Template).Error; err != nil {
				return apperror.New(apperror.Conflict, "version", "another version is active")
			}
			return nil
		}
		r := tx.Model(&database.Template{}).Where("tenant_id=? AND id=? AND version=?", tenantID, out.Version.TemplateID, expectedVersion).Updates(map[string]any{"active_version_id": versionID, "version": expectedVersion + 1, "updated_at": time.Now().UTC()})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "stale template version")
		}
		if err := tx.Model(&database.TemplateVersion{}).Where("tenant_id=? AND template_id=? AND status='ACTIVE'", tenantID, out.Version.TemplateID).Update("status", "RETIRED").Error; err != nil {
			return err
		}
		if err := tx.Model(&database.TemplateVersion{}).Where("tenant_id=? AND id=?", tenantID, versionID).Update("status", "ACTIVE").Error; err != nil {
			return err
		}
		out.Version.Status = "ACTIVE"
		return tx.Where("tenant_id=? AND id=?", tenantID, out.Version.TemplateID).First(&out.Template).Error
	})
	return out, err
}

func (s Service) GetVersion(ctx context.Context, tenantID, versionID identity.ID) (View, error) {
	if _, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin, auth.Manager, auth.Employee, auth.Viewer}, nil, false); err != nil {
		return View{}, err
	}
	var out View
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", tenantID, versionID).First(&out.Version).Error; err != nil {
			return apperror.New(apperror.NotFound, "versionId", "template version not found")
		}
		return tx.Where("tenant_id=? AND id=?", tenantID, out.Version.TemplateID).First(&out.Template).Error
	})
	return out, err
}

func (s Service) ResolveActive(ctx context.Context, tenantID, templateID identity.ID) (View, error) {
	var out View
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=? AND active_version_id IS NOT NULL", tenantID, templateID).First(&out.Template).Error; err != nil {
			return apperror.New(apperror.InvalidState, "templateId", "active template is required")
		}
		return tx.Where("tenant_id=? AND id=? AND status='ACTIVE'", tenantID, out.Template.ActiveVersionID).First(&out.Version).Error
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
	var rows []database.Template
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		q := tx.Where("tenant_id=?", tenantID)
		if search != "" {
			q = q.Where("name ILIKE ?", "%"+search+"%")
		}
		if after != "" {
			q = q.Where("id > ?", after)
		}
		return q.Order("id").Limit(first + 1).Find(&rows).Error
	})
	if err != nil {
		return nil, "", false, err
	}
	has := len(rows) > first
	if has {
		rows = rows[:first]
	}
	out := make([]View, len(rows))
	for i, row := range rows {
		out[i].Template = row
	}
	end := ""
	if len(rows) > 0 {
		end = rows[len(rows)-1].ID.String()
	}
	return out, end, has, nil
}
