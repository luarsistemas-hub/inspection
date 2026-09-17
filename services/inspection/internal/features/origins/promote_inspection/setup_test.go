package promote_inspection

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
)

func TestUniqueIDsProducesAStableReferenceSelection(t *testing.T) {
	first, second := identity.NewID(), identity.NewID()
	got := uniqueIDs([]identity.ID{second, first, second, identity.ID{}})
	if len(got) != 2 || got[0].String() > got[1].String() {
		t.Fatalf("stable unique ids = %#v", got)
	}
}

func TestRequestRejectsAnEmptyReferenceSelection(t *testing.T) {
	_, err := request(context.Background(), Dependencies{}, Command{TenantID: identity.NewID(), InspectionID: identity.NewID(), ExpectedAssetVersion: 1, ClientMutationID: "retry"})
	if code, _, _ := apperror.Public(err); code != apperror.InvalidInput {
		t.Fatalf("expected invalid input, got %v", err)
	}
}
