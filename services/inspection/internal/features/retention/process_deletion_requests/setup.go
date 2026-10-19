// Package process_deletion_requests executes tenant-scoped retention requests in bounded batches.
package process_deletion_requests

import (
	"context"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/retention/core"
	expiresources "inspection/services/inspection/internal/features/retention/expire_video_sources"
	purgedata "inspection/services/inspection/internal/features/retention/purge_data"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const DefaultBatchSize = 25

// Dependencies are the external adapters used by the batch executor.
type Dependencies struct {
	DB    *gorm.DB
	Store objectstore.Store
	Now   func() time.Time
}

// Executor processes retained deletion requests.
type Executor struct{ deps Dependencies }

// Setup validates dependencies and returns the request processor.
func Setup(d Dependencies) (*Executor, error) {
	if d.DB == nil || d.Store.Client == nil || d.Store.Bucket == "" {
		return nil, fmt.Errorf("slice retention/process_deletion_requests: missing dependency")
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Executor{deps: d}, nil
}

// RunDue processes pending and previously blocked requests, leaving transient
// failures pending so the next scheduler pass can retry them.
func (e *Executor) RunDue(ctx context.Context, tenantID identity.ID, limit int) (int, error) {
	if tenantID == (identity.ID{}) {
		return 0, fmt.Errorf("deletion request processing requires tenant")
	}
	if limit <= 0 || limit > 100 {
		limit = DefaultBatchSize
	}
	var requests []database.DeletionRequest
	err := (tenanttx.Runner{DB: e.deps.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("tenant_id=? AND status IN ('REQUESTED','BLOCKED')", tenantID).
			Order("requested_at,id").Limit(limit).Find(&requests).Error
	})
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, request := range requests {
		if ctx.Err() != nil {
			return processed, ctx.Err()
		}
		if err := e.process(ctx, tenantID, request); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func (e *Executor) process(ctx context.Context, tenantID identity.ID, request database.DeletionRequest) error {
	blocked, reason, err := e.blockReason(ctx, tenantID, request)
	if err != nil {
		return err
	}
	if blocked {
		return e.setStatus(ctx, tenantID, request.ID, "BLOCKED", reason, nil)
	}
	switch {
	case request.SourceMediaID != nil:
		if err := e.deleteRequestedSource(ctx, tenantID, *request.SourceMediaID); err != nil {
			return err
		}
	case request.OriginVersionID != nil:
		if err := e.invalidateOrigin(ctx, tenantID, *request.OriginVersionID); err != nil {
			return err
		}
	case request.InspectionID != nil:
		if err := e.purgeInspection(ctx, tenantID, *request.InspectionID); err != nil {
			if err == gorm.ErrInvalidData {
				return e.setStatus(ctx, tenantID, request.ID, "BLOCKED", "retention period has not elapsed", nil)
			}
			return err
		}
	default:
		return fmt.Errorf("deletion request %s has no scope", request.ID)
	}
	now := e.deps.Now().UTC()
	return e.setStatus(ctx, tenantID, request.ID, "COMPLETED", "", &now)
}

func (e *Executor) blockReason(ctx context.Context, tenantID identity.ID, request database.DeletionRequest) (bool, string, error) {
	var reason string
	var blocked int64
	err := (tenanttx.Runner{DB: e.deps.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		switch {
		case request.InspectionID != nil:
			if err := tx.Model(&database.LegalHold{}).Where("tenant_id=? AND inspection_id=? AND active=true", tenantID, *request.InspectionID).Count(&blocked).Error; err != nil {
				return err
			}
			reason = "legal hold prevents inspection purge"
		case request.SourceMediaID != nil:
			held, err := core.EffectiveSourceHeld(tx, tenantID, *request.SourceMediaID)
			if err != nil {
				return err
			}
			if held {
				blocked = 1
				reason = "legal hold prevents source deletion"
				break
			}
			now := e.deps.Now().UTC()
			var source database.VideoSource
			if err := tx.WithContext(ctx).Where("tenant_id=? AND media_id=?", tenantID, *request.SourceMediaID).First(&source).Error; err != nil {
				return err
			}
			if source.Status == "DELETING" && source.DeleteLeaseUntil != nil && source.DeleteLeaseUntil.After(now) {
				blocked, reason = 1, "source deletion lease is still active"
				break
			}
			var active int64
			if err := tx.Model(&database.VideoSourceLease{}).Where("tenant_id=? AND source_media_id=? AND lease_until>?", tenantID, *request.SourceMediaID, now).Count(&active).Error; err != nil {
				return err
			}
			if active == 0 {
				if err := tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND media_id=? AND status='PROCESSING' AND lease_expires_at>?", tenantID, *request.SourceMediaID, now).Count(&active).Error; err != nil {
					return err
				}
			}
			if active > 0 {
				blocked, reason = 1, "active source work prevents deletion"
			}
		case request.OriginVersionID != nil:
			if err := tx.Model(&database.LegalHold{}).Where("tenant_id=? AND origin_version_id=? AND active=true", tenantID, *request.OriginVersionID).Count(&blocked).Error; err != nil {
				return err
			}
			reason = "legal hold prevents origin invalidation"
			if blocked == 0 {
				var version database.OriginVersion
				if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, *request.OriginVersionID).First(&version).Error; err != nil {
					return err
				}
				if version.Status == "ACTIVE" {
					reason = "active origin version must be invalidated before deletion"
					blocked = 1
				}
			}
		}
		if blocked > 0 {
			return nil
		}
		if request.InspectionID != nil {
			var mediaIDs []identity.ID
			if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?)", tenantID, tenantID, *request.InspectionID).Pluck("id", &mediaIDs).Error; err != nil {
				return err
			}
			for _, mediaID := range mediaIDs {
				var source database.VideoSource
				if err := tx.WithContext(ctx).Where("tenant_id=? AND media_id=?", tenantID, mediaID).First(&source).Error; err == nil {
					if source.Status == "DELETING" {
						reason, blocked = "source deletion in progress prevents inspection purge", 1
						break
					}
					held, err := core.EffectiveSourceHeld(tx, tenantID, mediaID)
					if err != nil {
						return err
					}
					if held {
						reason, blocked = "source hold prevents inspection purge", 1
						break
					}
				} else if err != gorm.ErrRecordNotFound {
					return err
				}
			}
		}
		return nil
	})
	return blocked > 0, reason, err
}

