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

// Resource grants describe hierarchy only. New work is blocked when its
// target or any resource ancestor is archived; existing work and historical
// reads remain available within the current scope.

// GORMScopeResolver resolves only current canonical relationships. It never
// materializes descendants, so moves and revocations take effect immediately.
type GORMScopeResolver struct{ DB *gorm.DB }

// ValidateMutationTarget rejects mutations against archived resources while
// leaving historical reads governed only by their grants. Use ResolveNewWork
// when the target also needs a resource grant so both checks share a transaction.
func (r GORMScopeResolver) ValidateMutationTarget(ctx context.Context, tenantID identity.ID, target requestctx.Scope) (bool, error) {
	if r.DB == nil {
		return false, fmt.Errorf("scope resolver: missing database")
	}
	archived := false
	err := (tenanttx.Runner{DB: r.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		var err error
		archived, err = archivedTarget(tx, tenantID, target)
		return err
	})
	return archived, err
}

// ArchivedResource reports whether target, or any ancestor in the canonical
// hierarchy, is archived. A resource that does not exist reports false.
func ArchivedResource(tx *gorm.DB, tenantID identity.ID, target requestctx.Scope) (bool, error) {
	return archivedTarget(tx, tenantID, target)
}

func archivedTarget(tx *gorm.DB, tenantID identity.ID, target requestctx.Scope) (bool, error) {
	var archived bool
	var err error
	switch target.Kind {
	case "BUSINESS_UNIT":
		err = tx.Raw(`SELECT status='ARCHIVED' FROM tenancy.business_units WHERE tenant_id=? AND id=?`, tenantID, target.ID).Scan(&archived).Error
	case "ASSET":
		err = tx.Raw(`SELECT a.status='ARCHIVED' OR bu.status='ARCHIVED' FROM assets.assets a JOIN tenancy.business_units bu ON bu.tenant_id=a.tenant_id AND bu.id=a.business_unit_id WHERE a.tenant_id=? AND a.id=?`, tenantID, target.ID).Scan(&archived).Error
	case "PROJECT":
		err = tx.Raw(`SELECT p.status='ARCHIVED' OR a.status='ARCHIVED' OR bu.status='ARCHIVED' FROM projects.projects p JOIN assets.assets a ON a.tenant_id=p.tenant_id AND a.id=p.asset_id JOIN tenancy.business_units bu ON bu.tenant_id=p.tenant_id AND bu.id=p.business_unit_id WHERE p.tenant_id=? AND p.id=?`, tenantID, target.ID).Scan(&archived).Error
	case "INSPECTION":
		err = tx.Raw(`SELECT i.status='ARCHIVED' OR COALESCE(p.status='ARCHIVED',false) OR a.status='ARCHIVED' OR bu.status='ARCHIVED' FROM inspections.inspections i LEFT JOIN projects.projects p ON p.tenant_id=i.tenant_id AND p.id=i.project_id JOIN assets.assets a ON a.tenant_id=i.tenant_id AND a.id=i.asset_id JOIN tenancy.business_units bu ON bu.tenant_id=i.tenant_id AND bu.id=i.business_unit_id WHERE i.tenant_id=? AND i.id=?`, tenantID, target.ID).Scan(&archived).Error
	default:
		return false, nil
	}
	if err != nil {
		return false, normalizeNotFound(err)
	}
	return archived, nil
}

func (r GORMScopeResolver) Resolve(ctx context.Context, tenantID, membershipID identity.ID, target requestctx.Scope) (ScopeDecision, error) {
	decision, _, err := r.resolve(ctx, tenantID, membershipID, target, false)
	return decision, err
}

// ResolveNewWork checks the archive state and resource grant in one transaction.
func (r GORMScopeResolver) ResolveNewWork(ctx context.Context, tenantID, membershipID identity.ID, target requestctx.Scope) (ScopeDecision, bool, error) {
	return r.resolve(ctx, tenantID, membershipID, target, true)
}

func (r GORMScopeResolver) resolve(ctx context.Context, tenantID, membershipID identity.ID, target requestctx.Scope, newWork bool) (ScopeDecision, bool, error) {
	if r.DB == nil {
		return ScopeDecision{}, false, fmt.Errorf("scope resolver: missing database")
	}
	decision := ScopeDecision{Target: target}
	archived := false
	err := (tenanttx.Runner{DB: r.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if newWork {
			var err error
			archived, err = archivedTarget(tx, tenantID, target)
			if err != nil || archived {
				return err
			}
		}
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
	return decision, archived, err
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
			return err == nil, normalizeNotFound(err)
		case "PROJECT":
			var row database.Project
			err := tx.Where("tenant_id=? AND id=? AND business_unit_id=?", tenantID, target.ID, grantID).First(&row).Error
			return err == nil, normalizeNotFound(err)
		case "INSPECTION":
			var row database.Inspection
			err := tx.Where("tenant_id=? AND id=? AND business_unit_id=?", tenantID, target.ID, grantID).First(&row).Error
			return err == nil, normalizeNotFound(err)
		}
	}
	if grantKind == "ASSET" {
		switch target.Kind {
		case "PROJECT":
			var row database.Project
			err := tx.Where("tenant_id=? AND id=? AND asset_id=?", tenantID, target.ID, grantID).First(&row).Error
			return err == nil, normalizeNotFound(err)
		case "INSPECTION":
			var row database.Inspection
			err := tx.Where("tenant_id=? AND id=? AND asset_id=?", tenantID, target.ID, grantID).First(&row).Error
			return err == nil, normalizeNotFound(err)
		}
	}
	if grantKind == "PROJECT" && target.Kind == "INSPECTION" {
		var row database.Inspection
		err := tx.Where("tenant_id=? AND id=? AND project_id=?", tenantID, target.ID, grantID).First(&row).Error
		return err == nil, normalizeNotFound(err)
	}
	return false, nil
}

func activeTarget(tx *gorm.DB, tenantID identity.ID, target requestctx.Scope) (bool, error) {
	switch target.Kind {
	case "BUSINESS_UNIT":
		var row database.BusinessUnit
		err := tx.Where("tenant_id=? AND id=?", tenantID, target.ID).First(&row).Error
		return err == nil, normalizeNotFound(err)
	case "ASSET":
		var row database.Asset
		err := tx.Where("tenant_id=? AND id=?", tenantID, target.ID).First(&row).Error
		return err == nil, normalizeNotFound(err)
	case "PROJECT":
		var row database.Project
		err := tx.Where("tenant_id=? AND id=?", tenantID, target.ID).First(&row).Error
		return err == nil, normalizeNotFound(err)
	case "INSPECTION":
		var row database.Inspection
		err := tx.Where("tenant_id=? AND id=?", tenantID, target.ID).First(&row).Error
		return err == nil, normalizeNotFound(err)
	default:
		return false, nil
	}
}

func normalizeNotFound(err error) error {
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	return nil
}
