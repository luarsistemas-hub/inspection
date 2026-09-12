// Package execute_delivery reserves and executes durable notification work.
package execute_delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/notifications/callbacks"
	"inspection/services/inspection/internal/features/notifications/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/notifications"
	"inspection/services/inspection/internal/platform/observability"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Dependencies are the worker-owned dependencies for durable execution.
// Gateway is composed only by the worker, keeping provider credentials out of
// this slice and its callers.
type Dependencies struct {
	DB               *gorm.DB
	Gateway          *notifications.Gateway
	Catalog          core.Catalog
	Now              func() time.Time
	LeaseDuration    time.Duration
	MaxAttempts      int
	RetryDelays      []time.Duration
	CallbackURL      string
	ProviderAccounts map[notifications.Provider]string
	Payloads         *notifications.PayloadCipher
	Metrics          *observability.Metrics
}

// Executor executes due work one item at a time. Call RunDue from a worker
// ticker; provider I/O is deliberately outside both database transactions.
type Executor struct {
	db               *gorm.DB
	gateway          *notifications.Gateway
	catalog          core.Catalog
	now              func() time.Time
	leaseDuration    time.Duration
	maxAttempts      int
	retryDelays      []time.Duration
	callbackURL      string
	providerAccounts map[notifications.Provider]string
	payloads         *notifications.PayloadCipher
	metrics          *observability.Metrics
}

// Setup validates dependencies and returns a durable executor.
func Setup(deps Dependencies) (*Executor, error) {
	if deps.DB == nil || deps.Gateway == nil {
		return nil, errors.New("notifications/execute_delivery: missing dependency")
	}
	if len(deps.Catalog.Keys()) == 0 {
		deps.Catalog = core.DefaultCatalog()
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.MaxAttempts == 0 {
		deps.MaxAttempts = 4
	}
	if deps.MaxAttempts != 4 {
		return nil, errors.New("notifications/execute_delivery: max attempts must be four")
	}
	if len(deps.RetryDelays) == 0 {
		deps.RetryDelays = []time.Duration{5 * time.Second, 30 * time.Second, 5 * time.Minute}
	}
	if len(deps.RetryDelays) != deps.MaxAttempts-1 {
		return nil, errors.New("notifications/execute_delivery: invalid retry delays")
	}
	if deps.LeaseDuration <= 0 {
		deps.LeaseDuration = 35 * time.Second
	}
	accounts := make(map[notifications.Provider]string, len(deps.ProviderAccounts))
	for provider, account := range deps.ProviderAccounts {
		accounts[provider] = account
	}
	return &Executor{db: deps.DB, gateway: deps.Gateway, catalog: deps.Catalog, now: deps.Now, leaseDuration: deps.LeaseDuration, maxAttempts: deps.MaxAttempts, retryDelays: append([]time.Duration(nil), deps.RetryDelays...), callbackURL: deps.CallbackURL, providerAccounts: accounts, payloads: deps.Payloads, metrics: deps.Metrics}, nil
}

type reservation struct {
	delivery  database.Delivery
	work      database.ChannelAttempt
	history   database.NotificationAttempt
	startedAt time.Time
}

// RunDue reserves and processes at most one due work item. It returns false
// when no work was available. A stale lease is reconciled to UNKNOWN first,
// never automatically re-sent.
func (e *Executor) RunDue(ctx context.Context) (bool, error) {
	if e == nil {
		return false, errors.New("notifications/execute_delivery: unavailable")
	}
	reserved, err := e.reserve(ctx)
	if err != nil || reserved == nil {
		return reserved != nil, err
	}
	intent, err := e.intent(ctx, reserved.delivery, reserved.work)
	if err != nil {
		if errors.Is(err, errLinkUnavailable) {
			return true, e.cancel(ctx, *reserved)
		}
		return true, e.persist(ctx, *reserved, notifications.Receipt{}, &notifications.DeliveryError{Kind: notifications.ErrorPermanent, Code: "template_invalid", PreSend: true})
	}
	receipt, sendErr := e.gateway.Send(ctx, notifications.Channel(reserved.work.Channel), notifications.Provider(reserved.work.Provider), intent)
	e.metrics.ObserveProviderLatency(time.Since(reserved.startedAt))
	return true, e.persist(ctx, *reserved, receipt, sendErr)
}

func (e *Executor) reserve(ctx context.Context) (*reservation, error) {
	now := e.now().UTC()
	var value *reservation
	err := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// A worker that dies after starting I/O cannot prove whether the provider
		// accepted the request. Expiry is therefore UNKNOWN, not a retry.
		var expired []database.ChannelAttempt
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("status=? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?", string(core.StateProcessing), now).Find(&expired).Error; err != nil {
			return err
		}
		for _, stale := range expired {
			e.metrics.ExpiredLease(stale.Channel, stale.Provider)
			e.metrics.Unknown(stale.Channel, stale.Provider)
			if err := tx.Model(&database.ChannelAttempt{}).Where("tenant_id=? AND id=? AND status=?", stale.TenantID, stale.ID, string(core.StateProcessing)).Updates(map[string]any{"status": string(core.StateUnknown), "lease_expires_at": nil, "last_error": "lease_expired", "updated_at": now}).Error; err != nil {
				return err
			}
			if err := callbacks.UpdateDelivery(ctx, tx, stale.TenantID, stale.DeliveryID, now); err != nil {
				return err
			}
		}
		var work database.ChannelAttempt
		query := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("status=? AND next_attempt_at <= ?", string(core.StateQueued), now).Order("next_attempt_at ASC").Limit(1)
		if err := query.First(&work).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		var delivery database.Delivery
		if err := tx.Where("tenant_id=? AND id=?", work.TenantID, work.DeliveryID).First(&delivery).Error; err != nil {
			return err
		}
		leaseUntil := now.Add(e.leaseDuration)
		sequence := work.Attempts + 1
		if sequence > e.maxAttempts {
			return tx.Model(&database.ChannelAttempt{}).Where("tenant_id=? AND id=?", work.TenantID, work.ID).Updates(map[string]any{"status": string(core.StateFailed), "updated_at": now}).Error
		}
		account := work.ProviderAccount
		if account == "" {
			account = e.providerAccounts[notifications.Provider(work.Provider)]
		}
		result := tx.Model(&database.ChannelAttempt{}).Where("tenant_id=? AND id=? AND status=?", work.TenantID, work.ID, string(core.StateQueued)).Updates(map[string]any{"status": string(core.StateProcessing), "attempts": sequence, "provider_account": account, "lease_expires_at": leaseUntil, "last_attempt_at": now, "updated_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			if result.Error != nil {
				return result.Error
			}
			return nil
		}
		history := database.NotificationAttempt{ID: identity.NewID(), TenantID: work.TenantID, ChannelAttemptID: work.ID, Sequence: sequence, Provider: work.Provider, Status: string(core.StateProcessing), StartedAt: now}
		if err := tx.Create(&history).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.Delivery{}).Where("tenant_id=? AND id=?", work.TenantID, delivery.ID).Updates(map[string]any{"status": string(core.StateProcessing), "lease_expires_at": leaseUntil, "updated_at": now}).Error; err != nil {
			return err
		}
		work.Status, work.Attempts, work.LeaseExpiresAt, work.ProviderAccount = string(core.StateProcessing), sequence, &leaseUntil, account
		e.metrics.ObserveQueueLatency(now.Sub(work.CreatedAt))
		e.metrics.Attempt(work.Channel, work.Provider)
		value = &reservation{delivery: delivery, work: work, history: history, startedAt: now}
		return nil
	})
	return value, err
}

