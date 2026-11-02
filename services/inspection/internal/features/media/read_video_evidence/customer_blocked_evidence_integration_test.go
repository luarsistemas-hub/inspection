//go:build integration

package read_video_evidence

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"inspection/libs/identity"
	report "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
)

// TestPubFixCustomerCannotReadFramesBlockedInTheSnapshot covers M4: the
// endpoint, not only the URL minting, refuses a visual the published snapshot
// blocked (availability or sensitive flag), for the frame and for the source video that cites it; internal readers keep access and a clean frame of an
// active release stays readable.
func TestPubFixCustomerCannotReadFramesBlockedInTheSnapshot(t *testing.T) {
	cases := []struct {
		name         string
		mutate       func(*report.Evidence)
		wantCustomer int
	}{
		{"clean evidence of an active release", nil, http.StatusOK},
		{"availability SENSITIVE_BLOCKED", func(e *report.Evidence) { e.Availability = "SENSITIVE_BLOCKED" }, http.StatusNotFound},
		{"flag SENSITIVE", func(e *report.Evidence) { e.Flags = []string{"SENSITIVE"} }, http.StatusNotFound},
		{"flag SENSITIVE_DETECTION", func(e *report.Evidence) { e.Flags = []string{"SENSITIVE_DETECTION"} }, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := seedTask07Report(t, tc.mutate)
			now := time.Now().UTC().Truncate(time.Microsecond)
			if err := d.h.AdminDB.Create(&database.ReportPublication{ID: identity.NewID(), TenantID: d.fixture.TenantID, SnapshotID: d.snapshotID, InspectionID: d.fixture.InspectionID, PolicyVersion: 1, ClientMutationID: "pubfix-m4-" + d.snapshotID.String(), Status: "PUBLISHED", ActorID: d.fixture.IdentityID, Version: 1, PublishedAt: &now, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				t.Fatal(err)
			}
			customer := requestctx.Metadata{TenantID: d.fixture.TenantID, Principal: requestctx.Principal{IdentityID: d.fixture.IdentityID, MembershipID: d.fixture.MembershipID, TenantID: d.fixture.TenantID, Roles: []string{auth.CustomerViewer}, Scopes: []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: d.fixture.BusinessUnitID}}}}
			mux := d.mux(t, true, customer, &readGuardClient{})
			serve := func(path string) int {
				response := httptest.NewRecorder()
				mux.ServeHTTP(response, httptest.NewRequest(http.MethodHead, path, nil))
				return response.Code
			}
			frame := d.routes()["frame"]
			if got := serve(frame); got != tc.wantCustomer {
				t.Fatalf("customer frame status=%d, want %d", got, tc.wantCustomer)
			}
			if tc.wantCustomer == http.StatusNotFound {
				if got := serve(d.routes()["source"]); got != http.StatusNotFound {
					t.Fatalf("customer source of fully blocked evidence status=%d, want 404", got)
				}
			}
		})
	}
}
