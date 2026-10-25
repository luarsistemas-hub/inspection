package core

import (
	"context"
	"time"

	"inspection/libs/identity"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// BaselineView is the customer-safe baseline and counterparty status.
type BaselineView struct {
	Invitation       InvitationView
	Inventory        inventorycore.BaselineView
	AdjustmentStatus string
	Newer            bool
	ReadOnly         bool
}

// InvitationView contains only the invitation fields needed by the external baseline response.
type InvitationView struct {
	ID                  identity.ID
	OriginVersionID     identity.ID
	DeadlineAt          time.Time
	Response            string
	RespondedAt         *time.Time
	Resolution          string
	ResolutionReason    *string
	AdjustmentVersionID *identity.ID
}

// View returns the customer-safe baseline and immutable response/deadline state.
func (s Service) View(ctx context.Context, owner SessionOwner) (BaselineView, error) {
	var out BaselineView
	var assetID, versionID identity.ID
	err := s.within(ctx, owner.TenantID, func(tx *gorm.DB) error {
		var invitation database.CounterpartyInvitation
		if err := tx.Select("id", "origin_version_id", "deadline_at", "response", "responded_at", "resolution", "resolution_reason", "adjustment_version_id").Where("tenant_id=? AND id=?", owner.TenantID, owner.InvitationID).First(&invitation).Error; err != nil {
			return safeInvitationLookupError(err, "counterpartyInvitationId")
		}
		out.Invitation = InvitationView{ID: invitation.ID, OriginVersionID: invitation.OriginVersionID, DeadlineAt: invitation.DeadlineAt, Response: invitation.Response, RespondedAt: invitation.RespondedAt, Resolution: invitation.Resolution, ResolutionReason: invitation.ResolutionReason, AdjustmentVersionID: invitation.AdjustmentVersionID}
		var version database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", owner.TenantID, out.Invitation.OriginVersionID).First(&version).Error; err != nil {
			return err
		}
		var origin database.Origin
		if err := tx.Where("tenant_id=? AND id=?", owner.TenantID, version.OriginID).First(&origin).Error; err != nil {
			return err
		}
		assetID, versionID = origin.AssetID, version.ID
		out.ReadOnly = !deadlineOpen(s.now(), out.Invitation.DeadlineAt) || out.Invitation.Response != "PENDING"
		out.Newer = origin.ActiveVersionID != nil && *origin.ActiveVersionID != version.ID
		if out.Invitation.AdjustmentVersionID != nil {
			var adjustment database.OriginVersion
			if err := tx.Select("status").Where("tenant_id=? AND id=?", owner.TenantID, *out.Invitation.AdjustmentVersionID).First(&adjustment).Error; err != nil {
				return err
			}
			out.AdjustmentStatus = adjustment.Status
		}
		return nil
	})
	if err == nil {
		inventory := s.Inventory
		if inventory.DB == nil {
			inventory.DB = s.DB
		}
		out.Inventory, err = inventory.BaselineInventory(ctx, owner.TenantID, assetID, &versionID, true)
	}
	return out, err
}
