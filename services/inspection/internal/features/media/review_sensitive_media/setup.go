// Package review_sensitive_media owns the scoped privacy-review queue and original access.
package review_sensitive_media

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

type Query struct {
	TenantID identity.ID
	First    int
	After    string
}
type OriginalQuery struct{ TenantID, MediaID identity.ID }
type Review struct {
	MediaID, InspectionID                                            identity.ID
	AssetName, ParticipantName, MediaKind, RequirementKey, BlockedAt string
	Submitted, SourceAvailable                                       bool
}
type Connection struct {
	Nodes       []Review
	EndCursor   string
	HasNextPage bool
}
type Original struct {
	MediaID        identity.ID
	MediaKind, URL string
	ExpiresAt      time.Time
}
type Dependencies struct {
	DB         *gorm.DB
	Store      objectstore.Store
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
	Now        func() time.Time
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Store.Client == nil || d.Store.Bucket == "" || d.Bus == nil {
		return fmt.Errorf("slice media/review_sensitive_media: missing dependency")
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if err := d.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		q := raw.(Query)
		if q.TenantID == (identity.ID{}) {
			return nil, apperror.New(apperror.InvalidInput, "tenantId", "tenant is required")
		}
		meta, ok := requestctx.FromContext(ctx)
		if !ok || !meta.Principal.Internal {
			return nil, apperror.New(apperror.Forbidden, "", "permission denied")
		}
		if _, err := d.Authorizer.Authorize(ctx, q.TenantID, []string{auth.TenantAdmin, auth.Manager}, nil, false); err != nil {
			return nil, err
		}
		return list(ctx, d.DB, q, d.Authorizer)
	}); err != nil {
		return err
	}
	return d.Bus.RegisterQuery(OriginalQuery{}, func(ctx context.Context, raw any) (any, error) {
		q := raw.(OriginalQuery)
		if q.TenantID == (identity.ID{}) || q.MediaID == (identity.ID{}) {
			return nil, apperror.New(apperror.InvalidInput, "mediaId", "media id is required")
		}
		meta, ok := requestctx.FromContext(ctx)
		if !ok || !meta.Principal.Internal {
			return nil, apperror.New(apperror.Forbidden, "", "permission denied")
		}
		if _, err := d.Authorizer.Authorize(ctx, q.TenantID, []string{auth.TenantAdmin, auth.Manager}, nil, false); err != nil {
			return nil, err
		}
		return original(ctx, d.DB, d.Store, q, d.Authorizer, d.Now())
	})
}

type row struct {
	MediaID, InspectionID, AssetName, ParticipantName, MediaKind, RequirementKey string
	BlockedAt                                                                    time.Time
	Submitted, SourceAvailable                                                   bool
}

func list(ctx context.Context, db *gorm.DB, q Query, authorizer auth.Authorizer) (Connection, error) {
	limit := q.First
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		return Connection{}, apperror.New(apperror.InvalidInput, "first", "page size cannot exceed 100")
	}
	var cursorTime time.Time
	var cursorID identity.ID
	if q.After != "" {
		raw, err := base64.RawURLEncoding.DecodeString(q.After)
		if err != nil {
			return Connection{}, apperror.New(apperror.InvalidInput, "after", "invalid cursor")
		}
		parts := strings.SplitN(string(raw), "|", 2)
		if len(parts) != 2 {
			return Connection{}, apperror.New(apperror.InvalidInput, "after", "invalid cursor")
		}
		cursorTime, err = time.Parse(time.RFC3339Nano, parts[0])
		if err != nil {
			return Connection{}, apperror.New(apperror.InvalidInput, "after", "invalid cursor")
		}
		cursorID, err = identity.ParseID(parts[1])
		if err != nil {
			return Connection{}, apperror.New(apperror.InvalidInput, "after", "invalid cursor")
		}
	}
	query := `SELECT m.id AS media_id, i.id AS inspection_id, a.name AS asset_name, p.name AS participant_name, m.media_kind, m.requirement_key,
CASE WHEN m.media_kind='VIDEO' THEN vs.updated_at ELSE sr.created_at END AS blocked_at,
(d.status='SUBMITTED') AS submitted,
CASE WHEN m.media_kind='VIDEO' THEN (vs.status='RETAINED' AND (vs.source_expiry_at IS NULL OR vs.source_expiry_at > now())) ELSE EXISTS (SELECT 1 FROM media.derivatives md WHERE md.tenant_id=m.tenant_id AND md.media_id=m.id AND md.kind='DISPLAY') END AS source_available
FROM media.media_objects m JOIN capture.capture_drafts d ON d.tenant_id=m.tenant_id AND d.responsibility_id=m.responsibility_id
JOIN inspections.responsibilities r ON r.tenant_id=m.tenant_id AND r.id=m.responsibility_id JOIN inspections.inspections i ON i.tenant_id=r.tenant_id AND i.id=r.inspection_id
JOIN assets.assets a ON a.tenant_id=i.tenant_id AND a.id=i.asset_id JOIN participants.participants p ON p.tenant_id=i.tenant_id AND p.id=i.participant_id
LEFT JOIN media.video_sources vs ON vs.tenant_id=m.tenant_id AND vs.media_id=m.id
LEFT JOIN LATERAL (SELECT status,created_at FROM media.screening_runs x WHERE x.tenant_id=m.tenant_id AND x.media_id=m.id ORDER BY x.created_at DESC,x.id DESC LIMIT 1) sr ON true
WHERE m.tenant_id=? AND m.status='SCREENED' AND ((m.media_kind='VIDEO' AND vs.screening_status='BLOCKED') OR (m.media_kind='PHOTO' AND sr.status='BLOCKED'))
AND NOT EXISTS (SELECT 1 FROM media.screening_runs decision WHERE decision.tenant_id=m.tenant_id AND decision.media_id=m.id AND decision.status='OVERRIDDEN')`
	args := []any{q.TenantID}
	if q.After != "" {
		query += ` AND (CASE WHEN m.media_kind='VIDEO' THEN vs.updated_at ELSE sr.created_at END,m.id) < (?,?)`
		args = append(args, cursorTime, cursorID)
	}
	query += ` ORDER BY blocked_at DESC,m.id DESC`
	var rows []row
	if err := (tenanttx.Runner{DB: db}).Within(ctx, q.TenantID, func(tx *gorm.DB) error { return tx.Raw(query, args...).Scan(&rows).Error }); err != nil {
		return Connection{}, err
	}
	visible := rows[:0]
	for _, item := range rows {
		id, err := identity.ParseID(item.InspectionID)
		if err != nil {
			continue
		}
		if _, err = authorizer.Authorize(ctx, q.TenantID, []string{auth.TenantAdmin, auth.Manager}, &requestctx.Scope{Kind: "INSPECTION", ID: id}, false); err == nil {
			visible = append(visible, item)
		}
	}
	more := len(visible) > limit
	if more {
		visible = visible[:limit]
	}
	result := Connection{Nodes: make([]Review, 0, len(visible)), HasNextPage: more}
	for _, item := range visible {
		mediaID, _ := identity.ParseID(item.MediaID)
		inspectionID, _ := identity.ParseID(item.InspectionID)
		result.Nodes = append(result.Nodes, Review{MediaID: mediaID, InspectionID: inspectionID, AssetName: item.AssetName, ParticipantName: item.ParticipantName, MediaKind: item.MediaKind, RequirementKey: item.RequirementKey, BlockedAt: item.BlockedAt.UTC().Format(time.RFC3339Nano), Submitted: item.Submitted, SourceAvailable: item.SourceAvailable})
		result.EndCursor = base64.RawURLEncoding.EncodeToString([]byte(item.BlockedAt.UTC().Format(time.RFC3339Nano) + "|" + item.MediaID))
	}
	return result, nil
}

