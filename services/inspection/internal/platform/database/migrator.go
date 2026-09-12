package database

import (
	"context"
	"fmt"

	"inspection/services/inspection/internal/platform/database/migrations"

	"gorm.io/gorm"
)

// Migrator is the only schema-mutating component.
type Migrator struct{ DB *gorm.DB }

func (m Migrator) Migrate(ctx context.Context) error {
	if m.DB == nil {
		return fmt.Errorf("migrator: missing database")
	}
	db := m.DB.WithContext(ctx)
	if err := db.Exec(`CREATE SCHEMA IF NOT EXISTS platform; CREATE SCHEMA IF NOT EXISTS tenancy; CREATE SCHEMA IF NOT EXISTS access; CREATE SCHEMA IF NOT EXISTS audit; CREATE SCHEMA IF NOT EXISTS messaging; CREATE SCHEMA IF NOT EXISTS participants; CREATE SCHEMA IF NOT EXISTS segments; CREATE SCHEMA IF NOT EXISTS templates; CREATE SCHEMA IF NOT EXISTS assets; CREATE SCHEMA IF NOT EXISTS invitations; CREATE SCHEMA IF NOT EXISTS origins; CREATE SCHEMA IF NOT EXISTS capture; CREATE SCHEMA IF NOT EXISTS media; CREATE SCHEMA IF NOT EXISTS recapture; CREATE SCHEMA IF NOT EXISTS notifications; CREATE SCHEMA IF NOT EXISTS schedules; CREATE SCHEMA IF NOT EXISTS inspections; CREATE SCHEMA IF NOT EXISTS projects; CREATE SCHEMA IF NOT EXISTS analysis; CREATE SCHEMA IF NOT EXISTS reports; CREATE SCHEMA IF NOT EXISTS retention; CREATE SCHEMA IF NOT EXISTS dashboard; CREATE SCHEMA IF NOT EXISTS usage; CREATE SCHEMA IF NOT EXISTS onboarding`).Error; err != nil {
		return fmt.Errorf("create schemas: %w", err)
	}
	if err := db.AutoMigrate(Models()...); err != nil {
		return fmt.Errorf("additive migration: %w", err)
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS platform.schema_migrations(version integer PRIMARY KEY, name text NOT NULL, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`).Error; err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(73190421)`).Error; err != nil {
			return err
		}
		applied := map[int]string{}
		rows, err := tx.Raw(`SELECT version, checksum FROM platform.schema_migrations`).Rows()
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var version int
			var checksum string
			if err := rows.Scan(&version, &checksum); err != nil {
				return err
			}
			applied[version] = checksum
		}
		steps, err := migrations.Plan(migrations.Foundation(), applied)
		if err != nil {
			return err
		}
		for _, step := range steps {
			if err := tx.Exec(step.SQL).Error; err != nil {
				return fmt.Errorf("apply migration %d: %w", step.Version, err)
			}
			if err := tx.Exec(`INSERT INTO platform.schema_migrations(version,name,checksum) VALUES (?,?,?)`, step.Version, step.Name, step.Checksum()).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// Compatible reports whether runtime may become ready.
func Compatible(ctx context.Context, db *gorm.DB, minVersion, maxVersion int) error {
	if db == nil {
		return fmt.Errorf("database unavailable")
	}
	var version int
	if err := db.WithContext(ctx).Raw(`SELECT COALESCE(MAX(version),0) FROM platform.schema_migrations`).Scan(&version).Error; err != nil {
		return fmt.Errorf("schema version: %w", err)
	}
	if version < minVersion || version > maxVersion {
		return fmt.Errorf("schema version %d outside compatible range %d..%d", version, minVersion, maxVersion)
	}
	return nil
}
