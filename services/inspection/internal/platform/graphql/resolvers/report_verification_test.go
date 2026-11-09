package resolvers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestVerifyReportRateLimitAllowsThirtyRequestsPerMinute(t *testing.T) {
	publicVerificationRateLimit.Lock()
	publicVerificationRateLimit.windows = map[string]verificationWindow{}
	publicVerificationRateLimit.queue = nil
	publicVerificationRateLimit.cleanup = 0
	publicVerificationRateLimit.Unlock()
	t.Cleanup(func() {
		publicVerificationRateLimit.Lock()
		publicVerificationRateLimit.windows = map[string]verificationWindow{}
		publicVerificationRateLimit.queue = nil
		publicVerificationRateLimit.cleanup = 0
		publicVerificationRateLimit.Unlock()
	})

	start := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	for request := 1; request <= 30; request++ {
		if !allowPublicVerification("test-client", start) {
			t.Fatalf("request %d was rate limited before the limit", request)
		}
	}
	if allowPublicVerification("test-client", start) {
		t.Fatal("31st request in a minute was allowed")
	}
	if !allowPublicVerification("test-client", start.Add(time.Minute)) {
		t.Fatal("request after the fixed window was rate limited")
	}
}

func TestVerifyReportReturnsStableRateLimitedCode(t *testing.T) {
	publicVerificationRateLimit.Lock()
	publicVerificationRateLimit.windows = map[string]verificationWindow{
		"test-client": {started: time.Now().UTC(), count: verificationWindowLimit},
	}
	publicVerificationRateLimit.queue = nil
	publicVerificationRateLimit.cleanup = 0
	publicVerificationRateLimit.Unlock()
	t.Cleanup(func() {
		publicVerificationRateLimit.Lock()
		publicVerificationRateLimit.windows = map[string]verificationWindow{}
		publicVerificationRateLimit.queue = nil
		publicVerificationRateLimit.cleanup = 0
		publicVerificationRateLimit.Unlock()
	})

	ctx := requestctx.WithClientIP(context.Background(), "test-client")
	_, err := (&queryResolver{Resolver: &Resolver{}}).verifyReport(ctx, "short", 1)
	if !apperror.Is(err, apperror.RateLimited) {
		t.Fatalf("verifyReport error = %v, want stable RATE_LIMITED code", err)
	}
}

func TestVerifyReportWithoutClientIPDoesNotUseSharedRateLimitBucket(t *testing.T) {
	publicVerificationRateLimit.Lock()
	publicVerificationRateLimit.windows = map[string]verificationWindow{}
	publicVerificationRateLimit.queue = nil
	publicVerificationRateLimit.cleanup = 0
	publicVerificationRateLimit.Unlock()
	t.Cleanup(func() {
		publicVerificationRateLimit.Lock()
		publicVerificationRateLimit.windows = map[string]verificationWindow{}
		publicVerificationRateLimit.queue = nil
		publicVerificationRateLimit.cleanup = 0
		publicVerificationRateLimit.Unlock()
	})
	resolver := &queryResolver{Resolver: &Resolver{}}
	for range 40 {
		payload, err := resolver.verifyReport(context.Background(), "short", 1)
		if err != nil || payload == nil || payload.ReportNumber != nil {
			t.Fatalf("request without IP was rate-limited or leaked data: %+v err=%v", payload, err)
		}
	}
	publicVerificationRateLimit.Lock()
	defer publicVerificationRateLimit.Unlock()
	if len(publicVerificationRateLimit.windows) != 0 {
		t.Fatalf("anonymous no-IP requests created a shared bucket: %v", publicVerificationRateLimit.windows)
	}
}

func TestVerifyReportRateLimitBoundsClientBuckets(t *testing.T) {
	publicVerificationRateLimit.Lock()
	publicVerificationRateLimit.windows = map[string]verificationWindow{}
	publicVerificationRateLimit.queue = nil
	publicVerificationRateLimit.cleanup = 0
	publicVerificationRateLimit.Unlock()
	t.Cleanup(func() {
		publicVerificationRateLimit.Lock()
		publicVerificationRateLimit.windows = map[string]verificationWindow{}
		publicVerificationRateLimit.queue = nil
		publicVerificationRateLimit.cleanup = 0
		publicVerificationRateLimit.Unlock()
	})

	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	for i := 0; i <= verificationClientLimit; i++ {
		allowPublicVerification(fmt.Sprintf("client-%d", i), now)
	}
	publicVerificationRateLimit.Lock()
	defer publicVerificationRateLimit.Unlock()
	if got := len(publicVerificationRateLimit.windows); got > verificationClientLimit {
		t.Fatalf("client buckets=%d, limit=%d", got, verificationClientLimit)
	}
}

func TestIT060ReissueReportRefusesViewerAndAllowsEmployeeAuthorization(t *testing.T) {
	tenantID := identity.NewID()
	resolver := &mutationResolver{Resolver: &Resolver{}}
	for _, role := range []string{auth.Viewer, auth.CustomerViewer} {
		ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{role}, ProductEntitlements: auth.ProductsForRole(role), Product: auth.DashboardProduct}})
		_, err := resolver.reissueReport(ctx, graphql.ReissueReportInput{InspectionID: identity.NewID().String(), ExpectedVersion: 1, ClientMutationID: "it060-forbidden-" + role})
		if err == nil || !apperror.Is(err, apperror.Forbidden) || err.Error() != "Acesso negado." {
			t.Fatalf("%s caller was allowed to request a report reissue", role)
		}
	}
	employeeCtx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.Employee}, ProductEntitlements: auth.ProductsForRole(auth.Employee), Product: auth.DashboardProduct}})
	_, err := resolver.reissueReport(employeeCtx, graphql.ReissueReportInput{InspectionID: "invalid-id", ExpectedVersion: 1, ClientMutationID: "it060-employee-authorized"})
	if !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("employee was refused before input validation, err=%v", err)
	}
}
