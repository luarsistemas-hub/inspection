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
	EvidenceID        identity.ID
	SourceMediaID     identity.ID
	TimestampMS       int64
	RequirementKey    string
	Description       string
	Reason            string
	Environment       string
	Component         string
	View              string
	Ambiguous         bool
	Gap               bool
	Availability      string
	ImageURL          string
	ImageURLExpiresAt *time.Time
}

type videoScope struct {
	ScopeItemID          identity.ID `json:"scopeItemId"`
	FrameID              identity.ID `json:"frameId"`
	SourceMediaID        identity.ID `json:"sourceMediaId"`
	PTSMicros            int64       `json:"ptsMicros"`
	EnvironmentCandidate string      `json:"environmentCandidate"`
	ComponentLabel       string      `json:"componentLabel"`
	ViewLabel            string      `json:"viewLabel"`
	Visibility           string      `json:"visibility"`
	Gap                  bool        `json:"gap"`
	Ambiguous            bool        `json:"ambiguous"`
}

type videoRecaptureSelection struct {
	ComponentID     identity.ID   `json:"componentId"`
	RequirementKey  string        `json:"requirementKey"`
	OriginalMediaID identity.ID   `json:"originalMediaId"`
	Reason          string        `json:"reason"`
	OriginFrameIDs  []identity.ID `json:"originFrameIds"`
}

