package video

import (
	"errors"
	"fmt"
	"math/bits"
	"testing"
)

func TestSelectFramesV2KeepsSharpestAcrossSimilarTimeRunUT140(t *testing.T) {
	candidates := make([]Candidate, 20)
	for i := range candidates {
		candidates[i] = Candidate{ID: fmt.Sprintf("F%02d", i), PTSMicros: int64(i) * 500_000, Sharpness: float64(i + 1), DHash: uint64(i % 2)}
	}
	got, err := SelectFramesV2(candidates, 0)
	if err != nil || len(got) != 1 || got[0].Candidate.ID != "F19" {
		t.Fatalf("selected=%+v err=%v; want sharpest single frame", got, err)
	}
}

func TestSelectFramesV2KeepsAlternatingScenesUT141(t *testing.T) {
	highDistance := uint64((1 << 20) - 1)
	candidates := make([]Candidate, 8)
	for i := range candidates {
		hash := uint64(0)
		if i%2 == 1 {
			hash = highDistance
		}
		candidates[i] = Candidate{ID: fmt.Sprintf("F%d", i), PTSMicros: int64(i) * 1_000_000, Sharpness: 1, DHash: hash}
	}
	got, err := SelectFramesV2(candidates, 0)
	if err != nil || len(got) != len(candidates) {
		t.Fatalf("selected=%+v err=%v; want every scene transition", got, err)
	}
}

func TestSelectFramesV2ProtectsCloseViewUT142(t *testing.T) {
	got, err := SelectFramesV2([]Candidate{
		{ID: "wide", PTSMicros: 0, Sharpness: 2, DHash: 0},
		{ID: "close", PTSMicros: 1_000_000, Sharpness: 1, DHash: 1, CloseView: true},
	}, 0)
	if err != nil || len(got) != 2 || got[1].Candidate.ID != "close" {
		t.Fatalf("selected=%+v err=%v", got, err)
	}
}

func TestSelectFramesV2ProtectsDistinctSceneUT143(t *testing.T) {
	got, err := SelectFramesV2([]Candidate{
		{ID: "first", PTSMicros: 0, Sharpness: 2, DHash: 0},
		{ID: "new-scene", PTSMicros: 1_000_000, Sharpness: 1, DHash: 1, DistinctScene: true},
	}, 0)
	if err != nil || len(got) != 2 || got[1].Candidate.ID != "new-scene" {
		t.Fatalf("selected=%+v err=%v", got, err)
	}
}

func TestSelectFramesV2KeepsSharpestInSimilarRunUT144(t *testing.T) {
	got, err := SelectFramesV2([]Candidate{
		{ID: "first", PTSMicros: 0, Sharpness: 5, DHash: 0},
		{ID: "sharper", PTSMicros: 1_000_000, Sharpness: 7, DHash: 1},
	}, 0)
	if err != nil || len(got) != 1 || got[0].Candidate.ID != "sharper" {
		t.Fatalf("selected=%+v err=%v", got, err)
	}
}

func TestSelectFramesV2KeepsOneFrameFromShortClipUT145(t *testing.T) {
	got, err := SelectFramesV2([]Candidate{{ID: "only", PTSMicros: 250_000, Sharpness: 1}}, 0)
	if err != nil || len(got) != 1 || got[0].Candidate.ID != "only" {
		t.Fatalf("selected=%+v err=%v", got, err)
	}
}

func TestSelectFramesV2EmptyCandidatesUT146(t *testing.T) {
	got, err := SelectFramesV2(nil, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("selected=%+v err=%v", got, err)
	}
}

