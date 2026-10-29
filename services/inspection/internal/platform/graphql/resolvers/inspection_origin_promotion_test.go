package resolvers

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	promoteorigin "inspection/services/inspection/internal/features/origins/promote_inspection_to_origin"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
)

func promotionContext() context.Context {
	tenant := identity.NewID()
	meta := requestctx.Metadata{TenantID: tenant, CorrelationID: "promotion-test", Principal: requestctx.Principal{TenantID: tenant, IdentityID: identity.NewID(), MembershipID: identity.NewID(), Roles: []string{auth.TenantAdmin}}}
	return requestctx.WithMetadata(context.Background(), meta)
}

func TestInspectionOriginPromotionQueryMapsStatus(t *testing.T) {
	ctx := promotionContext()
	meta, _ := requestctx.FromContext(ctx)
	inspectionID := identity.NewID()
	bus := mediator.New()
	if err := bus.RegisterQuery(promoteorigin.Query{}, func(context.Context, any) (any, error) {
		return promoteorigin.StatusView{Eligible: false, Impediments: []promoteorigin.Impediment{{Code: "NO_CAPTURE", Message: "Envie a captura."}}, ActiveOrigin: &promoteorigin.ActiveOrigin{VersionID: identity.NewID(), Number: 4, MediaKind: "VIDEO", HasInventory: true}, ConfirmationDigest: "digest", GeneratedVersionActive: false}, nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := (&queryResolver{&Resolver{Bus: bus}}).InspectionOriginPromotion(ctx, inspectionID.String())
	if err != nil {
		t.Fatal(err)
	}
	if view.InspectionID != inspectionID.String() || view.Eligible || view.ConfirmationDigest != "digest" || len(view.Impediments) != 1 || view.Impediments[0].Message != "Envie a captura." || view.ActiveOrigin == nil || view.ActiveOrigin.VersionNumber != 4 {
		t.Fatalf("status view mapping = %+v", view)
	}
	if meta.TenantID == (identity.ID{}) {
		t.Fatal("test tenant id was not set")
	}
}

func TestPromoteInspectionToOriginMapsCommandAndIsIdempotent(t *testing.T) {
	ctx := promotionContext()
	meta, _ := requestctx.FromContext(ctx)
	inspectionID, replaceID := identity.NewID(), identity.NewID()
	var received promoteorigin.Command
	bus := mediator.New()
	if err := bus.RegisterCommand(promoteorigin.Command{}, func(_ context.Context, raw any) (any, error) {
		received = raw.(promoteorigin.Command)
		now := time.Now().UTC()
		return promoteorigin.Result{Promotion: database.InspectionOriginPromotion{ID: identity.NewID(), TenantID: meta.TenantID, InspectionID: inspectionID, ClientMutationID: received.ClientMutationID, Purpose: received.Purpose, Status: "PREPARING", CreatedAt: now}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	resolver := &mutationResolver{&Resolver{Bus: bus}}
	payload, err := resolver.PromoteInspectionToOrigin(ctx, graphql1.PromoteInspectionToOriginInput{InspectionID: inspectionID.String(), ClientMutationID: "click-1", Purpose: stringPointer("MOVE_IN"), ReplaceDraftID: stringPointer(replaceID.String()), ConfirmationDigest: "confirmed-digest"})
	if err != nil {
		t.Fatal(err)
	}
	if received.TenantID != meta.TenantID || received.InspectionID != inspectionID || received.ClientMutationID != "click-1" || received.Purpose != "MOVE_IN" || received.ReplaceDraftID == nil || *received.ReplaceDraftID != replaceID || received.ConfirmationDigest != "confirmed-digest" {
		t.Fatalf("promotion command = %+v", received)
	}
	if payload.Promotion == nil || payload.Promotion.Status != graphql1.InspectionOriginPromotionStatusPreparing || payload.ClientMutationID != "click-1" || len(payload.UserErrors) != 0 {
		t.Fatalf("promotion payload = %+v", payload)
	}
}

func TestPromoteInspectionToOriginReturnsExpectedUserErrors(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		code        apperror.Code
	}{
		{name: "digest changed", field: "confirmationDigest", code: apperror.Conflict},
		{name: "replacement missing", field: "replaceDraftId", code: apperror.Conflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := promotionContext()
			bus := mediator.New()
			if err := bus.RegisterCommand(promoteorigin.Command{}, func(context.Context, any) (any, error) {
				return nil, apperror.New(tc.code, tc.field, "Confirme novamente a promoção.")
			}); err != nil {
				t.Fatal(err)
			}
			payload, err := (&mutationResolver{&Resolver{Bus: bus}}).PromoteInspectionToOrigin(ctx, graphql1.PromoteInspectionToOriginInput{InspectionID: identity.NewID().String(), ClientMutationID: "retry", ConfirmationDigest: "digest"})
			if err != nil || len(payload.UserErrors) != 1 || payload.UserErrors[0].Code != string(tc.code) || payload.UserErrors[0].Field == nil || *payload.UserErrors[0].Field != tc.field {
				t.Fatalf("user error mapping = payload %+v, err %v", payload, err)
			}
		})
	}
}

func TestInspectionOriginPromotionSchemaKeepsLegacyMutationsDeprecated(t *testing.T) {
	schema, err := os.ReadFile("../../../../schema.graphqls")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"promoteInspectionPhotos", "promoteInspectionVideo"} {
		line := ""
		for _, candidate := range strings.Split(string(schema), "\n") {
			if strings.Contains(candidate, field+"(") {
				line = candidate
				break
			}
		}
		if !strings.Contains(line, `@deprecated(reason: "Use promoteInspectionToOrigin.")`) {
			t.Errorf("%s lacks unified-promotion deprecation: %q", field, line)
		}
	}
}
