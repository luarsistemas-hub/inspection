package resolvers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	adminactivation "inspection/services/inspection/internal/features/onboarding/admin_activation"
	onboardingcatalog "inspection/services/inspection/internal/features/onboarding/real_estate_catalog"
	onboardingsession "inspection/services/inspection/internal/features/onboarding/session"
	"inspection/services/inspection/internal/platform/apperror"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"
)

func optionalStringValue(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func mapOnboardingSession(value onboardingsession.Session) *graphql1.OnboardingSession {
	definition, _ := onboardingcatalog.Resolve(onboardingcatalog.Segment)
	return &graphql1.OnboardingSession{
		ID:          value.ID.String(),
		State:       value.State,
		CurrentStep: value.CurrentStep,
		Version:     int(value.Version),
		ExpiresAt:   value.ExpiresAt.Format(time.RFC3339Nano),
		Definition: map[string]any{
			"schemaVersion":   definition.SchemaVersion,
			"version":         definition.Version,
			"segment":         definition.Segment,
			"segmentVersion":  definition.SegmentVersion,
			"steps":           definition.Steps,
			"purposes":        definition.Purposes,
			"originModes":     definition.OriginModes,
			"templates":       definition.Templates,
			"analysisProfile": definition.AnalysisProfile,
		},
	}
}

func mapOnboardingActivation(value adminactivation.Activation) *graphql1.OnboardingActivation {
	var activatedAt *string
	if value.ActivatedAt != nil {
		formatted := value.ActivatedAt.Format(time.RFC3339Nano)
		activatedAt = &formatted
	}
	return &graphql1.OnboardingActivation{TenantID: value.TenantID.String(), IdentityID: value.IdentityID.String(), Purpose: value.Purpose, Status: value.Status, ActivatedAt: activatedAt}
}

func (r *mutationResolver) setOnboardingCookie(ctx context.Context, locator, csrf string) {
	writer, ok := requestctx.ResponseWriter(ctx)
	if !ok {
		return
	}
	http.SetCookie(writer, &http.Cookie{Name: "inspection_onboarding", Value: locator, Path: "/", HttpOnly: true, Secure: requestctx.SecureCookies(ctx), SameSite: http.SameSiteLaxMode, Expires: time.Now().UTC().Add(onboardingsessionCookieTTL)})
	writer.Header().Set("X-CSRF-Token", csrf)
}

const onboardingsessionCookieTTL = 2 * time.Hour

func onboardingPayloadString(payload map[string]any, key string) string {
	value, ok := payload[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func onboardingValidationPayload(err error, mutationID string) (*graphql1.OnboardingPayload, error) {
	code, field, message := apperror.Public(err)
	if code != apperror.InvalidInput && code != apperror.InvalidState && code != apperror.Conflict && code != apperror.RateLimited && code != apperror.SessionExpired {
		return nil, err
	}
	var fieldValue *string
	if field != "" {
		fieldValue = &field
	}
	return &graphql1.OnboardingPayload{
		UserErrors:       []*graphql1.UserError{{Code: string(code), Field: fieldValue, Message: message}},
		ClientMutationID: mutationID,
	}, nil
}

func onboardingAgencyProvisioningKey(sessionToken string, expectedVersion int64) string {
	key := sha256.Sum256([]byte(sessionToken + "\x00" + strconv.FormatInt(expectedVersion, 10)))
	return "onboarding:agency:" + hex.EncodeToString(key[:])
}