var errLinkUnavailable = errors.New("notification link unavailable")

func (e *Executor) intent(ctx context.Context, delivery database.Delivery, work database.ChannelAttempt) (notifications.DeliveryIntent, error) {
	var variables map[string]string
	if err := json.Unmarshal(work.TemplateVariables, &variables); err != nil {
		return notifications.DeliveryIntent{}, err
	}
	if err := e.hydrateSensitiveLink(ctx, delivery, &variables); err != nil {
		return notifications.DeliveryIntent{}, err
	}
	template, err := e.catalog.Resolve(core.TemplateRef{Name: delivery.LogicalTemplate, Version: delivery.TemplateVersion}, core.Channel(work.Channel))
	if err != nil {
		return notifications.DeliveryIntent{}, err
	}
	rendered, err := template.Render(variables)
	if err != nil {
		return notifications.DeliveryIntent{}, err
	}
	return notifications.DeliveryIntent{ID: work.ID.String(), Destination: work.Destination, Template: delivery.LogicalTemplate + ":" + delivery.TemplateVersion, Language: "pt-BR", Subject: rendered.Subject, Text: rendered.Text, HTML: rendered.HTML, TemplateParameters: rendered.Parameters, CallbackURL: e.callbackURL, TenantID: work.TenantID.String()}, nil
}

func (e *Executor) hydrateSensitiveLink(ctx context.Context, delivery database.Delivery, variables *map[string]string) error {
	var stored database.NotificationPayload
	err := e.db.WithContext(ctx).Where("tenant_id=? AND delivery_id=?", delivery.TenantID, delivery.ID).First(&stored).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil || e.payloads == nil {
		return errLinkUnavailable
	}
	plain, err := e.payloads.Decrypt(stored.KeyID, stored.Nonce, stored.Ciphertext, payloadAAD(delivery.TenantID, delivery.ID))
	if err != nil {
		return errLinkUnavailable
	}
	var execution core.ExecutionPayload
	if err := json.Unmarshal(plain, &execution); err != nil {
		return errLinkUnavailable
	}
	now := e.now().UTC()
	if execution.ExpiresAt > 0 && !now.Before(time.Unix(execution.ExpiresAt, 0).UTC()) {
		return errLinkUnavailable
	}
	var invitation database.Invitation
	if err := e.db.WithContext(ctx).Where("tenant_id=? AND id=? AND status='ACTIVE' AND revoked_at IS NULL AND expires_at>?", delivery.TenantID, execution.InvitationID, now).First(&invitation).Error; err != nil {
		return errLinkUnavailable
	}
	base := strings.TrimRight(execution.BaseURL, "/")
	if base == "" || execution.URLVariable == "" || execution.Token == "" {
		return errLinkUnavailable
	}
	(*variables)[execution.URLVariable] = base + "/capture/" + execution.Token
	return nil
}

