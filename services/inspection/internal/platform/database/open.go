package database

import (
	"fmt"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Open creates an application database handle without mutating schema.
func Open(dsn string) (*gorm.DB, error) {
	if dsn == "" {
		return nil, fmt.Errorf("database: empty DSN")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: false})
	if err != nil {
		return nil, fmt.Errorf("database connect: %w", err)
	}
	return db, nil
}
