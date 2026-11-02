package resolvers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	graph "inspection/services/inspection/internal/platform/graphql"

	"github.com/99designs/gqlgen/graphql/handler"
)

// TestCustomerSchemaExposesOnlyAllowlistedFieldsIT220 covers IT-220: the
// customer types carry no internal report body, object key or reasoning, so a
// document selecting them fails schema validation before any resolver runs.
func TestCustomerSchemaExposesOnlyAllowlistedFieldsIT220(t *testing.T) {
	schema := graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{}}).Schema()
	forbidden := map[string]bool{
		"canonicaljson": true, "html": true, "objectkey": true, "jsondigest": true, "htmldigest": true,
		"findings": true, "timeline": true, "reasoncodes": true, "reasoning": true, "promptdigest": true,
		"displaydigest": true, "framedigest": true, "pdfstatus": true, "evidence": true, "notes": true,
	}
	for _, name := range []string{"CustomerReport", "CustomerEvidenceItem", "CustomerEvidenceConnection", "CustomerTimelineEntry", "CustomerTimelineConnection", "CustomerInventory", "CustomerInventoryEntry", "CustomerInventoryPair", "CustomerInventoryCoverage", "CustomerPortfolioItem"} {
		definition := schema.Types[name]
		if definition == nil {
			t.Fatalf("customer type %s is missing", name)
		}
		for _, field := range definition.Fields {
			if forbidden[strings.ToLower(field.Name)] {
				t.Errorf("%s exposes internal field %s", name, field.Name)
			}
		}
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{}}))
	for _, selection := range []string{
		`customerReport(inspectionId:"1") { canonicalJSON }`,
		`customerReport(inspectionId:"1") { html }`,
		`customerEvidence(inspectionId:"1") { nodes { objectKey } }`,
		`customerEvidence(inspectionId:"1") { nodes { reasoning } }`,
		`customerTimeline { nodes { notes } }`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+quote(`query { `+selection+` }`)+`}`)).WithContext(context.Background())
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		body := recorder.Body.String()
		if !strings.Contains(body, "GRAPHQL_VALIDATION_FAILED") || strings.Contains(body, `"data":{`) {
			t.Errorf("%s was not rejected by schema validation: %s", selection, body)
		}
	}
}
