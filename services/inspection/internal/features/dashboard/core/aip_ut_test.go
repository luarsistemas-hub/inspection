package core

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func aipStream(now time.Time) []Projection {
	return []Projection{
		{InspectionID: "a", Classification: "NORMAL", Status: "COMPLETED", UpdatedAt: now},
		{InspectionID: "b", Classification: "CRITICAL", Status: "COMPLETED", UpdatedAt: now.Add(time.Minute)},
		{InspectionID: "c", Classification: "ATTENTION", Status: "ANALYZING", UpdatedAt: now.Add(2 * time.Minute)},
		{InspectionID: "d", Classification: "CRITICAL", Status: "COMPLETED", Invalidated: true, UpdatedAt: now.Add(3 * time.Minute)},
		{InspectionID: "a", Classification: "ATTENTION", Status: "COMPLETED", UpdatedAt: now.Add(4 * time.Minute)},
	}
}

func aipRows(r Reducer) []Projection {
	rows := make([]Projection, 0, len(r.Rows))
	for _, row := range r.Rows {
		rows = append(rows, row)
	}
	Order(rows)
	return rows
}

// TestAIP_UT_048ProjectionReducerAcceptsSequenceAndRebuildsDeterministically
// covers UT-048 of autonomous-inspection-platform: counts derive only from the
// accepted sequence, are ordered deterministically and can be rebuilt.
func TestAIP_UT_048ProjectionReducerAcceptsSequenceAndRebuildsDeterministically(t *testing.T) {
	now := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	stream := aipStream(now)
	var live Reducer
	for index, row := range stream {
		if applied, gap := live.Apply(int64(index+1), row); !applied || gap {
			t.Fatalf("sequence %d was not accepted: applied=%v gap=%v", index+1, applied, gap)
		}
	}
	// Latest event per inspection wins: "a" was reclassified to ATTENTION.
	want := Summary{Total: 4, Critical: 1, Attention: 2, Normal: 0, Pending: 1, Invalidated: 1}
	if got := Summarize(aipRows(live)); got != want {
		t.Fatalf("summary = %+v, want %+v", got, want)
	}
	// Rebuild from the full stream, in a fresh reducer, yields identical state.
	for rebuild := 0; rebuild < 3; rebuild++ {
		var rebuilt Reducer
		for index, row := range stream {
			rebuilt.Apply(int64(index+1), row)
		}
		if rebuilt.Sequence != live.Sequence || !reflect.DeepEqual(rebuilt.Rows, live.Rows) || Summarize(aipRows(rebuilt)) != want {
			t.Fatalf("rebuild %d diverged from the live projection", rebuild)
		}
	}
	// Ordering is risk first, then newest, then identifier; equal priority is stable.
	rows := aipRows(live)
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		order = append(order, row.InspectionID)
	}
	if !reflect.DeepEqual(order, []string{"d", "b", "a", "c"}) {
		t.Fatalf("unexpected deterministic order: %v", order)
	}
	// Cursor pagination round-trips each ordered row.
	for _, row := range rows {
		at, id, err := DecodeCursor(EncodeCursor(row))
		if err != nil || !at.Equal(row.UpdatedAt) || id != row.InspectionID {
			t.Fatalf("cursor round trip failed for %s: %v", row.InspectionID, err)
		}
		bucket, at, id, err := DecodeTriageCursor(EncodeTriageCursor(row))
		if err != nil || bucket != priority(row.Classification) || !at.Equal(row.UpdatedAt) || id != row.InspectionID {
			t.Fatalf("triage cursor round trip failed for %s: %v", row.InspectionID, err)
		}
	}
}

// TestAIP_UT_049ProjectionIgnoresDuplicatesAndDefersSequenceGapWithoutCorruption
// covers UT-049.
func TestAIP_UT_049ProjectionIgnoresDuplicatesAndDefersSequenceGapWithoutCorruption(t *testing.T) {
	var reducer Reducer
	first := Projection{InspectionID: "i-1", Classification: "NORMAL", Status: "COMPLETED"}
	if applied, gap := reducer.Apply(1, first); !applied || gap {
		t.Fatal("first event not applied")
	}
	snapshot := func() string { return fmt.Sprintf("%d/%v", reducer.Sequence, reducer.Rows) }
	before := snapshot()
	// A duplicate (same or older sequence) is harmless even with different content.
	for _, sequence := range []int64{1, 0, -4} {
		if applied, gap := reducer.Apply(sequence, Projection{InspectionID: "i-1", Classification: "CRITICAL"}); applied || gap {
			t.Fatalf("duplicate sequence %d applied=%v gap=%v", sequence, applied, gap)
		}
	}
	// A gap is deferred and neither advances the sequence nor adds a row.
	for _, sequence := range []int64{3, 4, 99} {
		if applied, gap := reducer.Apply(sequence, Projection{InspectionID: "i-2", Classification: "CRITICAL"}); applied || !gap {
			t.Fatalf("gap sequence %d applied=%v gap=%v", sequence, applied, gap)
		}
	}
	if snapshot() != before || Summarize(aipRows(reducer)) != (Summary{Total: 1, Normal: 1}) {
		t.Fatalf("duplicates or gaps corrupted the projection: before=%s after=%s", before, snapshot())
	}
	// The deferred event is accepted once the missing sequence arrives.
	if applied, gap := reducer.Apply(2, Projection{InspectionID: "i-2", Classification: "CRITICAL", Status: "COMPLETED"}); !applied || gap {
		t.Fatal("next sequence not applied")
	}
	if got := Summarize(aipRows(reducer)); got != (Summary{Total: 2, Critical: 1, Normal: 1}) {
		t.Fatalf("summary after recovery = %+v", got)
	}
}
