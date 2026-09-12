// Package callbacks owns durable provider callback recording and correlation.
package callbacks

import (
	"context"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/notifications/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Record persists a callback before attempting correlation. An early callback
// therefore remains available for replay after a provider receipt is stored.
func Record(ctx context.Context, tx *gorm.DB, tenantID identity.ID, provider, account, callbackID, receiptID, status string, now time.Time) error {
	callback := database.ProviderCallback{ID: identity.NewID(), TenantID: tenantID, Provider: provider, ProviderAccount: account, CallbackID: callbackID, ReceiptID: receiptID, Status: status, ReceivedAt: now.UTC()}
	result := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&callback)
	if result.Error != nil || result.RowsAffected == 0 {
		return result.Error
	}
	return correlate(ctx, tx, tenantID, provider, account, receiptID, now)
}

// Replay applies callbacks received before their provider receipt was known.
func Replay(ctx context.Context, tx *gorm.DB, tenantID identity.ID, provider, account, receiptID string, now time.Time) error {
	return correlate(ctx, tx, tenantID, provider, account, receiptID, now)
}

func correlate(ctx context.Context, tx *gorm.DB, tenantID identity.ID, provider, account, receiptID string, now time.Time) error {
	var attempt database.ChannelAttempt
	if err := tx.WithContext(ctx).Where("tenant_id=? AND provider=? AND provider_account=? AND receipt_id=?", tenantID, provider, account, receiptID).First(&attempt).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}
	var callbacks []database.ProviderCallback
	if err := tx.WithContext(ctx).Where("tenant_id=? AND provider=? AND provider_account=? AND receipt_id=?", tenantID, provider, account, receiptID).Order("received_at ASC, id ASC").Find(&callbacks).Error; err != nil {
		return err
	}
	for _, callback := range callbacks {
		if callback.ChannelAttemptID == nil {
			if err := tx.WithContext(ctx).Model(&database.ProviderCallback{}).Where("id=?", callback.ID).Update("channel_attempt_id", attempt.ID).Error; err != nil {
				return err
			}
		}
		next, ok := core.NormalizeProviderStatus(callback.Status)
		if !ok || !core.CanTransition(core.State(attempt.Status), next) {
			continue
		}
		if err := tx.WithContext(ctx).Model(&database.ChannelAttempt{}).Where("tenant_id=? AND id=?", tenantID, attempt.ID).Updates(map[string]any{"status": string(next), "updated_at": now.UTC()}).Error; err != nil {
			return err
		}
		attempt.Status = string(next)
	}
	return UpdateDelivery(ctx, tx, tenantID, attempt.DeliveryID, now)
}

// UpdateDelivery recomputes the aggregate without promoting partial results to
// DELIVERED. It is shared by callbacks and the executor after each result.
func UpdateDelivery(ctx context.Context, tx *gorm.DB, tenantID, deliveryID identity.ID, now time.Time) error {
	var rows []database.ChannelAttempt
	if err := tx.WithContext(ctx).Where("tenant_id=? AND delivery_id=?", tenantID, deliveryID).Find(&rows).Error; err != nil {
		return err
	}
	state := aggregate(rows)
	return tx.WithContext(ctx).Model(&database.Delivery{}).Where("tenant_id=? AND id=?", tenantID, deliveryID).Updates(map[string]any{"status": string(state), "updated_at": now.UTC()}).Error
}

func aggregate(rows []database.ChannelAttempt) core.State {
	if len(rows) == 0 {
		return core.StateQueued
	}
	allDelivered := true
	seenDelivered, seenAccepted, seenProcessing, seenQueued, seenUnknown := false, false, false, false, false
	for _, row := range rows {
		switch core.State(row.Status) {
		case core.StateDelivered:
			seenDelivered = true
		case core.StateAccepted, core.StateSent:
			allDelivered = false
			seenAccepted = true
		case core.StateProcessing:
			allDelivered = false
			seenProcessing = true
		case core.StateQueued:
			allDelivered = false
			seenQueued = true
		case core.StateUnknown:
			allDelivered = false
			seenUnknown = true
		default:
			allDelivered = false
		}
	}
	switch {
	case allDelivered:
		return core.StateDelivered
	case seenDelivered:
		return core.StateSent // partial success must not claim full delivery.
	case seenAccepted:
		return core.StateAccepted
	case seenProcessing:
		return core.StateProcessing
	case seenQueued:
		return core.StateQueued
	case seenUnknown:
		return core.StateUnknown
	default:
		return core.StateFailed
	}
}
