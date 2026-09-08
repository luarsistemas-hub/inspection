package core

import (
	"testing"
	"time"
)

func TestPolicyDefaultsAndVersionConflict(t *testing.T) {
	if got := EffectivePolicy(nil); got.Mode != Manual || got.Version != 0 {
		t.Fatalf("default policy: %+v", got)
	}
	if _, err := Configure(Automatic, 0, 1); err == nil {
		t.Fatal("stale policy accepted")
	}
	if got, err := Configure(Automatic, 0, 0); err != nil || got.Mode != Automatic || got.Version != 1 {
		t.Fatalf("configure: %+v %v", got, err)
	}
}

func TestPublicationTransitions(t *testing.T) {
	if _, _, err := Publish(false, "", time.Unix(0, 0)); err == nil {
		t.Fatal("non-final snapshot published")
	}
	if status, at, err := Publish(true, "", time.Unix(0, 0)); err != nil || status != Published || at == nil {
		t.Fatalf("publish: %s %v %v", status, at, err)
	}
	if status, _, err := Invalidate(Published, "safety review", 1, 1, time.Unix(0, 0)); err != nil || status != Invalidated {
		t.Fatalf("invalidate: %s %v", status, err)
	}
}
