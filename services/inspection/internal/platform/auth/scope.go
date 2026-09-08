package auth

import (
	"context"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

// GORMScopeResolver resolves only current canonical relationships. It never
// materializes descendants, so moves and revocations take effect immediately.
type GORMScopeResolver struct{ DB *gorm.DB }

func (r GORMScopeResolver) Resolve(ctx context.Context, tenantID, membershipID identity.ID, target requestctx.Scope) (ScopeDecision, error) {
	if r.DB == nil {
		return ScopeDecision{}, fmt.Errorf("scope resolver: missing database")
	}
	decision := ScopeDecision{Target: target}
	err := (tenanttx.Runner{DB: r.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		var grants []database.ResourceScope
		if err := tx.Where("tenant_id=? AND membership_id=?", tenantID, membershipID).Find(&grants).Error; err != nil {
			return err
		}
		for _, grant := range grants {
			matched, err := currentDescendant(tx, tenantID, grant.Kind, grant.ResourceID, target)
			if err != nil {
				return err
			}
			if matched {
				decision.Allowed = true
				decision.DirectGrant = requestctx.Scope{Kind: grant.Kind, ID: grant.ResourceID}
				return nil
			}
		}
		return nil
	})
	return decision, err
}

func currentDescendant(tx *gorm.DB, tenantID identity.ID, grantKind string, grantID identity.ID, target requestctx.Scope) (bool, error) {
	if grantKind == target.Kind && grantID == target.ID {
		return activeTarget(tx, tenantID, target)
	}
	if grantKind == "BUSINESS_UNIT" {
		switch target.Kind {
		case "ASSET":
			var row database.Asset
			err := tx.Where("tenant_id=? AND id=? AND business_unit_id=?", tenantID, target.ID, grantID).First(&row).Error
			return err == nil && row.Status != "ARCHIVED", normalizeNotFound(err)
		case "PROJECT":
			var row database.Project
			err := tx.Where("tenant_id=? AND id=? AND business_unit_id=?", tenantID, target.ID, grantID).First(&row).Error
			return err == nil && row.Status != "ARCHIVED", normalizeNotFound(err)
		case "INSPECTION":
			var row database.Inspection
			err := tx.Where("tenant_id=? AND id=? AND business_unit_id=?", tenantID, target.ID, grantID).First(&row).Error
			return err == nil && row.Status != "ARCHIVED", normalizeNotFound(err)
		}
	}
	if grantKind == "ASSET" {
		switch target.Kind {
		case "PROJECT":
			var row database.Project
			err := tx.Where("tenant_id=? AND id=? AND asset_id=?", tenantID, target.ID, grantID).First(&row).Error
			return err == nil && row.Status != "ARCHIVED", normalizeNotFound(err)
		case "INSPECTION":
			var row database.Inspection
			err := tx.Where("tenant_id=? AND id=? AND asset_id=?", tenantID, target.ID, grantID).First(&row).Error
			return err == nil && row.Status != "ARCHIVED", normalizeNotFound(err)
		}
	}
	if grantKind == "PROJECT" && target.Kind == "INSPECTION" {
		var row database.Inspection
		err := tx.Where("tenant_id=? AND id=? AND project_id=?", tenantID, target.ID, grantID).First(&row).Error
		return err == nil && row.Status != "ARCHIVED", normalizeNotFound(err)
	}
	return false, nil
}

func activeTarget(tx *gorm.DB, tenantID identity.ID, target requestctx.Scope) (bool, error) {
	var model interface{}
	switch target.Kind {
	case "BUSINESS_UNIT":
		model = &database.BusinessUnit{}
	case "ASSET":
		model = &database.Asset{}
	case "PROJECT":
		model = &database.Project{}
	case "INSPECTION":
		model = &database.Inspection{}
	default:
		return false, nil
	}
	err := tx.Where("tenant_id=? AND id=?", tenantID, target.ID).First(model).Error
	return err == nil, normalizeNotFound(err)
}

func normalizeNotFound(err error) error {
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	return nil
}
