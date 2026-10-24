// Package expire_video_sources removes expired original video bytes while preserving frame evidence.
package expire_video_sources

import (
	"context"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/observability"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	DefaultBatchSize = 25
	leaseDuration    = 2 * time.Minute
)

type Dependencies struct {
	DB      *gorm.DB
	Store   objectstore.Store
	Now     func() time.Time
	Metrics *observability.Metrics
}

type Executor struct{ deps Dependencies }

func Setup(d Dependencies) (*Executor, error) {
	if d.DB == nil || d.Store.Client == nil || d.Store.Bucket == "" {
		return nil, fmt.Errorf("slice retention/expire_video_sources: missing dependency")
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Executor{deps: d}, nil
}

// RunDue expires at most limit sources and returns the number claimed for cleanup.
func (e *Executor) RunDue(ctx context.Context, tenantID identity.ID, now time.Time, limit int) (int, error) {
	if tenantID == (identity.ID{}) {
		return 0, fmt.Errorf("source expiry requires tenant")
	}
	if now.IsZero() {
		now = e.deps.Now()
	}
	if limit <= 0 || limit > 100 {
		limit = DefaultBatchSize
	}
	e.observeExpirationLag(ctx, tenantID, now)
	var claimed []claim
	err := (tenanttx.Runner{DB: e.deps.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		var sources []database.VideoSource
		query := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("tenant_id=? AND source_expiry_at IS NOT NULL AND source_expiry_at<=? AND (status='RETAINED' OR (status='DELETING' AND (delete_lease_until IS NULL OR delete_lease_until<=?)))", tenantID, now, now).
			Order("source_expiry_at,id").Limit(limit)
		if err := query.Find(&sources).Error; err != nil {
			return err
		}
		for _, source := range sources {
			if source.FirstFrameReadyAt == nil || source.Status == "EXPIRED" {
				continue
			}
			if held, err := retentioncore.EffectiveSourceHeld(tx, tenantID, source.MediaID); err != nil {
				return err
			} else if held {
				continue
			}
			var active int64
			if err := tx.WithContext(ctx).Model(&database.VideoSourceLease{}).Where("tenant_id=? AND source_media_id=? AND lease_until>?", tenantID, source.MediaID, now).Count(&active).Error; err != nil {
				return err
			}
			if active > 0 {
				continue
			}
			var preparing int64
			if err := tx.WithContext(ctx).Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND media_id=? AND status='PROCESSING' AND lease_expires_at>?", tenantID, source.MediaID, now).Count(&preparing).Error; err != nil {
				return err
			}
			if preparing > 0 {
				continue
			}
			token := identity.NewID()
			until := now.Add(leaseDuration)
			if err := tx.WithContext(ctx).Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", tenantID, source.MediaID).Updates(map[string]any{"status": "DELETING", "delete_lease_token": token, "delete_lease_until": until, "delete_error_code": "", "updated_at": now}).Error; err != nil {
				return err
			}
			claimed = append(claimed, claim{tenantID: tenantID, mediaID: source.MediaID, token: token})
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	var firstErr error
	for _, item := range claimed {
		if err := e.deleteClaim(ctx, item, now); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return len(claimed), firstErr
}

// DeleteRequested removes a source on an authorized scoped deletion request,
// using the same fenced storage cleanup and retry state as scheduled expiry.
func (e *Executor) DeleteRequested(ctx context.Context, tenantID, mediaID identity.ID) error {
	if tenantID == (identity.ID{}) || mediaID == (identity.ID{}) {
		return fmt.Errorf("source deletion requires tenant and media")
	}
	now := e.deps.Now().UTC()
	var claimed *claim
	err := (tenanttx.Runner{DB: e.deps.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		var source database.VideoSource
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND media_id=?", tenantID, mediaID).First(&source).Error; err != nil {
			return err
		}
		if source.Status == "EXPIRED" {
			return nil
		}
		if source.Status == "DELETING" && source.DeleteLeaseUntil != nil && source.DeleteLeaseUntil.After(now) {
			return fmt.Errorf("source deletion lease is still active")
		}
		if held, err := retentioncore.EffectiveSourceHeld(tx, tenantID, mediaID); err != nil {
			return err
		} else if held {
			return fmt.Errorf("source deletion blocked by legal hold")
		}
		var leases, preparing int64
		if err := tx.WithContext(ctx).Model(&database.VideoSourceLease{}).Where("tenant_id=? AND source_media_id=? AND lease_until>?", tenantID, mediaID, now).Count(&leases).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND media_id=? AND status='PROCESSING' AND lease_expires_at>?", tenantID, mediaID, now).Count(&preparing).Error; err != nil {
			return err
		}
		if leases > 0 || preparing > 0 {
			return fmt.Errorf("source deletion blocked by active work")
		}
		token := identity.NewID()
		until := now.Add(leaseDuration)
		if err := tx.WithContext(ctx).Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", tenantID, mediaID).Updates(map[string]any{"status": "DELETING", "delete_lease_token": token, "delete_lease_until": until, "delete_error_code": "", "updated_at": now}).Error; err != nil {
			return err
		}
		claimed = &claim{tenantID: tenantID, mediaID: mediaID, token: token}
		return nil
	})
	if err != nil {
		return err
	}
	if claimed == nil {
		return nil
	}
	return e.deleteClaim(ctx, *claimed, now)
}

