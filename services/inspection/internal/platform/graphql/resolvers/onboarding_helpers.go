package resolvers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"inspection/libs/identity"
	adminactivation "inspection/services/inspection/internal/features/onboarding/admin_activation"
	onboardingcomplete "inspection/services/inspection/internal/features/onboarding/complete"
	onboardingcatalog "inspection/services/inspection/internal/features/onboarding/real_estate_catalog"
	onboardingsession "inspection/services/inspection/internal/features/onboarding/session"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"
)

func optionalStringValue(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func stringPointer(value string) *string { return &value }

func optionalOnboardingID(value *identity.ID) *string {
	if value == nil {
		return nil
	}
	formatted := value.String()
	return &formatted
}

func mapOnboardingRequest(value database.OnboardingRequest) *graphql1.OnboardingRequest {
	templateID := ""
	if value.TemplateID != nil {
		templateID = value.TemplateID.String()
	}
	return &graphql1.OnboardingRequest{
		ID: value.ID.String(), Status: value.Status, AssetID: optionalOnboardingID(value.AssetID),
		ParticipantID: optionalOnboardingID(value.ParticipantID), OriginVersionID: optionalOnboardingID(value.OriginVersionID), TemplateID: templateID,
	}
}

func mapOnboardingCompletion(value onboardingcomplete.Result) (*graphql1.OnboardingRequest, *graphql1.OnboardingStatus) {
	var request *graphql1.OnboardingRequest
	var requestID *string
	if value.Request.ID != (identity.ID{}) {
		request = mapOnboardingRequest(value.Request)
		requestID = stringPointer(value.Request.ID.String())
	}
	var inspectionID *string
	if value.InspectionID != (identity.ID{}) {
		inspectionID = stringPointer(value.InspectionID.String())
	}
	return request, &graphql1.OnboardingStatus{
		State: value.State, RequestID: requestID, InspectionID: inspectionID,
		NextAction: value.NextAction, OriginStatus: value.OriginStatus, DeliveryStatus: value.Delivery,
	}
}

func mapOnboardingSession(value onboardingsession.Session) *graphql1.OnboardingSession {
	definition, _ := onboardingcatalog.Resolve(onboardingcatalog.Segment)
	completedSteps := make(map[string]any, len(value.CompletedSteps))
	for step, payload := range value.CompletedSteps {
		completedSteps[step] = payload
	}
	var existingAgency *graphql1.OnboardingAgency
	if value.ExistingAgency != nil {
		existingAgency = &graphql1.OnboardingAgency{TenantID: value.ExistingAgency.TenantID.String(), BusinessUnitID: value.ExistingAgency.BusinessUnitID.String(), Name: value.ExistingAgency.Name, BusinessUnitCode: value.ExistingAgency.BusinessUnitCode, Status: value.ExistingAgency.Status}
	}
	return &graphql1.OnboardingSession{
		ID:             value.ID.String(),
		State:          value.State,
		CurrentStep:    value.CurrentStep,
		Version:        int(value.Version),
		ExpiresAt:      value.ExpiresAt.Format(time.RFC3339Nano),
		CompletedSteps: completedSteps,
		ExistingAgency: existingAgency,
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

func clearOnboardingCookie(ctx context.Context) {
	writer, ok := requestctx.ResponseWriter(ctx)
	if !ok {
		return
	}
	http.SetCookie(writer, &http.Cookie{Name: "inspection_onboarding", Value: "", Path: "/", HttpOnly: true, Secure: requestctx.SecureCookies(ctx), SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0).UTC()})
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
