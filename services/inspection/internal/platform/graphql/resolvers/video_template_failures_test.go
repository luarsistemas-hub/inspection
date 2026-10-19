package resolvers

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"inspection/libs/identity"
	templateactivate "inspection/services/inspection/internal/features/templates/activate_template"
	templatelist "inspection/services/inspection/internal/features/templates/list_templates"
	templatepublish "inspection/services/inspection/internal/features/templates/publish_template"
	"inspection/services/inspection/internal/platform/apperror"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
)

func TestVideoTemplateGraphQLOperationsMapAdapterFailures(t *testing.T) {
	for _, failure := range []struct {
		name string
		err  error
		code string
	}{
		{"unavailable", apperror.New(apperror.DependencyUnavailable, "template", "template store unavailable"), "DEPENDENCY_UNAVAILABLE"},
		{"unexpected", fmt.Errorf("template database error: qa-secret-123"), "INTERNAL"},
	} {
		t.Run(failure.name, func(t *testing.T) {
			bus := mediator.New()
			for _, register := range []func() error{
				func() error {
					return bus.RegisterCommand(templatepublish.Command{}, func(context.Context, any) (any, error) { return nil, failure.err })
				},
				func() error {
					return bus.RegisterCommand(templateactivate.Command{}, func(context.Context, any) (any, error) { return nil, failure.err })
				},
				func() error {
					return bus.RegisterQuery(templatelist.Query{}, func(context.Context, any) (any, error) { return nil, failure.err })
				},
				func() error {
					return bus.RegisterQuery(templatelist.VersionQuery{}, func(context.Context, any) (any, error) { return nil, failure.err })
				},
			} {
				if err := register(); err != nil {
					t.Fatal(err)
				}
			}
			server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{Bus: bus}}))
			server.SetErrorPresenter(graph.PresentError)
			tenantID := identity.NewID()
			ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID}, CorrelationID: "video-template-failure"})
			id := identity.NewID().String()
			for _, operation := range []struct{ name, query, absent string }{
				{"publishTemplateVersion", `mutation { publishTemplateVersion(input:{key:"video",name:"Video",definition:{schemaVersion:1,isVideo:true},clientMutationId:"k1"}) { version { id } } }`, `"publishTemplateVersion":{`},
				{"activateTemplateVersion", `mutation { activateTemplateVersion(input:{versionId:"` + id + `",expectedVersion:3,clientMutationId:"k2"}) { version { id } } }`, `"activateTemplateVersion":{`},
				{"templates", `query { templates { nodes { id } } }`, `"templates":{`},
				{"templateVersion", `query { templateVersion(id:"` + id + `") { id } }`, `"templateVersion":{`},
			} {
				response := executeVideoPolicyGraphQL(t, server, operation.query, ctx)
				if !strings.Contains(response, failure.code) || strings.Contains(response, operation.absent) {
					t.Fatalf("%s did not return safe %s without result data: %s", operation.name, failure.code, response)
				}
				if failure.code == "INTERNAL" && strings.Contains(response, "qa-secret-123") {
					t.Fatalf("%s exposed internal adapter details: %s", operation.name, response)
				}
			}
		})
	}
}
