package core

import "testing"

func TestSummarizeDoesNotInventProviderUsage(t *testing.T) {
	usage, err := Summarize([]Record{{Provider: "gateway", Model: "vision", LatencyMS: 11}})
	if err != nil || usage.CostKnown || usage.Requests != 1 {
		t.Fatalf("usage=%#v err=%v", usage, err)
	}
}
