// Command inspection-keycloak-check verifies the configured bootstrap subject.
package main

import (
	"context"
	"fmt"
	"os"

	"inspection/services/inspection/internal/platform/config"
	"inspection/services/inspection/internal/platform/keycloak"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "inspection-keycloak-check:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	client := keycloak.ProvisioningClient{
		BaseURL:      cfg.KeycloakAdminURL,
		Realm:        cfg.KeycloakRealm,
		ClientID:     cfg.KeycloakClientID,
		ClientSecret: cfg.KeycloakClientSecret,
		Stage:        cfg.Stage,
	}
	return client.VerifyUserID(ctx, "inspection-super-admin", cfg.SuperAdminSubject)
}
