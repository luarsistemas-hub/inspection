package review_sensitive_media

import "testing"

func TestSetupRejectsMissingDependencies(t *testing.T) {
	if err := Setup(Dependencies{}); err == nil {
		t.Fatal("setup accepted missing dependencies")
	}
}
