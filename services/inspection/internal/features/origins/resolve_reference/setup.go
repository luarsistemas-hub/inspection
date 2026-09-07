// Package resolve_reference exposes the active immutable origin snapshot to
// occurrence creation without leaking origin persistence across capabilities.
package resolve_reference

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

type Query struct {
	TenantID, AssetID, TemplateID identity.ID
	VersionID                     *identity.ID
}

type Result struct {
	Version      database.OriginVersion
	Evidence     []Evidence
	Requirements []core.Requirement
}

// Evidence is the public, immutable origin projection used by capture.
type Evidence struct {
	MediaID     identity.ID `json:"mediaId"`
	Category    string      `json:"category"`
	Description string      `json:"description"`
}

type Dependencies struct {
	DB  *gorm.DB
	Bus *mediator.Bus
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice origins/resolve_reference: missing dependency")
	}
	return d.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		query := raw.(Query)
		var result Result
		err := (tenanttx.Runner{DB: d.DB}).Within(ctx, query.TenantID, func(tx *gorm.DB) error {
			var resolveErr error
			result, resolveErr = resolve(tx, query)
			return resolveErr
		})
		return result, err
	})
}

func resolve(tx *gorm.DB, query Query) (Result, error) {
	var result Result
	var origin database.Origin
	if err := tx.Where("tenant_id=? AND asset_id=? AND template_id=?", query.TenantID, query.AssetID, query.TemplateID).First(&origin).Error; err != nil || origin.ActiveVersionID == nil {
		return Result{}, apperror.New(apperror.InvalidState, "referenceVersionId", "active origin is required")
	}
	if query.VersionID != nil && *query.VersionID != *origin.ActiveVersionID {
		return Result{}, apperror.New(apperror.Conflict, "referenceVersionId", "origin reference is no longer active")
	}
	if err := tx.Where("tenant_id=? AND id=? AND status='ACTIVE'", query.TenantID, *origin.ActiveVersionID).First(&result.Version).Error; err != nil {
		return Result{}, apperror.New(apperror.InvalidState, "referenceVersionId", "active origin is unavailable")
	}
	var rows []database.OriginEvidence
	if err := tx.Where("tenant_id=? AND origin_version_id=?", query.TenantID, result.Version.ID).Order("created_at,id").Find(&rows).Error; err != nil {
		return Result{}, err
	}
	if len(rows) == 0 || len(rows) > core.MaxActivePhotos {
		return Result{}, apperror.New(apperror.InvalidState, "referenceVersionId", "origin evidence must fit the capture limit")
	}
	for _, item := range rows {
		result.Evidence = append(result.Evidence, Evidence{MediaID: item.MediaID, Category: item.Category, Description: item.Description})
		result.Requirements = append(result.Requirements, core.Requirement{
			Key:                  "origin:" + item.MediaID.String(),
			Section:              item.Category,
			Label:                item.Category,
			Instructions:         item.Description,
			EvidenceKind:         "PHOTO",
			Required:             true,
			ImpossibilityAllowed: true,
			MinimumMedia:         1,
			MaximumMedia:         1,
			CaptureSourcePolicy:  "CAMERA_DEFAULT",
			ComparisonTarget:     "FIXED_ORIGIN",
		})
	}
	return result, nil
}
