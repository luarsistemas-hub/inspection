package lookup_postal_code

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"inspection/services/inspection/internal/platform/mediator"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestLookupPostalCodeSuccessAndNegativeCache(t *testing.T) {
	requests := 0
	service := &Service{Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		body := `{"cep":"01001-000","logradouro":"Praça da Sé","bairro":"Sé","localidade":"São Paulo","uf":"SP","ibge":"3550308"}`
		if strings.HasSuffix(request.URL.Path, "00000000/json/") {
			body = `{"erro":true}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	result, err := service.Lookup(context.Background(), "01001000", "client")
	if err != nil || !result.Found || result.PostalCode != "01001000" || result.City == nil || *result.City != "São Paulo" {
		t.Fatalf("success lookup: result=%#v err=%v", result, err)
	}
	if _, err := service.Lookup(context.Background(), "01001000", "client"); err != nil || requests != 1 {
		t.Fatalf("success cache: requests=%d err=%v", requests, err)
	}
	missing, err := service.Lookup(context.Background(), "00000000", "client")
	if err != nil || missing.Found {
		t.Fatalf("missing lookup: result=%#v err=%v", missing, err)
	}
	_, _ = service.Lookup(context.Background(), "00000000", "client")
	if requests != 2 {
		t.Fatalf("negative cache made %d requests", requests)
	}
}

func TestLookupPostalCodeInvalidUnavailableAndTimeout(t *testing.T) {
	called := false
	service := &Service{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { called = true; return nil, errors.New("offline") })}}
	if _, err := service.Lookup(context.Background(), "123", ""); err == nil || called {
		t.Fatal("invalid CEP reached provider")
	}
	if _, err := service.Lookup(context.Background(), "12345678", ""); err == nil {
		t.Fatal("provider outage was not reported")
	}
	service.Client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })
	if _, err := service.Lookup(context.Background(), "87654321", ""); err == nil {
		t.Fatal("timeout was not reported")
	}
}

func TestLookupPostalCodeRateLimit(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	service := &Service{Now: func() time.Time { return now }, Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"erro":true}`)), Header: make(http.Header)}, nil
	})}}
	for range 30 {
		if _, err := service.Lookup(context.Background(), "12345678", "same-client"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.Lookup(context.Background(), "87654321", "same-client"); err == nil {
		t.Fatal("rate limit did not reject the 31st query")
	}
}

func TestSetupValidatesDependenciesAndDispatches(t *testing.T) {
	bus := mediator.New()
	service := &Service{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"erro":true}`))}, nil
	})}}
	if Setup(nil, service) == nil || Setup(bus, nil) == nil {
		t.Fatal("missing dependency accepted")
	}
	if err := Setup(bus, service); err != nil {
		t.Fatal(err)
	}
	raw, err := bus.Ask(context.Background(), Query{PostalCode: "01001000", ClientKey: "test"})
	if err != nil || raw.(Result).Found {
		t.Fatalf("lookup dispatch: result=%#v err=%v", raw, err)
	}
	if _, err := bus.Ask(context.Background(), Query{PostalCode: "invalid"}); err == nil {
		t.Fatal("invalid input accepted through setup")
	}
	service.Client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })
	if _, err := bus.Ask(context.Background(), Query{PostalCode: "20040002"}); err == nil {
		t.Fatal("dependency failure swallowed through setup")
	}
}

func TestLookupStorageStaysBoundedAndReclaimsExpiredClients(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	service := &Service{limits: map[string][]time.Time{}, cache: map[string]cacheEntry{}}
	for index := range maxLookupEntries {
		key := fmt.Sprintf("client-%d", index)
		service.limits[key] = []time.Time{now.Add(-2 * time.Minute)}
		service.cache[key] = cacheEntry{expires: now.Add(-time.Minute)}
	}
	if !service.allow("new-client", now) {
		t.Fatal("expired clients were not reclaimed")
	}
	service.save("01001000", Result{Found: true}, now.Add(time.Hour), now)
	if len(service.limits) >= maxLookupEntries || len(service.cache) >= maxLookupEntries {
		t.Fatalf("expired lookup entries remain: clients=%d cache=%d", len(service.limits), len(service.cache))
	}
	service.limits = map[string][]time.Time{}
	service.cache = map[string]cacheEntry{}
	for index := range maxLookupEntries {
		key := fmt.Sprintf("active-%d", index)
		service.limits[key] = []time.Time{now}
		service.cache[key] = cacheEntry{expires: now.Add(time.Hour)}
	}
	if service.allow("another-client", now) {
		t.Fatal("unbounded new client was accepted")
	}
	service.save("another-cep", Result{}, now.Add(time.Hour), now)
	if len(service.cache) > maxLookupEntries || len(service.limits) > maxLookupEntries {
		t.Fatalf("lookup storage grew without bound: clients=%d cache=%d", len(service.limits), len(service.cache))
	}
}
