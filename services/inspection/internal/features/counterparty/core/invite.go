package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"inspection/libs/identity"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	notificationrequest "inspection/services/inspection/internal/features/notifications/request"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Invite creates one invitation for each unique participant during activation.
func (s Service) Invite(tx *gorm.DB, tenantID, versionID identity.ID, participantIDs []identity.ID, _ identity.ID) error {
	if tx == nil || tenantID == (identity.ID{}) || versionID == (identity.ID{}) {
		return apperror.New(apperror.InvalidInput, "counterpartyParticipantIds", "invalid counterparty invitation")
	}
	var version database.OriginVersion
	if err := tx.Where("tenant_id=? AND id=? AND status='ACTIVE' AND has_inventory", tenantID, versionID).First(&version).Error; err != nil {
		return err
	}
	var origin database.Origin
	if err := tx.Where("tenant_id=? AND id=?", tenantID, version.OriginID).First(&origin).Error; err != nil {
		return err
	}
	var asset database.Asset
	if err := tx.Where("tenant_id=? AND id=?", tenantID, origin.AssetID).First(&asset).Error; err != nil {
		return err
	}
	policy := database.BaselinePolicy{TenantID: tenantID, ContestationDeadlineDays: DefaultDeadlineDays, Version: 1}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}}, DoNothing: true}).Create(&policy).Error; err != nil {
		return err
	}
	if err := tx.Where("tenant_id=?", tenantID).First(&policy).Error; err != nil {
		return err
	}
	seen := map[identity.ID]struct{}{}
	for _, participantID := range participantIDs {
		if participantID == (identity.ID{}) {
			return apperror.New(apperror.InvalidInput, "counterpartyParticipantIds", "participant id is invalid")
		}
		if _, exists := seen[participantID]; exists {
			continue
		}
		seen[participantID] = struct{}{}
		var existing database.CounterpartyInvitation
		err := tx.Where("tenant_id=? AND origin_version_id=? AND participant_id=?", tenantID, versionID, participantID).First(&existing).Error
		if err == nil {
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var participant database.Participant
		if err := tx.Where("tenant_id=? AND id=? AND status='ACTIVE'", tenantID, participantID).First(&participant).Error; err != nil {
			return apperror.New(apperror.InvalidInput, "counterpartyParticipantIds", "counterparty participant is unavailable")
		}
		channels, err := selectedVerifiedChannelsFor(tx, tenantID, participantID, "counterpartyParticipantIds")
		if err != nil {
			return err
		}
		channelsJSON, err := json.Marshal(channels)
		if err != nil {
			return err
		}
		token, err := security.NewScopedToken(tenantID)
		if err != nil {
			return err
		}
		now := s.now()
		tokenHash := security.HashToken(token)
		row := database.CounterpartyInvitation{ID: identity.NewID(), TenantID: tenantID, OriginVersionID: versionID, ParticipantID: participantID, TokenHash: tokenHash[:], Channels: channelsJSON, DeadlineAt: now.AddDate(0, 0, policy.ContestationDeadlineDays), DeliveryGeneration: 1, Response: "PENDING", Resolution: "OPEN", CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		if err := s.sendInvitation(tx, row, token, asset.Name, purposeLabel(version.Purpose)); err != nil {
			return err
		}
	}
	return nil
}

func (s Service) sendInvitation(tx *gorm.DB, invitation database.CounterpartyInvitation, token, assetName, purpose string) error {
	if s.Notifications == nil || s.CaptureBaseURL == "" {
		return apperror.New(apperror.DependencyUnavailable, "notification", "counterparty delivery is unavailable")
	}
	var destinations []invitationcore.DeliveryIntent
	if err := json.Unmarshal(invitation.Channels, &destinations); err != nil {
		return err
	}
	ctx := tx.Statement.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = notificationrequest.InTransaction(ctx, tx)
	location, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		location = time.FixedZone("BRT", -3*60*60)
	}
	deadline := invitation.DeadlineAt.In(location).Format("02/01/2006 15:04")
	for _, destination := range destinations {
		channel := notificationcore.Channel(destination.Channel)
		_, err := s.Notifications.Send(ctx, notificationcore.Notification{
			TenantID:       invitation.TenantID,
			Recipient:      notificationcore.Recipient{ID: invitation.ParticipantID.String(), Destination: destination.Destination},
			Channel:        channel,
			Template:       notificationcore.TemplateRef{Name: "baseline-acknowledgment", Version: "v1"},
			Variables:      map[string]string{"assetName": assetName, "purposeLabel": purpose, "deadlineDate": deadline},
			CorrelationID:  fmt.Sprintf("counterparty-%s-g%d", invitation.ID, invitation.DeliveryGeneration),
			IdempotencyKey: notificationcore.RecipientIdempotencyKey(identity.NewDeterministicID("counterparty/delivery", invitation.ID.String()+":"+strconv.Itoa(invitation.DeliveryGeneration)), channel, destination.Destination),
			Execution:      &notificationcore.ExecutionPayload{InvitationID: invitation.ID, Token: token, URLVariable: "link", BaseURL: s.CaptureBaseURL, ExpiresAt: invitation.DeadlineAt.Unix()},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func selectedVerifiedChannels(tx *gorm.DB, tenantID, participantID identity.ID) ([]invitationcore.DeliveryIntent, error) {
	return selectedVerifiedChannelsFor(tx, tenantID, participantID, "counterpartyParticipantIds")
}

func selectedVerifiedChannelsFor(tx *gorm.DB, tenantID, participantID identity.ID, field string) ([]invitationcore.DeliveryIntent, error) {
	var channels []invitationcore.DeliveryIntent
	if err := tx.Raw(`SELECT c.channel, c.value AS destination, true AS verified FROM participants.channel_selections s
		JOIN participants.contacts c ON c.tenant_id=s.tenant_id AND c.id=s.contact_id
		WHERE s.tenant_id=? AND s.participant_id=? AND c.active=true AND EXISTS (
			SELECT 1 FROM participants.contact_verifications v WHERE v.tenant_id=c.tenant_id AND v.contact_id=c.id AND v.status='VERIFIED'
			AND NOT EXISTS (SELECT 1 FROM participants.contact_verifications newer WHERE newer.tenant_id=v.tenant_id AND newer.contact_id=v.contact_id AND (newer.created_at,newer.id)>(v.created_at,v.id))) ORDER BY c.channel,c.id`, tenantID, participantID).Scan(&channels).Error; err != nil {
		return nil, err
	}
	if len(channels) == 0 {
		return nil, apperror.New(apperror.InvalidInput, field, "counterparty requires a selected verified delivery channel")
	}
	return channels, nil
}

func purposeLabel(value *string) string {
	if value == nil {
		return "vistoria de referência"
	}
	switch *value {
	case "MOVE_IN":
		return "entrada"
	case "CONSTRUCTION_HANDOVER":
		return "entrega de obra"
	case "POST_RENOVATION":
		return "pós-reforma"
	case "INVENTORY_UPDATE":
		return "atualização de inventário"
	case "CONTESTATION_ADJUSTMENT":
		return "ajuste de contestação"
	default:
		return "vistoria de referência"
	}
}
