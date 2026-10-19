package main

import (
	"testing"
	"time"
)

func TestDueEvery(t *testing.T) {
	last := time.Date(2026, time.October, 17, 12, 0, 0, 0, time.UTC)
	interval := 5 * time.Minute
	tests := []struct {
		name string
		last time.Time
		now  time.Time
		want bool
	}{
		{name: "first run", now: last, want: true},
		{name: "before interval", last: last, now: last.Add(4*time.Minute + 59*time.Second), want: false},
		{name: "at interval", last: last, now: last.Add(interval), want: true},
		{name: "after delayed interval", last: last, now: last.Add(7 * time.Minute), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dueEvery(tt.last, tt.now, interval); got != tt.want {
				t.Fatalf("dueEvery(%v, %v, %v) = %t, want %t", tt.last, tt.now, interval, got, tt.want)
			}
		})
	}
}
