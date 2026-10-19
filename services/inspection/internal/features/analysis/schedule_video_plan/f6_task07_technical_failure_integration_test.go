//go:build integration

package schedule_video_plan

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/platform/llm"
)

// task07FailingGateway fails every request with a provider-level error.
type task07FailingGateway struct{ err error }

func (g task07FailingGateway) CompleteStructured(context.Context, llm.StructuredRequest) (llm.StructuredResult, error) {
	return llm.StructuredResult{}, g.err
}

func TestVV_IT_202GatewayTimeoutForV2IsTechnicalFailureNotDamageOrGap(t *testing.T) {
	fixture := newTask04PlanFixtureWithOriginStatus(t, false, true, "READY", true)
	plan := runTask04Plan(t, fixture)
	processTask07PlanJobs(t, fixture, plan, task07FailingGateway{err: llm.NewError(llm.CodeTimeout, 0, errors.New("gateway timeout"))})
	snapshot, html := task07WorkerReport(t, fixture, plan)
	kitchen := task04Requirement(t, snapshot, "kitchen — sink")
	if kitchen.AnalysisStatus != "FAILED" || kitchen.Coverage == generate_snapshot.VideoUnobservedCoverage || len(snapshot.Findings) != 0 {
		t.Fatalf("timed-out V2 kitchen=%+v findings=%d; want technical failure without damage or visual gap", kitchen, len(snapshot.Findings))
	}
	if !strings.Contains(html, "Falha técnica") || strings.Contains(html, "Não observado no vídeo atual") {
		t.Fatal("report does not present the timeout as a technical failure distinct from a visual gap")
	}
}

func TestVV_IT_211Gateway503AndUnfilmedSinkHaveSeparateStatuses(t *testing.T) {
	fixture := newTask04PlanFixture(t, true, true)
	plan := runTask04Plan(t, fixture)
	processTask07PlanJobs(t, fixture, plan, task07FailingGateway{err: llm.NewError(llm.CodeProviderHTTP, http.StatusServiceUnavailable, errors.New("service unavailable"))})
	snapshot, html := task07WorkerReport(t, fixture, plan)
	kitchen, bathroom := task04Requirement(t, snapshot, "kitchen — sink"), task04Requirement(t, snapshot, "bathroom — sink")
	if kitchen.AnalysisStatus != "FAILED" || kitchen.Coverage == generate_snapshot.VideoUnobservedCoverage {
		t.Fatalf("503 kitchen=%+v; want technical failure", kitchen)
	}
	if bathroom.AnalysisStatus != "INCONCLUSIVE" || bathroom.Coverage != generate_snapshot.VideoUnobservedCoverage {
		t.Fatalf("unfilmed bathroom sink=%+v; want visual-coverage gap", bathroom)
	}
	if len(snapshot.Findings) != 0 || !strings.Contains(html, "Falha técnica") || !strings.Contains(html, "Não observado no vídeo atual") {
		t.Fatal("report merges technical failure and visual coverage statuses")
	}
}
