package harness

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ProbeProviders checks the WireMock health endpoint used by the local
// LiteLLM and Gotenberg stubs. The endpoint is also harmless against a real
// WireMock-backed CI deployment.
func (h *Harness) ProbeProviders(ctx context.Context) error {
	if h == nil || h.HTTP == nil {
		return fmt.Errorf("integration harness: HTTP client unavailable")
	}
	for name, baseURL := range map[string]string{"LiteLLM": h.LiteLLMURL, "Gotenberg": h.GotenbergURL} {
		endpoint := strings.TrimRight(baseURL, "/") + "/__admin/health"
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return fmt.Errorf("%s probe: %w", name, err)
		}
		response, err := h.HTTP.Do(request)
		if err != nil {
			return fmt.Errorf("%s probe: %w", name, err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("%s probe status %d", name, response.StatusCode)
		}
	}
	return nil
}
