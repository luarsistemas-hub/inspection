package dispatch_outbox

import "testing"

func TestSetupRejectsMissingDependencies(t *testing.T) {
	if _, err := Setup(Dependencies{}); err == nil {
		t.Fatal("missing dependencies accepted")
	}
}
