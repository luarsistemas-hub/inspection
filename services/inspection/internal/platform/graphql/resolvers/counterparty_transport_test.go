package resolvers

import (
	"context"
	"strings"
	"testing"

	"inspection/libs/identity"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
)

func TestUnit_UT323_CounterpartyOperationsRequireExternalSession(t *testing.T) {
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{}}))
	server.SetErrorPresenter(graph.PresentError)
	for _, tc := range []struct{ name, query string }{
		{name: "UT-323 baseline query", query: `query { counterpartyBaseline { response } }`},
		{name: "UT-323 acknowledgment", query: `mutation { acknowledgeBaseline(input:{clientMutationId:"unauth-ack"}) { response clientMutationId } }`},
		{name: "UT-323 contestation", query: `mutation { contestBaseline(input:{statement:"not allowed",clientMutationId:"unauth-contest"}) { response clientMutationId } }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, ctx := range []context.Context{
				context.Background(),
				requestctx.WithMetadata(context.Background(), requestctx.Metadata{Principal: requestctx.Principal{IdentityID: identity.NewID()}}),
			} {
				response := executeVideoPolicyGraphQL(t, server, tc.query, ctx)
				if !strings.Contains(response, "UNAUTHENTICATED") || !strings.Contains(response, `"data":null`) {
					t.Fatalf("counterparty operation without a COUNTERPARTY session was not rejected: %s", response)
				}
			}
		})
	}
}
