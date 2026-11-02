package harness

import (
	"context"
	"database/sql"
	"fmt"

	"inspection/services/inspection/internal/platform/database"
)

// NewPostgresFromEnv opens only the configured PostgreSQL databases and applies
// migrations when INSPECTION_TEST_MIGRATE is enabled. It is intended for
// integration cases that exercise database behavior without external services.
func NewPostgresFromEnv(ctx context.Context) (*Harness, error) {
	cfg := ConfigFromEnv()
	admin, err := database.Open(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("integration harness database: %w", err)
	}
	migration := admin
	if cfg.MigrationDatabaseURL != cfg.DatabaseURL {
		migration, err = database.Open(cfg.MigrationDatabaseURL)
		if err != nil {
			closeDB(admin)
			return nil, fmt.Errorf("integration harness migration database: %w", err)
		}
	}
	if cfg.Migrate {
		if err := (database.Migrator{DB: migration}).Migrate(ctx); err != nil {
			closeDB(admin)
			if migration != admin {
				closeDB(migration)
			}
			return nil, fmt.Errorf("integration harness migrate: %w", err)
		}
	}
	primary := admin
	if cfg.RuntimeDatabaseURL != "" {
		primary, err = database.Open(cfg.RuntimeDatabaseURL)
		if err != nil {
			closeDB(admin)
			if migration != admin {
				closeDB(migration)
			}
			return nil, fmt.Errorf("integration harness runtime database: %w", err)
		}
	}
	return &Harness{Config: cfg, DB: primary, AdminDB: admin, MigrationDB: migration}, nil
}

func closeDB(db interface{ DB() (*sql.DB, error) }) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
