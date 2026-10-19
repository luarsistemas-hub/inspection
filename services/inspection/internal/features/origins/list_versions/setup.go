package list_versions

import (
	"context"
	"errors"
	"fmt"

	"inspection/libs/identity"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

type Query struct {
	TenantID, AssetID identity.ID
	First             int
	After             string
}
type VersionDetailsQuery struct{ TenantID, VersionID identity.ID }
type Result struct {
	Versions    []database.OriginVersion
	Details     map[identity.ID]Details
	EndCursor   string
	HasNextPage bool
}
type Details struct {
	PreparationStatus  *string
	CaptureSubmitted   bool
	Selectable         bool
	SourceAvailability *string
}
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
	Within     func(context.Context, identity.ID, func(*gorm.DB) error) error
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice origins/list_versions: missing dependency")
	}
	if err := d.Bus.RegisterQuery(VersionDetailsQuery{}, func(ctx context.Context, raw any) (any, error) {
		query := raw.(VersionDetailsQuery)
		within := d.Within
		if within == nil {
			within = func(ctx context.Context, tenantID identity.ID, fn func(*gorm.DB) error) error {
				return (tenanttx.Runner{DB: d.DB}).Within(ctx, tenantID, fn)
			}
		}
		var version database.OriginVersion
		var origin database.Origin
		if err := within(ctx, query.TenantID, func(tx *gorm.DB) error {
			if err := tx.Where("tenant_id=? AND id=?", query.TenantID, query.VersionID).First(&version).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.NotFound, "versionId", "origin version not found")
			} else if err != nil {
				return err
			}
			if err := tx.Where("tenant_id=? AND id=?", query.TenantID, version.OriginID).First(&origin).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.NotFound, "versionId", "origin version not found")
			} else if err != nil {
				return err
			}
			return nil
		}); err != nil {
			return nil, err
		}
		assetRaw, err := d.Bus.Ask(ctx, assetget.Query{TenantID: query.TenantID, AssetID: origin.AssetID})
		if err != nil {
			return nil, err
		}
		asset := assetRaw.(assetcore.View)
		if _, err := d.Authorizer.Authorize(ctx, query.TenantID, []string{auth.TenantAdmin, auth.OrganizationAdmin, auth.Manager, auth.Employee, auth.Viewer}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: asset.Asset.BusinessUnitID}, false); err != nil {
			return nil, err
		}
		details, err := loadDetails(ctx, within, query.TenantID, []database.OriginVersion{version})
		if err != nil {
			return nil, err
		}
		return Result{Versions: []database.OriginVersion{version}, Details: details}, nil
	}); err != nil {
		return err
	}
	return d.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		query := raw.(Query)
		assetRaw, err := d.Bus.Ask(ctx, assetget.Query{TenantID: query.TenantID, AssetID: query.AssetID})
		if err != nil {
			return nil, err
		}
		asset := assetRaw.(assetcore.View)
		if _, err := d.Authorizer.Authorize(ctx, query.TenantID, []string{auth.TenantAdmin, auth.OrganizationAdmin, auth.Manager, auth.Employee, auth.Viewer}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: asset.Asset.BusinessUnitID}, false); err != nil {
			return nil, err
		}
		first := query.First
		if first <= 0 {
			first = 25
		}
		if first > 100 {
			return nil, apperror.New(apperror.InvalidInput, "first", "page size must not exceed 100")
		}
		var rows []database.OriginVersion
		within := d.Within
		if within == nil {
			within = func(ctx context.Context, tenantID identity.ID, fn func(*gorm.DB) error) error {
				return (tenanttx.Runner{DB: d.DB}).Within(ctx, tenantID, fn)
			}
		}
		err = within(ctx, query.TenantID, func(tx *gorm.DB) error {
			statement := tx.Model(&database.OriginVersion{}).Joins("JOIN origins.origins o ON o.id=origins.origin_versions.origin_id AND o.tenant_id=origins.origin_versions.tenant_id").Where("origins.origin_versions.tenant_id=? AND o.asset_id=?", query.TenantID, query.AssetID)
			if query.After != "" {
				cursorID, err := identity.ParseID(query.After)
				if err != nil {
					return apperror.New(apperror.InvalidInput, "after", "invalid origin cursor")
				}
				var cursor database.OriginVersion
				if err := statement.Session(&gorm.Session{}).Where("origins.origin_versions.id=?", cursorID).First(&cursor).Error; err != nil {
					return apperror.New(apperror.InvalidInput, "after", "origin cursor not found")
				}
				if cursor.ActivatedAt == nil {
					statement = statement.Where("origins.origin_versions.activated_at IS NULL AND (origins.origin_versions.created_at,origins.origin_versions.id) < (?,?)", cursor.CreatedAt, cursor.ID)
				} else {
					statement = statement.Where("origins.origin_versions.activated_at IS NULL OR (origins.origin_versions.activated_at,origins.origin_versions.id) < (?,?)", cursor.ActivatedAt, cursor.ID)
				}
			}
			return statement.Order("origins.origin_versions.activated_at DESC NULLS LAST, CASE WHEN origins.origin_versions.activated_at IS NULL THEN origins.origin_versions.created_at END DESC, origins.origin_versions.id DESC").Limit(first + 1).Find(&rows).Error
		})
		if err != nil {
			return nil, err
		}
		result := Result{Versions: rows}
		if len(rows) > first {
			result.HasNextPage = true
			result.Versions = rows[:first]
		}
		if len(result.Versions) > 0 {
			result.EndCursor = result.Versions[len(result.Versions)-1].ID.String()
		}
		result.Details, err = loadDetails(ctx, within, query.TenantID, result.Versions)
		if err != nil {
			return nil, err
		}
		return result, nil
	})
}