type videoRecaptureProjection struct {
	PlanID     identity.ID               `json:"planId"`
	Generation int                       `json:"generation"`
	Selected   []videoRecaptureSelection `json:"selected"`
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
		MediaKind      string `json:"mediaKind"`
		Items          []struct {
			MediaID     identity.ID `json:"mediaId"`
			Description string      `json:"description"`
		} `json:"items"`
		Scope          []videoScope             `json:"scope"`
		VideoRecapture videoRecaptureProjection `json:"videoRecapture"`
	}
	media := map[identity.ID]database.MediaObject{}
	recaptureFrames := map[identity.ID]database.VideoFrame{}
	recaptureScope := map[identity.ID]database.VideoScopeItem{}
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
		acceptance := tx.WithContext(ctx).Model(&database.ProcessingAcceptance{}).Where("tenant_id=? AND responsibility_id=? AND ai_analysis AND gps_use", query.TenantID, query.ResponsibilityID)
		if draft.MediaKind == "VIDEO" {
			acceptance = acceptance.Where("video_processing")
		} else {
			acceptance = acceptance.Where("photo_processing")
		}
		if err := acceptance.Count(&accepted).Error; err != nil {
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
		if draft.Kind == "RECAPTURE" && draft.MediaKind == "VIDEO" {
			projection := pinned.VideoRecapture
			if projection.PlanID == (identity.ID{}) || len(projection.Selected) == 0 {
				return nil
			}
			var plan database.VideoPlan
			if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND status='SEALED'", query.TenantID, projection.PlanID).Take(&plan).Error; err != nil {
				return err
			}
			for _, selected := range projection.Selected {
				if selected.ComponentID == (identity.ID{}) || selected.OriginalMediaID == (identity.ID{}) || selected.RequirementKey == "" || len(selected.OriginFrameIDs) == 0 {
					continue
				}
				var planItem database.VideoPlanItem
				if err := tx.WithContext(ctx).Where("tenant_id=? AND plan_id=? AND component_id=? AND requirement_key=?", query.TenantID, plan.ID, selected.ComponentID, selected.RequirementKey).Take(&planItem).Error; err != nil {
					continue
				}
				var allowedIDs []identity.ID
				if json.Unmarshal(planItem.OriginFrameIDs, &allowedIDs) != nil {
					continue
				}
				// Active-plan recapture projections use the component's current
				// evidence frames. Keep origin membership for older projections and
				// union both memberships while reading historical generations.
				var currentIDs []identity.ID
				if json.Unmarshal(planItem.CurrentFrameIDs, &currentIDs) != nil {
					continue
				}
				allowedIDs = append(allowedIDs, currentIDs...)
				if len(allowedIDs) == 0 {
					continue
				}
				allowed := make(map[identity.ID]bool, len(allowedIDs))
				for _, id := range allowedIDs {
					allowed[id] = true
				}
				ids := make([]identity.ID, 0, len(selected.OriginFrameIDs))
				for _, id := range selected.OriginFrameIDs {
					if allowed[id] {
						ids = append(ids, id)
					}
				}
				if len(ids) == 0 {
					continue
				}
				var scopeRows []database.VideoScopeItem
				if err := tx.WithContext(ctx).Where("tenant_id=? AND preparation_id=? AND frame_id IN ?", query.TenantID, plan.CurrentPreparationID, ids).Order("created_at, id").Find(&scopeRows).Error; err != nil {
					return err
				}
				var frames []database.VideoFrame
				if err := tx.WithContext(ctx).Where("tenant_id=? AND source_media_id=? AND preparation_id=? AND id IN ? AND sha256<>'' AND object_key<>''", query.TenantID, selected.OriginalMediaID, plan.CurrentPreparationID, ids).Find(&frames).Error; err != nil {
					return err
				}
				frameByID := make(map[identity.ID]database.VideoFrame, len(frames))
				for _, frame := range frames {
					frameByID[frame.ID] = frame
					recaptureFrames[frame.ID] = frame
				}
				for _, scope := range scopeRows {
					frame, ok := frameByID[scope.FrameID]
					if !ok {
						continue
					}
					recaptureScope[frame.ID] = scope
					media[frame.ID] = database.MediaObject{ID: frame.ID, TenantID: frame.TenantID, ObjectKey: frame.ObjectKey, ContentType: "image/jpeg", SHA256: frame.SHA256, SizeBytes: frame.SizeBytes, Status: "READY"}
				}
			}
			return nil
		}
		if pinned.ComparisonMode != "FIXED_ORIGIN" || len(pinned.Items) == 0 {
			if pinned.ComparisonMode != "FIXED_ORIGIN" || pinned.MediaKind != "VIDEO" || len(pinned.Scope) == 0 {
				return nil
			}
			ids := make([]identity.ID, 0, len(pinned.Scope))
			preparationID := identity.ID{}
			for _, item := range pinned.Scope {
				ids = append(ids, item.FrameID)
				if item.SourceMediaID != (identity.ID{}) {
					preparationID = item.SourceMediaID
				}
			}
			var frames []database.VideoFrame
			if err := tx.WithContext(ctx).Where("tenant_id=? AND source_media_id=? AND id IN ? AND sha256<>'' AND object_key<>''", query.TenantID, preparationID, ids).Find(&frames).Error; err != nil {
				return err
			}
			for _, frame := range frames {
				media[frame.ID] = database.MediaObject{ID: frame.ID, TenantID: frame.TenantID, ObjectKey: frame.ObjectKey, ContentType: "image/jpeg", SHA256: frame.SHA256, SizeBytes: frame.SizeBytes, Status: "READY"}
			}
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
	if draft.Kind == "RECAPTURE" && draft.MediaKind == "VIDEO" {
		result := make([]Item, 0)
		for _, selected := range pinned.VideoRecapture.Selected {
			for _, frameID := range selected.OriginFrameIDs {
				frame, found := recaptureFrames[frameID]
				scope, scoped := recaptureScope[frameID]
				if !found || !scoped {
					continue
				}
				item := Item{MediaID: frame.ID, EvidenceID: frame.ID, SourceMediaID: selected.OriginalMediaID, TimestampMS: frame.PTSMicros / 1000, RequirementKey: selected.RequirementKey, Description: scope.EnvironmentCandidate + " · " + scope.ComponentLabel, Reason: selected.Reason, Environment: scope.EnvironmentCandidate, Component: scope.ComponentLabel, View: scope.ViewLabel, Availability: "UNAVAILABLE"}
				if frame.TenantID == query.TenantID {
					item.Availability = "AVAILABLE"
					item.ImageURL = fmt.Sprintf("/media/video-evidence/%s?responsibilityId=%s", frame.ID, query.ResponsibilityID)
				}
				result = append(result, item)
			}
		}
		return result, nil
	}
	if pinned.ComparisonMode != "FIXED_ORIGIN" {
		return []Item{}, nil
	}
	result := make([]Item, 0, len(requirements))
	if pinned.MediaKind == "VIDEO" {
		scopeByID := make(map[identity.ID]videoScope, len(pinned.Scope))
		for _, scope := range pinned.Scope {
			scopeByID[scope.ScopeItemID] = scope
		}
		for _, requirement := range requirements {
			// New drafts use one video:main upload requirement. Older drafts stored
			// one video:<scopeItemID> requirement per component; accept both shapes.
			selectedScopes := make([]videoScope, 0, len(pinned.Scope))
			if requirement.Key == "video:main" && requirement.EvidenceKind == "VIDEO" {
				selectedScopes = append(selectedScopes, pinned.Scope...)
			} else if len(requirement.Key) > len("video:") && requirement.Key[:len("video:")] == "video:" {
				scopeID, _ := identity.ParseID(requirement.Key[len("video:"):])
				if scope, found := scopeByID[scopeID]; found {
					selectedScopes = append(selectedScopes, scope)
				}
			}
			for _, scope := range selectedScopes {
				if scope.FrameID == (identity.ID{}) {
					continue
				}
				frame, ok := media[scope.FrameID]
				item := Item{MediaID: scope.FrameID, EvidenceID: scope.FrameID, SourceMediaID: scope.SourceMediaID, TimestampMS: scope.PTSMicros / 1000, RequirementKey: requirement.Key, Environment: scope.EnvironmentCandidate, Component: scope.ComponentLabel, View: scope.ViewLabel, Ambiguous: scope.Ambiguous, Gap: scope.Gap, Availability: "UNAVAILABLE"}
				if ok && frame.TenantID == query.TenantID {
					item.Availability = "AVAILABLE"
					item.ImageURL = fmt.Sprintf("/media/video-evidence/%s?responsibilityId=%s", item.EvidenceID, query.ResponsibilityID)
				}
				result = append(result, item)
			}
		}
		return result, nil
	}
	byRequirement := make(map[string]string, len(pinned.Items))
	for _, item := range pinned.Items {
		byRequirement["origin:"+item.MediaID.String()] = item.Description
	}
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
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
