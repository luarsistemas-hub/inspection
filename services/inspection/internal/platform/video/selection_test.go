package video

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestSelectFramesChoosesSharpestBaselinePerSecondUT019(t *testing.T) {
	input := []Candidate{{ID: "F1", PTSMicros: 100_000, Sharpness: 1}, {ID: "F2", PTSMicros: 300_000, Sharpness: 4}, {ID: "F3", PTSMicros: 600_000, Sharpness: 2}, {ID: "F4", PTSMicros: 900_000, Sharpness: 3}}
	selected := SelectFrames(input, 0)
	if len(selected) != 1 || selected[0].Candidate.ID != "F2" || selected[0].Role != "BASELINE" {
		t.Fatalf("got %+v", selected)
	}
}

func TestValidateScopeRejectsUnknownFrameUT020(t *testing.T) {
	err := ValidateScope([]string{"F1"}, []ScopeObservation{{FrameID: "F99", EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "front", Visibility: "VISIBLE"}}, nil)
	if !errors.Is(err, ErrModelOutputInvalid) {
		t.Fatalf("got %v", err)
	}
}

func TestDetailSelectionUsesStrictDHashThresholdUT021(t *testing.T) {
	base := Candidate{ID: "base", PTSMicros: 100_000, Sharpness: 5, DHash: 0}
	atEight := Candidate{ID: "eight", PTSMicros: 300_000, Sharpness: 4, DHash: 255}
	atNine := Candidate{ID: "nine", PTSMicros: 600_000, Sharpness: 3, DHash: 511}
	if got := SelectFrames([]Candidate{base, atEight}, 0); len(got) != 1 {
		t.Fatalf("distance 8 should be omitted: %+v", got)
	}
	if got := SelectFrames([]Candidate{base, atNine}, 0); len(got) != 2 || got[1].Candidate.ID != "nine" {
		t.Fatalf("distance 9 should be kept: %+v", got)
	}
}

func TestSelectFramesTieUsesEarlierPresentationTimeUT022(t *testing.T) {
	got := SelectFrames([]Candidate{{ID: "later", PTSMicros: 300_000, Sharpness: 3}, {ID: "earlier", PTSMicros: 100_000, Sharpness: 3}}, 0)
	if len(got) != 1 || got[0].Candidate.ID != "earlier" {
		t.Fatalf("got %+v", got)
	}
}

func TestSelectFramesRetainsBlurredBaselineWithQualityFlagUT023(t *testing.T) {
	got := SelectFrames([]Candidate{{ID: "blurred", PTSMicros: 100_000, Sharpness: 0.2}}, 1)
	if len(got) != 1 || got[0].Role != "BASELINE" || got[0].Quality != "LOW_QUALITY" {
		t.Fatalf("got %+v", got)
	}
}

func TestDiscoverScopeRetriesMalformedResponseOnceUT024(t *testing.T) {
	calls := 0
	got, err := DiscoverScope(context.Background(), []string{"F1"}, func(_ context.Context, batch []string) ([]ScopeObservation, []string, error) {
		calls++
		if calls == 1 {
			return []ScopeObservation{{FrameID: "F99"}}, nil, nil
		}
		return []ScopeObservation{{FrameID: batch[0], EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE"}}, nil, nil
	})
	if err != nil || calls != 2 || len(got) != 1 {
		t.Fatalf("calls=%d scope=%+v err=%v", calls, got, err)
	}
	calls = 0
	_, err = DiscoverScope(context.Background(), []string{"F1"}, func(context.Context, []string) ([]ScopeObservation, []string, error) {
		calls++
		return []ScopeObservation{{FrameID: "F99"}}, nil, nil
	})
	if !errors.Is(err, ErrModelOutputInvalid) || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	calls = 0
	got, err = DiscoverScope(context.Background(), []string{"F1"}, func(_ context.Context, batch []string) ([]ScopeObservation, []string, error) {
		calls++
		if calls == 1 {
			return nil, nil, ErrModelOutputInvalid
		}
		return []ScopeObservation{{FrameID: batch[0], EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE"}}, nil, nil
	})
	if err != nil || calls != 2 || len(got) != 1 {
		t.Fatalf("malformed JSON retry: calls=%d scope=%+v err=%v", calls, got, err)
	}
}

func TestScopeBatchesStayBoundedAndOverlapByTwo(t *testing.T) {
	frames := make([]string, 50)
	for i := range frames {
		frames[i] = string(rune('A' + i))
	}
	batches := ScopeBatches(frames)
	if len(batches) != 3 || len(batches[0]) != 24 || len(batches[1]) != 24 || len(batches[2]) != 6 {
		t.Fatalf("unexpected batch sizes: %v", []int{len(batches[0]), len(batches[1]), len(batches[2])})
	}
	if batches[0][22] != batches[1][0] || batches[0][23] != batches[1][1] || batches[1][22] != batches[2][0] || batches[1][23] != batches[2][1] {
		t.Fatalf("missing overlap: %+v", batches)
	}
}

func TestConsolidateScopeKeepsDistinctComponentsWithoutMultiplyingRepeatedViewsIT058(t *testing.T) {
	frames := make([]string, 900)
	observations := make([]ScopeObservation, 900)
	for i := range frames {
		frames[i] = fmt.Sprintf("F%03d", i)
		component := "wall"
		if i == 899 {
			component = "door"
		}
		observations[i] = ScopeObservation{FrameID: frames[i], EnvironmentCandidate: "kitchen", ComponentLabel: component, ViewLabel: "wide", Visibility: "VISIBLE"}
	}
	grouped, err := ConsolidateScope(frames, observations)
	if err != nil || len(grouped) != 2 || grouped[0].ComponentLabel != "wall" || grouped[1].ComponentLabel != "door" {
		t.Fatalf("repeated views were multiplied or distinct door lost: %+v, err=%v", grouped, err)
	}
}

func TestConsolidateScopeDoesNotMergeSeparateIdenticalBedrooms(t *testing.T) {
	frames := []string{"F1", "F2", "F3"}
	observations := []ScopeObservation{
		{FrameID: "F1", EnvironmentCandidate: "bedroom", ComponentLabel: "window", ViewLabel: "wide", Visibility: "VISIBLE"},
		{FrameID: "F2", EnvironmentCandidate: "hall", ComponentLabel: "door", ViewLabel: "wide", Visibility: "VISIBLE"},
		{FrameID: "F3", EnvironmentCandidate: "bedroom", ComponentLabel: "window", ViewLabel: "wide", Visibility: "VISIBLE"},
	}
	grouped, err := ConsolidateScope(frames, observations)
	if err != nil || len(grouped) != 3 || grouped[0].IdentityStatus != "AMBIGUOUS" || grouped[1].IdentityStatus != "RESOLVED" || grouped[2].IdentityStatus != "AMBIGUOUS" {
		t.Fatalf("separate room occurrences were merged: %+v, err=%v", grouped, err)
	}
	conflicting := append(observations, ScopeObservation{FrameID: "F1", EnvironmentCandidate: "roof", ComponentLabel: "window", ViewLabel: "wide", Visibility: "VISIBLE"})
	if _, err := ConsolidateScope(frames, conflicting); !errors.Is(err, ErrModelOutputInvalid) {
		t.Fatalf("conflicting overlap accepted: %v", err)
	}
}
