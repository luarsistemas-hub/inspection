// Package reference_items resolves only the private, pinned images that the
// current capture responsibility may compare with.
package reference_items

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

const imageTTL = 10 * time.Minute

// Query identifies the already authenticated external responsibility.
type Query struct{ TenantID, ResponsibilityID identity.ID }

// Item is a short-lived display projection, never an original object key.
type Item struct {
	MediaID           identity.ID
	RequirementKey    string
	Description       string
	Availability      string
	ImageURL          string
	ImageURLExpiresAt *time.Time
}

// Dependencies are the storage and persistence capabilities of this slice.
type Dependencies struct {
	Bus    *mediator.Bus
	DB     *gorm.DB
	Store  objectstore.Store
	Now    func() time.Time
	Within func(context.Context, identity.ID, func(*gorm.DB) error) error
}

// Setup registers the authorized reference projection query.
func Setup(d Dependencies) error {
	if d.Bus == nil || d.DB == nil || d.Store.Client == nil || d.Store.Bucket == "" {
		return fmt.Errorf("slice capture/reference_items: missing dependency")
	}
	return d.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		query := raw.(Query)
		return d.load(ctx, query)
	})
}

func (d Dependencies) load(ctx context.Context, query Query) ([]Item, error) {
	if query.TenantID == (identity.ID{}) || query.ResponsibilityID == (identity.ID{}) {
		return nil, apperror.New(apperror.InvalidInput, "responsibility", "capture responsibility is required")
	}
	var draft database.CaptureDraft
	var requirements []capturecore.Requirement
	var pinned struct {
		ComparisonMode string `json:"comparisonMode"`
		Items          []struct {
			MediaID     identity.ID `json:"mediaId"`
			Description string      `json:"description"`
		} `json:"items"`
	}
	media := map[identity.ID]database.MediaObject{}
	displays := map[identity.ID]database.MediaDerivative{}
	within := d.Within
	if within == nil {
		within = (tenanttx.Runner{DB: d.DB}).Within
	}
	err := within(ctx, query.TenantID, func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Where("tenant_id=? AND responsibility_id=?", query.TenantID, query.ResponsibilityID).First(&draft).Error; err != nil || draft.Status != "OPEN" {
			return apperror.New(apperror.InvalidState, "responsibility", "capture is unavailable")
		}
		var accepted int64
		if err := tx.WithContext(ctx).Model(&database.ProcessingAcceptance{}).Where("tenant_id=? AND responsibility_id=? AND photo_processing AND ai_analysis AND gps_use", query.TenantID, query.ResponsibilityID).Count(&accepted).Error; err != nil {
			return err
		}
		if accepted != 1 {
			return apperror.New(apperror.Forbidden, "consent", "processing acceptance is required")
		}
		if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
			return err
		}
		if err := json.Unmarshal(draft.ReferencePayload, &pinned); err != nil {
			return err
		}
		if pinned.ComparisonMode != "FIXED_ORIGIN" || len(pinned.Items) == 0 {
			return nil
		}
		ids := make([]identity.ID, 0, len(pinned.Items))
		for _, item := range pinned.Items {
			if item.MediaID != (identity.ID{}) {
				ids = append(ids, item.MediaID)
			}
		}
		var rows []database.MediaObject
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id IN ? AND status='READY'", query.TenantID, ids).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			media[row.ID] = row
		}
		var derivatives []database.MediaDerivative
		if err := tx.WithContext(ctx).Where("tenant_id=? AND media_id IN ? AND kind='DISPLAY'", query.TenantID, ids).Find(&derivatives).Error; err != nil {
			return err
		}
		for _, row := range derivatives {
			displays[row.MediaID] = row
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if pinned.ComparisonMode != "FIXED_ORIGIN" {
		return []Item{}, nil
	}
	byRequirement := make(map[string]string, len(pinned.Items))
	for _, item := range pinned.Items {
		byRequirement["origin:"+item.MediaID.String()] = item.Description
	}
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	result := make([]Item, 0, len(requirements))
	for _, requirement := range requirements {
		description, owned := byRequirement[requirement.Key]
		if !owned || requirement.ComparisonTarget != "FIXED_ORIGIN" {
			continue
		}
		mediaID, err := identity.ParseID(requirement.Key[len("origin:"):])
		if err != nil {
			continue
		}
		item := Item{MediaID: mediaID, RequirementKey: requirement.Key, Description: description, Availability: "UNAVAILABLE"}
		if source, ok := media[mediaID]; ok && source.TenantID == query.TenantID {
			if display, found := displays[mediaID]; found && display.ObjectKey != "" {
				if url, signErr := d.Store.PresignGet(ctx, display.ObjectKey, imageTTL); signErr == nil {
					expires := now().Add(imageTTL)
					item.Availability, item.ImageURL, item.ImageURLExpiresAt = "AVAILABLE", url, &expires
				}
			}
		}
		result = append(result, item)
	}
	return result, nil
}
