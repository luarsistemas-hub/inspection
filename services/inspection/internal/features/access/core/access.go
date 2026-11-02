package core

import (
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ReplaceAccess makes the membership's explicit product entitlements and
// resource scopes exactly equal to the requested sets inside the caller's
// tenant transaction. Rows already present are preserved, missing rows are
// created, and rows outside the request are removed, so the final state is
// never broader than the latest authorized decision.
func ReplaceAccess(tx *gorm.DB, tenantID, membershipID identity.ID, products []string, scopes []Scope, now time.Time) error {
	if len(products) == 0 {
		if err := tx.Where("tenant_id = ? AND membership_id = ?", tenantID, membershipID).Delete(&database.ProductEntitlement{}).Error; err != nil {
			return err
		}
	} else if err := tx.Where("tenant_id = ? AND membership_id = ? AND product NOT IN ?", tenantID, membershipID, products).Delete(&database.ProductEntitlement{}).Error; err != nil {
		return err
	}
	for _, product := range products {
		entitlement := database.ProductEntitlement{ID: identity.NewID(), TenantID: tenantID, MembershipID: membershipID, Product: product, CreatedAt: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&entitlement).Error; err != nil {
			return err
		}
	}
	var existing []database.ResourceScope
	if err := tx.Where("tenant_id = ? AND membership_id = ?", tenantID, membershipID).Find(&existing).Error; err != nil {
		return err
	}
	wanted := make(map[Scope]struct{}, len(scopes))
	for _, scope := range scopes {
		wanted[scope] = struct{}{}
	}
	for _, row := range existing {
		key := Scope{Kind: row.Kind, ResourceID: row.ResourceID}
		if _, keep := wanted[key]; keep {
			delete(wanted, key)
			continue
		}
		if err := tx.Where("tenant_id = ? AND id = ?", tenantID, row.ID).Delete(&database.ResourceScope{}).Error; err != nil {
			return err
		}
	}
	for _, scope := range scopes {
		if _, missing := wanted[scope]; !missing {
			continue
		}
		row := database.ResourceScope{ID: identity.NewID(), TenantID: tenantID, MembershipID: membershipID, Kind: scope.Kind, ResourceID: scope.ResourceID, CreatedAt: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

// RecordAudit appends one access-change audit event in the caller's tenant
// transaction. The reason carries only identifiers and counts, never personal
// data.
func RecordAudit(tx *gorm.DB, tenantID, actorID identity.ID, action, targetType, targetID, outcome, reason, correlationID string, at time.Time) error {
	if correlationID == "" {
		correlationID = action + ":" + targetID
	}
	return tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: tenantID, ActorID: actorID, Action: action, TargetType: targetType, TargetID: targetID, Outcome: outcome, Reason: reason, CorrelationID: correlationID, OccurredAt: at}).Error
}
