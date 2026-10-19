package process_comparison

import (
	"context"
	"encoding/json"
	"errors"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

// ErrVideoPlanPending defers a video condition request whose plan is not
// sealed yet; the consumer keeps it retryable.
var ErrVideoPlanPending = errors.New("video plan is not sealed yet")

// VideoRequestHandler consumes analysis.video_comparison_requested.v1. It
// accepts only a job pinned to a sealed plan item of the same tenant and then
// runs the ordinary condition processor for that job.
func VideoRequestHandler(processJob func(context.Context, *gorm.DB, []byte, identity.ID) error) func(context.Context, *gorm.DB, events.RawEnvelope) error {
	return func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var payload events.VideoComparisonRequestedPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.ComparisonJobID == (identity.ID{}) || payload.VideoPlanID == (identity.ID{}) || payload.VideoPlanItemID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		var job database.ComparisonJob
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND video_plan_id=? AND video_plan_item_id=?", envelope.TenantID, payload.ComparisonJobID, payload.VideoPlanID, payload.VideoPlanItemID).Take(&job).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return messaging.ErrPermanent
			}
			return err
		}
		var plan database.VideoPlan
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND inspection_id=?", envelope.TenantID, payload.VideoPlanID, job.InspectionID).Take(&plan).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return messaging.ErrPermanent
			}
			return err
		}
		switch plan.Status {
		case "SEALED":
		case "BUILDING", "QUEUED":
			// The plan is not authoritative yet; redeliver instead of dropping
			// the request or running it against an unsealed membership.
			return ErrVideoPlanPending
		default:
			return messaging.ErrPermanent
		}
		var item database.VideoPlanItem
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND plan_id=?", envelope.TenantID, payload.VideoPlanItemID, plan.ID).Take(&item).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return messaging.ErrPermanent
			}
			return err
		}
		input, _ := json.Marshal(map[string]any{"jobId": payload.ComparisonJobID})
		return processJob(ctx, tx, input, envelope.TenantID)
	}
}
