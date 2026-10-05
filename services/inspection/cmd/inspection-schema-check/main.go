// Command inspection-schema-check verifies the database against this image's schema range.
package main

import (
	"context"
	"fmt"
	"os"

	"inspection/services/inspection/internal/platform/config"
	"inspection/services/inspection/internal/platform/database"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "inspection-schema-check:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	db, err := database.Open(cfg.RuntimeDatabaseURL)
	if err != nil {
		return err
	}
	return database.Compatible(ctx, db, cfg.SchemaMin, cfg.SchemaMax)
}
