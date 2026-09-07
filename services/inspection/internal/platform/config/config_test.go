package config

import "testing"

func TestConfigContractsUT058UT059(t *testing.T) {
	valid := Config{Environment: "production", DatabaseURL: "postgres://runtime@db/inspection", MigrationDatabaseURL: "postgres://migrator@db/inspection", AllowedOrigin: "https://app.example", MetricsToken: "secret", OIDCIssuer: "https://id.example", OIDCAudience: "inspection", SchemaMin: 1, SchemaMax: 1, RuntimeDBRole: "inspection_runtime"}
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
