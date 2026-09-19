package correct_responsible_email

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"inspection/libs/identity"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	notificationrequest "inspection/services/inspection/internal/features/notifications/request"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/observability"
	"inspection/services/inspection/internal/platform/security"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Service owns the atomic replacement of a pending capture invitation.
// Public onboarding and authenticated administration call this capability;
// neither boundary composes another use case.
type Service struct {
	DB             *gorm.DB
	Notifications  notificationcore.NotificationService
	CaptureBaseURL string
	Metrics        *observability.Metrics
	Now            func() time.Time
}

type Input struct {
	TenantID, InspectionID, ResponsibilityID, ActorID               identity.ID
	Email, EmailConfirmation, IdempotencyKey, Source, CorrelationID string
	ExpectedResponsibilityVersion                                   int64
}

type Result struct {
	InspectionID, ResponsibilityID, InvitationID identity.ID
	DeliveryID                                   *identity.ID
	Recipient                                    string
	DeliveryStatus, ResponsibilityStatus         string
	ResponsibilityVersion                        int64
}

func (s Service) Correct(ctx context.Context, in Input) (Result, error) {
	if s.DB == nil || s.Notifications == nil || strings.TrimSpace(s.CaptureBaseURL) == "" {
		return Result{}, apperror.New(apperror.DependencyUnavailable, "correction", "correction unavailable")
	}
	if in.TenantID == (identity.ID{}) || in.InspectionID == (identity.ID{}) || in.ResponsibilityID == (identity.ID{}) {
		return Result{}, apperror.New(apperror.InvalidInput, "inspectionId", "inspection is required")
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" || strings.TrimSpace(in.Source) == "" {
		return Result{}, apperror.New(apperror.InvalidInput, "clientMutationId", "client mutation id is required")
	}
	if in.ExpectedResponsibilityVersion <= 0 {
		return Result{}, apperror.New(apperror.InvalidInput, "expectedResponsibilityVersion", "responsibility version is required")
	}
	email, err := participantcore.Normalize("EMAIL", in.Email)
	if err != nil {
		return Result{}, apperror.New(apperror.InvalidInput, "email", "invalid email")
	}
	confirmation, err := participantcore.Normalize("EMAIL", in.EmailConfirmation)
	if err != nil || confirmation != email {
		return Result{}, apperror.New(apperror.InvalidInput, "emailConfirmation", "email confirmation does not match")
	}
	if s.Now == nil {
		s.Now = time.Now
	}

	var result Result
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		var existing database.Invitation
		if err := tx.Where("tenant_id=? AND idempotency_key=?", in.TenantID, in.IdempotencyKey).First(&existing).Error; err == nil {
			if err := s.loadResult(tx, in.TenantID, in.InspectionID, existing, &result); err != nil {
				return err
			}
			s.Metrics.ResponsibleEmailCorrection(in.Source)
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		var responsibility database.Responsibility
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=? AND inspection_id=?", in.TenantID, in.ResponsibilityID, in.InspectionID).First(&responsibility).Error; err != nil {
			return apperror.New(apperror.NotFound, "inspectionId", "inspection responsibility not found")
		}
		if responsibility.Status != "PENDING" {
			return apperror.New(apperror.InvalidState, "responsibleEmail", "responsible email can no longer be changed")
		}
		if responsibility.Version != in.ExpectedResponsibilityVersion {
			return apperror.New(apperror.Conflict, "expectedResponsibilityVersion", "stale responsibility version")
		}

		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", in.TenantID, in.InspectionID).First(&inspection).Error; err != nil {
			return apperror.New(apperror.NotFound, "inspectionId", "inspection not found")
		}
		now := s.Now().UTC()
		if !now.Before(inspection.DeadlineAt) {
			return apperror.New(apperror.InvalidState, "inspectionId", "inspection deadline has passed")
		}
		var participant database.Participant
		if err := tx.Where("tenant_id=? AND id=?", in.TenantID, responsibility.ParticipantID).First(&participant).Error; err != nil {
			return err
		}
		var asset database.Asset
		if err := tx.Where("tenant_id=? AND id=?", in.TenantID, inspection.AssetID).First(&asset).Error; err != nil {
			return err
		}
		var old database.Invitation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=? AND status='ACTIVE'", in.TenantID, in.ResponsibilityID).Order("created_at desc").First(&old).Error; err != nil {
			return apperror.New(apperror.InvalidState, "responsibleEmail", "active invitation not found")
		}

		var contact database.ParticipantContact
		if err := tx.Where("tenant_id=? AND participant_id=? AND channel='EMAIL' AND normalized=?", in.TenantID, participant.ID, email).First(&contact).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			contact = database.ParticipantContact{ID: identity.NewID(), TenantID: in.TenantID, ParticipantID: participant.ID, Channel: "EMAIL", Value: email, Normalized: email, Active: true, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&contact).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if !contact.Active {
			if err := tx.Model(&contact).Updates(map[string]any{"active": true, "value": email, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		var verified int64
		if err := tx.Model(&database.ContactVerification{}).Where("tenant_id=? AND contact_id=? AND status='VERIFIED'", in.TenantID, contact.ID).Count(&verified).Error; err != nil {
			return err
		}
		if verified == 0 {
			verification := database.ContactVerification{ID: identity.NewID(), TenantID: in.TenantID, ContactID: contact.ID, IdempotencyKey: in.IdempotencyKey + ":contact", Status: "VERIFIED", VerifiedAt: &now, CreatedAt: now}
			if err := tx.Create(&verification).Error; err != nil {
				return err
			}
		}
		if err := tx.Exec("DELETE FROM participants.channel_selections WHERE tenant_id=? AND participant_id=? AND contact_id IN (SELECT id FROM participants.contacts WHERE tenant_id=? AND participant_id=? AND channel='EMAIL')", in.TenantID, participant.ID, in.TenantID, participant.ID).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ChannelSelection{ID: identity.NewID(), TenantID: in.TenantID, ParticipantID: participant.ID, ContactID: contact.ID, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&participant).Where("tenant_id=? AND id=?", in.TenantID, participant.ID).Updates(map[string]any{"version": gorm.Expr("version + 1"), "updated_at": now}).Error; err != nil {
			return err
		}

		if err := tx.Model(&database.ExternalSession{}).Where("tenant_id=? AND invitation_id=? AND revoked_at IS NULL", in.TenantID, old.ID).Update("revoked_at", now).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.Invitation{}).Where("tenant_id=? AND id=? AND status='ACTIVE'", in.TenantID, old.ID).Updates(map[string]any{"status": "REVOKED", "revoked_at": now}).Error; err != nil {
			return err
		}
		var oldDeliveries []database.Delivery
		if err := tx.Where("tenant_id=? AND invitation_id=?", in.TenantID, old.ID).Find(&oldDeliveries).Error; err != nil {
			return err
		}
		for _, delivery := range oldDeliveries {
			if err := tx.Model(&database.ChannelAttempt{}).Where("tenant_id=? AND delivery_id=? AND status IN ('QUEUED','PROCESSING')", in.TenantID, delivery.ID).Updates(map[string]any{"status": "CANCELED", "last_error": "recipient_corrected", "lease_expires_at": nil, "updated_at": now}).Error; err != nil {
				return err
			}
		}

		deliveryIntents, _ := json.Marshal([]invitationDelivery{{Channel: "EMAIL", Destination: email}})
		token, err := security.NewScopedToken(in.TenantID)
		if err != nil {
			return err
		}
		hash := security.HashToken(token)
		invitation := database.Invitation{ID: identity.NewID(), TenantID: in.TenantID, ResponsibilityID: in.ResponsibilityID, TokenHash: hash[:], DeliveryIntents: deliveryIntents, IdempotencyKey: in.IdempotencyKey, Status: "ACTIVE", ExpiresAt: inspection.DeadlineAt, CreatedAt: now}
		if err := tx.Create(&invitation).Error; err != nil {
			return err
		}
		template, variables := notificationcore.CaptureLinkNotification(notificationcore.ChannelEmail, participant.Name, asset.Name, asset.Address, invitation.ExpiresAt)
		correlation := in.CorrelationID
		if correlation == "" {
			correlation = "responsible-email-correction:" + in.InspectionID.String()
		}
		deliveryResult, err := s.Notifications.Send(notificationrequest.InTransaction(ctx, tx), notificationcore.Notification{
			TenantID: in.TenantID, InspectionID: &in.InspectionID, InvitationID: &invitation.ID,
			Recipient: notificationcore.Recipient{Destination: email}, Channel: notificationcore.ChannelEmail,
			Template: template, Variables: variables, CorrelationID: correlation,
			IdempotencyKey: in.IdempotencyKey + ":delivery",
			Execution:      &notificationcore.ExecutionPayload{InvitationID: invitation.ID, Token: token, URLVariable: "captureUrl", BaseURL: s.CaptureBaseURL, ExpiresAt: invitation.ExpiresAt.Unix()},
		})
		if err != nil {
			return err
		}
		if err := tx.Model(&responsibility).Updates(map[string]any{"version": gorm.Expr("version + 1"), "updated_at": now}).Error; err != nil {
			return err
		}
		responsibility.Version++
		if in.ActorID == (identity.ID{}) {
			in.ActorID = responsibility.ID
		}
		if err := tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: in.TenantID, ActorID: in.ActorID, Action: "RESPONSIBLE_EMAIL_CORRECTED", TargetType: "INSPECTION", TargetID: in.InspectionID.String(), Outcome: "SUCCESS", Reason: "capture invitation recipient corrected", CorrelationID: correlation, OccurredAt: now}).Error; err != nil {
			return err
		}
		result = Result{InspectionID: in.InspectionID, ResponsibilityID: in.ResponsibilityID, InvitationID: invitation.ID, DeliveryID: &deliveryResult.ID, Recipient: email, DeliveryStatus: string(deliveryResult.State), ResponsibilityStatus: responsibility.Status, ResponsibilityVersion: responsibility.Version}
		observability.LogResponsibleEmailCorrected(ctx, in.TenantID, in.InspectionID, invitation.ID, in.Source, responsibility.Version)
		s.Metrics.ResponsibleEmailCorrection(in.Source)
		return nil
	})
	return result, err
}

type invitationDelivery struct {
	Channel     string `json:"channel"`
	Destination string `json:"destination"`
}

func (s Service) loadResult(tx *gorm.DB, tenantID, inspectionID identity.ID, invitation database.Invitation, result *Result) error {
	var responsibility database.Responsibility
	if err := tx.Where("tenant_id=? AND id=?", tenantID, invitation.ResponsibilityID).First(&responsibility).Error; err != nil {
		return err
	}
	var delivery database.Delivery
	err := tx.Where("tenant_id=? AND invitation_id=?", tenantID, invitation.ID).Order("created_at desc").First(&delivery).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err != nil {
		return err
	}
	result.DeliveryID = &delivery.ID
	result.InspectionID = inspectionID
	result.ResponsibilityID = invitation.ResponsibilityID
	result.InvitationID = invitation.ID
	result.DeliveryStatus = delivery.Status
	result.ResponsibilityStatus = responsibility.Status
	result.ResponsibilityVersion = responsibility.Version
	return nil
}
