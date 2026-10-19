package handle_classified

import "testing"

func TestSetupRequiresNotificationAndSnapshotAdapters(t *testing.T) {
	if handler, err := Setup(Dependencies{}); err == nil || handler != nil {
		t.Fatalf("Setup() returned handler=%t err=%v, want missing dependency error", handler != nil, err)
	}
}
