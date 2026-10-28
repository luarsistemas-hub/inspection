package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Resend replaces only the secret link and creates a fresh durable delivery intent.
func (s Service) Resend(ctx context.Context, tenantID, invitationID identity.ID) (database.CounterpartyInvitation, error) {
	var row database.CounterpartyInvitation
	var token string
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, invitationID).First(&row).Error; err != nil {
			return safeInvitationLookupError(err, "counterpartyInvitationId")
		}
		if row.Response == "NO_RESPONSE" {
			return apperror.New(apperror.DeadlinePassed, "deadlineAt", "deadline passed")
		}
		if row.Response != "PENDING" {
			return apperror.New(apperror.InvalidState, "counterpartyInvitationId", "invitation already responded")
		}
		var priorDeliveries, failedDeliveries int64
		priorCorrelation := fmt.Sprintf("counterparty-%s-g%d", row.ID, row.DeliveryGeneration)
		if err := tx.Model(&database.Delivery{}).Where("tenant_id=? AND correlation_id=?", tenantID, priorCorrelation).Count(&priorDeliveries).Error; err != nil {
			return err
		}
		if priorDeliveries == 0 {
			return apperror.New(apperror.InvalidState, "counterpartyInvitationId", "no failed delivery is available to resend")
		}
		if err := tx.Model(&database.Delivery{}).Where("tenant_id=? AND correlation_id=? AND status IN ('FAILED','UNKNOWN')", tenantID, priorCorrelation).Count(&failedDeliveries).Error; err != nil {
			return err
		}
		if priorDeliveries != failedDeliveries {
			return apperror.New(apperror.InvalidState, "counterpartyInvitationId", "invitation delivery is not fully failed")
		}
		if !deadlineOpen(s.now(), row.DeadlineAt) {
			return apperror.New(apperror.DeadlinePassed, "deadlineAt", "deadline passed")
		}
		var participant database.Participant
		if err := tx.Where("tenant_id=? AND id=? AND status='ACTIVE'", tenantID, row.ParticipantID).First(&participant).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.InvalidState, "counterpartyInvitationId", "counterparty is unavailable")
			}
			return err
		}
		channels, err := selectedVerifiedChannelsFor(tx, tenantID, row.ParticipantID, "counterpartyInvitationId")
		if err != nil {
			return err
		}
		row.Channels, err = json.Marshal(channels)
		if err != nil {
			return err
		}
		generated, err := security.NewScopedToken(tenantID)
		if err != nil {
			return err
		}
		token = generated
		hash := security.HashToken(token)
		row.TokenHash = hash[:]
		row.OTPHash, row.OTPExpiresAt = nil, nil
		row.OTPAttempts = 0
		row.DeliveryGeneration++
		row.UpdatedAt = s.now()
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.ExternalSession{}).Where("tenant_id=? AND kind='COUNTERPARTY' AND counterparty_invitation_id=? AND revoked_at IS NULL", tenantID, row.ID).Update("revoked_at", row.UpdatedAt).Error; err != nil {
			return err
		}
		var version database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", tenantID, row.OriginVersionID).First(&version).Error; err != nil {
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
		return s.sendInvitation(tx, row, token, asset.Name, purposeLabel(version.Purpose))
	})
	return row, err
}
