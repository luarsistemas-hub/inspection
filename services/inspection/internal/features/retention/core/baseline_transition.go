package core

import (
	"context"
	"errors"
	"sort"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DefaultSupersededBaselineRetentionDays is used when the tenant has no video policy row.
const DefaultSupersededBaselineRetentionDays = 365

// BaselineTransition recomputes source retention across every baseline version
// that references a source touched by an activation, supersession or invalidation.
type BaselineTransition struct {
	Now func() time.Time
}

// TransitionVersions recomputes retention for every source referenced by the
// given versions. Callers pass both the version that became active and the
// version it replaced (or the one that was invalidated).
func (t BaselineTransition) TransitionVersions(ctx context.Context, tx *gorm.DB, tenantID identity.ID, versionIDs []identity.ID) error {
	mediaIDs, err := VersionSourceMediaIDs(tx, tenantID, versionIDs)
	if err != nil {
		return err
	}
	return t.Transition(ctx, tx, tenantID, mediaIDs)
}

// Transition updates the source-level retention anchor of each media and never
// shortens a previously assigned expiry. Sources are locked in a stable order
// after the caller's origin lock, so concurrent transitions cannot deadlock.
// Sources whose original is already gone (EXPIRED) are left untouched.
func (t BaselineTransition) Transition(_ context.Context, tx *gorm.DB, tenantID identity.ID, mediaIDs []identity.ID) error {
	now := time.Now().UTC()
	if t.Now != nil {
		now = t.Now().UTC()
	}
	for _, mediaID := range uniqueSorted(mediaIDs) {
		var source database.VideoSource
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND media_id=?", tenantID, mediaID).First(&source).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
		}
		if source.Status == "DELETING" {
			return apperror.New(apperror.Conflict, "videoSourceId", "video source deletion is in progress; retry later")
		}
		if source.Status != "RETAINED" {
			continue
		}
		active, err := ActiveBaselineReferences(tx, tenantID, mediaID)
		if err != nil {
			return err
		}
		if active {
			if source.RetentionAnchor == "ACTIVE_BASELINE" {
				continue
			}
			updates := map[string]any{"retention_anchor": "ACTIVE_BASELINE", "source_expiry_at": nil, "updated_at": now}
			if floor := laterOf(source.RetentionFloorAt, source.SourceExpiryAt); floor != nil {
				updates["retention_floor_at"] = *floor
			}
			if err := tx.Model(&source).Updates(updates).Error; err != nil {
				return err
			}
			continue
		}
		releasedAt, err := latestRelease(tx, tenantID, mediaID)
		if err != nil {
			return err
		}
		if releasedAt == nil {
			if source.RetentionAnchor != "ACTIVE_BASELINE" {
				// No baseline ever owned this source: it keeps its own schedule.
				continue
			}
			releasedAt = &now
		}
		days, err := supersededRetentionDays(tx, tenantID)
		if err != nil {
			return err
		}
		expiry := releasedAt.AddDate(0, 0, days)
		if floor := laterOf(source.RetentionFloorAt, source.SourceExpiryAt); floor != nil && floor.After(expiry) {
			expiry = *floor
		}
		if source.RetentionAnchor == "FIXED" && source.SourceExpiryAt != nil && !expiry.After(*source.SourceExpiryAt) {
			continue
		}
		if err := tx.Model(&source).Updates(map[string]any{"retention_anchor": "FIXED", "source_expiry_at": expiry, "updated_at": now}).Error; err != nil {
			return err
		}
	}
	return nil
}

// latestRelease returns when the last baseline that owned the source stopped
// being active (superseded or invalidated).
func latestRelease(tx *gorm.DB, tenantID, mediaID identity.ID) (*time.Time, error) {
	var versions []database.OriginVersion
	if err := tx.Raw(`SELECT v.superseded_at, v.invalidated_at FROM origins.origin_versions v
		WHERE v.tenant_id=@tenant AND v.status IN ('SUPERSEDED','INVALIDATED') AND v.id IN (`+referencingVersionsSQL+`)`, sourceArgs(tenantID, mediaID)).Scan(&versions).Error; err != nil {
		return nil, err
	}
	var released *time.Time
	for _, version := range versions {
		releasedAt := version.SupersededAt
		if releasedAt == nil {
			releasedAt = version.InvalidatedAt
		}
		released = laterOf(released, releasedAt)
	}
	return released, nil
}

func supersededRetentionDays(tx *gorm.DB, tenantID identity.ID) (int, error) {
	var policy database.VideoPolicy
	err := tx.Select("superseded_baseline_retention_days").Where("tenant_id=?", tenantID).Take(&policy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && policy.SupersededBaselineRetentionDays == 0) {
		return DefaultSupersededBaselineRetentionDays, nil
	}
	return policy.SupersededBaselineRetentionDays, err
}

func laterOf(a, b *time.Time) *time.Time {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case b.After(*a):
		return b
	default:
		return a
	}
}

func uniqueSorted(ids []identity.ID) []identity.ID {
	seen := make(map[identity.ID]struct{}, len(ids))
	result := make([]identity.ID, 0, len(ids))
	for _, id := range ids {
		if id == (identity.ID{}) {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}
