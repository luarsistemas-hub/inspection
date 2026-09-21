package database

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"inspection/libs/identity"
	analysisprompt "inspection/services/inspection/internal/features/analysis/prompt"
	"inspection/services/inspection/internal/platform/database/migrations"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	if err := repairLegacyAnalysisProfileTenantIDs(db); err != nil {
		return err
	}
	models := Models()
	// Version 2 created the legacy tenant-scoped profile tables before the
	// global prompt migration removed them. Bootstrap those tables only on a
	// brand-new database so subsequent AutoMigrate runs cannot recreate them.
	var migrationTable *string
	if err := db.Raw(`SELECT to_regclass('platform.schema_migrations')`).Scan(&migrationTable).Error; err != nil {
		return fmt.Errorf("inspect migration history: %w", err)
	}
	if migrationTable == nil {
		models = append(models, &legacyAnalysisProfile{}, &legacyAnalysisProfileVersion{})
	}
	if err := db.AutoMigrate(models...); err != nil {
		return fmt.Errorf("additive migration: %w", err)
	}
	if err := ensureAnalysisPromptSeed(db); err != nil {
		return err
	}
	if err := ensureLegacyAnalysisPromptColumns(db); err != nil {
		return err
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

func ensureAnalysisPromptSeed(db *gorm.DB) error {
	definition, digest, err := analysisprompt.Canonicalize(analysisprompt.DefaultDefinition())
	if err != nil {
		return fmt.Errorf("canonicalize analysis prompt seed: %w", err)
	}
	now := time.Now().UTC()
	row := AnalysisPrompt{ID: identity.NewID(), AnalysisType: analysisprompt.RealEstate, DefinitionJSON: definition, CanonicalDigest: digest, Revision: 1, UpdatedBy: identity.ID{}, CreatedAt: now, UpdatedAt: now}
	if err := db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "analysis_type"}}, DoNothing: true}).Create(&row).Error; err != nil {
		return fmt.Errorf("seed analysis prompt: %w", err)
	}
	var current AnalysisPrompt
	if err := db.Where("analysis_type=?", analysisprompt.RealEstate).First(&current).Error; err != nil {
		return fmt.Errorf("find analysis prompt seed: %w", err)
	}
	var snapshot AnalysisPromptSnapshot
	if err := db.Where("analysis_type=? AND canonical_digest=?", current.AnalysisType, current.CanonicalDigest).First(&snapshot).Error; err == gorm.ErrRecordNotFound {
		snapshot = AnalysisPromptSnapshot{ID: identity.NewID(), AnalysisType: current.AnalysisType, DefinitionJSON: current.DefinitionJSON, CanonicalDigest: current.CanonicalDigest, CreatedAt: now}
		if err := db.Create(&snapshot).Error; err != nil {
			return fmt.Errorf("seed analysis prompt snapshot: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("find analysis prompt snapshot: %w", err)
	}
	return nil
}

func repairLegacyAnalysisProfileTenantIDs(db *gorm.DB) error {
	const query = `
DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM information_schema.columns
    WHERE table_schema = 'templates'
      AND table_name = 'analysis_profiles'
      AND column_name = 'tenant_id'
      AND udt_name <> 'uuid'
  ) THEN
    ALTER TABLE templates.analysis_profiles
      ALTER COLUMN tenant_id TYPE uuid USING tenant_id::uuid;
  END IF;
  IF EXISTS (
    SELECT 1
    FROM information_schema.columns
    WHERE table_schema = 'templates'
      AND table_name = 'analysis_profile_versions'
      AND column_name = 'tenant_id'
      AND udt_name <> 'uuid'
  ) THEN
    ALTER TABLE templates.analysis_profile_versions
      ALTER COLUMN tenant_id TYPE uuid USING tenant_id::uuid;
  END IF;
END $$`
	if err := db.Exec(query).Error; err != nil {
		return fmt.Errorf("repair legacy analysis profile tenant ids: %w", err)
	}
	return nil
}

func ensureLegacyAnalysisPromptColumns(db *gorm.DB) error {
	var migrationTable *string
	if err := db.Raw(`SELECT to_regclass('platform.schema_migrations')`).Scan(&migrationTable).Error; err != nil {
		return fmt.Errorf("inspect migration history for legacy analysis columns: %w", err)
	}
	if migrationTable != nil {
		var latestVersion int
		if err := db.Raw(`SELECT COALESCE(MAX(version), 0) FROM platform.schema_migrations`).Scan(&latestVersion).Error; err != nil {
			return fmt.Errorf("inspect latest migration for legacy analysis columns: %w", err)
		}
		if latestVersion >= 37 {
			return dropLegacyAnalysisPromptColumns(db)
		}
	}
	const query = `
ALTER TABLE inspections.inspections
  ADD COLUMN IF NOT EXISTS analysis_profile_version_id uuid;
ALTER TABLE analysis.comparison_jobs
  ADD COLUMN IF NOT EXISTS prompt_version varchar(200) NOT NULL DEFAULT '';
ALTER TABLE analysis.analysis_runs
  ADD COLUMN IF NOT EXISTS prompt_version varchar(200) NOT NULL DEFAULT '';
ALTER TABLE analysis.classification_runs
  ADD COLUMN IF NOT EXISTS profile_version_id uuid;
ALTER TABLE usage.records
  ADD COLUMN IF NOT EXISTS prompt_version varchar(200) NOT NULL DEFAULT '';`
	if err := db.Exec(query).Error; err != nil {
		return fmt.Errorf("ensure legacy analysis prompt columns: %w", err)
	}
	return nil
}

func dropLegacyAnalysisPromptColumns(db *gorm.DB) error {
	const query = `
ALTER TABLE inspections.inspections
  DROP COLUMN IF EXISTS analysis_profile_version_id;
ALTER TABLE analysis.comparison_jobs
  DROP COLUMN IF EXISTS prompt_version;
ALTER TABLE analysis.analysis_runs
  DROP COLUMN IF EXISTS prompt_version;
ALTER TABLE analysis.classification_runs
  DROP COLUMN IF EXISTS profile_version_id;
ALTER TABLE usage.records
  DROP COLUMN IF EXISTS prompt_version;`
	if err := db.Exec(query).Error; err != nil {
		return fmt.Errorf("drop legacy analysis prompt columns: %w", err)
	}
	return nil
}

// These bootstrap-only shapes let the immutable historical migration run on a
// fresh database. They are never part of the runtime model allowlist and are
// dropped by the global prompt migration in the same migrate invocation.
type legacyAnalysisProfile struct {
	ID              identity.ID `gorm:"type:uuid;primaryKey"`
	TenantID        identity.ID `gorm:"type:uuid;not null;uniqueIndex:idx_profile_key,priority:1"`
	Key             string      `gorm:"size:100;not null;uniqueIndex:idx_profile_key,priority:2"`
	ActiveVersionID identity.ID `gorm:"type:uuid;not null"`
	Version         int64       `gorm:"not null;default:1"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (legacyAnalysisProfile) TableName() string { return "templates.analysis_profiles" }

type legacyAnalysisProfileVersion struct {
	ID              identity.ID     `gorm:"type:uuid;primaryKey"`
	TenantID        identity.ID     `gorm:"type:uuid;not null;uniqueIndex:idx_profile_key_version,priority:1;uniqueIndex:idx_profile_publish_idempotency,priority:1"`
	ProfileID       identity.ID     `gorm:"type:uuid;not null;index"`
	Key             string          `gorm:"size:100;not null;uniqueIndex:idx_profile_key_version,priority:2"`
	VersionNumber   int             `gorm:"not null;uniqueIndex:idx_profile_key_version,priority:3"`
	SchemaVersion   int             `gorm:"not null"`
	DefinitionJSON  json.RawMessage `gorm:"type:jsonb;not null"`
	CanonicalDigest string          `gorm:"size:64;not null"`
	Status          string          `gorm:"size:16;not null"`
	IdempotencyKey  string          `gorm:"size:200;not null;uniqueIndex:idx_profile_publish_idempotency,priority:2"`
	PublishedAt     time.Time       `gorm:"not null"`
	CreatedBy       identity.ID     `gorm:"type:uuid;not null"`
}

func (legacyAnalysisProfileVersion) TableName() string { return "templates.analysis_profile_versions" }

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