func original(ctx context.Context, db *gorm.DB, store objectstore.Store, q OriginalQuery, authorizer auth.Authorizer, now time.Time) (Original, error) {
	meta, _ := requestctx.FromContext(ctx)
	var media database.MediaObject
	var key string
	kind := "PHOTO"
	err := (tenanttx.Runner{DB: db}).Within(ctx, q.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", q.TenantID, q.MediaID).Take(&media).Error; err != nil {
			return apperror.New(apperror.NotFound, "mediaId", "media not found")
		}
		var inspection database.Inspection
		if err := tx.Table("inspections.inspections i").Select("i.*").Joins("JOIN inspections.responsibilities r ON r.tenant_id=i.tenant_id AND r.inspection_id=i.id").Where("i.tenant_id=? AND r.id=?", q.TenantID, media.ResponsibilityID).Take(&inspection).Error; err != nil {
			return apperror.New(apperror.NotFound, "mediaId", "media not found")
		}
		if _, err := authorizer.Authorize(ctx, q.TenantID, []string{auth.TenantAdmin, auth.Manager}, &requestctx.Scope{Kind: "INSPECTION", ID: inspection.ID}, false); err != nil {
			return apperror.New(apperror.NotFound, "mediaId", "media not found")
		}
		if media.MediaKind == "VIDEO" {
			kind = "VIDEO"
			var source database.VideoSource
			if err := tx.Where("tenant_id=? AND media_id=? AND screening_status='BLOCKED' AND status='RETAINED' AND (source_expiry_at IS NULL OR source_expiry_at > ?)", q.TenantID, q.MediaID, now).Take(&source).Error; err != nil {
				return apperror.New(apperror.SourceUnavailable, "mediaId", "Original indisponível")
			}
			key = media.ObjectKey
		} else {
			var run database.ScreeningRun
			if err := tx.Where("tenant_id=? AND media_id=?", q.TenantID, q.MediaID).Order("created_at DESC,id DESC").Take(&run).Error; err != nil || run.Status != "BLOCKED" {
				return apperror.New(apperror.SourceUnavailable, "mediaId", "Original indisponível")
			}
			var derivative database.MediaDerivative
			if err := tx.Where("tenant_id=? AND media_id=? AND kind='DISPLAY'", q.TenantID, q.MediaID).Take(&derivative).Error; err != nil {
				return apperror.New(apperror.SourceUnavailable, "mediaId", "Original indisponível")
			}
			key = derivative.ObjectKey
		}
		if meta.Principal.IdentityID == (identity.ID{}) {
			return apperror.New(apperror.Unauthenticated, "", "authentication required")
		}
		corr := meta.CorrelationID
		if corr == "" {
			corr = "sensitive-original-" + q.MediaID.String()
		}
		return tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: q.TenantID, ActorID: meta.Principal.IdentityID, Action: "media.sensitive_original_viewed", TargetType: "media", TargetID: q.MediaID.String(), Outcome: "ACCEPTED", CorrelationID: corr, OccurredAt: now.UTC()}).Error
	})
	if err != nil {
		return Original{}, err
	}
	if _, err := store.Client.Head(ctx, store.Bucket, key); err != nil {
		return Original{}, apperror.New(apperror.SourceUnavailable, "mediaId", "Original indisponível")
	}
	url, err := store.PresignGet(ctx, key, 5*time.Minute)
	if err != nil {
		return Original{}, apperror.New(apperror.SourceUnavailable, "mediaId", "Original indisponível")
	}
	return Original{MediaID: q.MediaID, MediaKind: kind, URL: url, ExpiresAt: now.UTC().Add(5 * time.Minute)}, nil
}
