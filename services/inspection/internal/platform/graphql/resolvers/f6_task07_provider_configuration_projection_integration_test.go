//go:build integration

package resolvers

import (
	"strings"
	"testing"

	"inspection/services/inspection/internal/platform/database"
)

// This is projection evidence only; the provider authentication error itself
// is injected and exercised by the preparation executor in the complete IT-149.
func TestVV_IT_149ProviderConfigurationFailureIsSafeAndRetryableProjection(t *testing.T) {
	fx := newAPI08Fixture(t)
	failed := seedAPI08Preparation(t, fx, "FAILED", 4, "RETAINED")
	if err := fx.h.AdminDB.Model(&database.VideoPreparationJob{}).
		Where("tenant_id=? AND id=?", fx.seed.TenantID, failed.preparationID).
		Update("error_code", "PROVIDER_CONFIGURATION").Error; err != nil {
		t.Fatal(err)
	}
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture,
		`query { videoPreparations(status:["FAILED"]) { nodes { mediaId preparationId status stage errorCode retryable sourceAvailable } } }`, fx.ctx, "", "")
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"errorCode":"PROVIDER_CONFIGURATION"`) || !strings.Contains(response, `"retryable":true`) || !strings.Contains(response, failed.mediaID.String()) {
		t.Fatalf("safe provider failure projection = %s", response)
	}
	if strings.Contains(response, "qa-secret-123") || strings.Contains(response, "authentication failed") {
		t.Fatalf("provider credential detail leaked in operations projection: %s", response)
	}
	retried := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture,
		api08Query(fx, "retryVideoPreparation", failed.mediaID, 4, "", "vv-it-149-retry"), fx.ctx, "", "")
	if strings.Contains(retried, `"errors"`) || !strings.Contains(retried, `"status":"QUEUED"`) || !strings.Contains(retried, `"preparationVersion":5`) {
		t.Fatalf("provider configuration recovery action = %s", retried)
	}
}
