package core

import (
	"testing"
	"time"
)

func TestOrderAndSummaryExcludeInvalidated(t *testing.T) {
	now := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	rows := []Projection{{InspectionID: "normal", Classification: "NORMAL", UpdatedAt: now}, {InspectionID: "critical", Classification: "CRITICAL", UpdatedAt: now}, {InspectionID: "old-critical", Classification: "CRITICAL", Invalidated: true, UpdatedAt: now.Add(time.Hour)}}
	Order(rows)
	if rows[0].InspectionID != "old-critical" || Summarize(rows).Critical != 1 || Summarize(rows).Normal != 1 {
		t.Fatalf("unexpected projection result: %#v %#v", rows, Summarize(rows))
	}
	if _, _, err := DecodeCursor(EncodeCursor(rows[0])); err != nil {
		t.Fatal(err)
	}
}

func TestReducerDefersGapAndIgnoresDuplicate(t *testing.T) {
	var reducer Reducer
	if applied, gap := reducer.Apply(2, Projection{InspectionID: "later"}); applied || !gap {
		t.Fatal("sequence gap was applied")
	}
	if applied, gap := reducer.Apply(1, Projection{InspectionID: "first"}); !applied || gap {
		t.Fatal("first event not applied")
	}
	if applied, gap := reducer.Apply(1, Projection{InspectionID: "first"}); applied || gap {
		t.Fatal("duplicate was applied")
	}
}
