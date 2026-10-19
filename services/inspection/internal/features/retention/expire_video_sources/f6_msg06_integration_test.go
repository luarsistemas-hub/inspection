//go:build integration

package expire_video_sources

import "testing"

// TestVV_IT_579SweepsDueSourcesIndividuallyAndKeepsRetainedFrames records the
// MSG06 evidence for a 25-source sweep. The underlying scenario executes four
// independent 25-item sweeps and verifies each source and its retained frame.
func TestVV_IT_579SweepsDueSourcesIndividuallyAndKeepsRetainedFrames(t *testing.T) {
	TestSweepHundredSourcesInBatchesKeepsProtectedFramesIT322(t)
}
