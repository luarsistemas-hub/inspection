package graphql

import (
	"os"
	"strings"
	"testing"
)

func TestLifecycleQueriesIT413ToIT422(t *testing.T) {
	schema := catalogSchema(t)
	for _, field := range []string{"schedules", "projects", "project", "inspections", "inspection"} {
		if !strings.Contains(schema, field+"(") {
			t.Fatalf("query %s is absent", field)
		}
	}
	for _, contract := range []string{"ScheduleConnection", "ProjectConnection", "InspectionConnection", "pageInfo: PageInfo!", "history: Boolean = false"} {
		if !strings.Contains(schema, contract) {
			t.Fatalf("lifecycle query contract is missing %q", contract)
		}
	}
}

func TestLifecycleMutationsIT479ToIT502(t *testing.T) {
	schema := catalogSchema(t)
	for _, field := range []string{"createSchedule", "updateSchedule", "cancelSchedule", "createInspection", "cancelInspection", "invalidateInspection", "createProject", "addExceptionalStage", "startProjectStage", "skipProjectStage", "closeProject", "reopenProject"} {
		if !strings.Contains(schema, field+"(") {
			t.Fatalf("mutation %s is absent", field)
		}
	}
	for _, contract := range []string{"expectedVersion: Int!", "clientMutationId: String!", "userErrors: [UserError!]!", "reason: String!"} {
		if !strings.Contains(schema, contract) {
			t.Fatalf("lifecycle mutation contract is missing %q", contract)
		}
	}
	resolver, err := os.ReadFile("resolvers/schema.resolvers.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"Schedules", "Projects", "Project", "Inspections", "Inspection", "CreateSchedule", "CreateInspection", "CreateProject", "StartProjectStage"} {
		if strings.Contains(string(resolver), "not implemented: "+field) {
			t.Fatalf("resolver %s is not implemented", field)
		}
	}
}
