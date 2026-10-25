package core

import (
	"context"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Sweep marks only pending invitations whose immutable deadline has passed.
func (s Service) Sweep(ctx context.Context, tenantID identity.ID, now time.Time, limit int) (int, error) {
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	updated := 0
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var rows []database.CounterpartyInvitation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("tenant_id=? AND response='PENDING' AND deadline_at<=?", tenantID, now).Order("deadline_at,id").Limit(limit).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			if err := tx.Model(&row).Updates(map[string]any{"response": "NO_RESPONSE", "updated_at": now}).Error; err != nil {
				return err
			}
			updated++
		}
		return nil
	})
	return updated, err
}
