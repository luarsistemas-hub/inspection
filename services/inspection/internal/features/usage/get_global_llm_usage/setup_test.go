package get_global_llm_usage

import (
	"testing"
	"time"
)

func TestNormalizeWindowDefaultsToThirtyDays(t *testing.T) {
	before := time.Now().UTC()
	from, to, err := normalizeWindow(time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if to.Before(before) || to.Sub(from) > defaultWindow+time.Second {
		t.Fatalf("unexpected default window: %s to %s", from, to)
	}
}

func TestNormalizeWindowRejectsInvalidAndLongRanges(t *testing.T) {
	now := time.Now().UTC()
	if _, _, err := normalizeWindow(now, now); err == nil {
		t.Fatal("equal window accepted")
	}
	if _, _, err := normalizeWindow(now.Add(-maxWindow-time.Second), now); err == nil {
		t.Fatal("long window accepted")
	}
}