func (e *Executor) persist(ctx context.Context, reserved reservation, receipt notifications.Receipt, sendErr error) error {
	now := e.now().UTC()
	return e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current database.ChannelAttempt
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", reserved.work.TenantID, reserved.work.ID).First(&current).Error; err != nil {
			return err
		}
		if current.Status != string(core.StateProcessing) {
			return nil
		}
		state, nextAt, code := e.outcome(current.Attempts, sendErr, now)
		e.metrics.ObserveProcessingLatency(now.Sub(reserved.startedAt))
		if sendErr != nil {
			e.metrics.Failure(current.Channel, current.Provider, code)
		}
		if state == core.StateQueued {
			e.metrics.Retry(current.Channel, current.Provider)
		}
		if state == core.StateUnknown {
			e.metrics.Unknown(current.Channel, current.Provider)
		}
		updates := map[string]any{"status": string(state), "lease_expires_at": nil, "updated_at": now, "last_error": code}
		if !nextAt.IsZero() {
			updates["next_attempt_at"] = nextAt
		}
		if receipt.ID != "" {
			updates["receipt_id"] = receipt.ID
		}
		if err := tx.Model(&database.ChannelAttempt{}).Where("tenant_id=? AND id=?", current.TenantID, current.ID).Updates(updates).Error; err != nil {
			return err
		}
		history := map[string]any{"status": string(state), "receipt_id": receipt.ID, "error_code": code, "finished_at": now}
		if err := tx.Model(&database.NotificationAttempt{}).Where("tenant_id=? AND id=?", current.TenantID, reserved.history.ID).Updates(history).Error; err != nil {
			return err
		}
		if receipt.ID != "" {
			if err := callbacks.Replay(ctx, tx, current.TenantID, current.Provider, current.ProviderAccount, receipt.ID, now); err != nil {
				return err
			}
		}
		if state == core.StateAccepted || state == core.StateFailed || state == core.StateUnknown || state == core.StateCanceled {
			if err := tx.WithContext(ctx).Where("tenant_id=? AND delivery_id=?", current.TenantID, current.DeliveryID).Delete(&database.NotificationPayload{}).Error; err != nil {
				return err
			}
		}
		return callbacks.UpdateDelivery(ctx, tx, current.TenantID, current.DeliveryID, now)
	})
}

func (e *Executor) cancel(ctx context.Context, reserved reservation) error {
	now := e.now().UTC()
	return e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.ChannelAttempt{}).Where("tenant_id=? AND id=? AND status=?", reserved.work.TenantID, reserved.work.ID, string(core.StateProcessing)).Updates(map[string]any{"status": string(core.StateCanceled), "lease_expires_at": nil, "last_error": "link_unavailable", "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.NotificationAttempt{}).Where("tenant_id=? AND id=?", reserved.work.TenantID, reserved.history.ID).Updates(map[string]any{"status": string(core.StateCanceled), "error_code": "link_unavailable", "finished_at": now}).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Where("tenant_id=? AND delivery_id=?", reserved.work.TenantID, reserved.work.DeliveryID).Delete(&database.NotificationPayload{}).Error; err != nil {
			return err
		}
		return callbacks.UpdateDelivery(ctx, tx, reserved.work.TenantID, reserved.work.DeliveryID, now)
	})
}

func payloadAAD(tenantID, deliveryID identity.ID) []byte {
	return []byte(tenantID.String() + ":" + deliveryID.String())
}

func (e *Executor) outcome(attempt int, sendErr error, now time.Time) (core.State, time.Time, string) {
	if sendErr == nil {
		return core.StateAccepted, time.Time{}, ""
	}
	providerErr, ok := notifications.AsDeliveryError(sendErr)
	if !ok {
		return core.StateUnknown, time.Time{}, "provider_interrupted"
	}
	if providerErr.Kind == notifications.ErrorPermanent {
		return core.StateFailed, time.Time{}, providerErr.Code
	}
	if providerErr.Kind == notifications.ErrorUnknown || !providerErr.PreSend {
		return core.StateUnknown, time.Time{}, providerErr.Code
	}
	if attempt >= e.maxAttempts {
		return core.StateFailed, time.Time{}, providerErr.Code
	}
	delay := e.retryDelays[attempt-1]
	if providerErr.RetryAfter > delay {
		delay = providerErr.RetryAfter
	}
	return core.StateQueued, now.Add(delay), providerErr.Code
}

// String keeps reserve errors free of rendered content and destinations.
func (r reservation) String() string { return fmt.Sprintf("notification reservation %s", r.work.ID) }
