//go:build integration

package resolvers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestAPI03VideoConsentAndFrozenCameraAttemptIT398(t *testing.T) {
	// v1 pinned until template schema 1 is removed
	fixture := newAPI03FailureFixtureWithOptions(t, "beginVideoCapture", harness.Task06FixtureOptions{SchemaVersion: 1, RequirementKey: "room-1"})
	accept := executeAPI03Success(t, fixture, `mutation { acceptProcessing(input:{disclosureVersion:"pt-BR-1",photoProcessing:true,videoProcessing:true,aiAnalysis:true,gpsUse:true,clientMutationId:"it398-consent"}) { status } }`)
	if !strings.Contains(accept, `"status":"ACCEPTED"`) {
		t.Fatalf("acceptProcessing did not persist video disclosure v2: %s", accept)
	}
	begin := executeAPI03Success(t, fixture, `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"it398-attempt"}) { attempt { id kind acquisitionSource maxDurationSeconds maxBytes policyVersion admittedAt expiresAt status } } }`)
	for _, expected := range []string{`"kind":"CURRENT"`, `"acquisitionSource":"CAMERA"`, `"maxDurationSeconds":900`, `"maxBytes":1073741824`, `"status":"ACTIVE"`} {
		if !strings.Contains(begin, expected) {
			t.Fatalf("beginVideoCapture response missing %s: %s", expected, begin)
		}
	}
	var attemptPayload struct {
		Data struct {
			Begin struct {
				Attempt struct {
					ID                 string `json:"id"`
					MaxDurationSeconds int    `json:"maxDurationSeconds"`
					MaxBytes           int64  `json:"maxBytes"`
					ExpiresAt          string `json:"expiresAt"`
				} `json:"attempt"`
			} `json:"beginVideoCapture"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(begin), &attemptPayload); err != nil {
		t.Fatal(err)
	}
	if attemptPayload.Data.Begin.Attempt.ID == "" || attemptPayload.Data.Begin.Attempt.MaxDurationSeconds != 900 || attemptPayload.Data.Begin.Attempt.MaxBytes != 1<<30 {
		t.Fatalf("attempt did not freeze canonical limits: %+v", attemptPayload.Data.Begin.Attempt)
	}
	expiry, err := time.Parse(time.RFC3339Nano, attemptPayload.Data.Begin.Attempt.ExpiresAt)
	if err != nil || !expiry.After(time.Now().UTC()) || expiry.After(time.Now().UTC().Add(24*time.Hour+time.Minute)) {
		t.Fatalf("attempt expiry is missing or outside the frozen admission window: %q (%v)", attemptPayload.Data.Begin.Attempt.ExpiresAt, err)
	}
}

func executeAPI03Success(t *testing.T, fixture api03FailureFixture, query string) string {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+quote(query)+`}`))
	req.Header.Set("Content-Type", "application/json")
	ctx := requestctx.WithExternalCredentials(req.Context(), fixture.credentials)
	fixture.server.ServeHTTP(w, req.WithContext(ctx))
	if w.Code != http.StatusOK {
		t.Fatalf("GraphQL status=%d body=%s", w.Code, w.Body.String())
	}
	return w.Body.String()
}
