package complete

import (
	"context"
	"testing"

	"inspection/services/inspection/internal/features/onboarding/coordinator"
	onboardingsession "inspection/services/inspection/internal/features/onboarding/session"
	"inspection/services/inspection/internal/platform/apperror"
)

func TestCompleteRejectsMissingDependencies(t *testing.T) {
	_, err := (Service{}).Complete(context.Background(), "locator", "csrf", "mutation")
	if err == nil || err.Error() != "onboarding complete: missing dependency" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeadlineAtRejectsMalformedDate(t *testing.T) {
	_, err := deadlineAt(map[string]any{"deadline": "tomorrow"})
	if err == nil {
		t.Fatal("malformed deadline accepted")
	}
	if code, field, _ := apperror.Public(err); code != apperror.InvalidInput || field != "deadline" {
		t.Fatalf("unexpected public error: code=%s field=%q", code, field)
	}
}

func TestPendingResultProjectsOriginStateWithoutCompletedResources(t *testing.T) {
	result := pendingResult(onboardingsession.Session{State: coordinator.StateParticipantSaved}, coordinator.SubmitResult{
		State:          coordinator.StateOriginPending,
		NextAction:     coordinator.NextActionWaitForOrigin,
		OriginStatus:   "PENDING",
		DeliveryStatus: deliveryPending,
	})

	if result.State != coordinator.StateOriginPending || result.NextAction != coordinator.NextActionWaitForOrigin {
		t.Fatalf("unexpected pending transition: %#v", result)
	}
	if result.OriginStatus != "PENDING" || result.Delivery != deliveryPending {
		t.Fatalf("unexpected pending statuses: %#v", result)
	}
	if result.Request.ID.String() != "00000000-0000-0000-0000-000000000000" || result.InspectionID.String() != "00000000-0000-0000-0000-000000000000" {
		t.Fatalf("pending result claimed completed resources: %#v", result)
	}
}
