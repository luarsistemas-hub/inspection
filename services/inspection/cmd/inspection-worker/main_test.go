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

func TestReportRequirementsFromCapturePreservesEffectiveKeys(t *testing.T) {
	requirements, err := reportRequirementsFromCapture([]byte(`[{"Key":"origin:media-1","Section":"Referência","Label":"Referência","Instructions":"Cozinha"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(requirements) != 1 || requirements[0].Key != "origin:media-1" || requirements[0].Label != "Referência" {
		t.Fatalf("unexpected report requirements: %+v", requirements)
	}
}
