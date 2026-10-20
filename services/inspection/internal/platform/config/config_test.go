package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestMigrationDatabaseURLIsOnlyRequiredByMigrationCommands(t *testing.T) {
	permanent := Config{
		Environment: "production", DatabaseURL: "postgres://runtime@db/inspection",
		AllowedOrigin: "https://app.example", AdminOrigin: "https://app.example",
		MetricsToken: "metrics", OIDCIssuer: "https://id.example", OIDCAudience: "inspection",
		SuperAdminIssuer: "https://id.example", SuperAdminSubject: "subject", SuperAdminPassword: "password",
		SchemaMin: 1, SchemaMax: 1, RuntimeDBRole: "inspection_runtime",
		MinIOEndpoint: "s3.example", MinIOBucket: "inspection", MinIOAccessKey: "key",
		MinIOSecretKey: "secret", MinIORegion: "region", StoragePublicBaseURL: "https://storage.example",
	}
	if err := permanent.Validate(); err != nil {
		t.Fatalf("permanent process config should not require privileged DSN: %v", err)
	}
	if _, err := permanent.RequireMigrationDatabaseURL(); err == nil {
		t.Fatal("migration command accepted a missing privileged DSN")
	}
	permanent.MigrationDatabaseURL = "postgres://postgres@db/inspection"
	if got, err := permanent.RequireMigrationDatabaseURL(); err != nil || got != permanent.MigrationDatabaseURL {
		t.Fatalf("migration DSN = %q, %v", got, err)
	}
}

func TestStageFromEnvironmentDefaultsToProduction(t *testing.T) {
	t.Setenv("STAGE", "")
	if got := stageFromEnvironment(); got != "production" {
		t.Fatalf("empty stage = %q, want production", got)
	}

	t.Setenv("STAGE", " StAgInG ")
	if got := stageFromEnvironment(); got != "staging" {
		t.Fatalf("normalized stage = %q, want staging", got)
	}
}

