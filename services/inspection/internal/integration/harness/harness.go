package harness

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/tenanttx"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
)

// Harness owns the connections shared by one external integration run.
// Callers should create one harness per test process and close it when the
// process exits.
type Harness struct {
	Config
	DB          *gorm.DB
	AdminDB     *gorm.DB
	MigrationDB *gorm.DB
	Rabbit      *amqp.Connection
	Channel     *amqp.Channel
	Publisher   *messaging.RabbitPublisher
	Store       objectstore.Store
	HTTP        *http.Client
}

// New connects to the configured dependencies and, by default, applies the
// additive schema migrations before returning.
func New(ctx context.Context, cfg Config) (*Harness, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg.DatabaseURL == "" || cfg.RabbitMQURL == "" {
		return nil, errors.New("integration harness: database and RabbitMQ URLs are required")
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 30 * time.Second
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 100 * time.Millisecond
	}
	migrationURL := cfg.MigrationDatabaseURL
	if migrationURL == "" {
		migrationURL = cfg.DatabaseURL
	}
	adminDB, err := database.Open(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("integration harness database: %w", err)
	}
	primary := adminDB
	migrationDB := adminDB
	if migrationURL != cfg.DatabaseURL {
		migrationDB, err = database.Open(migrationURL)
		if err != nil {
			return nil, fmt.Errorf("integration harness migration database: %w", err)
		}
	}
	if cfg.Migrate {
		if err := (database.Migrator{DB: migrationDB}).Migrate(ctx); err != nil {
			return nil, fmt.Errorf("integration harness migrate: %w", err)
		}
	}
	if cfg.RuntimeDatabaseURL != "" {
		if cfg.RuntimeDatabasePassword == "" {
			return nil, errors.New("integration harness: runtime database password is required")
		}
		password := strings.ReplaceAll(cfg.RuntimeDatabasePassword, "'", "''")
		if err := adminDB.Exec("ALTER ROLE inspection_runtime LOGIN PASSWORD '" + password + "'").Error; err != nil {
			return nil, fmt.Errorf("integration harness configure runtime role: %w", err)
		}
		primary, err = database.Open(cfg.RuntimeDatabaseURL)
		if err != nil {
			return nil, fmt.Errorf("integration harness runtime database: %w", err)
		}
	}
	connection, err := amqp.DialConfig(cfg.RabbitMQURL, amqp.Config{Properties: amqp.NewConnectionProperties()})
	if err != nil {
		return nil, fmt.Errorf("integration harness RabbitMQ: %w", err)
	}
	channel, err := connection.Channel()
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("integration harness RabbitMQ channel: %w", err)
	}
	publisher, err := messaging.NewRabbitPublisher(channel)
	if err != nil {
		_ = channel.Close()
		_ = connection.Close()
		return nil, fmt.Errorf("integration harness publisher: %w", err)
	}
	minioClient, err := objectstore.NewMinIO(cfg.MinIOEndpoint, cfg.MinIOAccessKey, cfg.MinIOSecretKey, cfg.MinIOSecure)
	if err != nil {
		_ = channel.Close()
		_ = connection.Close()
		return nil, fmt.Errorf("integration harness MinIO: %w", err)
	}
	return &Harness{Config: cfg, DB: primary, AdminDB: adminDB, MigrationDB: migrationDB, Rabbit: connection, Channel: channel, Publisher: publisher, Store: objectstore.Store{Bucket: cfg.MinIOBucket, Client: minioClient}, HTTP: &http.Client{Timeout: cfg.RequestTimeout}}, nil
}

// NewFromEnv creates a harness using ConfigFromEnv.
func NewFromEnv(ctx context.Context) (*Harness, error) {
	return New(ctx, ConfigFromEnv())
}

// Close releases all network and database resources owned by the harness.
func (h *Harness) Close() error {
	if h == nil {
		return nil
	}
	var errs []error
	if h.Channel != nil {
		if err := h.Channel.Close(); err != nil && !strings.Contains(err.Error(), "already closed") {
			errs = append(errs, err)
		}
	}
	if h.Rabbit != nil {
		if err := h.Rabbit.Close(); err != nil && !strings.Contains(err.Error(), "already closed") {
			errs = append(errs, err)
		}
	}
	if h.DB != nil {
		if sqlDB, err := h.DB.DB(); err == nil {
			if err := sqlDB.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if h.AdminDB != nil && h.AdminDB != h.DB && h.AdminDB != h.MigrationDB {
		if sqlDB, err := h.AdminDB.DB(); err == nil {
			if err := sqlDB.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if h.MigrationDB != nil && h.MigrationDB != h.DB && h.MigrationDB != h.AdminDB {
		if sqlDB, err := h.MigrationDB.DB(); err == nil {
			if err := sqlDB.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// Declare declares the queues needed by a worker under test.
func (h *Harness) Declare(contracts ...messaging.QueueContract) error {
	if h == nil || h.Channel == nil {
		return errors.New("integration harness: RabbitMQ channel unavailable")
	}
	return messaging.DeclareTopology(h.Channel, contracts)
}

// WithinTenant runs database assertions with the same transaction-local RLS
// context used by production feature slices.
func (h *Harness) WithinTenant(ctx context.Context, tenantID identity.ID, fn func(*gorm.DB) error) error {
	if h == nil {
		return errors.New("integration harness: nil harness")
	}
	return (tenanttx.Runner{DB: h.DB}).Within(ctx, tenantID, fn)
}
