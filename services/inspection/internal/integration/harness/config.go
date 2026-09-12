package harness

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
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
	TwilioURL               string
	MetaURL                 string
	MailpitURL              string
	MailpitSMTPAddress      string
	MinIOEndpoint           string
	MinIOAccessKey          string
	MinIOSecretKey          string
	MinIOBucket             string
	MinIOSecure             bool
	RequestTimeout          time.Duration
	PollInterval            time.Duration
	Migrate                 bool
}

// LiveSmokeConfig contains only explicit live-provider settings. It never
// inherits local defaults and is used solely by opt-in deployment acceptance.
type LiveSmokeConfig struct {
	Enabled                 bool
	AllowlistedRecipients   map[string]bool
	TwilioBaseURL           string
	TwilioAccountSID        string
	TwilioAuthToken         string
	TwilioSMSFrom           string
	TwilioWhatsAppFrom      string
	TwilioSMSRecipient      string
	TwilioWhatsAppRecipient string
	TwilioWhatsAppTemplate  string
	MetaBaseURL             string
	MetaAccessToken         string
	MetaPhoneNumberID       string
	MetaAPIVersion          string
	MetaRecipient           string
	MetaTemplate            string
	MetaLanguage            string
	SMTPAddress             string
	SMTPUsername            string
	SMTPPassword            string
	SMTPFrom                string
	SMTPRecipient           string
	SMTPTLSMode             string
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
		TwilioURL:               firstEnv("INSPECTION_TEST_TWILIO_URL", "INSPECTION_TWILIO_BASE_URL", "http://localhost:1081"),
		MetaURL:                 firstEnv("INSPECTION_TEST_META_URL", "META_BASE_URL", "http://localhost:1082"),
		MailpitURL:              firstEnv("INSPECTION_TEST_MAILPIT_URL", "http://localhost:8026"),
		MailpitSMTPAddress:      firstEnv("INSPECTION_TEST_MAILPIT_SMTP", "localhost:1026"),
		MinIOEndpoint:           firstEnv("INSPECTION_TEST_MINIO_ENDPOINT", "INSPECTION_MINIO_ENDPOINT", "localhost:9000"),
		MinIOAccessKey:          firstEnv("INSPECTION_TEST_MINIO_ACCESS_KEY", "INSPECTION_MINIO_ACCESS_KEY", "inspection"),
		MinIOSecretKey:          firstEnv("INSPECTION_TEST_MINIO_SECRET_KEY", "INSPECTION_MINIO_SECRET_KEY", "inspection-local-secret"),
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
	if cfg.TwilioURL == "" {
		cfg.TwilioURL = "http://localhost:1081"
	}
	if cfg.MetaURL == "" {
		cfg.MetaURL = "http://localhost:1082"
	}
	if cfg.MailpitURL == "" {
		cfg.MailpitURL = "http://localhost:8026"
	}
	if cfg.MailpitSMTPAddress == "" {
		cfg.MailpitSMTPAddress = "localhost:1026"
	}
	if cfg.MinIOEndpoint == "" {
		cfg.MinIOEndpoint = "localhost:9000"
	}
	if cfg.MinIOAccessKey == "" {
		cfg.MinIOAccessKey = "inspection"
	}
	if cfg.MinIOSecretKey == "" {
		cfg.MinIOSecretKey = "inspection-local-secret"
	}
	if cfg.MinIOBucket == "" {
		cfg.MinIOBucket = "inspection-private"
	}
	return cfg
}

