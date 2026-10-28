// Package core owns baseline invitations and counterparty responses.
package core

import (
	"context"
	"errors"
	"time"

	"inspection/libs/identity"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

// Service owns the external counterparty lifecycle.
type Service struct {
	DB             *gorm.DB
	Pepper         []byte
	Limits         invitationcore.Limits
	Notifier       invitationcore.Notifier
	Notifications  notificationcore.NotificationService
	CaptureBaseURL string
	Inventory      inventorycore.Service
	Clock          func() time.Time
	Within         func(context.Context, identity.ID, func(*gorm.DB) error) error
	Stage          string
}

func (s Service) now() time.Time {
	if s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}

func safeInvitationLookupError(err error, field string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperror.New(apperror.NotFound, field, "invitation not found")
	}
	return apperror.Wrap(apperror.DependencyUnavailable, err)
}

func (s Service) within(ctx context.Context, tenantID identity.ID, fn func(*gorm.DB) error) error {
	if s.Within != nil {
		return s.Within(ctx, tenantID, fn)
	}
	return (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, fn)
}
