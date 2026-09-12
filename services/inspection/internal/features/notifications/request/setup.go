// Package request accepts one durable operational notification request.
package request

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/features/notifications/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	platformnotifications "inspection/services/inspection/internal/platform/notifications"
	"inspection/services/inspection/internal/platform/observability"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Dependencies are infrastructure dependencies for the request slice.
type Dependencies struct {
	DB        *gorm.DB
	Catalog   core.Catalog
	Providers *platformnotifications.ProviderResolver
	Payloads  *platformnotifications.PayloadCipher
	Metrics   *observability.Metrics
	Now       func() time.Time
	// V2ProducersEnabled is the deployment-controlled rollout gate. It must be
	// enabled only after compatible consumers have been deployed.
	V2ProducersEnabled bool
}

var ErrV2ProducersDisabled = errors.New("notifications/request: v2 producers are disabled")

// Setup constructs the provider-neutral notification service. Catalog may be
// zero only to select the built-in, versioned operational catalog.
func Setup(dependencies Dependencies) (core.NotificationService, error) {
	if dependencies.DB == nil {
		return nil, fmt.Errorf("notifications/request: missing database")
	}
	if len(dependencies.Catalog.Keys()) == 0 {
		dependencies.Catalog = core.DefaultCatalog()
	}
	if dependencies.Now == nil {
		dependencies.Now = time.Now
	}
	if dependencies.Providers == nil {
		var err error
		dependencies.Providers, err = platformnotifications.NewProviderResolver(platformnotifications.ProviderTwilio)
		if err != nil {
			return nil, err
		}
	}
	return service{db: dependencies.DB, catalog: dependencies.Catalog, providers: dependencies.Providers, payloads: dependencies.Payloads, metrics: dependencies.Metrics, now: dependencies.Now, v2ProducersEnabled: dependencies.V2ProducersEnabled}, nil
}

type service struct {
	db                 *gorm.DB
	catalog            core.Catalog
	providers          *platformnotifications.ProviderResolver
	payloads           *platformnotifications.PayloadCipher
	metrics            *observability.Metrics
	now                func() time.Time
	v2ProducersEnabled bool
}

type transactionKey struct{}

// InTransaction binds a business transaction to a request context. The public
// NotificationService contract remains free of ORM types, while a caller that
// already owns a transaction gets atomic domain change, delivery, and outbox.
func InTransaction(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, transactionKey{}, tx)
}

func (s service) Send(ctx context.Context, notification core.Notification) (core.NotificationResult, error) {
	if err := core.Validate(notification, s.catalog); err != nil {
		return core.NotificationResult{}, err
	}
	if !s.v2ProducersEnabled {
		return core.NotificationResult{}, ErrV2ProducersDisabled
	}
	digest, err := core.CanonicalDigest(notification)
	if err != nil {
		return core.NotificationResult{}, err
	}
	if tx, ok := ctx.Value(transactionKey{}).(*gorm.DB); ok && tx != nil {
		return s.persist(ctx, tx, notification, digest)
	}
	var result core.NotificationResult
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var persistErr error
		result, persistErr = s.persist(ctx, tx, notification, digest)
		return persistErr
	})
	return result, err
}

