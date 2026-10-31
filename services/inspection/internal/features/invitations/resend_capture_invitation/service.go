// Package resend_capture_invitation resends a pending capture link by issuing
// a fresh, single-use invitation for the selected email contact.
package resend_capture_invitation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"inspection/libs/identity"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	notificationrequest "inspection/services/inspection/internal/features/notifications/request"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/security"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Service owns resending an inspection's pending capture invitation.
type Service struct {
	DB             *gorm.DB
	Notifications  notificationcore.NotificationService
	CaptureBaseURL string
	Now            func() time.Time
	Within         func(context.Context, identity.ID, func(*gorm.DB) error) error
}

// Result reports the newly queued email delivery without exposing its address.
type Result struct {
	InspectionID, InvitationID identity.ID
	DeliveryID                 identity.ID
	DeliveryStatus             string
}

// Resend issues a new capture link while the inspection is still in planning.
func (s Service) Resend(ctx context.Context, tenantID, inspectionID, actorID identity.ID, idempotencyKey, correlationID string) (Result, error) {
	if s.DB == nil || s.Notifications == nil || strings.TrimSpace(s.CaptureBaseURL) == "" {
		return Result{}, apperror.New(apperror.DependencyUnavailable, "resend", "Não foi possível reenviar o convite agora.")
	}
	if tenantID == (identity.ID{}) || inspectionID == (identity.ID{}) || strings.TrimSpace(idempotencyKey) == "" {
		return Result{}, apperror.New(apperror.InvalidInput, "inspectionId", "Informe uma vistoria válida.")
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	within := s.Within
	if within == nil {
		within = (tenanttx.Runner{DB: s.DB}).Within
	}
	var result Result
	err := within(ctx, tenantID, func(tx *gorm.DB) error {
		var inspection database.Inspection
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, inspectionID).First(&inspection).Error; err != nil {
			return apperror.New(apperror.NotFound, "inspectionId", "Vistoria não encontrada.")
		}
		var existing database.Invitation
		if err := tx.Where("tenant_id=? AND idempotency_key=?", tenantID, idempotencyKey).First(&existing).Error; err == nil {
			var responsibility database.Responsibility
			if err := tx.Where("tenant_id=? AND id=? AND inspection_id=?", tenantID, existing.ResponsibilityID, inspectionID).First(&responsibility).Error; err != nil {
				return apperror.New(apperror.Conflict, "clientMutationId", "Esta solicitação já foi usada para outra vistoria.")
			}
			var delivery database.Delivery
			if err := tx.Where("tenant_id=? AND invitation_id=?", tenantID, existing.ID).Order("created_at DESC, id DESC").First(&delivery).Error; err != nil {
				return err
			}
			result = Result{InspectionID: inspectionID, InvitationID: existing.ID, DeliveryID: delivery.ID, DeliveryStatus: delivery.Status}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if inspection.Status != "PLANNED" && inspection.Status != "INVITED" {
			return apperror.New(apperror.InvalidState, "inspectionId", "A vistoria não está mais em planejamento.")
		}
		now := s.Now().UTC()
		if !now.Before(inspection.DeadlineAt) {
			return apperror.New(apperror.InvalidState, "inspectionId", "O prazo da vistoria venceu. Não é possível reenviar o convite.")
		}
		var responsibility database.Responsibility
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).First(&responsibility).Error; err != nil {
			return apperror.New(apperror.NotFound, "inspectionId", "Responsável pela vistoria não encontrado.")
		}
		if responsibility.Status != "PENDING" {
			return apperror.New(apperror.InvalidState, "inspectionId", "O convite da vistoria não está mais pendente.")
		}
		var contact database.ParticipantContact
		if err := tx.Table("participants.contacts AS c").Select("c.*").Joins("JOIN participants.channel_selections s ON s.contact_id=c.id AND s.tenant_id=c.tenant_id").Where("c.tenant_id=? AND c.participant_id=? AND c.channel='EMAIL' AND c.active", tenantID, responsibility.ParticipantID).Order("c.created_at ASC, c.id ASC").First(&contact).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.InvalidState, "inspectionId", "O responsável pela vistoria não tem um email selecionado para envio.")
			}
			return err
		}
		var participant database.Participant
		if err := tx.Where("tenant_id=? AND id=?", tenantID, responsibility.ParticipantID).First(&participant).Error; err != nil {
			return err
		}
		var previous database.Invitation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=? AND status='ACTIVE'", tenantID, responsibility.ID).Order("created_at DESC, id DESC").First(&previous).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if previous.ID != (identity.ID{}) {
			if err := tx.Model(&database.ExternalSession{}).Where("tenant_id=? AND invitation_id=? AND revoked_at IS NULL", tenantID, previous.ID).Update("revoked_at", now).Error; err != nil {
				return err
			}
			if err := tx.Model(&database.Invitation{}).Where("tenant_id=? AND id=? AND status='ACTIVE'", tenantID, previous.ID).Updates(map[string]any{"status": "REVOKED", "revoked_at": now}).Error; err != nil {
				return err
			}
			var deliveries []database.Delivery
			if err := tx.Where("tenant_id=? AND invitation_id=?", tenantID, previous.ID).Find(&deliveries).Error; err != nil {
				return err
			}
			for _, delivery := range deliveries {
				if err := tx.Model(&database.ChannelAttempt{}).Where("tenant_id=? AND delivery_id=? AND status IN ('QUEUED','PROCESSING')", tenantID, delivery.ID).Updates(map[string]any{"status": "CANCELED", "last_error": "invitation_resent", "lease_expires_at": nil, "updated_at": now}).Error; err != nil {
					return err
				}
			}
		}
		token, err := security.NewScopedToken(tenantID)
		if err != nil {
			return err
		}
		hash := security.HashToken(token)
		encoded, _ := json.Marshal([]map[string]string{{"channel": "EMAIL", "destination": contact.Value}})
		invitation := database.Invitation{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibility.ID, TokenHash: hash[:], DeliveryIntents: encoded, IdempotencyKey: idempotencyKey, Status: "ACTIVE", ExpiresAt: inspection.DeadlineAt, CreatedAt: now}
		if err := tx.Create(&invitation).Error; err != nil {
			return err
		}
		if correlationID == "" {
			correlationID = "capture-invitation-resend:" + inspectionID.String()
		}
		delivery, err := s.Notifications.Send(notificationrequest.InTransaction(ctx, tx), notificationcore.Notification{
			TenantID: tenantID, InspectionID: &inspectionID, InvitationID: &invitation.ID,
			Recipient: notificationcore.Recipient{Destination: contact.Value}, Channel: notificationcore.ChannelEmail,
			Template: notificationcore.TemplateRef{Name: "capture-link", Version: "v1"}, Variables: map[string]string{"recipientName": participant.Name},
			CorrelationID: correlationID, IdempotencyKey: idempotencyKey + ":delivery",
			Execution: &notificationcore.ExecutionPayload{InvitationID: invitation.ID, Token: token, URLVariable: "captureUrl", BaseURL: s.CaptureBaseURL, ExpiresAt: invitation.ExpiresAt.Unix()},
		})
		if err != nil {
			return err
		}
		if inspection.Status == "PLANNED" {
			if err := tx.Model(&inspection).Where("status='PLANNED'").Updates(map[string]any{"status": "INVITED", "version": gorm.Expr("version + 1"), "updated_at": now}).Error; err != nil {
				return err
			}
		}
		if actorID == (identity.ID{}) {
			actorID = responsibility.ID
		}
		if err := tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: tenantID, ActorID: actorID, Action: "CAPTURE_INVITATION_RESENT", TargetType: "INSPECTION", TargetID: inspectionID.String(), Outcome: "SUCCESS", Reason: "capture invitation resent", CorrelationID: correlationID, OccurredAt: now}).Error; err != nil {
			return err
		}
		result = Result{InspectionID: inspectionID, InvitationID: invitation.ID, DeliveryID: delivery.ID, DeliveryStatus: string(delivery.State)}
		return nil
	})
	return result, err
}
