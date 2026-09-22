package build_request

import (
	"testing"
)

func TestSetupRequiresObjectStore(t *testing.T) {
	if _, err := Setup(Dependencies{}); err == nil {
		t.Fatal("missing object store accepted")
	}
}
