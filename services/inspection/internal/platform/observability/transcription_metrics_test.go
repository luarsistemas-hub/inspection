package observability

import (
	"strings"
	"testing"
)

func TestTranscriptionSweepMetricsUseBoundedLabels(t *testing.T) {
	m := NewMetrics()
	m.TranscriptionSweep("cleanup", "deleted", 2)
	m.TranscriptionSweep("expiry", "expired", 1)
	m.TranscriptionSweep("tenant-a/note-1", "audio/key", 3)
	m.TranscriptionSweep("cleanup", "failed", 0)
	got := m.Prometheus()
	for _, want := range []string{
		`inspection_transcription_sweep_items_total{outcome="deleted",scan="cleanup"} 2`,
		`inspection_transcription_sweep_items_total{outcome="expired",scan="expiry"} 1`,
		`inspection_transcription_sweep_items_total{outcome="other",scan="other"} 3`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Prometheus output missing %q", want)
		}
	}
	if strings.Contains(got, "tenant-a") || strings.Contains(got, "audio/key") || strings.Contains(got, `outcome="failed"`) {
		t.Fatalf("unexpected transcription sweep label or zero sample: %s", got)
	}
}
