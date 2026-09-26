package observability

import (
	"strings"
	"testing"
	"time"
)

func TestLLMMetricsExposeBoundedSignalsAndBuckets(t *testing.T) {
	m := NewMetrics()
	input := int64(10)
	cached := int64(4)
	m.LLMCallStarted("live", "inspection-vision")
	m.LLMCallFinished("live", "COMPARE_ORIGIN_CURRENT", "inspection-vision", "success", 2*time.Second, true, &input, nil, &cached, 2, 1500)
	m.LLMCallFinished("live", "CURRENT_ONLY", "inspection-vision", "success", time.Second, true, &input, nil, nil, 1, 900)
	m.AnalysisValidation("live", "COMPARE_ORIGIN_CURRENT", "rejected", "response_invalid")
	m.AnalysisStage("validation", "rejected", 2*time.Second)
	m.AnalysisProcessing("inconclusive")
	m.AnalysisDelivery("retry", "published")

	output := m.Prometheus()
	for _, expected := range []string{
		"# TYPE inspection_llm_calls_total counter",
		"inspection_llm_calls_total{comparison_mode=\"compare_origin_current\",mode=\"live\",model_alias=\"inspection-vision\",outcome=\"success\"} 1",
		"inspection_llm_call_duration_seconds_bucket{comparison_mode=\"compare_origin_current\",le=\"2\",mode=\"live\",model_alias=\"inspection-vision\",outcome=\"success\"} 1",
		"inspection_llm_call_duration_seconds_bucket{comparison_mode=\"compare_origin_current\",le=\"+Inf\",mode=\"live\",model_alias=\"inspection-vision\",outcome=\"success\"} 1",
		"inspection_llm_tokens_total{direction=\"input\",mode=\"live\",model_alias=\"inspection-vision\"} 20",
		"inspection_llm_cached_input_tokens_total{mode=\"live\",model_alias=\"inspection-vision\"} 4",
		"inspection_llm_cache_hit_calls_total{mode=\"live\",model_alias=\"inspection-vision\"} 1",
		"inspection_llm_cache_usage_missing_total{mode=\"live\",model_alias=\"inspection-vision\"} 1",
		"inspection_llm_images_total{mode=\"live\",model_alias=\"inspection-vision\"} 3",
		"inspection_llm_request_body_bytes_total{mode=\"live\",model_alias=\"inspection-vision\"} 2400",
		"inspection_llm_usage_missing_total{direction=\"output\",mode=\"live\",model_alias=\"inspection-vision\"} 2",
		"inspection_llm_inflight{comparison_mode=\"unknown\",mode=\"live\",model_alias=\"inspection-vision\"} 0",
		"inspection_analysis_validation_total{code=\"response_invalid\",comparison_mode=\"compare_origin_current\",mode=\"live\",outcome=\"rejected\"} 1",
		"inspection_analysis_processing_total{outcome=\"inconclusive\"} 1",
		"inspection_analysis_delivery_actions_total{action=\"retry\",result=\"published\"} 1",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("metric %q missing from:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "tenant=") || strings.Contains(output, "provider=") {
		t.Fatalf("unbounded labels leaked into LLM metrics:\n%s", output)
	}
}
