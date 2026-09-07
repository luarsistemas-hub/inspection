package process_comparison

import (
	"context"
	"testing"
	"time"

	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"

	"gorm.io/gorm"
)

type gateway struct{ response llm.StructuredResult }

func (g gateway) CompleteStructured(context.Context, llm.StructuredRequest) (llm.StructuredResult, error) {
	return g.response, nil
}

func TestSetupRequiresAuthorizedRequestSeam(t *testing.T) {
	if _, err := Setup(Dependencies{Gateway: gateway{}}); err == nil {
		t.Fatal("missing authorized request builder accepted")
	}
	if _, err := Setup(Dependencies{Gateway: gateway{}, BuildRequest: func(context.Context, *gorm.DB, database.ComparisonJob, eventsPayload) (llm.StructuredRequest, error) {
		return llm.StructuredRequest{}, nil
	}, Now: time.Now}); err != nil {
		t.Fatalf("valid dependencies rejected: %v", err)
	}
}
