package resolvers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	capturebootstrap "inspection/services/inspection/internal/features/capture/bootstrap"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	capturereferences "inspection/services/inspection/internal/features/capture/reference_items"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"
)

func TestCaptureReviewHandoffRequiresReviewerAndUsesDraftVersionHelper(t *testing.T) {
	inspectionID, versionID, reviewerID := identity.NewID(), identity.NewID(), identity.NewID()
	if got := captureReviewHandoff(database.Invitation{}, inspectionID, &versionID); got != nil {
		t.Fatalf("handoff without reviewer = %+v", got)
	}
	got := captureReviewHandoff(database.Invitation{ReviewerMembershipID: &reviewerID}, inspectionID, &versionID)
	if got == nil || got.InspectionID != inspectionID.String() || got.OriginVersionID == nil || *got.OriginVersionID != versionID.String() {
		t.Fatalf("handoff did not use draft origin version: %+v", got)
	}
	got = captureReviewHandoff(database.Invitation{ReviewerMembershipID: &reviewerID}, inspectionID, nil)
	if got == nil || got.OriginVersionID != nil {
		t.Fatalf("handoff without a ready draft should omit originVersionId: %+v", got)
	}
}

func TestCaptureBootstrapMapsEnvironmentStateAndMediaCountHelper(t *testing.T) {
	mediaID := identity.NewID()
	mediaIDs, _ := json.Marshal([]identity.ID{mediaID})
	view := capturecore.Bootstrap{
		Requirements: []capturecore.Requirement{{Key: "env:new:room", EnvironmentIdentityID: "environment-id", EnvironmentTypeCode: "BEDROOM", EnvironmentName: "Quarto 2", Expected: false, AddedDuringCapture: true}},
		Answers:      []database.RequirementAnswer{{RequirementKey: "env:new:room", MediaIDs: mediaIDs}},
	}
	got := mapCaptureEnvironments(view)
	if len(got) != 1 || got[0].RequirementKey != "env:new:room" || got[0].Expected || !got[0].AddedDuringCapture || got[0].MediaCount != 1 || got[0].Status != "CAPTURED" {
		t.Fatalf("environment projection = %+v", got)
	}
}

func TestDescriptiveProjectionPreservesResolvedCoverage(t *testing.T) {
	response := &graphql1.ExternalCapture{}
	view := capturecore.Bootstrap{Scope: json.RawMessage(`{"rooms":[{"key":"kitchen"}]}`), Coverage: capturecore.CoverageSummary{Expected: 2, FullyAssessed: 1, NotObserved: 1, Suggestions: []capturecore.RecaptureSuggestion{}, PendingRecords: []string{}}}
	if err := projectDescriptiveCapture(response, view); err != nil {
		t.Fatal(err)
	}
	if response.Coverage == nil || response.Coverage.Expected != 2 || response.Coverage.UnknownScope {
		t.Fatalf("descriptive projection dropped known coverage: %+v", response.Coverage)
	}
}

