package session

import (
	"context"
	"errors"

	"inspection/services/inspection/internal/platform/notifications"
)

type RegistryNotifier struct{ Registry *notifications.Registry }

func (n RegistryNotifier) SendOTP(ctx context.Context, destination, code string) error {
	if n.Registry == nil {
		return errors.New("onboarding notifier: missing registry")
	}
	result := notifications.Deliver(ctx, n.Registry, map[notifications.Channel]notifications.Intent{
		notifications.Email: {ID: "onboarding:" + destination, Destination: destination, Template: "onboarding-otp", Parameters: map[string]string{"code": code}},
	})
	if result.Status != "DELIVERED" {
		return errors.New("onboarding notifier: delivery failed")
	}
	return nil
}