func loadDetails(ctx context.Context, within func(context.Context, identity.ID, func(*gorm.DB) error) error, tenantID identity.ID, versions []database.OriginVersion) (map[identity.ID]Details, error) {
	details := make(map[identity.ID]Details, len(versions))
	if len(versions) == 0 {
		return details, nil
	}
	prepByVersion := make(map[identity.ID]identity.ID)
	prepIDs := make([]identity.ID, 0, len(versions))
	responsibilityIDs := make([]identity.ID, 0, len(versions))
	originIDs := make([]identity.ID, 0, len(versions))
	photoIDs := make([]identity.ID, 0, len(versions))
	for _, version := range versions {
		details[version.ID] = Details{}
		responsibilityIDs = append(responsibilityIDs, version.ResponsibilityID)
		originIDs = append(originIDs, version.OriginID)
		if version.MediaKind != "VIDEO" {
			photoIDs = append(photoIDs, version.ID)
			continue
		}
		missing := "MISSING"
		detail := details[version.ID]
		detail.SourceAvailability = &missing
		details[version.ID] = detail
		if version.VideoPreparationID != nil {
			prepByVersion[version.ID] = *version.VideoPreparationID
			prepIDs = append(prepIDs, *version.VideoPreparationID)
		}
	}
	var media []database.MediaObject
	var jobs []database.VideoPreparationJob
	var sources []database.VideoSource
	var scope []database.VideoScopeItem
	var frames []database.VideoFrame
	var photos []database.OriginEvidence
	var drafts []database.CaptureDraft
	var origins []database.Origin
	var err error
	err = within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id IN ?", tenantID, originIDs).Find(&origins).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND responsibility_id IN ? AND media_kind='VIDEO'", tenantID, responsibilityIDs).Find(&media).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND responsibility_id IN ?", tenantID, responsibilityIDs).Find(&drafts).Error; err != nil {
			return err
		}
		if len(prepIDs) > 0 || len(media) > 0 {
			if err := tx.Where("tenant_id=? AND (id IN ? OR media_id IN ?)", tenantID, prepIDs, mediaIDsForVersions(versions, media)).Order("created_at DESC,id DESC").Find(&jobs).Error; err != nil {
				return err
			}
		}
		if len(photoIDs) > 0 {
			if err := tx.Where("tenant_id=? AND origin_version_id IN ?", tenantID, photoIDs).Find(&photos).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	activeByOrigin := make(map[identity.ID]identity.ID, len(origins))
	for _, origin := range origins {
		if origin.ActiveVersionID != nil {
			activeByOrigin[origin.ID] = *origin.ActiveVersionID
		}
	}
	mediaByResponsibility := make(map[identity.ID]database.MediaObject, len(media))
	for _, row := range media {
		mediaByResponsibility[row.ResponsibilityID] = row
	}
	jobByMedia := make(map[identity.ID]database.VideoPreparationJob, len(jobs))
	jobByID := make(map[identity.ID]database.VideoPreparationJob, len(jobs))
	for _, row := range jobs {
		jobByID[row.ID] = row
		if _, exists := jobByMedia[row.MediaID]; !exists {
			jobByMedia[row.MediaID] = row
		}
	}
	photoCount := make(map[identity.ID]int, len(photos))
	for _, row := range photos {
		photoCount[row.OriginVersionID]++
	}
	draftSubmittedByResponsibility := make(map[identity.ID]bool, len(drafts))
	for _, draft := range drafts {
		draftSubmittedByResponsibility[draft.ResponsibilityID] = draft.Status == "SUBMITTED"
	}
	for _, version := range versions {
		item := details[version.ID]
		item.CaptureSubmitted = draftSubmittedByResponsibility[version.ResponsibilityID]
		details[version.ID] = item
		if version.MediaKind != "VIDEO" {
			item.Selectable = version.Status == "ACTIVE" && activeByOrigin[version.OriginID] == version.ID && photoCount[version.ID] > 0 && photoCount[version.ID] <= 6
			details[version.ID] = item
			continue
		}
		mediaRow, hasMedia := mediaByResponsibility[version.ResponsibilityID]
		prepID, hasPrep := prepByVersion[version.ID]
		if !hasPrep && hasMedia {
			prepID = mediaRow.ID
		}
		job, hasJob := jobByID[prepID]
		if !hasPrep && hasMedia {
			job, hasJob = jobByMedia[mediaRow.ID]
			if hasJob {
				prepID = job.ID
			}
		}
		if hasJob {
			phase := job.Phase
			d := details[version.ID]
			d.PreparationStatus = &phase
			details[version.ID] = d
			prepIDs = append(prepIDs, job.ID)
		}
	}
	prepIDs = uniqueIDs(prepIDs)
	if len(prepIDs) > 0 {
		err = within(ctx, tenantID, func(tx *gorm.DB) error {
			if err := tx.Where("tenant_id=? AND media_id IN ?", tenantID, videoSourceMediaIDs(mediaByResponsibility)).Find(&sources).Error; err != nil {
				return err
			}
			if err := tx.Where("tenant_id=? AND preparation_id IN ?", tenantID, prepIDs).Find(&scope).Error; err != nil {
				return err
			}
			return tx.Where("tenant_id=? AND preparation_id IN ? AND sha256<>'' AND object_key<>''", tenantID, prepIDs).Find(&frames).Error
		})
		if err != nil {
			return nil, err
		}
	}
	sourceByMedia := make(map[identity.ID]string, len(sources))
	for _, source := range sources {
		sourceByMedia[source.MediaID] = source.Status
	}
	scopeCounts, frameCounts := map[identity.ID]int{}, map[identity.ID]int{}
	for _, item := range scope {
		scopeCounts[item.PreparationID]++
	}
	frameIDs := make(map[identity.ID]map[identity.ID]struct{}, len(prepIDs))
	for _, frame := range frames {
		job, exists := jobByID[frame.PreparationID]
		if !exists || frame.SourceMediaID != job.MediaID {
			continue
		}
		if frameIDs[frame.PreparationID] == nil {
			frameIDs[frame.PreparationID] = make(map[identity.ID]struct{})
		}
		frameIDs[frame.PreparationID][frame.ID] = struct{}{}
	}
	for _, item := range scope {
		if _, ok := frameIDs[item.PreparationID][item.FrameID]; ok {
			frameCounts[item.PreparationID]++
		}
	}
	for _, version := range versions {
		if version.MediaKind != "VIDEO" {
			continue
		}
		mediaRow, hasMedia := mediaByResponsibility[version.ResponsibilityID]
		prepID := prepByVersion[version.ID]
		if prepID == (identity.ID{}) && hasMedia {
			prepID = mediaRow.ID
		}
		job, hasJob := jobByID[prepID]
		if !hasJob && hasMedia {
			job, hasJob = jobByMedia[mediaRow.ID]
			if hasJob {
				prepID = job.ID
			}
		}
		d := details[version.ID]
		if hasMedia {
			if availability, ok := sourceByMedia[mediaRow.ID]; ok {
				d.SourceAvailability = &availability
			} else {
				missing := "MISSING"
				d.SourceAvailability = &missing
			}
		}
		d.Selectable = version.Status == "ACTIVE" && activeByOrigin[version.OriginID] == version.ID && hasJob && job.Status == "READY" && version.VideoPreparationID != nil && job.ID == *version.VideoPreparationID && version.ScopeManifestDigest != "" && job.ManifestDigest == version.ScopeManifestDigest && scopeCounts[job.ID] > 0 && frameCounts[job.ID] == scopeCounts[job.ID]
		details[version.ID] = d
	}
	return details, nil
}

func mediaIDsForVersions(versions []database.OriginVersion, media []database.MediaObject) []identity.ID {
	ids := make([]identity.ID, 0, len(media))
	for _, row := range media {
		for _, version := range versions {
			if version.ResponsibilityID == row.ResponsibilityID && version.MediaKind == "VIDEO" {
				ids = append(ids, row.ID)
				break
			}
		}
	}
	return uniqueIDs(ids)
}

func videoSourceMediaIDs(byResponsibility map[identity.ID]database.MediaObject) []identity.ID {
	ids := make([]identity.ID, 0, len(byResponsibility))
	for _, row := range byResponsibility {
		ids = append(ids, row.ID)
	}
	return ids
}

func uniqueIDs(ids []identity.ID) []identity.ID {
	seen := make(map[identity.ID]struct{}, len(ids))
	result := make([]identity.ID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			result = append(result, id)
		}
	}
	return result
}