// PurgeInactiveOriginBanks removes unreferenced frame banks from inactive
// origin versions in bounded batches.
func (e *Executor) PurgeInactiveOriginBanks(ctx context.Context, tenantID identity.ID, limit int) (int, error) {
	if tenantID == (identity.ID{}) {
		return 0, fmt.Errorf("origin frame purge requires tenant")
	}
	if limit <= 0 || limit > 100 {
		limit = DefaultBatchSize
	}
	var preparations []database.VideoPreparationJob
	err := (tenanttx.Runner{DB: e.deps.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where(`tenant_id=? AND id IN (
				SELECT ov.video_preparation_id FROM origins.origin_versions ov
				WHERE ov.tenant_id=? AND ov.media_kind='VIDEO' AND ov.status IN ('INVALIDATED','SUPERSEDED')
				AND NOT EXISTS (SELECT 1 FROM media.video_evidence_references r WHERE r.tenant_id=ov.tenant_id AND r.preparation_id=ov.video_preparation_id)
				AND NOT EXISTS (SELECT 1 FROM retention.legal_holds h WHERE h.tenant_id=ov.tenant_id AND h.origin_version_id=ov.id AND h.active=true)
				AND EXISTS (SELECT 1 FROM media.video_frames f WHERE f.tenant_id=ov.tenant_id AND f.preparation_id=ov.video_preparation_id)
				AND NOT EXISTS (
					SELECT 1 FROM media.video_frames f
					JOIN origins.inventory_item_evidence e ON e.tenant_id=f.tenant_id AND e.frame_id=f.id
					JOIN origins.inventory_items i ON i.tenant_id=e.tenant_id AND i.id=e.item_id
					JOIN origins.origin_versions v ON v.tenant_id=i.tenant_id AND v.id=i.origin_version_id
					WHERE f.tenant_id=ov.tenant_id AND f.preparation_id=ov.video_preparation_id AND v.status IN ('DRAFT','ACTIVE')
				)
			)`, tenantID, tenantID).Order("id").Limit(limit).Find(&preparations).Error
	})
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, preparation := range preparations {
		purged := false
		err := (tenanttx.Runner{DB: e.deps.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
			var locked database.VideoPreparationJob
			if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, preparation.ID).First(&locked).Error; err != nil {
				return err
			}
			var live int64
			if err := tx.Model(&database.VideoEvidenceReference{}).Where("tenant_id=? AND preparation_id=?", tenantID, preparation.ID).Count(&live).Error; err != nil {
				return err
			}
			if live > 0 {
				return nil
			}
			var held int64
			if err := tx.Model(&database.LegalHold{}).Where("tenant_id=? AND active=true AND origin_version_id IN (SELECT id FROM origins.origin_versions WHERE tenant_id=? AND video_preparation_id=?)", tenantID, tenantID, preparation.ID).Count(&held).Error; err != nil {
				return err
			}
			if held > 0 {
				return nil
			}
			var frames []database.VideoFrame
			if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND preparation_id=?", tenantID, preparation.ID).Find(&frames).Error; err != nil {
				return err
			}
			if len(frames) > 0 {
				var referencedFrames int64
				if err := tx.Table("origins.inventory_item_evidence e").
					Joins("JOIN origins.inventory_items i ON i.tenant_id=e.tenant_id AND i.id=e.item_id").
					Joins("JOIN origins.origin_versions v ON v.tenant_id=i.tenant_id AND v.id=i.origin_version_id").
					Where("e.tenant_id=? AND e.frame_id IN ? AND v.status IN ('DRAFT','ACTIVE')", tenantID, frameIDs(frames)).Count(&referencedFrames).Error; err != nil {
					return err
				}
				if referencedFrames > 0 {
					return nil
				}
				if err := tx.Exec(`DELETE FROM origins.inventory_item_evidence e
					USING origins.inventory_items i, origins.origin_versions v
					WHERE e.tenant_id=? AND e.frame_id IN ?
					AND i.tenant_id=e.tenant_id AND i.id=e.item_id
					AND v.tenant_id=i.tenant_id AND v.id=i.origin_version_id
					AND v.status NOT IN ('DRAFT','ACTIVE')`, tenantID, frameIDs(frames)).Error; err != nil {
					return err
				}
			}
			for _, frame := range frames {
				if err := e.deps.Store.Delete(ctx, frame.ObjectKey); err != nil && !errors.Is(err, objectstore.ErrNotFound) {
					return err
				}
			}
			if err := tx.Where("tenant_id=? AND preparation_id=?", tenantID, preparation.ID).Delete(&database.VideoScopeItem{}).Error; err != nil {
				return err
			}
			if err := tx.Where("tenant_id=? AND preparation_id=?", tenantID, preparation.ID).Delete(&database.VideoFrame{}).Error; err != nil {
				return err
			}
			purged = len(frames) > 0
			return nil
		})
		if err != nil {
			return completed, err
		}
		if purged {
			completed++
		}
	}
	return completed, nil
}

