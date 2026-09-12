package config

import (
	"encoding/json"
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
	SuperAdminIssuer      string
	SuperAdminSubject     string
	SuperAdminPassword    string
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
	KeycloakAdminURL      string
	KeycloakRealm         string
	KeycloakClientID      string
	KeycloakClientSecret  string
	SMTPAddress           string
	SMTPFrom              string
	SMTPUsername          string
	SMTPPassword          string
	SMTPReplyTo           string
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
	Notification          NotificationConfig
}

// NotificationConfig contains operational delivery settings. Authentication
// notifier settings remain in their existing top-level configuration fields.
type NotificationConfig struct {
	MaxAttempts        int
	RetryDelays        []time.Duration
	V2ProducersEnabled bool
	SMTPTLSMode        string
	TwilioSMSFrom      string
	TwilioWhatsAppFrom string
	WhatsAppProvider   string
	MetaBaseURL        string
	MetaAccessToken    string
	MetaPhoneNumberID  string
	MetaAPIVersion     string
	MetaVerifyToken    string
	MetaAppSecret      string
	TwilioTemplates    map[string]string
	MetaTemplates      map[string]string
	PayloadKeys        map[string]string
	ActivePayloadKey   string
}

// Load reads and validates environment configuration without requiring a file.
func Load() (Config, error) {
	c := Config{
		Environment: env("INSPECTION_ENV", "local"), HTTPAddress: env("INSPECTION_HTTP_ADDR", ":8080"),
		DatabaseURL: os.Getenv("INSPECTION_DATABASE_URL"), DispatcherDatabaseURL: os.Getenv("INSPECTION_DISPATCHER_DATABASE_URL"), MigrationDatabaseURL: env("INSPECTION_MIGRATION_DATABASE_URL", os.Getenv("INSPECTION_DATABASE_URL")), AllowedOrigin: os.Getenv("INSPECTION_ALLOWED_ORIGIN"), AllowedOrigins: splitExact(os.Getenv("INSPECTION_ALLOWED_ORIGINS")), CaptureOrigin: os.Getenv("INSPECTION_CAPTURE_ORIGIN"),
		MetricsToken: os.Getenv("INSPECTION_METRICS_TOKEN"), OIDCIssuer: os.Getenv("INSPECTION_OIDC_ISSUER"),
		OIDCAudience: os.Getenv("INSPECTION_OIDC_AUDIENCE"), OIDCAudiences: splitExact(os.Getenv("INSPECTION_OIDC_AUDIENCES")), OIDCJWKSURL: os.Getenv("INSPECTION_OIDC_JWKS_URL"), SuperAdminIssuer: os.Getenv("INSPECTION_SUPER_ADMIN_ISSUER"), SuperAdminSubject: os.Getenv("INSPECTION_SUPER_ADMIN_SUBJECT"), SuperAdminPassword: os.Getenv("INSPECTION_SUPER_ADMIN_PASSWORD"), SchemaMin: envInt("INSPECTION_SCHEMA_MIN", 13),
		SchemaMax: envInt("INSPECTION_SCHEMA_MAX", 24), ShutdownTimeout: 10 * time.Second,
		RuntimeDBRole:    env("INSPECTION_RUNTIME_DB_ROLE", "inspection_runtime"),
		StoragePublic:    strings.EqualFold(os.Getenv("INSPECTION_STORAGE_PUBLIC"), "true"),
		RabbitMQURL:      env("INSPECTION_RABBITMQ_URL", "amqp://inspection:inspection@localhost:5672/"),
		DragonflyAddress: env("INSPECTION_DRAGONFLY_ADDRESS", "localhost:6379"), DragonflyPassword: os.Getenv("INSPECTION_DRAGONFLY_PASSWORD"),
		MinIOEndpoint: env("INSPECTION_MINIO_ENDPOINT", "localhost:9000"), MinIOPublicEndpoint: env("INSPECTION_MINIO_PUBLIC_ENDPOINT", ""), MinIOAccessKey: env("INSPECTION_MINIO_ACCESS_KEY", "inspection"), MinIOSecretKey: env("INSPECTION_MINIO_SECRET_KEY", "inspection-local-secret"), MinIOBucket: env("INSPECTION_MINIO_BUCKET", "inspection-private"), MinIOSecure: strings.EqualFold(os.Getenv("INSPECTION_MINIO_SECURE"), "true"), MinIOPublicSecure: strings.EqualFold(os.Getenv("INSPECTION_MINIO_PUBLIC_SECURE"), "true"),
		OTPPepper: env("INSPECTION_OTP_PEPPER", "local-development-pepper-change-me-32"), SMTPAddress: env("INSPECTION_SMTP_ADDRESS", "localhost:1025"), SMTPFrom: env("INSPECTION_SMTP_FROM", "inspection@localhost"), SMTPUsername: os.Getenv("INSPECTION_SMTP_USERNAME"), SMTPPassword: os.Getenv("INSPECTION_SMTP_PASSWORD"), SMTPReplyTo: os.Getenv("INSPECTION_SMTP_REPLY_TO"),
		KeycloakAdminURL: env("INSPECTION_KEYCLOAK_ADMIN_URL", "http://localhost:8081"), KeycloakRealm: env("INSPECTION_KEYCLOAK_REALM", "inspection"), KeycloakClientID: os.Getenv("INSPECTION_KEYCLOAK_PROVISIONING_CLIENT_ID"), KeycloakClientSecret: os.Getenv("INSPECTION_KEYCLOAK_PROVISIONING_CLIENT_SECRET"),
		TwilioBaseURL: env("INSPECTION_TWILIO_BASE_URL", "http://localhost:1080"), TwilioAccountSID: env("INSPECTION_TWILIO_ACCOUNT_SID", "AC-local"), TwilioAuthToken: env("INSPECTION_TWILIO_AUTH_TOKEN", "local-token"), TwilioFrom: env("INSPECTION_TWILIO_FROM", "+15550000000"), TwilioCallbackURL: env("INSPECTION_TWILIO_CALLBACK_URL", "http://localhost:8080/webhooks/twilio/status"), LiteLLMURL: env("INSPECTION_LITELLM_URL", "http://localhost:18080"), LiteLLMModelAlias: env("INSPECTION_LITELLM_MODEL_ALIAS", "inspection-vision"), LiteLLMPromptVersion: env("INSPECTION_LITELLM_PROMPT_VERSION", "analysis-v1"), GotenbergURL: env("INSPECTION_GOTENBERG_URL", "http://localhost:18081"), ProviderTimeout: envDuration("INSPECTION_PROVIDER_TIMEOUT", 30*time.Second),
	}
	notification, err := loadNotification(c.Environment)
	if err != nil {
		return Config{}, err
	}
	c.Notification = notification
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

func notificationEnv(key string) (string, bool) {
	if value, ok := os.LookupEnv(key); ok {
		return value, true
	}
	return os.LookupEnv("INSPECTION_" + key)
}

func loadNotification(environment string) (NotificationConfig, error) {
	c := NotificationConfig{MaxAttempts: 4, RetryDelays: []time.Duration{5 * time.Second, 30 * time.Second, 5 * time.Minute}, V2ProducersEnabled: false, SMTPTLSMode: "starttls", WhatsAppProvider: "twilio", TwilioTemplates: map[string]string{}, MetaTemplates: map[string]string{}, PayloadKeys: map[string]string{}}
	if raw, ok := notificationEnv("NOTIFICATION_V2_PRODUCERS_ENABLED"); ok {
		value, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return NotificationConfig{}, fmt.Errorf("configuration: invalid NOTIFICATION_V2_PRODUCERS_ENABLED")
		}
		c.V2ProducersEnabled = value
	}
	if raw, ok := notificationEnv("NOTIFICATION_MAX_ATTEMPTS"); ok {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return NotificationConfig{}, fmt.Errorf("configuration: invalid NOTIFICATION_MAX_ATTEMPTS")
		}
		c.MaxAttempts = value
	}
	if raw, ok := notificationEnv("NOTIFICATION_RETRY_DELAYS"); ok {
		parts := strings.Split(raw, ",")
		if len(parts) == 0 {
			return NotificationConfig{}, fmt.Errorf("configuration: invalid NOTIFICATION_RETRY_DELAYS")
		}
		c.RetryDelays = make([]time.Duration, 0, len(parts))
		for _, part := range parts {
			delay, err := time.ParseDuration(strings.TrimSpace(part))
			if err != nil || delay <= 0 {
				return NotificationConfig{}, fmt.Errorf("configuration: invalid NOTIFICATION_RETRY_DELAYS")
			}
			c.RetryDelays = append(c.RetryDelays, delay)
		}
	}
	if raw, ok := notificationEnv("SMTP_TLS_MODE"); ok {
		c.SMTPTLSMode = strings.ToLower(strings.TrimSpace(raw))
	}
	if raw, ok := notificationEnv("TWILIO_SMS_FROM"); ok {
		c.TwilioSMSFrom = raw
	}
	if c.TwilioSMSFrom == "" {
		c.TwilioSMSFrom = env("INSPECTION_TWILIO_FROM", "")
	}
	if raw, ok := notificationEnv("TWILIO_WHATSAPP_FROM"); ok {
		c.TwilioWhatsAppFrom = raw
	}
	if c.TwilioWhatsAppFrom == "" {
		c.TwilioWhatsAppFrom = env("INSPECTION_TWILIO_FROM", "")
	}
	if raw, ok := notificationEnv("NOTIFICATION_WHATSAPP_PROVIDER"); ok {
		c.WhatsAppProvider = strings.ToLower(strings.TrimSpace(raw))
	}
	if raw, ok := notificationEnv("META_ACCESS_TOKEN"); ok {
		c.MetaAccessToken = raw
	}
	if raw, ok := notificationEnv("META_PHONE_NUMBER_ID"); ok {
		c.MetaPhoneNumberID = raw
	}
	if raw, ok := notificationEnv("META_API_VERSION"); ok {
		c.MetaAPIVersion = raw
	}
	if raw, ok := notificationEnv("META_BASE_URL"); ok {
		c.MetaBaseURL = raw
	}
	if raw, ok := notificationEnv("META_VERIFY_TOKEN"); ok {
		c.MetaVerifyToken = raw
	}
	if raw, ok := notificationEnv("META_APP_SECRET"); ok {
		c.MetaAppSecret = raw
	}
	for _, target := range []struct {
		key         string
		destination *map[string]string
	}{{"TWILIO_TEMPLATE_MAP", &c.TwilioTemplates}, {"META_TEMPLATE_MAP", &c.MetaTemplates}, {"NOTIFICATION_PAYLOAD_KEYS", &c.PayloadKeys}} {
		if raw, ok := notificationEnv(target.key); ok {
			if err := json.Unmarshal([]byte(raw), target.destination); err != nil || *target.destination == nil {
				return NotificationConfig{}, fmt.Errorf("configuration: invalid %s", target.key)
			}
		}
	}
	if raw, ok := notificationEnv("NOTIFICATION_ACTIVE_PAYLOAD_KEY"); ok {
		c.ActivePayloadKey = raw
	}
	if err := c.Validate(environment); err != nil {
		return NotificationConfig{}, err
	}
	return c, nil
}

