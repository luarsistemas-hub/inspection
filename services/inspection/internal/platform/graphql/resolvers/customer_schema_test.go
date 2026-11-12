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
	checklistTypes := []string{"CustomerChecklist", "CustomerChecklistRoom", "CustomerChecklistComponent", "CustomerHandover", "CustomerHandoverEntry", "CustomerComparison", "CustomerComparisonItem", "CustomerComparisonPair", "CustomerComparisonObservation", "ReportFindingCounts"}
	for _, field := range []string{"id", "url", "typeCode", "key", "outsideScope", "attributeSources"} {
		forbidden[field] = true
	}
	for _, name := range append([]string{"CustomerReport", "CustomerEvidenceItem", "CustomerEvidenceConnection", "CustomerTimelineEntry", "CustomerTimelineConnection", "CustomerInventory", "CustomerInventoryEntry", "CustomerInventoryPair", "CustomerInventoryCoverage", "CustomerPortfolioItem"}, checklistTypes...) {
		definition := schema.Types[name]
		if definition == nil {
			t.Fatalf("customer type %s is missing", name)
		}
		for _, field := range definition.Fields {
			if forbidden[strings.ToLower(field.Name)] && (field.Name != "id" && field.Name != "url" || containsString(checklistTypes, name)) {
				t.Errorf("%s exposes internal field %s", name, field.Name)
			}
		}
	}
	for _, typeName := range checklistTypes {
		if schema.Types[typeName] == nil {
			t.Fatalf("customer type %s is missing", typeName)
		}
	}
	allowlist := map[string][]string{
		"CustomerReport":               {"inspectionId", "snapshotId", "version", "classification", "classificationLabel", "limited", "advisory", "status", "historical", "evidenceNotice", "scopeProfile", "scopeStatements", "reasonLabels", "findingCounts", "context", "requirements", "inventory", "checklist", "processing", "handover", "comparison"},
		"ReportFindingCounts":          {"total", "critical", "high", "medium", "low", "informational"},
		"CustomerChecklist":             {"coverage", "rooms"},
		"CustomerChecklistRoom":         {"name", "components"},
		"CustomerChecklistComponent":    {"label", "displayGroup", "status", "description", "condition", "reviewLabel", "evidenceIds", "limitations"},
		"CustomerHandover":              {"entries"},
		"CustomerHandoverEntry":         {"label", "kind", "status", "type", "value", "unit", "date", "declarant", "evidenceIds"},
		"CustomerComparison":            {"baselineVersionNumber", "purpose", "purposeLabel", "activatedAt", "status", "statusLabel", "counts", "displayed", "items", "outsideBaseline", "disclaimer"},
		"CustomerComparisonItem":        {"room", "name", "outcome", "outcomeLabel", "baselineCondition", "currentCondition", "limitations", "pairs"},
		"CustomerComparisonPair":        {"baselineEvidence", "currentEvidence", "baselineAvailability", "currentAvailability"},
		"CustomerComparisonObservation": {"room", "name"},
	}
	for typeName, fields := range allowlist {
		actual := map[string]bool{}
		for _, field := range schema.Types[typeName].Fields {
			actual[field.Name] = true
		}
		if len(actual) != len(fields) {
			t.Errorf("%s field allowlist has %d fields; want %d", typeName, len(actual), len(fields))
		}
		for _, field := range fields {
			if !actual[field] {
				t.Errorf("%s is missing allowlisted field %s", typeName, field)
			}
		}
	}
	evidenceFields := map[string]bool{}
	for _, field := range schema.Types["CustomerEvidenceItem"].Fields {
		evidenceFields[field.Name] = true
	}
	if !evidenceFields["reference"] {
		t.Error("CustomerEvidenceItem is missing its customer-safe reference field")
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

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
