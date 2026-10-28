package core

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"inspection/libs/identity"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Resolve closes a contestation and optionally creates an adjustment draft.
func (s Service) Resolve(ctx context.Context, tenantID, invitationID, actorID identity.ID, outcome, reason string) (database.CounterpartyInvitation, error) {
	var result database.CounterpartyInvitation
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, invitationID).First(&result).Error; err != nil {
			return safeInvitationLookupError(err, "counterpartyInvitationId")
		}
		if outcome != "ACCEPT" && outcome != "DECLINE" {
			return apperror.New(apperror.InvalidInput, "outcome", "invalid resolution")
		}
		if !validResolutionReason(reason) {
			return apperror.New(apperror.InvalidInput, "reason", "reason must contain at most 500 characters")
		}
		if outcome == "DECLINE" && strings.TrimSpace(reason) == "" {
			return apperror.New(apperror.InvalidInput, "reason", "reason is required")
		}
		if result.Response != "CONTESTED" {
			return apperror.New(apperror.InvalidState, "counterpartyInvitationId", "invitation is not contested")
		}
		if result.Resolution != "OPEN" {
			return apperror.New(apperror.AlreadyResolved, "counterpartyInvitationId", "already resolved")
		}
		now := s.now()
		result.Resolution = map[string]string{"ACCEPT": "ACCEPTED", "DECLINE": "DECLINED"}[outcome]
		if strings.TrimSpace(reason) != "" {
			value := strings.TrimSpace(reason)
			result.ResolutionReason = &value
		}
		result.ResolvedBy, result.ResolvedAt, result.UpdatedAt = &actorID, &now, now
		if outcome == "ACCEPT" {
			var version database.OriginVersion
			if err := tx.Where("tenant_id=? AND id=?", tenantID, result.OriginVersionID).First(&version).Error; err != nil {
				return err
			}
			var origin database.Origin
			if err := tx.Where("tenant_id=? AND id=?", tenantID, version.OriginID).First(&origin).Error; err != nil {
				return err
			}
			if origin.ActiveVersionID == nil || *origin.ActiveVersionID != version.ID {
				return apperror.New(apperror.InvalidState, "counterpartyInvitationId", "contested baseline is no longer active")
			}
			draft, err := s.Inventory.CreateDraft(tx, inventorycore.DraftInput{TenantID: tenantID, AssetID: origin.AssetID, Source: "CONTESTATION", SourceVersionID: version.ID, ActorMembershipID: actorID})
			if err != nil {
				if apperror.Is(err, apperror.Conflict) {
					return apperror.New(apperror.Conflict, "counterpartyInvitationId", "an inventory draft already exists for this asset")
				}
				return err
			}
			result.AdjustmentVersionID = &draft.ID
		}
		return tx.Save(&result).Error
	})
	return result, err
}

func validResolutionReason(reason string) bool {
	trimmed := strings.TrimSpace(reason)
	return utf8.ValidString(trimmed) && len([]rune(trimmed)) <= 500 && strings.IndexFunc(trimmed, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
	}) < 0
}
