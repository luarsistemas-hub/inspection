package generate_snapshot

import (
	"reflect"
	"testing"

	report "inspection/services/inspection/internal/features/reports/core"
)

func TestVideoScopeStatements(t *testing.T) {
	kitchen := report.Requirement{Label: "kitchen — sink", AnalysisMode: "COMPARE_ORIGIN_CURRENT", AnalysisStatus: "COMPLETED"}
	bathroomGap := report.Requirement{Label: "bathroom — sink", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "INCONCLUSIVE"}
	pending := report.Requirement{Label: "Vídeo principal", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "PENDING"}
	observed := videoObservedScopePrefix + "kitchen — sink" + videoObservedScopeWithoutPropertyEnd
	cases := map[string]struct {
		reference    bool
		requirements []report.Requirement
		want         []string
	}{
		"comparative scope":                {true, []report.Requirement{kitchen, bathroomGap, pending}, []string{observed}},
		"all comparative items unresolved": {true, []report.Requirement{bathroomGap, {Label: "kitchen — sink", AnalysisMode: "COMPARE_ORIGIN_CURRENT", AnalysisStatus: "INCONCLUSIVE"}}, []string{VideoNoComparativeConclusion}},
		"comparative plan without matches": {true, []report.Requirement{bathroomGap}, []string{VideoNoComparativeConclusion}},
		"current-only plan":                {false, []report.Requirement{{Label: "kitchen — sink", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "COMPLETED"}, {Label: "kitchen — sink", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "COMPLETED"}}, []string{VideoNoReferenceStatement, observed}},
		"no requirements":                  {false, nil, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := VideoScopeStatements(tc.reference, tc.requirements); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("VideoScopeStatements() = %q, want %q", got, tc.want)
			}
		})
	}
}
