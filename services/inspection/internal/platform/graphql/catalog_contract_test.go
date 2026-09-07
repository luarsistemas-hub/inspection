package graphql

import (
	"os"
	"strings"
	"testing"
)

func TestCatalogQueriesIT397ToIT410(t *testing.T) {
	schema := catalogSchema(t)
	for _, field := range []string{"participants", "participant", "segmentDefinitions", "templates", "templateVersion", "assets", "asset"} {
		t.Run(field, func(t *testing.T) {
			if !strings.Contains(schema, field+"(") {
				t.Fatalf("query %s is absent from generated contract", field)
			}
		})
	}
}

func TestCatalogMutationsIT453ToIT472(t *testing.T) {
	schema := catalogSchema(t)
	for _, field := range []string{"upsertParticipant", "verifyContact", "setDeliveryChannels", "publishSegmentDefinition", "publishTemplateVersion", "activateTemplateVersion", "publishAnalysisProfile", "registerAsset", "updateAsset", "archiveAsset"} {
		t.Run(field, func(t *testing.T) {
			if !strings.Contains(schema, field+"(") {
				t.Fatalf("mutation %s is absent from generated contract", field)
			}
		})
	}
	for _, contract := range []string{"userErrors: [UserError!]!", "clientMutationId: String!", "canonicalDigest: String!", "pageInfo: PageInfo!"} {
		if !strings.Contains(schema, contract) {
			t.Fatalf("safe generated contract is missing %q", contract)
		}
	}
}

func catalogSchema(t *testing.T) string {
	t.Helper()
	payload, err := os.ReadFile("../../../schema.graphqls")
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}
