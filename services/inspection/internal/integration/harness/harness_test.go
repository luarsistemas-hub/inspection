package harness

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestConfigFromEnvHasSafeDefaults(t *testing.T) {
	cfg := ConfigFromEnv()
	if cfg.DatabaseURL == "" || cfg.RabbitMQURL == "" || cfg.LiteLLMURL == "" || cfg.GotenbergURL == "" {
		t.Fatalf("incomplete defaults: %+v", cfg)
	}
	if cfg.RequestTimeout <= 0 || cfg.PollInterval <= 0 {
		t.Fatalf("invalid timeouts: %+v", cfg)
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
	if len(cases) != 148 {
		t.Fatalf("assigned cases=%d want 148", len(cases))
	}
	seen := make(map[string]struct{}, len(cases))
	unit, integration := 0, 0
	for _, item := range cases {
		if item.ID == "" {
			t.Fatal("manifest contains an empty case id")
		}
		if _, exists := seen[item.ID]; exists {
			t.Fatalf("duplicate case %s", item.ID)
		}
		seen[item.ID] = struct{}{}
		switch item.Kind {
		case "unit":
			unit++
		case "integration":
			integration++
		default:
			t.Fatalf("unknown case kind %q", item.Kind)
		}
	}
	if unit != 14 || integration != 134 {
		t.Fatalf("case split unit=%d integration=%d", unit, integration)
	}
}
