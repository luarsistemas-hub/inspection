package retry_draft

import (
	"testing"

	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/mediator"
)

func TestSetupValidatesDependencies(t *testing.T) {
	if err := Setup(nil, core.Service{}); err == nil {
		t.Fatal("nil bus accepted")
	}
	if err := Setup(mediator.New(), core.Service{}); err == nil {
		t.Fatal("service without database accepted")
	}
}
