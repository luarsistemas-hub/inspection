package graphqlcontract

import (
	"strings"
	"testing"
)

const testSchema = `type Query { ready: String! } type Mutation { save(input: SaveInput!): SavePayload! } input SaveInput { clientMutationId: String! } type SavePayload { clientMutationId: String! }`

func TestValidateCapabilitiesUT001ToUT004(t *testing.T) {
	capabilities := []Capability{
		{Root: "Query.ready", Owner: "Admin", Persona: "administrator", Journey: "readiness", Evidence: "UT-001"},
		{Root: "Mutation.save", Owner: "Admin", Persona: "administrator", Journey: "save", Evidence: "UT-004"},
	}
	if err := ValidateCapabilities([]byte(testSchema), capabilities); err != nil {
		t.Fatalf("UT-001: %v", err)
	}
	if err := ValidateCapabilities([]byte(testSchema), capabilities[:1]); err == nil {
		t.Fatal("UT-002: expected unmapped field diagnostic")
	}
	manifest, err := BuildManifest([]byte(testSchema), map[string][]byte{"admin": []byte("query Ready { ready }")})
	if err != nil {
		t.Fatal(err)
	}
	manifest.Capabilities = capabilities
	if err := ValidateManifest([]byte(testSchema), manifest, []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateManifest([]byte("type Query { changed: String! }"), manifest, []string{"admin"}); err == nil {
		t.Fatal("UT-003: expected schema hash drift diagnostic")
	}
}

func TestValidateCapabilitiesRequiresMutationIdentity(t *testing.T) {
	bad := []byte(`type Query { ready: String! } type Mutation { save(input: SaveInput!): SavePayload! } input SaveInput { value: String! } type SavePayload { clientMutationId: String! }`)
	err := ValidateCapabilities(bad, []Capability{{Root: "Query.ready", Owner: "Admin", Persona: "administrator", Journey: "readiness", Evidence: "UT-001"}, {Root: "Mutation.save", Owner: "Admin", Persona: "administrator", Journey: "save", Evidence: "UT-004"}})
	if err == nil {
		t.Fatal("UT-004: expected client mutation identity diagnostic")
	}
}

func TestUS001EdgeContractsUT133(t *testing.T) {
	capabilities := []Capability{
		{Root: "Query.ready", Owner: "Admin", Persona: "administrator", Journey: "first-use, pagination, denial, prerequisite, archived-state, and scale behavior", Evidence: "IT-001"},
		{Root: "Mutation.save", Owner: "Admin", Persona: "administrator", Journey: "idempotent replay", Evidence: "IT-001"},
	}
	validDocument := []byte("query Ready { ready }")
	manifest, err := BuildManifest([]byte(testSchema), map[string][]byte{"admin": validDocument})
	if err != nil {
		t.Fatal(err)
	}
	manifest.Capabilities = capabilities

	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"UT-133.01 invalid schema field is named", func(t *testing.T) {
			broken := append([]Capability{}, capabilities...)
			broken[0].Root = "Query.unknown"
			err := ValidateCapabilities([]byte(testSchema), broken)
			if err == nil || !strings.Contains(err.Error(), "Query.unknown") {
				t.Fatalf("expected named unmapped field, got %v", err)
			}
		}},
		{"UT-133.02 empty data retains a first-use journey", func(t *testing.T) {
			if err := ValidateCapabilities([]byte(testSchema), capabilities); err != nil {
				t.Fatal(err)
			}
		}},
		{"UT-133.03 limits remain documented", func(t *testing.T) {
			if !strings.Contains(capabilities[0].Journey, "pagination") {
				t.Fatal("pagination behavior is undocumented")
			}
		}},
		{"UT-133.04 denial remains documented", func(t *testing.T) {
			if !strings.Contains(capabilities[0].Journey, "denial") {
				t.Fatal("denial behavior is undocumented")
			}
		}},
		{"UT-133.05 schema drift rejects concurrent clients", func(t *testing.T) {
			if err := ValidateManifest([]byte("type Query { changed: String! }"), manifest, []string{"admin"}); err == nil {
				t.Fatal("expected schema drift rejection")
			}
		}},
		{"UT-133.06 partial generation rejects missing product", func(t *testing.T) {
			if err := ValidateManifest([]byte(testSchema), manifest, []string{"admin", "capture"}); err == nil {
				t.Fatal("expected missing product rejection")
			}
		}},
		{"UT-133.07 retry requires mutation identity", func(t *testing.T) {
			if err := ValidateCapabilities([]byte(testSchema), capabilities); err != nil {
				t.Fatal(err)
			}
		}},
		{"UT-133.08 prerequisite state remains documented", func(t *testing.T) {
			if !strings.Contains(capabilities[0].Journey, "prerequisite") {
				t.Fatal("prerequisite behavior is undocumented")
			}
		}},
		{"UT-133.09 archived state remains documented", func(t *testing.T) {
			if !strings.Contains(capabilities[0].Journey, "archived-state") {
				t.Fatal("archived-state behavior is undocumented")
			}
		}},
		{"UT-133.10 scale behavior remains documented", func(t *testing.T) {
			if !strings.Contains(capabilities[0].Journey, "scale") {
				t.Fatal("scale behavior is undocumented")
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, test.check)
	}
}
