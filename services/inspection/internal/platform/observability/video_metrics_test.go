package observability

import (
	"strings"
	"testing"
	"time"
)

func TestVideoMetricsExposeOnlyBoundedLabels(t *testing.T) {
	m := NewMetrics()
	m.VideoPreparation("CUSTOMER SUPPLIED TENANT", "secret text")
	m.VideoGateway("matching", "success", int64ptr(4), int64ptr(2))
	m.VideoQueue("plan", "running", 3, 2*time.Minute)
	m.VideoLeaseLost("decode")
	m.VideoScope("components", "success", 3)
	m.VideoScope("untrusted", "untrusted", 9)
	m.VideoPlanSeal(map[string]int{"MATCHED": 2, "CURRENT_ONLY": 1, "INCONCLUSIVE": 3, "TECHNICAL_FAILURE": 1})
	m.VideoProviderConfigFailure("matching")
	m.VideoWorkHeartbeat(time.Unix(1234, 0))
	m.VideoExpirationLag(20)
	m.VideoExpirationLag(10)
	m.VideoPartialDeletion()
	got := m.Prometheus()
	for _, want := range []string{
		`inspection_video_preparations_total{stage="other",status="other"} 1`,
		`inspection_video_gateway_requests_total{purpose="matching",status="success"} 1`,
		`inspection_video_gateway_tokens_total{direction="input",purpose="matching"} 4`,
		`inspection_video_queue_depth{stage="plan",status="running"} 3`,
		`inspection_video_lease_losses_total{stage="decode"} 1`,
		`inspection_video_scope_discoveries_total{resource="components",result="success",stage="scope"} 3`,
		`inspection_video_plan_items_total{outcome="matched"} 2`,
		`inspection_video_plan_items_total{outcome="technical_failure"} 1`,
		`inspection_video_unprocessed_candidates_total 1`,
		`inspection_video_provider_config_failures_total{purpose="matching"} 1`,
		`inspection_video_work_heartbeat_timestamp_seconds 1234`,
		`inspection_video_expiration_lag_seconds 20`,
		`inspection_video_partial_deletions_total 1`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Prometheus output missing %q", want)
		}
	}
	m.ResetVideoExpirationLag()
	if !strings.Contains(m.Prometheus(), "inspection_video_expiration_lag_seconds 0") {
		t.Fatal("expiration lag did not reset between sweeps")
	}
	if strings.Contains(got, "CUSTOM") || strings.Contains(got, "secret text") {
		t.Fatalf("unbounded label value leaked: %s", got)
	}
}

func TestInventoryAnalysisMetricsUseTaskPurposes(t *testing.T) {
	m := NewMetrics()
	m.AnalysisGateway("inventory_catalog", "success", int64ptr(8), int64ptr(3))
	m.AnalysisGateway("untrusted-secret", "success", nil, nil)
	got := m.Prometheus()
	if !strings.Contains(got, `inspection_analysis_gateway_requests_total{purpose="inventory_catalog",status="success"} 1`) {
		t.Fatalf("inventory metric missing: %s", got)
	}
	if strings.Contains(got, "untrusted-secret") {
		t.Fatalf("unbounded purpose leaked: %s", got)
	}
}

func int64ptr(value int64) *int64 { return &value }
