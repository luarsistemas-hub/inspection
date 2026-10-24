package core

import (
	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"time"

	"gorm.io/gorm"
)

func (s Service) audit(tx *gorm.DB, tenant, version identity.ID, targetType string, target identity.ID, action string, before, after any, reason string, actor identity.ID, now time.Time) error {
	var r *string
	if reason != "" {
		r = &reason
	}
	row := database.InventoryDecision{ID: identity.NewID(), TenantID: tenant, OriginVersionID: version, TargetType: targetType, TargetID: target, Action: action, Before: marshalRaw(before), After: marshalRaw(after), Reason: r, ActorMembershipID: &actor, OccurredAt: now, CreatedAt: now}
	return tx.Create(&row).Error
}
