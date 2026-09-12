package harness

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestConfigFromEnvHasSafeDefaults(t *testing.T) {
	cfg := ConfigFromEnv()
	if cfg.DatabaseURL == "" || cfg.RabbitMQURL == "" || cfg.LiteLLMURL == "" || cfg.GotenbergURL == "" || cfg.TwilioURL == "" || cfg.MetaURL == "" || cfg.MailpitURL == "" {
		t.Fatalf("incomplete defaults: %+v", cfg)
	}
	if cfg.RequestTimeout <= 0 || cfg.PollInterval <= 0 {
		t.Fatalf("invalid timeouts: %+v", cfg)
	}
}

func TestLiveSmokeConfigIsExplicitAndFailClosed(t *testing.T) {
	t.Setenv("INSPECTION_LIVE_SMOKES", "false")
	cfg, reason, err := LoadLiveSmokeConfig()
	if err != nil || cfg.Enabled || reason == "" {
		t.Fatalf("disabled live config: %+v reason=%q err=%v", cfg, reason, err)
	}
	t.Setenv("INSPECTION_LIVE_SMOKES", "true")
	t.Setenv("INSPECTION_LIVE_ALLOWLISTED_RECIPIENTS", "+15550000001")
	cfg, _, err = LoadLiveSmokeConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate("twilio-sms"); err == nil {
		t.Fatal("incomplete enabled configuration was accepted")
	}
}

func TestGraphQLResponseRequireNoErrors(t *testing.T) {
	response := GraphQLResponse{Data: json.RawMessage(`{"ok":true}`)}
	if err := response.RequireNoErrors(); err != nil {
		t.Fatal(err)
	}
	response.Errors = []GraphQLError{{Message: "boom"}}
	if err := response.RequireNoErrors(); err == nil {
		t.Fatal("expected GraphQL error")
	}
}

func TestEventuallyHonoursContext(t *testing.T) {
	h := &Harness{Config: Config{PollInterval: time.Millisecond}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := h.Eventually(ctx, func() (bool, error) { return false, nil }); err == nil {
		t.Fatal("expected timeout")
	}
}

func TestTask06AssignedCaseManifest(t *testing.T) {
	cases := Task06AssignedCases()
	if len(cases) != 9 {
		t.Fatalf("assigned cases=%d want 9", len(cases))
	}
	seen := make(map[string]struct{}, len(cases))
	integration, endToEnd := 0, 0
	for _, item := range cases {
		if item.ID == "" {
			t.Fatal("manifest contains an empty case id")
		}
		if _, exists := seen[item.ID]; exists {
			t.Fatalf("duplicate case %s", item.ID)
		}
		seen[item.ID] = struct{}{}
		switch item.Kind {
		case "integration":
			integration++
		case "end-to-end":
			endToEnd++
		default:
			t.Fatalf("unknown case kind %q", item.Kind)
		}
	}
	if integration != 4 || endToEnd != 5 {
		t.Fatalf("case split integration=%d end-to-end=%d", integration, endToEnd)
	}
}
