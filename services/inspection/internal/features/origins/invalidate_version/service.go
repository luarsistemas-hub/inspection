package invalidate_version

import (
	"context"
	"errors"
	"time"

	"inspection/libs/identity"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Service invalidates an origin version while preserving retention transitions.
type Service struct {
	DB     *gorm.DB
	Now    func() time.Time
	Within func(context.Context, identity.ID, func(*gorm.DB) error) error
}

// Invalidate marks a version unusable and releases retained video ownership.
func (s Service) Invalidate(ctx context.Context, tenantID, versionID identity.ID) (database.OriginVersion, error) {
	var row database.OriginVersion
	now := s.now()
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, versionID).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return apperror.New(apperror.NotFound, "versionId", "origin version not found")
		} else if err != nil {
			return err
		}
		if row.Status == "INVALIDATED" {
			return nil
		}
		row.Status = "INVALIDATED"
		if row.InvalidatedAt == nil {
			row.InvalidatedAt = &now
		}
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND owner_type='ORIGIN_VERSION' AND owner_id=?", tenantID, row.ID).Delete(&database.VideoEvidenceReference{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.Origin{}).Where("tenant_id=? AND id=? AND active_version_id=?", tenantID, row.OriginID, row.ID).Updates(map[string]any{"active_version_id": nil, "updated_at": now, "version": gorm.Expr("version + 1")}).Error; err != nil {
			return err
		}
		return (retentioncore.BaselineTransition{Now: func() time.Time { return now }}).TransitionVersions(ctx, tx, tenantID, []identity.ID{row.ID})
	})
	return row, err
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s Service) within(ctx context.Context, tenant identity.ID, fn func(*gorm.DB) error) error {
	if s.Within != nil {
		return s.Within(ctx, tenant, fn)
	}
	return (tenanttx.Runner{DB: s.DB}).Within(ctx, tenant, fn)
}