func (e *Executor) deleteRequestedSource(ctx context.Context, tenantID, mediaID identity.ID) error {
	executor, err := expiresources.Setup(expiresources.Dependencies{DB: e.deps.DB, Store: e.deps.Store, Now: e.deps.Now})
	if err != nil {
		return err
	}
	return executor.DeleteRequested(ctx, tenantID, mediaID)
}

func (e *Executor) invalidateOrigin(ctx context.Context, tenantID, originID identity.ID) error {
	return (tenanttx.Runner{DB: e.deps.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		var version database.OriginVersion
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, originID).First(&version).Error; err != nil {
			return err
		}
		if version.Status == "ACTIVE" {
			return fmt.Errorf("active origin cannot be invalidated by deletion request")
		}
		if err := tx.WithContext(ctx).Model(&database.OriginVersion{}).Where("tenant_id=? AND id=? AND status IN ('INVALIDATED','SUPERSEDED')", tenantID, originID).Update("status", "INVALIDATED").Error; err != nil {
			return err
		}
		return tx.WithContext(ctx).Where("tenant_id=? AND owner_type='ORIGIN_VERSION' AND owner_id=?", tenantID, originID).Delete(&database.VideoEvidenceReference{}).Error
	})
}

func (e *Executor) purgeInspection(ctx context.Context, tenantID, inspectionID identity.ID) error {
	var inspection database.Inspection
	policy := core.DefaultPolicy()
	err := (tenanttx.Runner{DB: e.deps.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, inspectionID).First(&inspection).Error; err != nil {
			return err
		}
		var configured database.RetentionPolicy
		if err := tx.WithContext(ctx).Where("tenant_id=?", tenantID).Order("version DESC").First(&configured).Error; err == nil {
			policy.EvidenceReports = time.Duration(configured.EvidenceDays) * 24 * time.Hour
			policy.Operational = time.Duration(configured.OperationalDays) * 24 * time.Hour
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	clock, err := core.NewClock(core.EvidenceReports, inspection.UpdatedAt, time.Time{}, policy)
	if err != nil {
		return err
	}
	if !clock.Eligible(e.deps.Now().UTC()) {
		return gorm.ErrInvalidData
	}
	_, err = purgedata.PurgeWithStore(ctx, e.deps.DB, e.deps.Store, tenantID, inspectionID, clock)
	return err
}

func (e *Executor) setStatus(ctx context.Context, tenantID, requestID identity.ID, status, reason string, processedAt *time.Time) error {
	return (tenanttx.Runner{DB: e.deps.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		updates := map[string]any{"status": status, "reason": reason}
		if processedAt != nil {
			updates["processed_at"] = processedAt
		}
		return tx.WithContext(ctx).Model(&database.DeletionRequest{}).Where("tenant_id=? AND id=?", tenantID, requestID).Updates(updates).Error
	})
}
