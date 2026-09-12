package config

import "testing"

import "time"

func TestConfigContractsUT058UT059(t *testing.T) {
	valid := Config{Environment: "production", DatabaseURL: "postgres://runtime@db/inspection", MigrationDatabaseURL: "postgres://migrator@db/inspection", AllowedOrigin: "https://app.example", MetricsToken: "secret", OIDCIssuer: "https://id.example", OIDCAudience: "inspection", SuperAdminIssuer: "https://id.example", SuperAdminSubject: "admin-subject", SuperAdminPassword: "fixture-secret", SchemaMin: 1, SchemaMax: 1, RuntimeDBRole: "inspection_runtime"}
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
	c := Config{Environment: "production", DatabaseURL: "postgres://runtime@db/inspection", MigrationDatabaseURL: "postgres://migrator@db/inspection", AllowedOrigins: []string{"https://admin.example", "https://dashboard.example", "https://capture.example"}, CaptureOrigin: "https://capture.example", MetricsToken: "secret", OIDCIssuer: "https://id.example", OIDCAudiences: []string{"inspection-admin", "inspection-dashboard"}, SuperAdminIssuer: "https://id.example", SuperAdminSubject: "admin-subject", SuperAdminPassword: "fixture-secret", SchemaMin: 1, SchemaMax: 1, RuntimeDBRole: "inspection_runtime"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.AllowedOrigins = append(c.AllowedOrigins, "https://admin.example")
	if err := c.Validate(); err == nil {
		t.Fatal("duplicate origin accepted")
	}
}

func TestConfigRequiresSecretBackedSuperAdminBootstrap(t *testing.T) {
	valid := Config{Environment: "production", DatabaseURL: "postgres://runtime@db/inspection", MigrationDatabaseURL: "postgres://migrator@db/inspection", AllowedOrigin: "https://app.example", MetricsToken: "secret", OIDCIssuer: "https://id.example", OIDCAudience: "inspection", SuperAdminIssuer: "https://id.example", SuperAdminSubject: "admin-subject", SuperAdminPassword: "fixture-secret", SchemaMin: 1, SchemaMax: 1, RuntimeDBRole: "inspection_runtime"}
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
