// Package handle_state_changed applies terminal inspection state to dashboard projections.
package handle_state_changed

import (
	"context"
	"encoding/json"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	consumeevents "inspection/services/inspection/internal/features/messaging/consume_events"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Dependencies supplies the clock used for projection timestamps.
type Dependencies struct {
	Now func() time.Time
}

// Setup validates dependencies and returns the inspection state change handler.
func Setup(deps Dependencies) (func(context.Context, *gorm.DB, events.RawEnvelope) error, error) {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var payload struct {
			InspectionID identity.ID `json:"inspectionId"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.InspectionID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		var inspection database.Inspection
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", envelope.TenantID, payload.InspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		if err := consumeevents.HandleLifecycleEvent(ctx, tx.WithContext(ctx), envelope); err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, payload.InspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		if inspection.Status != "CANCELED" && inspection.Status != "INVALIDATED" {
			return nil
		}
		now := deps.Now().UTC()
		if err := tx.WithContext(ctx).Model(&database.DashboardInspection{}).
			Where("tenant_id=? AND inspection_id=? AND invalidated=false", envelope.TenantID, payload.InspectionID).
			Updates(map[string]any{"status": inspection.Status, "invalidated": true, "updated_at": now}).Error; err != nil {
			return err
		}
		var triage database.TriageCase
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND inspection_id=?", envelope.TenantID, payload.InspectionID).Take(&triage).Error; err == gorm.ErrRecordNotFound {
			return nil
		} else if err != nil {
			return err
		}
		if triage.Status == "ARCHIVED" {
			return nil
		}
		if err := tx.WithContext(ctx).Model(&triage).Updates(map[string]any{"status": "ARCHIVED", "assignee_id": nil, "version": triage.Version + 1, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.WithContext(ctx).Create(&database.TriageCaseEvent{ID: identity.NewID(), TenantID: envelope.TenantID, CaseID: triage.ID, ActorID: envelope.TenantID, Kind: "ARCHIVED", Body: "Vistoria cancelada ou invalidada.", CreatedAt: now}).Error
	}, nil
}