func (s service) persist(ctx context.Context, tx *gorm.DB, notification core.Notification, digest string) (core.NotificationResult, error) {
	provider, err := s.providers.ProviderFor(platformnotifications.Channel(notification.Channel))
	if err != nil {
		return core.NotificationResult{}, err
	}
	var existing database.Delivery
	err = tx.WithContext(ctx).Where("tenant_id=? AND idempotency_key=?", notification.TenantID, notification.IdempotencyKey).First(&existing).Error
	if err == nil {
		if existing.RequestDigest != digest {
			return core.NotificationResult{}, &core.Error{Code: core.IdempotencyConflict, Field: "idempotencyKey"}
		}
		return core.NotificationResult{ID: existing.ID, State: core.State(existing.Status), Reused: true}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return core.NotificationResult{}, err
	}
	now := s.now().UTC()
	delivery := database.Delivery{
		ID: identity.NewID(), TenantID: notification.TenantID, IntentID: identity.NewID(),
		Status: string(core.StateQueued), LogicalTemplate: notification.Template.Name,
		TemplateVersion: notification.Template.Version, CorrelationID: notification.CorrelationID,
		IdempotencyKey: notification.IdempotencyKey, RequestDigest: digest,
		RecipientID: notification.Recipient.ID, SelectedProvider: string(provider), CreatedAt: now, UpdatedAt: now,
	}
	create := tx.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "idempotency_key"}}, DoNothing: true}).Create(&delivery)
	if create.Error != nil {
		return core.NotificationResult{}, create.Error
	}
	if create.RowsAffected == 0 {
		if err := tx.WithContext(ctx).Where("tenant_id=? AND idempotency_key=?", notification.TenantID, notification.IdempotencyKey).First(&existing).Error; err != nil {
			return core.NotificationResult{}, err
		}
		if existing.RequestDigest != digest {
			return core.NotificationResult{}, &core.Error{Code: core.IdempotencyConflict, Field: "idempotencyKey"}
		}
		return core.NotificationResult{ID: existing.ID, State: core.State(existing.Status), Reused: true}, nil
	}
	if delivery.ID == (identity.ID{}) {
		return core.NotificationResult{}, fmt.Errorf("notifications/request: delivery was not persisted")
	}
	variables := cloneVariables(notification.Variables)
	if notification.Execution != nil {
		if s.payloads == nil {
			return core.NotificationResult{}, fmt.Errorf("notifications/request: sensitive payload cipher is unavailable")
		}
		delete(variables, notification.Execution.URLVariable)
		plain, marshalErr := json.Marshal(notification.Execution)
		if marshalErr != nil {
			return core.NotificationResult{}, marshalErr
		}
		keyID, nonce, ciphertext, encryptErr := s.payloads.Encrypt(plain, payloadAAD(notification.TenantID, delivery.ID))
		if encryptErr != nil {
			return core.NotificationResult{}, encryptErr
		}
		var expiresAt *time.Time
		if notification.Execution.ExpiresAt > 0 {
			value := time.Unix(notification.Execution.ExpiresAt, 0).UTC()
			expiresAt = &value
		}
		payload := database.NotificationPayload{ID: identity.NewID(), TenantID: notification.TenantID, DeliveryID: delivery.ID, KeyID: keyID, Nonce: nonce, Ciphertext: ciphertext, CreatedAt: now, ExpiresAt: expiresAt}
		if err := tx.WithContext(ctx).Create(&payload).Error; err != nil {
			return core.NotificationResult{}, err
		}
	}
	encodedVariables, err := json.Marshal(variables)
	if err != nil {
		return core.NotificationResult{}, err
	}
	channelWork := database.ChannelAttempt{
		ID: identity.NewID(), TenantID: notification.TenantID, DeliveryID: delivery.ID,
		Channel: string(notification.Channel), Destination: notification.Recipient.Destination,
		Status: string(core.StateQueued), Provider: string(provider), TemplateVariables: encodedVariables, NextAttemptAt: now,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := tx.WithContext(ctx).Create(&channelWork).Error; err != nil {
		return core.NotificationResult{}, err
	}
	payload, err := json.Marshal(struct {
		NotificationID identity.ID `json:"notificationId"`
	}{NotificationID: delivery.ID})
	if err != nil {
		return core.NotificationResult{}, err
	}
	envelope := events.Envelope[json.RawMessage]{
		ID: identity.NewID(), Type: "notification.delivery_requested.v2", SchemaVersion: 2,
		OccurredAt: now, TenantID: notification.TenantID, AggregateID: delivery.ID,
		CorrelationID: notification.CorrelationID, Payload: payload,
	}
	if err := messaging.AddOutbox(tx.WithContext(ctx), envelope); err != nil {
		return core.NotificationResult{}, err
	}
	s.metrics.Request(string(notification.Channel), string(provider))
	return core.NotificationResult{ID: delivery.ID, State: core.StateQueued}, nil
}

func payloadAAD(tenantID, deliveryID identity.ID) []byte {
	return []byte(tenantID.String() + ":" + deliveryID.String())
}

func cloneVariables(values map[string]string) map[string]string {
	copy := make(map[string]string, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}
