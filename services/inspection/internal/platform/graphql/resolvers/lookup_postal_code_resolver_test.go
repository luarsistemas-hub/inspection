package resolvers

import (
	"context"
	"errors"
	"fmt"
	"testing"

	postallookup "inspection/services/inspection/internal/features/addresses/lookup_postal_code"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestLookupPostalCodeMapsFailuresToStablePortugueseErrors(t *testing.T) {
	cases := []struct {
		name       string
		postalCode string
		failure    error
		wantCode   apperror.Code
		wantText   string
		wantAsked  bool
	}{
		{name: "invalid format", postalCode: "01001-000", wantCode: apperror.InvalidInput, wantText: "CEP inválido."},
		{name: "rate limited", postalCode: "01001000", failure: fmt.Errorf("limite de consultas atingido; tente novamente em instantes"), wantCode: apperror.RateLimited, wantText: "Muitas consultas de CEP em sequência. Aguarde alguns instantes e tente novamente.", wantAsked: true},
		{name: "provider unavailable", postalCode: "01001000", failure: fmt.Errorf("consulta de CEP indisponível"), wantCode: apperror.DependencyUnavailable, wantText: "A consulta de CEP está indisponível no momento.", wantAsked: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := mediator.New()
			asked := false
			if err := bus.RegisterQuery(postallookup.Query{}, func(context.Context, any) (any, error) {
				asked = true
				return nil, tc.failure
			}); err != nil {
				t.Fatal(err)
			}
			resolver := queryResolver{&Resolver{Bus: bus}}
			_, err := resolver.LookupPostalCode(requestctx.WithClientIP(context.Background(), "203.0.113.10"), tc.postalCode)
			var public *apperror.Error
			if !errors.As(err, &public) {
				t.Fatalf("error = %v, want apperror", err)
			}
			if public.Code != tc.wantCode || public.Field != "postalCode" || public.Safe != tc.wantText {
				t.Fatalf("error = %#v, want code %s, field postalCode and %q", public, tc.wantCode, tc.wantText)
			}
			if asked != tc.wantAsked {
				t.Fatalf("lookup asked = %v, want %v", asked, tc.wantAsked)
			}
		})
	}
}

func TestLookupPostalCodeReturnsTheFoundAddress(t *testing.T) {
	bus := mediator.New()
	street := "Praça da Sé"
	if err := bus.RegisterQuery(postallookup.Query{}, func(_ context.Context, raw any) (any, error) {
		if query := raw.(postallookup.Query); query.ClientKey != "203.0.113.10" {
			t.Fatalf("client key = %q", query.ClientKey)
		}
		return postallookup.Result{Found: true, PostalCode: "01001000", Street: &street}, nil
	}); err != nil {
		t.Fatal(err)
	}
	resolver := queryResolver{&Resolver{Bus: bus}}
	result, err := resolver.LookupPostalCode(requestctx.WithClientIP(context.Background(), "203.0.113.10"), " 01001000 ")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Found || result.Street == nil || *result.Street != street {
		t.Fatalf("result = %#v", result)
	}
}