// Validate fails closed only for settings that were explicitly selected.
func (c NotificationConfig) Validate(environment string) error {
	if c.MaxAttempts <= 0 {
		return fmt.Errorf("configuration: invalid NOTIFICATION_MAX_ATTEMPTS")
	}
	if len(c.RetryDelays) == 0 {
		return fmt.Errorf("configuration: invalid NOTIFICATION_RETRY_DELAYS")
	}
	for _, delay := range c.RetryDelays {
		if delay <= 0 {
			return fmt.Errorf("configuration: invalid NOTIFICATION_RETRY_DELAYS")
		}
	}
	if c.SMTPTLSMode != "none" && c.SMTPTLSMode != "starttls" && c.SMTPTLSMode != "tls" {
		return fmt.Errorf("configuration: invalid SMTP_TLS_MODE")
	}
	if environment != "local" && environment != "test" && c.SMTPTLSMode == "none" {
		return fmt.Errorf("configuration: SMTP_TLS_MODE none is forbidden outside local")
	}
	if c.WhatsAppProvider != "twilio" && c.WhatsAppProvider != "meta" {
		return fmt.Errorf("configuration: invalid NOTIFICATION_WHATSAPP_PROVIDER")
	}
	if c.WhatsAppProvider == "meta" {
		missing := make([]string, 0, 4)
		if c.MetaAccessToken == "" {
			missing = append(missing, "META_ACCESS_TOKEN")
		}
		if c.MetaPhoneNumberID == "" {
			missing = append(missing, "META_PHONE_NUMBER_ID")
		}
		if c.MetaAPIVersion == "" {
			missing = append(missing, "META_API_VERSION")
		}
		if c.MetaVerifyToken == "" {
			missing = append(missing, "META_VERIFY_TOKEN")
		}
		if c.MetaAppSecret == "" {
			missing = append(missing, "META_APP_SECRET")
		}
		if len(c.MetaTemplates) == 0 {
			missing = append(missing, "META_TEMPLATE_MAP")
		}
		if len(missing) > 0 {
			return fmt.Errorf("configuration: missing %s", strings.Join(missing, ","))
		}
	}
	if c.ActivePayloadKey != "" {
		if _, ok := c.PayloadKeys[c.ActivePayloadKey]; !ok {
			return fmt.Errorf("configuration: missing active NOTIFICATION_PAYLOAD_KEYS entry")
		}
	}
	for key, value := range c.PayloadKeys {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return fmt.Errorf("configuration: invalid NOTIFICATION_PAYLOAD_KEYS")
		}
	}
	return nil
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
	if c.SuperAdminIssuer == "" || c.SuperAdminSubject == "" || c.SuperAdminPassword == "" {
		return fmt.Errorf("configuration: missing super administrator bootstrap secret")
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
