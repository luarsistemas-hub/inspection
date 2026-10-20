// Package expire_video_replacement consumes durable replacement deadlines.
package expire_video_replacement

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/features/recapture/core"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

// Dependencies are the collaborators of the deadline consumer.
type Dependencies struct{ Now func() time.Time }

// Setup constructs the idempotent consumer for video replacement deadlines.
func Setup(d Dependencies) (func(context.Context, *gorm.DB, events.RawEnvelope) error, error) {
	if d.Now == nil {
		return nil, fmt.Errorf("slice recapture/expire_video_replacement: missing clock")
	}
	return func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var payload struct {
			ReplacementID identity.ID `json:"replacementId"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.ReplacementID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		return core.Service{DB: tx, Now: d.Now}.ExpireVideoReplacementTx(tx.WithContext(ctx), envelope.TenantID, payload.ReplacementID)
	}, nil
}