func TestSelectFramesV2UsesFirstFrameAsRunAnchorUT140(t *testing.T) {
	candidates := []Candidate{
		{ID: "anchor", PTSMicros: 0, Sharpness: 1, DHash: 0x00ff},
		{ID: "drift-1", PTSMicros: 1_000_000, Sharpness: 2, DHash: 0x01fe},
		{ID: "drift-2", PTSMicros: 2_000_000, Sharpness: 3, DHash: 0x03fc},
		{ID: "drift-3", PTSMicros: 3_000_000, Sharpness: 4, DHash: 0x07f8},
		{ID: "drift-4", PTSMicros: 4_000_000, Sharpness: 5, DHash: 0x0ff0},
		{ID: "drift-5", PTSMicros: 5_000_000, Sharpness: 6, DHash: 0x1fe0},
		{ID: "drift-6", PTSMicros: 6_000_000, Sharpness: 7, DHash: 0x3fc0},
	}
	got, err := SelectFramesV2(candidates, 0)
	if err != nil || len(got) != 2 || got[0].Candidate.ID != "drift-5" || got[1].Candidate.ID != "drift-6" {
		t.Fatalf("similarity run drifted from its first frame: selected=%+v err=%v", got, err)
	}
}

func TestSelectFramesV2FlagsLowQualityUT147(t *testing.T) {
	got, err := SelectFramesV2([]Candidate{
		{ID: "blurred", PTSMicros: 0, Sharpness: 0.1, DHash: 0},
		{ID: "still-blurred", PTSMicros: 1_000_000, Sharpness: 0.5, DHash: 1},
	}, 1)
	if err != nil || len(got) != 1 || got[0].Candidate.ID != "still-blurred" || got[0].Quality != "LOW_QUALITY" {
		t.Fatalf("selected=%+v err=%v", got, err)
	}
}

func TestSelectRepresentativesUsesDiversityAndCapsAtTwelveUT148(t *testing.T) {
	frames := make([]SelectedFrame, 40)
	for i := range frames {
		frames[i] = SelectedFrame{Candidate: Candidate{ID: fmt.Sprintf("F%02d", i), PTSMicros: int64(i), Sharpness: float64(i + 1), DHash: uint64(i)}}
	}
	got := SelectRepresentatives(frames, 40)
	selected := representativeFrames(got)
	if selected != 12 {
		t.Fatalf("selected representatives=%d; want cap 12", selected)
	}
	for i := 0; i < len(got); i++ {
		if !got[i].Representative {
			continue
		}
		for j := i + 1; j < len(got); j++ {
			if got[j].Representative && bits.OnesCount64(got[i].Candidate.DHash^got[j].Candidate.DHash) == 0 {
				t.Fatalf("selected duplicate hashes at %d and %d", i, j)
			}
		}
	}
	five := SelectRepresentatives(frames[:5], 12)
	if representativeFrames(five) != 5 {
		t.Fatalf("selected=%d; want all five available frames", representativeFrames(five))
	}
}

func TestSelectRepresentativesPrioritizesDiversityOverSharpnessUT148(t *testing.T) {
	frames := []SelectedFrame{
		{Candidate: Candidate{ID: "seed", Sharpness: 2000, DHash: 0}},
		{Candidate: Candidate{ID: "sharp-near-duplicate", Sharpness: 1500, DHash: 1}},
		{Candidate: Candidate{ID: "distinct", Sharpness: 50, DHash: (1 << 40) - 1}},
	}
	got := SelectRepresentatives(frames, 2)
	if !got[0].Representative || !got[2].Representative || got[1].Representative {
		t.Fatalf("representatives=%+v; want seed and the less-sharp distinct frame", got)
	}
}

func TestSelectFramesV2RejectsRetainedFrameOverflowUT149(t *testing.T) {
	candidates := make([]Candidate, maxRetainedFrames+1)
	highDistance := uint64((1 << 20) - 1)
	for i := range candidates {
		hash := uint64(0)
		if i%2 == 1 {
			hash = highDistance
		}
		candidates[i] = Candidate{ID: fmt.Sprintf("F%04d", i), PTSMicros: int64(i) * 1_000_000, Sharpness: 1, DHash: hash}
	}
	if _, err := SelectFramesV2(candidates, 0); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("overflow error=%v; want ErrResourceLimit", err)
	}
}

func representativeFrames(frames []SelectedFrame) int {
	count := 0
	for _, frame := range frames {
		if frame.Representative {
			count++
		}
	}
	return count
}
