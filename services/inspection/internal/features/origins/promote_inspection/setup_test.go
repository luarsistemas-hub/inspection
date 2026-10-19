package promote_inspection

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
)

func TestUniqueIDsProducesAStableReferenceSelection(t *testing.T) {
	first, second := identity.NewID(), identity.NewID()
	got := uniqueIDs([]identity.ID{second, first, second, identity.ID{}})
	if len(got) != 2 || got[0].String() > got[1].String() {
		t.Fatalf("stable unique ids = %#v", got)
	}
}

func TestPhotoPromotionEligibilityAcceptsCompletedPhotoInspectionsRegardlessOfSource(t *testing.T) {
	for _, source := range []string{"MANUAL", "SCHEDULE", "ONBOARDING"} {
		inspection := database.Inspection{Status: "COMPLETED", MediaKind: "PHOTO", Source: source}
		if !isEligibleInspection(inspection) {
			t.Errorf("completed photo inspection from %s should be eligible", source)
		}
	}
}

func TestPhotoPromotionEligibilityRejectsUnfinishedAndVideoInspections(t *testing.T) {
	for _, inspection := range []database.Inspection{
		{Status: "IN_PROGRESS", MediaKind: "PHOTO"},
		{Status: "INVALIDATED", MediaKind: "PHOTO"},
		{Status: "COMPLETED", MediaKind: "VIDEO"},
	} {
		if isEligibleInspection(inspection) {
			t.Errorf("inspection should not be eligible: status=%s media=%s", inspection.Status, inspection.MediaKind)
		}
	}
}

func TestRequestRejectsAnEmptyReferenceSelection(t *testing.T) {
	_, err := request(context.Background(), Dependencies{}, Command{TenantID: identity.NewID(), InspectionID: identity.NewID(), ExpectedAssetVersion: 1, ClientMutationID: "retry"})
	if code, _, _ := apperror.Public(err); code != apperror.InvalidInput {
		t.Fatalf("expected invalid input, got %v", err)
	}
}
