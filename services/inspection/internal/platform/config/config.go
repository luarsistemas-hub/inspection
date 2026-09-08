package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config contains only process-wide, validated runtime configuration.
type Config struct {
	Environment           string
	TestAuthEnabled       bool
	HTTPAddress           string
	DatabaseURL           string
	DispatcherDatabaseURL string
	MigrationDatabaseURL  string
	AllowedOrigin         string
	AllowedOrigins        []string
	CaptureOrigin         string
	MetricsToken          string
	OIDCIssuer            string
	OIDCAudience          string
	OIDCAudiences         []string
	OIDCJWKSURL           string
	SchemaMin             int
	SchemaMax             int
	ShutdownTimeout       time.Duration
	RuntimeDBRole         string
	StoragePublic         bool
	RabbitMQURL           string
	DragonflyAddress      string
	DragonflyPassword     string
	MinIOEndpoint         string
	MinIOPublicEndpoint   string
	MinIOAccessKey        string
	MinIOSecretKey        string
	MinIOBucket           string
	MinIOSecure           bool
	MinIOPublicSecure     bool
	OTPPepper             string
	SMTPAddress           string
	SMTPFrom              string
	TwilioBaseURL         string
	TwilioAccountSID      string
	TwilioAuthToken       string
	TwilioFrom            string
	TwilioCallbackURL     string
	LiteLLMURL            string
	LiteLLMModelAlias     string
	LiteLLMPromptVersion  string
	GotenbergURL          string
	ProviderTimeout       time.Duration
}

// Load reads and validates environment configuration without requiring a file.
func Load() (Config, error) {
	c := Config{
		Environment: env("INSPECTION_ENV", "local"), HTTPAddress: env("INSPECTION_HTTP_ADDR", ":8080"),
		DatabaseURL: os.Getenv("INSPECTION_DATABASE_URL"), DispatcherDatabaseURL: os.Getenv("INSPECTION_DISPATCHER_DATABASE_URL"), MigrationDatabaseURL: env("INSPECTION_MIGRATION_DATABASE_URL", os.Getenv("INSPECTION_DATABASE_URL")), AllowedOrigin: os.Getenv("INSPECTION_ALLOWED_ORIGIN"), AllowedOrigins: splitExact(os.Getenv("INSPECTION_ALLOWED_ORIGINS")), CaptureOrigin: os.Getenv("INSPECTION_CAPTURE_ORIGIN"),
		MetricsToken: os.Getenv("INSPECTION_METRICS_TOKEN"), OIDCIssuer: os.Getenv("INSPECTION_OIDC_ISSUER"),
		OIDCAudience: os.Getenv("INSPECTION_OIDC_AUDIENCE"), OIDCAudiences: splitExact(os.Getenv("INSPECTION_OIDC_AUDIENCES")), OIDCJWKSURL: os.Getenv("INSPECTION_OIDC_JWKS_URL"), SchemaMin: envInt("INSPECTION_SCHEMA_MIN", 13),
		SchemaMax: envInt("INSPECTION_SCHEMA_MAX", 21), ShutdownTimeout: 10 * time.Second,
		RuntimeDBRole:    env("INSPECTION_RUNTIME_DB_ROLE", "inspection_runtime"),
		StoragePublic:    strings.EqualFold(os.Getenv("INSPECTION_STORAGE_PUBLIC"), "true"),
		RabbitMQURL:      env("INSPECTION_RABBITMQ_URL", "amqp://inspection:inspection@localhost:5672/"),
		DragonflyAddress: env("INSPECTION_DRAGONFLY_ADDRESS", "localhost:6379"), DragonflyPassword: os.Getenv("INSPECTION_DRAGONFLY_PASSWORD"),
		MinIOEndpoint: env("INSPECTION_MINIO_ENDPOINT", "localhost:9000"), MinIOPublicEndpoint: env("INSPECTION_MINIO_PUBLIC_ENDPOINT", ""), MinIOAccessKey: env("INSPECTION_MINIO_ACCESS_KEY", "inspection"), MinIOSecretKey: env("INSPECTION_MINIO_SECRET_KEY", "inspection-local-secret"), MinIOBucket: env("INSPECTION_MINIO_BUCKET", "inspection-private"), MinIOSecure: strings.EqualFold(os.Getenv("INSPECTION_MINIO_SECURE"), "true"), MinIOPublicSecure: strings.EqualFold(os.Getenv("INSPECTION_MINIO_PUBLIC_SECURE"), "true"),
		OTPPepper: env("INSPECTION_OTP_PEPPER", "local-development-pepper-change-me-32"), SMTPAddress: env("INSPECTION_SMTP_ADDRESS", "localhost:1025"), SMTPFrom: env("INSPECTION_SMTP_FROM", "inspection@localhost"),
		TwilioBaseURL: env("INSPECTION_TWILIO_BASE_URL", "http://localhost:1080"), TwilioAccountSID: env("INSPECTION_TWILIO_ACCOUNT_SID", "AC-local"), TwilioAuthToken: env("INSPECTION_TWILIO_AUTH_TOKEN", "local-token"), TwilioFrom: env("INSPECTION_TWILIO_FROM", "+15550000000"), TwilioCallbackURL: env("INSPECTION_TWILIO_CALLBACK_URL", "http://localhost:8080/webhooks/twilio/status"), LiteLLMURL: env("INSPECTION_LITELLM_URL", "http://localhost:18080"), LiteLLMModelAlias: env("INSPECTION_LITELLM_MODEL_ALIAS", "inspection-vision"), LiteLLMPromptVersion: env("INSPECTION_LITELLM_PROMPT_VERSION", "analysis-v1"), GotenbergURL: env("INSPECTION_GOTENBERG_URL", "http://localhost:18081"), ProviderTimeout: envDuration("INSPECTION_PROVIDER_TIMEOUT", 30*time.Second),
	}
	if c.MinIOPublicEndpoint == "" {
		c.MinIOPublicEndpoint = c.MinIOEndpoint
		c.MinIOPublicSecure = c.MinIOSecure
	}
	c.TestAuthEnabled = c.Environment == "test" && strings.EqualFold(os.Getenv("INSPECTION_TEST_AUTH_ENABLED"), "true")
	if len(c.AllowedOrigins) == 0 && c.AllowedOrigin != "" {
		c.AllowedOrigins = []string{c.AllowedOrigin}
	} else if c.AllowedOrigin == "" && len(c.AllowedOrigins) > 0 {
		c.AllowedOrigin = c.AllowedOrigins[0]
	}
	if len(c.OIDCAudiences) == 0 && c.OIDCAudience != "" {
		c.OIDCAudiences = []string{c.OIDCAudience}
	} else if c.OIDCAudience == "" && len(c.OIDCAudiences) > 0 {
		c.OIDCAudience = c.OIDCAudiences[0]
	}
	return c, c.Validate()
}

