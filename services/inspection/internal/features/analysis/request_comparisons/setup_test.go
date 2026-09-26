package request_comparisons

import (
	"encoding/json"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
)

func TestComparisonStatusSkipsLLMForDeclaredImpossibility(t *testing.T) {
	for _, test := range []struct {
		name   string
		answer database.RequirementAnswer
		want   string
	}{
		{name: "impossibility", answer: database.RequirementAnswer{MediaIDs: json.RawMessage(`[]`), ImpossibilityReason: "Area inaccessible"}, want: "INCONCLUSIVE"},
		{name: "with evidence", answer: database.RequirementAnswer{MediaIDs: json.RawMessage(`["` + identity.NewID().String() + `"]`)}, want: "PENDING"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := comparisonStatus(test.answer)
			if err != nil || got != test.want {
				t.Fatalf("comparison status = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}
