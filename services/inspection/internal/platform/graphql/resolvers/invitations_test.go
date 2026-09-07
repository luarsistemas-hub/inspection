package resolvers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	requestotp "inspection/services/inspection/internal/features/invitations/request_otp"
	revokeinvitation "inspection/services/inspection/internal/features/invitations/revoke_invitation"
	verifyotp "inspection/services/inspection/internal/features/invitations/verify_otp"
	"inspection/services/inspection/internal/platform/apperror"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
)

func TestIT503ToIT508InvitationGraphQLContracts(t *testing.T) {
	bus := mediator.New()
	_ = bus.RegisterCommand(requestotp.Command{}, func(context.Context, any) (any, error) { return requestotp.Result{Status: "SENT"}, nil })
	_ = bus.RegisterCommand(verifyotp.Command{}, func(context.Context, any) (any, error) {
		return verifyotp.Result{Token: "session-secret", CSRF: "csrf-proof", ExpiresAt: time.Now().Add(time.Hour)}, nil
	})
	_ = bus.RegisterCommand(revokeinvitation.Command{}, func(context.Context, any) (any, error) { return revokeinvitation.Result{Status: "REVOKED"}, nil })
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{Bus: bus}}))
	server.SetErrorPresenter(graph.PresentError)
	cases := []struct {
		name, query, contains string
		cookie                bool
	}{{"IT-503 request success", `mutation { requestInvitationOtp(input:{linkToken:"01234567890123456789012345678901",clientMutationId:"c"}) { status clientMutationId } }`, "SENT", false}, {"IT-505 verify success", `mutation { verifyInvitationOtp(input:{linkToken:"01234567890123456789012345678901",code:"123456",clientMutationId:"c"}) { status csrfToken } }`, "csrf-proof", true}, {"IT-507 revoke success", `mutation { revokeInvitation(input:{linkToken:"01234567890123456789012345678901",clientMutationId:"c"}) { status } }`, "REVOKED", false}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+quote(tc.query)+`}`))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(requestctx.WithResponseWriter(req.Context(), w))
			server.ServeHTTP(w, req)
			if !strings.Contains(w.Body.String(), tc.contains) {
				t.Fatalf("response: %s", w.Body.String())
			}
			if tc.cookie {
				cookie := w.Header().Get("Set-Cookie")
				if !strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "Secure") || !strings.Contains(cookie, "SameSite=Lax") {
					t.Fatalf("unsafe cookie: %s", cookie)
				}
			}
		})
	}

	failing := mediator.New()
	failure := func(context.Context, any) (any, error) {
		return nil, apperror.New(apperror.InvalidInput, "linkToken", "invalid invitation")
	}
	_ = failing.RegisterCommand(requestotp.Command{}, failure)
	_ = failing.RegisterCommand(verifyotp.Command{}, failure)
	_ = failing.RegisterCommand(revokeinvitation.Command{}, failure)
	badServer := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{Bus: failing}}))
	badServer.SetErrorPresenter(graph.PresentError)
	for _, tc := range []struct{ name, field, input string }{{"IT-504 request failure", "requestInvitationOtp", `{linkToken:"bad",clientMutationId:"c"}`}, {"IT-506 verify failure", "verifyInvitationOtp", `{linkToken:"bad",code:"000000",clientMutationId:"c"}`}, {"IT-508 revoke failure", "revokeInvitation", `{linkToken:"bad",clientMutationId:"c"}`}} {
		t.Run(tc.name, func(t *testing.T) {
			query := `mutation { ` + tc.field + `(input:` + tc.input + `) { clientMutationId } }`
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+quote(query)+`}`))
			req.Header.Set("Content-Type", "application/json")
			badServer.ServeHTTP(w, req)
			if !strings.Contains(w.Body.String(), "INVALID_INPUT") || strings.Contains(w.Body.String(), "session-secret") {
				t.Fatalf("unsafe response: %s", w.Body.String())
			}
		})
	}
}

func quote(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}