// LoadLiveSmokeConfig reads an explicit provider smoke configuration. The
// returned error is reserved for an enabled run with invalid configuration;
// disabled runs return a stable skip reason and no defaults.
func LoadLiveSmokeConfig() (LiveSmokeConfig, string, error) {
	cfg := LiveSmokeConfig{Enabled: envBool("INSPECTION_LIVE_SMOKES", false), AllowlistedRecipients: splitSet(os.Getenv("INSPECTION_LIVE_ALLOWLISTED_RECIPIENTS"))}
	if !cfg.Enabled {
		return cfg, "INSPECTION_LIVE_SMOKES is not true", nil
	}
	cfg = LiveSmokeConfig{
		Enabled: true, AllowlistedRecipients: cfg.AllowlistedRecipients,
		TwilioBaseURL: os.Getenv("INSPECTION_LIVE_TWILIO_BASE_URL"), TwilioAccountSID: os.Getenv("INSPECTION_LIVE_TWILIO_ACCOUNT_SID"), TwilioAuthToken: os.Getenv("INSPECTION_LIVE_TWILIO_AUTH_TOKEN"), TwilioSMSFrom: os.Getenv("INSPECTION_LIVE_TWILIO_SMS_FROM"), TwilioWhatsAppFrom: os.Getenv("INSPECTION_LIVE_TWILIO_WHATSAPP_FROM"), TwilioSMSRecipient: os.Getenv("INSPECTION_LIVE_TWILIO_SMS_RECIPIENT"), TwilioWhatsAppRecipient: os.Getenv("INSPECTION_LIVE_TWILIO_WHATSAPP_RECIPIENT"), TwilioWhatsAppTemplate: os.Getenv("INSPECTION_LIVE_TWILIO_WHATSAPP_TEMPLATE"),
		MetaBaseURL: os.Getenv("INSPECTION_LIVE_META_BASE_URL"), MetaAccessToken: os.Getenv("INSPECTION_LIVE_META_ACCESS_TOKEN"), MetaPhoneNumberID: os.Getenv("INSPECTION_LIVE_META_PHONE_NUMBER_ID"), MetaAPIVersion: os.Getenv("INSPECTION_LIVE_META_API_VERSION"), MetaRecipient: os.Getenv("INSPECTION_LIVE_META_RECIPIENT"), MetaTemplate: os.Getenv("INSPECTION_LIVE_META_TEMPLATE"), MetaLanguage: os.Getenv("INSPECTION_LIVE_META_LANGUAGE"),
		SMTPAddress: os.Getenv("INSPECTION_LIVE_SMTP_ADDRESS"), SMTPUsername: os.Getenv("INSPECTION_LIVE_SMTP_USERNAME"), SMTPPassword: os.Getenv("INSPECTION_LIVE_SMTP_PASSWORD"), SMTPFrom: os.Getenv("INSPECTION_LIVE_SMTP_FROM"), SMTPRecipient: os.Getenv("INSPECTION_LIVE_SMTP_RECIPIENT"), SMTPTLSMode: os.Getenv("INSPECTION_LIVE_SMTP_TLS_MODE"),
	}
	if len(cfg.AllowlistedRecipients) == 0 {
		return cfg, "", fmt.Errorf("live smoke: INSPECTION_LIVE_ALLOWLISTED_RECIPIENTS is required")
	}
	return cfg, "", nil
}

// Validate validates only the fields needed by one named live smoke.
func (c LiveSmokeConfig) Validate(provider string) error {
	if !c.Enabled {
		return errors.New("live smokes disabled")
	}
	require := func(values ...string) error {
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return errors.New("missing explicit live smoke configuration")
			}
		}
		return nil
	}
	var recipient string
	switch provider {
	case "twilio-sms":
		if err := require(c.TwilioBaseURL, c.TwilioAccountSID, c.TwilioAuthToken, c.TwilioSMSFrom, c.TwilioSMSRecipient); err != nil {
			return err
		}
		recipient = c.TwilioSMSRecipient
	case "twilio-whatsapp":
		if err := require(c.TwilioBaseURL, c.TwilioAccountSID, c.TwilioAuthToken, c.TwilioWhatsAppFrom, c.TwilioWhatsAppRecipient, c.TwilioWhatsAppTemplate); err != nil {
			return err
		}
		recipient = c.TwilioWhatsAppRecipient
	case "meta-whatsapp":
		if err := require(c.MetaBaseURL, c.MetaAccessToken, c.MetaPhoneNumberID, c.MetaAPIVersion, c.MetaRecipient, c.MetaTemplate); err != nil {
			return err
		}
		recipient = c.MetaRecipient
	case "smtp":
		if err := require(c.SMTPAddress, c.SMTPFrom, c.SMTPRecipient, c.SMTPTLSMode); err != nil {
			return err
		}
		recipient = c.SMTPRecipient
	default:
		return fmt.Errorf("unknown live smoke provider %q", provider)
	}
	if !c.AllowlistedRecipients[recipient] {
		return errors.New("live smoke recipient is not allowlisted")
	}
	return nil
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

func splitSet(value string) map[string]bool {
	result := make(map[string]bool)
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result[item] = true
		}
	}
	return result
}
