package main

import (
	"context"
	"fmt"
	"os"

	seedprompt "inspection/services/inspection/internal/features/analysis/seed_prompt"
	"inspection/services/inspection/internal/platform/config"
	"inspection/services/inspection/internal/platform/database"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "inspection-prompt-seed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	db, err := database.Open(cfg.MigrationDatabaseURL)
	if err != nil {
		return err
	}
	return seedprompt.Run(ctx, seedprompt.Dependencies{DB: db})
}
