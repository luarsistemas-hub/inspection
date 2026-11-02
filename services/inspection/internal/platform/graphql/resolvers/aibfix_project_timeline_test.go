package resolvers

import (
	"strings"
	"testing"

	"inspection/libs/identity"
)

// AIBFIX: projectTimeline answers "Projeto não encontrado." only for a project
// that does not exist; any other failure is the generic internal failure in
// Portuguese, never a missing project and never a technical detail.
func TestAIBFIXProjectTimelineOnlyMissingProjectsAreNotFound(t *testing.T) {
	f := newAIPTransport(t)
	query := `query { projectTimeline(projectId:"` + identity.NewID().String() + `") { projectId } }`
	missing := f.failure(t, f.admin, query, "NOT_FOUND", "projectId")
	if missing.Message != "Projeto não encontrado." {
		t.Fatalf("missing project message = %q", missing.Message)
	}
	if err := f.db.Exec("ALTER TABLE projects.projects RENAME TO projects_gone").Error; err != nil {
		t.Fatal(err)
	}
	broken := f.failure(t, f.admin, query, "INTERNAL", "")
	if broken.Message != "Não foi possível concluir a operação." || strings.Contains(strings.ToLower(broken.Message), "table") {
		t.Fatalf("an internal failure was reported as %q, want the generic Portuguese message", broken.Message)
	}
}
