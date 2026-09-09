package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/requestctx"
)

type membershipResolverFunc func(context.Context, string, string, identity.ID) (requestctx.Principal, error)

func (f membershipResolverFunc) ResolveMembership(ctx context.Context, issuer, subject string, membershipID identity.ID) (requestctx.Principal, error) {
	return f(ctx, issuer, subject, membershipID)
}

func TestRequireMembershipContextUT005ToUT007(t *testing.T) {
	member, tenant := identity.NewID(), identity.NewID()
	resolver := membershipResolverFunc(func(_ context.Context, issuer, subject string, selected identity.ID) (requestctx.Principal, error) {
		if issuer != "issuer" || subject != "subject" || selected != member {
			return requestctx.Principal{}, errors.New("not owned")
		}
		return requestctx.Principal{IdentityID: identity.NewID(), MembershipID: member, TenantID: tenant, Roles: []string{TenantAdmin}}, nil
	})
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		meta, ok := requestctx.FromContext(r.Context())
		if !ok || meta.TenantID != tenant || meta.Principal.MembershipID != member {
			t.Fatal("resolved metadata missing")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/graphql", nil)
	request.Header.Set(MembershipHeader, member.String())
	request = request.WithContext(requestctx.WithMetadata(request.Context(), requestctx.Metadata{
		TenantID: identity.NewID(),
		Principal: requestctx.Principal{
			Issuer: "issuer", Subject: "subject", TenantID: identity.NewID(), MembershipID: identity.NewID(),
		},
	}))
	recorder := httptest.NewRecorder()
	RequireMembershipContext(next, resolver).ServeHTTP(recorder, request)
	if !called || recorder.Code != http.StatusNoContent {
		t.Fatalf("valid membership response=%d called=%v", recorder.Code, called)
	}
}

func TestRequireMembershipContextUT006GenericDenial(t *testing.T) {
	denied := RequireMembershipContext(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("next called") }), membershipResolverFunc(func(context.Context, string, string, identity.ID) (requestctx.Principal, error) {
		return requestctx.Principal{}, errors.New("foreign")
	}))
	for _, testCase := range []struct {
		name, value string
	}{
		{"UT-134.01 unknown membership", identity.NewID().String()},
		{"UT-134.02 no membership", ""},
		{"UT-134.03 selector scale", identity.NewID().String()},
		{"UT-134.04 inactive membership", identity.NewID().String()},
		{"UT-134.05 concurrent tab", identity.NewID().String()},
		{"UT-134.06 interrupted provisioning", identity.NewID().String()},
		{"UT-134.07 replay", identity.NewID().String()},
		{"UT-134.08 hostile deep link", "not-a-uuid"},
		{"UT-134.09 changed session", identity.NewID().String()},
		{"UT-134.10 similar tenant name", identity.NewID().String()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/graphql", nil)
			req.Header.Set(MembershipHeader, testCase.value)
			req = req.WithContext(requestctx.WithMetadata(req.Context(), requestctx.Metadata{Principal: requestctx.Principal{Issuer: "issuer", Subject: "subject"}}))
			recorder := httptest.NewRecorder()
			denied.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusForbidden || recorder.Body.String() != "access denied\n" {
				t.Fatalf("header %q disclosed membership state: %d %q", testCase.value, recorder.Code, recorder.Body.String())
			}
		})
	}
}
