package harness

import (
	"os"
	"strconv"
	"time"
)

// Config describes the endpoints used by the external integration suite.
// Values are intentionally environment-driven so the same harness can run
// against Docker Compose, CI services, or an ephemeral test environment.
type Config struct {
	DatabaseURL             string
	RuntimeDatabaseURL      string
	RuntimeDatabasePassword string
	MigrationDatabaseURL    string
	RabbitMQURL             string
	APIURL                  string
	LiteLLMURL              string
	GotenbergURL            string
	MinIOEndpoint           string
	MinIOAccessKey          string
	MinIOSecretKey          string
	MinIOBucket             string
	MinIOSecure             bool
	RequestTimeout          time.Duration
	PollInterval            time.Duration
	Migrate                 bool
}

// ConfigFromEnv returns the integration defaults and applies test-specific
// overrides before the regular service environment variables.
func ConfigFromEnv() Config {
	cfg := Config{
		DatabaseURL:             firstEnv("INSPECTION_TEST_DATABASE_URL", "INSPECTION_DATABASE_URL", "postgres://postgres:postgres@localhost:5432/inspection?sslmode=disable"),
		RuntimeDatabaseURL:      firstEnv("INSPECTION_TEST_RUNTIME_DATABASE_URL", "INSPECTION_RUNTIME_DATABASE_URL"),
		RuntimeDatabasePassword: firstEnv("INSPECTION_TEST_RUNTIME_PASSWORD", "INSPECTION_RUNTIME_DATABASE_PASSWORD"),
		MigrationDatabaseURL:    firstEnv("INSPECTION_TEST_MIGRATION_DATABASE_URL", "INSPECTION_TEST_DATABASE_URL", "INSPECTION_DATABASE_URL", "postgres://postgres:postgres@localhost:5432/inspection?sslmode=disable"),
		RabbitMQURL:             firstEnv("INSPECTION_TEST_RABBITMQ_URL", "INSPECTION_RABBITMQ_URL", "amqp://inspection:inspection@localhost:5672/"),
		APIURL:                  firstEnv("INSPECTION_TEST_API_URL", "INSPECTION_API_URL", "http://localhost:8080"),
		LiteLLMURL:              firstEnv("INSPECTION_TEST_LITELLM_URL", "INSPECTION_LITELLM_URL", "http://localhost:18080"),
		GotenbergURL:            firstEnv("INSPECTION_TEST_GOTENBERG_URL", "INSPECTION_GOTENBERG_URL", "http://localhost:18081"),
		MinIOEndpoint:           firstEnv("INSPECTION_TEST_MINIO_ENDPOINT", "INSPECTION_MINIO_ENDPOINT", "localhost:9000"),
		MinIOAccessKey:          firstEnv("INSPECTION_TEST_MINIO_ACCESS_KEY", "INSPECTION_MINIO_ACCESS_KEY", "contract"),
		MinIOSecretKey:          firstEnv("INSPECTION_TEST_MINIO_SECRET_KEY", "INSPECTION_MINIO_SECRET_KEY", "contract"),
		MinIOBucket:             firstEnv("INSPECTION_TEST_MINIO_BUCKET", "INSPECTION_MINIO_BUCKET", "inspection-private"),
		MinIOSecure:             envBool("INSPECTION_TEST_MINIO_SECURE", false),
		RequestTimeout:          envDuration("INSPECTION_TEST_TIMEOUT", 30*time.Second),
		PollInterval:            envDuration("INSPECTION_TEST_POLL_INTERVAL", 100*time.Millisecond),
		Migrate:                 envBool("INSPECTION_TEST_MIGRATE", true),
	}
	if cfg.RuntimeDatabasePassword == "" {
		cfg.RuntimeDatabasePassword = "runtime-test"
	}
	if cfg.DatabaseURL == "" {
		cfg.DatabaseURL = "postgres://postgres:postgres@localhost:5432/inspection?sslmode=disable"
	}
	if cfg.MigrationDatabaseURL == "" {
		cfg.MigrationDatabaseURL = cfg.DatabaseURL
	}
	if cfg.RabbitMQURL == "" {
		cfg.RabbitMQURL = "amqp://inspection:inspection@localhost:5672/"
	}
	if cfg.APIURL == "" {
		cfg.APIURL = "http://localhost:8080"
	}
	if cfg.LiteLLMURL == "" {
		cfg.LiteLLMURL = "http://localhost:18080"
	}
	if cfg.GotenbergURL == "" {
		cfg.GotenbergURL = "http://localhost:18081"
	}
	if cfg.MinIOEndpoint == "" {
		cfg.MinIOEndpoint = "localhost:9000"
	}
	if cfg.MinIOAccessKey == "" {
		cfg.MinIOAccessKey = "contract"
	}
	if cfg.MinIOSecretKey == "" {
		cfg.MinIOSecretKey = "contract"
	}
	if cfg.MinIOBucket == "" {
		cfg.MinIOBucket = "inspection-private"
	}
	return cfg
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(os.Getenv(key))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	value, err := strconv.ParseBool(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return value
}
