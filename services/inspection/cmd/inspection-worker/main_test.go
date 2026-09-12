package main

import "testing"

func TestRolloutV2ProducerCountReflectsConfiguration(t *testing.T) {
	if got := rolloutV2ProducerCount(false); got != 0 {
		t.Fatalf("disabled v2 producers reported as %d", got)
	}
	if got := rolloutV2ProducerCount(true); got != 1 {
		t.Fatalf("enabled v2 producers reported as %d", got)
	}
}