func splitExact(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
func envDuration(key string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(os.Getenv(key))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err == nil {
		return value
	}
	return fallback
}

// Validate fails closed for security-sensitive values.
func (c Config) Validate() error {
	if len(c.AllowedOrigins) == 0 && c.AllowedOrigin != "" {
		c.AllowedOrigins = []string{c.AllowedOrigin}
	}
	if len(c.OIDCAudiences) == 0 && c.OIDCAudience != "" {
		c.OIDCAudiences = []string{c.OIDCAudience}
	}
	if c.DatabaseURL == "" || c.MigrationDatabaseURL == "" || len(c.AllowedOrigins) == 0 || c.OIDCIssuer == "" || len(c.OIDCAudiences) == 0 {
		return fmt.Errorf("configuration: missing required endpoint or OIDC setting")
	}
	seenOrigins := map[string]struct{}{}
	for _, raw := range c.AllowedOrigins {
		origin, err := url.Parse(raw)
		if err != nil || origin.Scheme == "" || origin.Host == "" || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || strings.Contains(raw, "*") || origin.User != nil || (c.Environment != "local" && origin.Scheme != "https") {
			return fmt.Errorf("configuration: invalid allowed origin")
		}
		if _, ok := seenOrigins[raw]; ok {
			return fmt.Errorf("configuration: duplicate allowed origin")
		}
		seenOrigins[raw] = struct{}{}
	}
	if c.CaptureOrigin != "" {
		if _, ok := seenOrigins[c.CaptureOrigin]; !ok {
			return fmt.Errorf("configuration: capture origin is not allowed")
		}
	}
	seenAudiences := map[string]struct{}{}
	for _, audience := range c.OIDCAudiences {
		if audience == "" {
			return fmt.Errorf("configuration: invalid OIDC audience")
		}
		if _, ok := seenAudiences[audience]; ok {
			return fmt.Errorf("configuration: duplicate OIDC audience")
		}
		seenAudiences[audience] = struct{}{}
	}
	if c.StoragePublic {
		return fmt.Errorf("configuration: public storage is forbidden")
	}
	if c.OIDCJWKSURL != "" {
		parsed, parseErr := url.Parse(c.OIDCJWKSURL)
		if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("configuration: invalid OIDC JWKS endpoint")
		}
	}
	role := strings.ToLower(c.RuntimeDBRole)
	if role == "postgres" || role == "root" || role == "inspection_migrator" {
		return fmt.Errorf("configuration: privileged runtime database role")
	}
	if c.SchemaMin <= 0 || c.SchemaMax < c.SchemaMin {
		return fmt.Errorf("configuration: invalid schema compatibility range")
	}
	for name, endpoint := range map[string]string{"LiteLLM": c.LiteLLMURL, "Gotenberg": c.GotenbergURL} {
		if endpoint == "" {
			continue
		}
		parsed, parseErr := url.Parse(endpoint)
		if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || strings.Contains(endpoint, "*") {
			return fmt.Errorf("configuration: invalid %s endpoint", name)
		}
	}
	if c.ProviderTimeout < 0 {
		return fmt.Errorf("configuration: invalid provider timeout")
	}
	if c.Environment != "local" && c.MetricsToken == "" {
		return fmt.Errorf("configuration: missing metrics secret")
	}
	return nil
}
