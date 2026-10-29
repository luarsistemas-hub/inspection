package promote_inspection_to_origin

import "testing"

func TestPromotionTerminalStatesAreNoOps(t *testing.T) {
	for _, status := range []string{"COMPLETED", "FAILED", "CANCELED", "NEEDS_CONFIRMATION"} {
		if !terminal(status) {
			t.Errorf("%s should be terminal", status)
		}
	}
	for _, status := range []string{"PREPARING", "ACTIVATING"} {
		if terminal(status) {
			t.Errorf("%s should remain resumable", status)
		}
	}
}
