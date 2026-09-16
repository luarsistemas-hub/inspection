package database

import (
	"fmt"
	"log"
	"os"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open creates an application database handle without mutating schema.
func Open(dsn string) (*gorm.DB, error) {
	if dsn == "" {
		return nil, fmt.Errorf("database: empty DSN")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: false,
		Logger: logger.New(log.New(os.Stdout, "\r\n", log.LstdFlags), logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  logger.Error,
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		}),
	})
	if err != nil {
		return nil, fmt.Errorf("database connect: %w", err)
	}
	return db, nil
}
