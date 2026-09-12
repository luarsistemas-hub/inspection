package core

import "testing"

func TestUT046StateTransitionsAreMonotonic(t *testing.T) {
	for _, transition := range [][2]State{{StateDelivered, StateSent}, {StateFailed, StateProcessing}, {StateCanceled, StateQueued}} {
		if CanTransition(transition[0], transition[1]) {
			t.Fatalf("unexpected transition %s -> %s", transition[0], transition[1])
		}
	}
	if !CanTransition(StateProcessing, StateQueued) {
		t.Fatal("proven pre-send retry must return work to QUEUED")
	}
}