func frameIDs(frames []database.VideoFrame) []identity.ID {
	ids := make([]identity.ID, 0, len(frames))
	for _, frame := range frames {
		ids = append(ids, frame.ID)
	}
	return ids
}

type claim struct {
	tenantID, mediaID, token identity.ID
}

func (e *Executor) deleteClaim(ctx context.Context, c claim, now time.Time) error {
	var media database.MediaObject
	var derivatives []database.MediaDerivative
	err := (tenanttx.Runner{DB: e.deps.DB}).Within(ctx, c.tenantID, func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", c.tenantID, c.mediaID).First(&media).Error; err != nil {
			return err
		}
		return tx.WithContext(ctx).Where("tenant_id=? AND media_id=? AND kind='VIDEO_PLAYBACK'", c.tenantID, c.mediaID).Find(&derivatives).Error
	})
	if err != nil {
		return err
	}
	keys := []string{media.ObjectKey}
	for _, derivative := range derivatives {
		keys = append(keys, derivative.ObjectKey)
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		if err := e.deps.Store.Delete(ctx, key); err != nil && !errors.Is(err, objectstore.ErrNotFound) {
			if e.recordFailure(ctx, c, "SOURCE_DELETE_FAILED", now) == nil && e.deps.Metrics != nil {
				e.deps.Metrics.VideoPartialDeletion()
			}
			return err
		}
	}
	return (tenanttx.Runner{DB: e.deps.DB}).Within(ctx, c.tenantID, func(tx *gorm.DB) error {
		result := tx.WithContext(ctx).Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=? AND status='DELETING' AND delete_lease_token=?", c.tenantID, c.mediaID, c.token).
			Updates(map[string]any{"status": "EXPIRED", "original_deleted_at": now, "delete_error_code": "", "delete_lease_token": nil, "delete_lease_until": nil, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("source deletion lease lost")
		}
		return nil
	})
}

func (e *Executor) observeExpirationLag(ctx context.Context, tenantID identity.ID, now time.Time) {
	if e.deps.Metrics == nil {
		return
	}
	var sources []database.VideoSource
	err := (tenanttx.Runner{DB: e.deps.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.WithContext(ctx).Where("tenant_id=? AND source_expiry_at IS NOT NULL AND source_expiry_at<=? AND status IN ('RETAINED','DELETING')", tenantID, now).Find(&sources).Error
	})
	if err != nil {
		return
	}
	var maximum float64
	for _, source := range sources {
		if source.DeleteLeaseUntil != nil && source.DeleteLeaseUntil.After(now) {
			continue
		}
		held := false
		err := (tenanttx.Runner{DB: e.deps.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
			var holdErr error
			held, holdErr = retentioncore.EffectiveSourceHeld(tx, tenantID, source.MediaID)
			if holdErr != nil || held {
				return holdErr
			}
			var leases, preparing int64
			if err := tx.WithContext(ctx).Model(&database.VideoSourceLease{}).Where("tenant_id=? AND source_media_id=? AND lease_until>?", tenantID, source.MediaID, now).Count(&leases).Error; err != nil {
				return err
			}
			if err := tx.WithContext(ctx).Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND media_id=? AND status='PROCESSING' AND lease_expires_at>?", tenantID, source.MediaID, now).Count(&preparing).Error; err != nil {
				return err
			}
			held = leases > 0 || preparing > 0
			return nil
		})
		if err == nil && !held && source.SourceExpiryAt != nil {
			maximum = max(maximum, now.Sub(*source.SourceExpiryAt).Seconds())
		}
	}
	e.deps.Metrics.VideoExpirationLag(maximum)
}

func (e *Executor) recordFailure(ctx context.Context, c claim, code string, now time.Time) error {
	return (tenanttx.Runner{DB: e.deps.DB}).Within(ctx, c.tenantID, func(tx *gorm.DB) error {
		return tx.WithContext(ctx).Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=? AND status='DELETING' AND delete_lease_token=?", c.tenantID, c.mediaID, c.token).
			Updates(map[string]any{"delete_error_code": code, "delete_lease_until": now, "updated_at": now}).Error
	})
}

// ExpiryAt is the frozen source deadline measured in elapsed UTC days.
func ExpiryAt(firstReady time.Time, role string, currentDays, originDays int) (time.Time, bool) {
	if firstReady.IsZero() {
		return time.Time{}, false
	}
	days := currentDays
	if role == "ORIGIN" {
		days = originDays
	}
	if days <= 0 {
		return time.Time{}, false
	}
	return firstReady.UTC().Add(time.Duration(days) * 24 * time.Hour), true
}
