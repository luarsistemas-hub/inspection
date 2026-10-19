package process_deadline

import (
	"context"
	"encoding/json"
	"errors"
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
		terminalFailure := false
		if request.Status == "RECEIVED" {
			deferExpiry, failed, err := videoReceiptState(ctx, tx, envelope.TenantID, request)
			if err != nil {
				return err
			}
			terminalFailure = failed
			if deferExpiry {
				return rescheduleDeadline(tx, envelope.TenantID, request, d.Now().UTC())
			}
			if terminalFailure {
				if err := tx.Model(&database.RecaptureRequirement{}).Where("tenant_id=? AND request_id=? AND status='OPEN'", envelope.TenantID, request.ID).Update("status", "UNRESOLVED").Error; err != nil {
					return err
				}
			}
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

// videoReceiptState protects a timely VIDEO receipt until preparation and visual coverage reach a terminal state.
func videoReceiptState(ctx context.Context, tx *gorm.DB, tenantID identity.ID, request database.RecaptureRequest) (deferExpiry, terminalFailure bool, err error) {
	var draft database.CaptureDraft
	if err := tx.WithContext(ctx).Where("tenant_id=? AND responsibility_id=? AND kind='RECAPTURE'", tenantID, request.ResponsibilityID).Take(&draft).Error; err != nil {
		return false, false, err
	}
	if draft.MediaKind != "VIDEO" {
		return false, false, nil
	}
	var submissions []database.SubmissionVersion
	if err := tx.WithContext(ctx).Where("tenant_id=? AND draft_id=? AND responsibility_id=?", tenantID, draft.ID, request.ResponsibilityID).Order("version_number DESC").Find(&submissions).Error; err != nil {
		return false, false, err
	}
	for _, submission := range submissions {
		var receipt struct {
			MediaKind string      `json:"mediaKind"`
			MediaID   identity.ID `json:"mediaId"`
		}
		if json.Unmarshal(submission.Payload, &receipt) != nil || receipt.MediaKind != "VIDEO" || receipt.MediaID == (identity.ID{}) || !submission.SubmittedAt.Before(request.DeadlineAt) {
			continue
		}
		var preparation database.VideoPreparationJob
		err := tx.WithContext(ctx).Where("tenant_id=? AND media_id=?", tenantID, receipt.MediaID).Order("generation DESC").Take(&preparation).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return true, false, nil
		}
		if err != nil {
			return false, false, err
		}
		if preparation.Status == "FAILED" {
			return false, true, nil
		}
		if preparation.Status != "READY" {
			return true, false, nil
		}
		var coverage database.VideoCoverageJob
		err = tx.WithContext(ctx).Where("tenant_id=? AND preparation_id=?", tenantID, preparation.ID).Take(&coverage).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return true, false, nil
		}
		if err != nil {
			return false, false, err
		}
		if coverage.Status == "COMPLETED" || coverage.Status == "FAILED" {
			return false, true, nil
		}
		return true, false, nil
	}
	return false, false, nil
}

func rescheduleDeadline(tx *gorm.DB, tenantID identity.ID, request database.RecaptureRequest, now time.Time) error {
	const retryDelay = time.Minute
	next := now.Add(retryDelay).UTC().Truncate(time.Minute)
	if !next.After(now) {
		next = now.Add(retryDelay)
	}
	id := identity.NewDeterministicID("recapture-deadline-retry", request.ID.String()+":"+next.Format(time.RFC3339))
	if err := messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: id, Type: "recapture.deadline_reached.v1", SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: request.ID, CorrelationID: "recapture-deadline-" + request.ID.String(), Payload: map[string]any{"requestId": request.ID, "deadlineAt": request.DeadlineAt}}); err != nil {
		return err
	}
	return tx.Model(&database.OutboxIntent{}).Where("tenant_id=? AND id=?", tenantID, id).Update("next_attempt_at", next).Error
}
