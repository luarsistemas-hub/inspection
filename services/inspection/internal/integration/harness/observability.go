package harness

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// CountTenant returns the number of rows visible through the same forced-RLS
// transaction boundary used by the application.
func (h *Harness) CountTenant(ctx context.Context, tenantID identity.ID, model any, query string, args ...any) (int64, error) {
	if model == nil {
		return 0, errors.New("integration harness: count model is required")
	}
	var count int64
	err := h.WithinTenant(ctx, tenantID, func(tx *gorm.DB) error {
		statement := tx.Model(model)
		if strings.TrimSpace(query) != "" {
			statement = statement.Where(query, args...)
		}
		return statement.Count(&count).Error
	})
	return count, err
}

// WaitOutbox waits for a durable outbox intent matching the tenant and type.
// When statuses are provided, at least one status must match.
func (h *Harness) WaitOutbox(ctx context.Context, tenantID identity.ID, eventType string, statuses ...string) (database.OutboxIntent, error) {
	if eventType == "" {
		return database.OutboxIntent{}, errors.New("integration harness: outbox type is required")
	}
	var found database.OutboxIntent
	err := h.Eventually(ctx, func() (bool, error) {
		var row database.OutboxIntent
		err := h.WithinTenant(ctx, tenantID, func(tx *gorm.DB) error {
			return tx.Where("type = ?", eventType).Order("created_at DESC").First(&row).Error
		})
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if len(statuses) > 0 {
			matched := false
			for _, status := range statuses {
				if row.Status == status {
					matched = true
					break
				}
			}
			if !matched {
				return false, nil
			}
		}
		found = row
		return true, nil
	})
	return found, err
}

// WaitInbox waits for one consumer receipt, preserving the generation check
// used by replay and duplicate-delivery cases.
func (h *Harness) WaitInbox(ctx context.Context, tenantID identity.ID, consumer string, eventID identity.ID, generation int) (database.InboxReceipt, error) {
	if consumer == "" || eventID == (identity.ID{}) {
		return database.InboxReceipt{}, errors.New("integration harness: inbox identity is required")
	}
	var found database.InboxReceipt
	err := h.Eventually(ctx, func() (bool, error) {
		var row database.InboxReceipt
		err := h.WithinTenant(ctx, tenantID, func(tx *gorm.DB) error {
			return tx.Where("consumer = ? AND event_id = ? AND generation = ?", consumer, eventID, generation).First(&row).Error
		})
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		found = row
		return true, nil
	})
	return found, err
}

// WaitQueue waits until RabbitMQ reports at least min messages in queue.
func (h *Harness) WaitQueue(ctx context.Context, queue string, min int) (int, error) {
	if h == nil || h.Channel == nil {
		return 0, errors.New("integration harness: RabbitMQ channel unavailable")
	}
	if queue == "" || min < 0 {
		return 0, errors.New("integration harness: invalid queue wait")
	}
	var count int
	err := h.Eventually(ctx, func() (bool, error) {
		state, err := h.Channel.QueueInspect(queue)
		if err != nil {
			return false, err
		}
		count = state.Messages
		return count >= min, nil
	})
	return count, err
}

// ResetProviderStubs restores deterministic WireMock mappings between cases.
func (h *Harness) ResetProviderStubs(ctx context.Context) error {
	if h == nil || h.HTTP == nil {
		return errors.New("integration harness: HTTP client unavailable")
	}
	for name, baseURL := range map[string]string{"LiteLLM": h.LiteLLMURL, "Gotenberg": h.GotenbergURL} {
		endpoint := strings.TrimRight(baseURL, "/") + "/__admin/mappings/reset"
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
		if err != nil {
			return fmt.Errorf("%s reset: %w", name, err)
		}
		response, err := h.HTTP.Do(request)
		if err != nil {
			return fmt.Errorf("%s reset: %w", name, err)
		}
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("%s reset status %d", name, response.StatusCode)
		}
	}
	return nil
}
