package handle_classified

import (
	"encoding/json"
	"testing"

	capturecore "inspection/services/inspection/internal/features/capture/core"
)

func TestReportRequirementsSkipEnvironmentsRemovedDuringCapture(t *testing.T) {
	raw, err := json.Marshal([]capturecore.Requirement{
		{Key: "env:kept", Label: "Sala"},
		{Key: "env:new:removed", Label: "Quarto", AddedDuringCapture: true, Deleted: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	requirements, err := reportRequirementsFromCapture(raw)
	if err != nil || len(requirements) != 1 || requirements[0].Key != "env:kept" {
		t.Fatalf("report requirements = %+v, %v", requirements, err)
	}
}