func TestEnvBoolUsesSafeFallback(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		fallback bool
		want     bool
	}{
		{name: "unset", fallback: true, want: true},
		{name: "false", value: "false", fallback: true, want: false},
		{name: "zero", value: "0", fallback: true, want: false},
		{name: "true", value: "true", fallback: false, want: true},
		{name: "invalid", value: "maybe", fallback: true, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := "TEST_ENV_BOOL_" + test.name
			t.Setenv(key, test.value)
			if got := envBool(key, test.fallback); got != test.want {
				t.Fatalf("envBool() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestStrictSensitiveContentBypassBoolean(t *testing.T) {
	key := "INSPECTION_SKIP_SENSITIVE_CONTENT_VALIDATION"
	for _, value := range []string{"true", "1", "yes", "on", "TRUE"} {
		t.Setenv(key, value)
		got, err := strictEnvBool(key, false)
		if err != nil || !got {
			t.Errorf("strictEnvBool(%q) = %v, %v; want true, nil", value, got, err)
		}
	}
	for _, value := range []string{"false", "0", "no", "off", "FALSE"} {
		t.Setenv(key, value)
		got, err := strictEnvBool(key, true)
		if err != nil || got {
			t.Errorf("strictEnvBool(%q) = %v, %v; want false, nil", value, got, err)
		}
	}
	t.Setenv(key, "invalid")
	if _, err := strictEnvBool(key, false); err == nil {
		t.Fatal("strictEnvBool accepted an invalid value")
	}
}

func TestConfigContractsUT058UT059(t *testing.T) {
	valid := Config{Environment: "production", DatabaseURL: "postgres://runtime@db/inspection", AllowedOrigin: "https://app.example", MetricsToken: "secret", OIDCIssuer: "https://id.example", OIDCAudience: "inspection", SuperAdminIssuer: "https://id.example", SuperAdminSubject: "admin-subject", SuperAdminPassword: "fixture-secret", SchemaMin: 1, SchemaMax: 1, RuntimeDBRole: "inspection_runtime", MinIOEndpoint: "s3.example", MinIOBucket: "inspection", MinIOAccessKey: "key", MinIOSecretKey: "secret", MinIORegion: "us-ashburn-1", StoragePublicBaseURL: "https://storage.example"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := []Config{valid, valid, valid, valid}
	cases[0].AllowedOrigin = "*"
	cases[1].StoragePublic = true
	cases[2].RuntimeDBRole = "postgres"
	cases[3].MetricsToken = ""
	for i, c := range cases {
		if err := c.Validate(); err == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
}

func TestTurnstileConfigurationFailsClosedWhenEnabled(t *testing.T) {
	valid := Config{Environment: "production", DatabaseURL: "postgres://runtime@db/inspection", MigrationDatabaseURL: "postgres://migrator@db/inspection", AllowedOrigin: "https://app.example", AllowedOrigins: []string{"https://app.example", "https://onboard.example"}, AdminOrigin: "https://app.example", MetricsToken: "secret", OIDCIssuer: "https://id.example", OIDCAudience: "inspection", SuperAdminIssuer: "https://id.example", SuperAdminSubject: "admin-subject", SuperAdminPassword: "fixture-secret", SchemaMin: 1, SchemaMax: 1, RuntimeDBRole: "inspection_runtime", MinIOEndpoint: "s3.example", MinIOBucket: "inspection", MinIOAccessKey: "key", MinIOSecretKey: "secret", MinIORegion: "us-ashburn-1", StoragePublicBaseURL: "https://storage.example", TurnstileEnabled: true, OnboardingOrigin: "https://onboard.example", TurnstileSiteKey: "public-site-key", TurnstileSecret: "private-secret"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid Turnstile config rejected: %v", err)
	}

	missingKey := valid
	missingKey.TurnstileSiteKey = ""
	if err := missingKey.Validate(); err == nil {
		t.Fatal("enabled Turnstile accepted without a site key")
	}
	wrongOrigin := valid
	wrongOrigin.OnboardingOrigin = "https://unlisted.example"
	if err := wrongOrigin.Validate(); err == nil {
		t.Fatal("Turnstile accepted an onboarding origin outside the allowlist")
	}
}

func TestNotificationConfigurationUT013ToUT021(t *testing.T) {
	c := NotificationConfig{MaxAttempts: 4, RetryDelays: []time.Duration{5 * time.Second, 30 * time.Second, 5 * time.Minute}, SMTPTLSMode: "starttls", WhatsAppProvider: "twilio", PayloadKeys: map[string]string{}}
	if err := c.Validate("local"); err != nil {
		t.Fatal(err)
	}
	c.MaxAttempts = 0
	if err := c.Validate("local"); err == nil {
		t.Fatal("zero attempts accepted")
	}
	c.MaxAttempts = 4
	c.SMTPTLSMode = "none"
	if err := c.Validate("test"); err != nil {
		t.Fatalf("test SMTP none rejected: %v", err)
	}
	if err := c.Validate("production"); err == nil {
		t.Fatal("production SMTP none accepted")
	}
	c.SMTPTLSMode = "tls"
	c.WhatsAppProvider = "meta"
	if err := c.Validate("local"); err == nil {
		t.Fatal("incomplete meta accepted")
	}
	c.MetaAccessToken, c.MetaPhoneNumberID, c.MetaAPIVersion = "token", "phone", "v22"
	c.MetaVerifyToken, c.MetaAppSecret = "verify", "secret"
	c.MetaTemplates = map[string]string{"capture-link:v1:pt-BR": "capture"}
	if err := c.Validate("local"); err != nil {
		t.Fatal(err)
	}
}

func TestConfigAcceptsExactMultiProductOriginsAndAudiences(t *testing.T) {
	c := Config{Environment: "production", DatabaseURL: "postgres://runtime@db/inspection", MigrationDatabaseURL: "postgres://migrator@db/inspection", AllowedOrigins: []string{"https://admin.example", "https://dashboard.example", "https://capture.example"}, CaptureOrigin: "https://capture.example", MetricsToken: "secret", OIDCIssuer: "https://id.example", OIDCAudiences: []string{"inspection-admin", "inspection-dashboard"}, SuperAdminIssuer: "https://id.example", SuperAdminSubject: "admin-subject", SuperAdminPassword: "fixture-secret", SchemaMin: 1, SchemaMax: 1, RuntimeDBRole: "inspection_runtime", MinIOEndpoint: "s3.example", MinIOBucket: "inspection", MinIOAccessKey: "key", MinIOSecretKey: "secret", MinIORegion: "us-ashburn-1", StoragePublicBaseURL: "https://storage.example"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.AllowedOrigins = append(c.AllowedOrigins, "https://admin.example")
	if err := c.Validate(); err == nil {
		t.Fatal("duplicate origin accepted")
	}
}

func TestConfigRequiresSecretBackedSuperAdminBootstrap(t *testing.T) {
	valid := Config{Environment: "production", DatabaseURL: "postgres://runtime@db/inspection", MigrationDatabaseURL: "postgres://migrator@db/inspection", AllowedOrigin: "https://app.example", MetricsToken: "secret", OIDCIssuer: "https://id.example", OIDCAudience: "inspection", SuperAdminIssuer: "https://id.example", SuperAdminSubject: "admin-subject", SuperAdminPassword: "fixture-secret", SchemaMin: 1, SchemaMax: 1, RuntimeDBRole: "inspection_runtime", MinIOEndpoint: "s3.example", MinIOBucket: "inspection", MinIOAccessKey: "key", MinIOSecretKey: "secret", MinIORegion: "us-ashburn-1", StoragePublicBaseURL: "https://storage.example"}
	for _, clear := range []func(*Config){
		func(c *Config) { c.SuperAdminIssuer = "" },
		func(c *Config) { c.SuperAdminSubject = "" },
		func(c *Config) { c.SuperAdminPassword = "" },
	} {
		candidate := valid
		clear(&candidate)
		if err := candidate.Validate(); err == nil {
			t.Fatal("missing super administrator bootstrap secret accepted")
		}
	}
}

func TestConfigLLMModeContracts(t *testing.T) {
	valid := Config{Environment: "production", DatabaseURL: "postgres://runtime@db/inspection", MigrationDatabaseURL: "postgres://migrator@db/inspection", AllowedOrigin: "https://app.example", MetricsToken: "secret", OIDCIssuer: "https://id.example", OIDCAudience: "inspection", SuperAdminIssuer: "https://id.example", SuperAdminSubject: "admin-subject", SuperAdminPassword: "fixture-secret", SchemaMin: 1, SchemaMax: 1, RuntimeDBRole: "inspection_runtime", MinIOEndpoint: "s3.example", MinIOBucket: "inspection", MinIOAccessKey: "key", MinIOSecretKey: "secret", MinIORegion: "us-ashburn-1", StoragePublicBaseURL: "https://storage.example", LLMMode: "mock"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	live := valid
	live.LLMMode = "live"
	live.LiteLLMURL = "http://litellm:4000"
	live.LiteLLMAPIKey = "test-only"
	if err := live.Validate(); err != nil {
		t.Fatal(err)
	}
	live.LiteLLMAPIKey = ""
	if err := live.Validate(); err == nil {
		t.Fatal("live without gateway key accepted")
	}
	invalid := valid
	invalid.LLMMode = "other"
	if err := invalid.Validate(); err == nil {
		t.Fatal("unknown LLM mode accepted")
	}
}

func TestSensitiveContentBypassMatrixThroughLoad(t *testing.T) {
	const skip, legacy = "INSPECTION_SKIP_SENSITIVE_CONTENT_VALIDATION", "INSPECTION_IMAGE_VALIDATION_ENABLED"
	cases := []struct {
		name         string
		skip, legacy *string
		wantSource   string
		wantErr      bool
	}{
		{"defaults keep screening on", nil, nil, "", false},
		{"skip false and legacy true", ptr("false"), ptr("true"), "", false},
		{"skip true bypasses", ptr("true"), nil, skip, false},
		{"skip true wins over legacy false", ptr("true"), ptr("false"), skip, false},
		{"legacy false still bypasses", nil, ptr("false"), legacy, false},
		{"legacy false with skip false still bypasses", ptr("false"), ptr("false"), legacy, false},
		{"legacy invalid falls back to screening on", nil, ptr("maybe"), "", false},
		{"skip invalid prevents startup", ptr("maybe"), nil, "", true},
		{"skip invalid prevents startup even with legacy false", ptr("maybe"), ptr("false"), "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for key, value := range map[string]*string{skip: tc.skip, legacy: tc.legacy} {
				if value == nil {
					t.Setenv(key, "")
					os.Unsetenv(key)
					continue
				}
				t.Setenv(key, *value)
			}
			// Load also validates unrelated required settings this test does not provide,
			// so only the bypass parsing error is significant here.
			cfg, err := Load()
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), skip+" must be a boolean") {
					t.Fatalf("Load error = %v, want boolean parsing error for %s", err, skip)
				}
				return
			}
			if err != nil && strings.Contains(err.Error(), "must be a boolean") {
				t.Fatalf("valid bypass configuration rejected: %v", err)
			}
			if got := cfg.SensitiveContentBypassSource(); got != tc.wantSource {
				t.Fatalf("bypass source = %q, want %q", got, tc.wantSource)
			}
		})
	}
}

func ptr(value string) *string { return &value }
