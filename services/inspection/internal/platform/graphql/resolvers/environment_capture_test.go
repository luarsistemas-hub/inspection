package resolvers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"inspection/libs/identity"
	capturebootstrap "inspection/services/inspection/internal/features/capture/bootstrap"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
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
	if got.Status != "CONFIRMED" || got.ReviewHandoff != nil || len(got.Environments) != 0 || len(got.Answers) != 0 {
		t.Fatalf("confirmation-only external capture=%+v; want no handoff or capture payload", got)
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
