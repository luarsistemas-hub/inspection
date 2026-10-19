package prepare_video_comparisons

import (
	"context"
	"errors"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

// DBFrameReader loads only frames from a sealed, ready preparation.
type DBFrameReader struct {
	DB     *gorm.DB
	Within func(context.Context, identity.ID, func(*gorm.DB) error) error
}

func (r DBFrameReader) LoadPreparationFrames(ctx context.Context, tenantID, preparationID identity.ID) ([]Frame, error) {
	if r.DB == nil || tenantID == (identity.ID{}) || preparationID == (identity.ID{}) {
		return nil, fmt.Errorf("%w: missing frame reader scope", ErrInvalidPlan)
	}
	var result []Frame
	within := r.Within
	if within == nil {
		within = (tenanttx.Runner{DB: r.DB}).Within
	}
	err := within(ctx, tenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id = ? AND id = ? AND status = ? AND manifest_digest <> ''", tenantID, preparationID, "READY").Take(&job).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: preparation is not ready", ErrInvalidPlan)
			}
			return err
		}
		var media database.MediaObject
		if err := tx.Where("tenant_id = ? AND id = ?", tenantID, job.MediaID).Take(&media).Error; err != nil {
			return err
		}
		var responsibility database.Responsibility
		if err := tx.Where("tenant_id = ? AND id = ?", tenantID, media.ResponsibilityID).Take(&responsibility).Error; err != nil {
			return err
		}
		var inspection database.Inspection
		if err := tx.Where("tenant_id = ? AND id = ?", tenantID, responsibility.InspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		var frames []database.VideoFrame
		if err := tx.Where("tenant_id = ? AND preparation_id = ? AND source_media_id = ? AND sha256 <> '' AND object_key <> ''", tenantID, preparationID, job.MediaID).Order("pts_us, id").Find(&frames).Error; err != nil {
			return err
		}
		frameIDs := make([]identity.ID, len(frames))
		for i := range frames {
			frameIDs[i] = frames[i].ID
		}
		scopes := make(map[identity.ID]database.VideoScopeItem, len(frameIDs))
		if len(frameIDs) > 0 {
			var items []database.VideoScopeItem
			if err := tx.Where("tenant_id = ? AND preparation_id = ? AND frame_id IN ?", tenantID, preparationID, frameIDs).Order("created_at, id").Find(&items).Error; err != nil {
				return err
			}
			for _, item := range items {
				if _, exists := scopes[item.FrameID]; !exists {
					scopes[item.FrameID] = item
				}
			}
		}
		result = make([]Frame, 0, len(frames))
		for _, row := range frames {
			frame := Frame{ID: row.ID, TenantID: tenantID, AssetID: inspection.AssetID, PreparationID: preparationID, Generation: job.Generation, Digest: row.SHA256, PTS: row.PTSMicros, Quality: row.Quality}
			if scope, ok := scopes[row.ID]; ok {
				frame.Environment = scope.EnvironmentCandidate
				frame.Component = scope.ID.String()
				frame.ComponentLabel = scope.ComponentLabel
				frame.View = scope.ViewLabel
				frame.Visibility = scope.Visibility
				frame.IdentityStatus = scope.IdentityStatus
				frame.Visible = scope.Visibility == "VISIBLE"
			}
			result = append(result, frame)
		}
		return nil
	})
	return result, err
}
