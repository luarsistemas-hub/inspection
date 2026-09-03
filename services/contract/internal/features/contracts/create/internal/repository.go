package internal

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// Repository expresses only the persistence operation required by this use
// case. It is intentionally private to the create-contract slice.
type Repository interface {
	Create(context.Context, Contract) error
}

type gormRepository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) (Repository, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	if err := db.AutoMigrate(&Contract{}); err != nil {
		return nil, fmt.Errorf("migrate contract schema: %w", err)
	}
	return gormRepository{db: db}, nil
}

func (r gormRepository) Create(ctx context.Context, contract Contract) error {
	return r.db.WithContext(ctx).Create(&contract).Error
}
