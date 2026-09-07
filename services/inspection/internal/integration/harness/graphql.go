package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// GraphQLError is the stable error shape returned by gqlgen.
type GraphQLError struct {
	Message    string         `json:"message"`
	Path       []any          `json:"path,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

// GraphQLResponse preserves raw data so each assigned IT case can decode only
// the fields it owns without creating a second generated client.
type GraphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []GraphQLError  `json:"errors,omitempty"`
}

// Task06AuthHeaders returns the explicit test principal headers understood by
// the API only in its test authentication mode.
func Task06AuthHeaders(fixture Task06Fixture) map[string]string {
	return Task06AuthHeadersForRoles(fixture, "TENANT_ADMIN")
}

// Task06AuthHeadersForRoles returns the same principal with an explicit role
// set, allowing authorization cases to exercise manager, employee, or viewer
// behavior without bypassing the API boundary.
func Task06AuthHeadersForRoles(fixture Task06Fixture, roles ...string) map[string]string {
	roleValue := strings.Join(roles, ",")
	if roleValue == "" {
		roleValue = "TENANT_ADMIN"
	}
	return map[string]string{
		"X-Inspection-Test-Tenant":     fixture.TenantID.String(),
		"X-Inspection-Test-Identity":   fixture.IdentityID.String(),
		"X-Inspection-Test-Membership": fixture.MembershipID.String(),
		"X-Inspection-Test-Roles":      roleValue,
		"X-Correlation-ID":             "task06-" + fixture.InspectionID.String(),
	}
}

// HasErrors reports whether the GraphQL response contains execution errors.
func (r GraphQLResponse) HasErrors() bool { return len(r.Errors) > 0 }

// RequireNoErrors turns a GraphQL execution error into a regular Go error.
func (r GraphQLResponse) RequireNoErrors() error {
	if len(r.Errors) == 0 {
		return nil
	}
	return fmt.Errorf("graphql: %s", r.Errors[0].Message)
}

// GraphQL executes a query against the configured inspection API.
func (h *Harness) GraphQL(ctx context.Context, query string, variables map[string]any, headers map[string]string) (GraphQLResponse, error) {
	if h == nil || h.HTTP == nil {
		return GraphQLResponse{}, errors.New("integration harness: HTTP client unavailable")
	}
	if strings.TrimSpace(query) == "" {
		return GraphQLResponse{}, errors.New("integration harness: empty GraphQL query")
	}
	payload := struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables,omitempty"`
	}{Query: query, Variables: variables}
	body, err := json.Marshal(payload)
	if err != nil {
		return GraphQLResponse{}, fmt.Errorf("graphql request: %w", err)
	}
	endpoint := strings.TrimRight(h.APIURL, "/")
	if !strings.HasSuffix(endpoint, "/graphql") {
		endpoint += "/graphql"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return GraphQLResponse{}, fmt.Errorf("graphql request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := h.HTTP.Do(request)
	if err != nil {
		return GraphQLResponse{}, fmt.Errorf("graphql HTTP: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return GraphQLResponse{}, fmt.Errorf("graphql response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return GraphQLResponse{}, fmt.Errorf("graphql HTTP status %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var result GraphQLResponse
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return GraphQLResponse{}, fmt.Errorf("graphql response JSON: %w", err)
	}
	return result, nil
}