func TestExternalCaptureProjectsBootstrappedPinnedCoverage(t *testing.T) {
	tenantID, responsibilityID := identity.NewID(), identity.NewID()
	invitations, credentials := captureSessionFixture(t, tenantID, responsibilityID)
	if err := invitations.DB.Exec("ATTACH DATABASE ':memory:' AS capture").Error; err != nil {
		t.Fatal(err)
	}
	if err := invitations.DB.Exec("CREATE TABLE capture.capture_drafts (id text, tenant_id text, responsibility_id text, media_kind text)").Error; err != nil {
		t.Fatal(err)
	}
	if err := invitations.DB.Exec("INSERT INTO capture.capture_drafts (id,tenant_id,responsibility_id,media_kind) VALUES (?,?,?,?)", identity.NewID(), tenantID, responsibilityID, "PHOTO").Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	baselineIdentityID := identity.NewID().String()
	if err := bus.RegisterQuery(capturebootstrap.Query{}, func(context.Context, any) (any, error) {
		return capturecore.Bootstrap{Draft: database.CaptureDraft{ID: identity.NewID(), Kind: "INSPECTION", Status: "OPEN", MediaKind: "PHOTO", TemplateVersionID: identity.NewID(), ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`)}, SchemaVersion: 3, Scope: json.RawMessage(`{"rooms":[{"key":"kitchen"}]}`), Requirements: []capturecore.Requirement{{Key: "env:kitchen", EnvironmentIdentityID: baselineIdentityID, EnvironmentTypeCode: "KITCHEN", EnvironmentName: "Cozinha", Expected: true}}, Coverage: capturecore.CoverageSummary{Expected: 1, NotObserved: 1, UnknownScope: false, Suggestions: []capturecore.RecaptureSuggestion{}, PendingRecords: []string{}}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(capturereferences.Query{}, func(context.Context, any) (any, error) { return []capturereferences.Item{}, nil }); err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithExternalCredentials(context.Background(), credentials)
	resolver := &queryResolver{Resolver: &Resolver{DB: invitations.DB, Invitations: invitations, Bus: bus}}
	got, err := resolver.ExternalCapture(ctx)
	if err != nil || got.Coverage == nil || got.Coverage.Expected != 1 || got.Coverage.UnknownScope || len(got.Requirements) != 1 || got.Requirements[0].EnvironmentIdentityID == nil || *got.Requirements[0].EnvironmentIdentityID != baselineIdentityID {
		t.Fatalf("externalCapture lost pinned coverage: capture=%+v err=%v", got, err)
	}
}

func TestSaveCaptureMetadataDeviceTimestampUsesRFC3339Validation(t *testing.T) {
	valid := "2026-11-03T12:34:56-03:00"
	parsed, err := parseDeviceCaptureTime(&valid)
	if err != nil || parsed.Format(time.RFC3339) != valid {
		t.Fatalf("valid device timestamp=%v err=%v", parsed, err)
	}
	invalid := "2026-11-03 12:34:56"
	if _, err := parseDeviceCaptureTime(&invalid); !apperror.Is(err, apperror.InvalidInput) || !strings.Contains(err.Error(), "RFC 3339") {
		t.Fatalf("invalid device timestamp error=%v", err)
	}
}

func TestExternalCaptureConfirmationOnlyOmitsCapturePayloadWithoutReviewer(t *testing.T) {
	tenantID, responsibilityID := identity.NewID(), identity.NewID()
	invitations, credentials := captureSessionFixture(t, tenantID, responsibilityID)
	mediaID := identity.NewID()
	mediaIDs, _ := json.Marshal([]identity.ID{mediaID})
	bus := mediator.New()
	if err := bus.RegisterQuery(capturebootstrap.Query{}, func(context.Context, any) (any, error) {
		return capturecore.Bootstrap{
			Draft:         database.CaptureDraft{ID: identity.NewID(), Kind: "INSPECTION", Status: "SUBMITTED", MediaKind: "PHOTO", TemplateVersionID: identity.NewID(), ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`)},
			SchemaVersion: 2, ConfirmationOnly: true,
			Requirements: []capturecore.Requirement{{Key: "env:room", EnvironmentTypeCode: "BEDROOM", EnvironmentName: "Quarto 2", Expected: true, Required: true, MinimumMedia: 1, MaximumMedia: 20}},
			Answers:      []database.RequirementAnswer{{RequirementKey: "env:room", MediaIDs: mediaIDs}},
		}, nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithExternalCredentials(context.Background(), credentials)
	resolver := &queryResolver{Resolver: &Resolver{DB: invitations.DB, Bus: bus, Invitations: invitations}}
	got, err := resolver.ExternalCapture(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "CONFIRMED" || got.ReviewHandoff != nil || len(got.Environments) != 0 || len(got.Answers) != 0 || len(got.Scope) != 0 || len(got.ComponentAnswers) != 0 || len(got.HandoverRecords) != 0 || got.Coverage == nil || !got.Coverage.UnknownScope {
		t.Fatalf("confirmation-only external capture=%+v; want no handoff or capture payload", got)
	}
}

func TestIT032ExpiredCaptureLinkDoesNotExposeScope(t *testing.T) {
	tenantID, responsibilityID := identity.NewID(), identity.NewID()
	invitations, credentials := captureSessionFixture(t, tenantID, responsibilityID)
	expiredAt := time.Now().Add(-time.Minute)
	if err := invitations.DB.Model(&database.ExternalSession{}).Where("tenant_id = ? AND responsibility_id = ?", tenantID, responsibilityID).Update("expires_at", expiredAt).Error; err != nil {
		t.Fatal(err)
	}
	if err := invitations.DB.Model(&database.Invitation{}).Where("tenant_id = ? AND responsibility_id = ?", tenantID, responsibilityID).Update("expires_at", expiredAt).Error; err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithExternalCredentials(context.Background(), credentials)
	resolver := &queryResolver{Resolver: &Resolver{Invitations: invitations}}
	got, err := resolver.ExternalCapture(ctx)
	if err == nil || got != nil {
		t.Fatalf("expired capture link returned capture data=%+v err=%v; want authorization error and no scope", got, err)
	}
	mutation := &mutationResolver{Resolver: &Resolver{Invitations: invitations, Bus: mediator.New()}}
	answer, mutationErr := mutation.RecordComponentAnswer(ctx, graphql1.RecordComponentAnswerInput{RequirementKey: "env:kitchen", ComponentKey: "floor", State: "NOT_OBSERVED"})
	if mutationErr != nil || answer == nil || len(answer.UserErrors) == 0 || answer.Answer != nil {
		t.Fatalf("expired capture link mutation returned data=%+v err=%v; want user error and no answer", answer, mutationErr)
	}
}

func TestUT019CaptureRejectsSessionScopedToAnotherTenant(t *testing.T) {
	tenantID, responsibilityID := identity.NewID(), identity.NewID()
	invitations, credentials := captureSessionFixture(t, tenantID, responsibilityID)
	credentials.SessionToken, _ = security.NewScopedToken(identity.NewID())
	ctx := requestctx.WithExternalCredentials(context.Background(), credentials)
	resolver := &queryResolver{Resolver: &Resolver{Invitations: invitations}}
	got, err := resolver.ExternalCapture(ctx)
	if err == nil || got != nil {
		t.Fatalf("wrong-tenant capture session returned data=%+v err=%v", got, err)
	}
}

func TestB9PendingEnvironmentKeysAreExposedOnGraphQLUserError(t *testing.T) {
	err := capturecore.PendingRequirementError{Keys: []string{"env:kitchen"}, Err: apperror.New(apperror.InvalidState, "env:kitchen", "environment capture is incomplete")}
	got := userErrorFrom(err)
	if got.Code != string(apperror.InvalidState) || got.PendingRequirementKeys == nil || len(got.PendingRequirementKeys) != 1 || got.PendingRequirementKeys[0] != "env:kitchen" {
		t.Fatalf("GraphQL user error lost pending environment keys: %+v", got)
	}
}

func TestPlanInspectionRecurringReviewerMembershipReturnsInvalidInput(t *testing.T) {
	tenantID := identity.NewID()
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID})
	rrule := "FREQ=DAILY"
	_, err := (&mutationResolver{Resolver: &Resolver{}}).PlanInspection(ctx, graphql1.PlanInspectionInput{
		AssetID:              identity.NewID().String(),
		ParticipantID:        identity.NewID().String(),
		TemplateID:           identity.NewID().String(),
		Rrule:                &rrule,
		ReviewerMembershipID: ptrString(identity.NewID().String()),
	})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.InvalidInput || appErr.Field != "reviewerMembershipId" {
		t.Fatalf("recurring plan with reviewer = %v; want INVALID_INPUT reviewerMembershipId", err)
	}
}

func ptrString(value string) *string { return &value }
