package process_deadline

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/features/recapture/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Dependencies struct{ Now func() time.Time }

// Setup constructs the idempotent consumer for durable recapture deadlines.
func Setup(d Dependencies) (func(context.Context, *gorm.DB, events.RawEnvelope) error, error) {
	if d.Now == nil {
		return nil, fmt.Errorf("slice recapture/process_deadline: missing clock")
	}
	return func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var payload struct {
			RequestID identity.ID `json:"requestId"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.RequestID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		var request database.RecaptureRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", envelope.TenantID, payload.RequestID).First(&request).Error; err != nil {
			return err
		}
		if request.Status == "SUBMITTED" || request.Status == "EXPIRED" || request.Status == "CANCELED" {
			return nil
		}
		service := core.Service{DB: tx, Now: d.Now}
		request, err := service.FinalizeTx(tx, envelope.TenantID, payload.RequestID, false)
		if err != nil {
			return err
		}
		var triage database.TriageCase
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND inspection_id=?", envelope.TenantID, request.InspectionID).First(&triage).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil
			}
			return err
		}
		if triage.Status != "AWAITING_EVIDENCE" {
			return nil
		}
		now := d.Now().UTC()
		if err := tx.Model(&triage).Updates(map[string]any{"status": "IN_REVIEW", "version": triage.Version + 1, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.TriageCaseEvent{ID: identity.NewID(), TenantID: envelope.TenantID, CaseID: triage.ID, ActorID: envelope.TenantID, Kind: "EVIDENCE_EXPIRED", Body: "O prazo do complemento expirou; o caso retornou à revisão sem novas evidências.", CreatedAt: now}).Error
	}, nil
}
