package record_event

import "testing"

func TestAuditReasonContractsUT070UT071(t *testing.T) {
	for _, action := range []string{"recapture.requested", "sensitive.false_positive", "inspection.invalidated", "project.reopened", "project.stage_inserted", "retention.deletion_requested"} {
		if !requiresReason(action) {
			t.Fatalf("reason not required: %s", action)
		}
	}
	if requiresReason("tenant.created") {
		t.Fatal("ordinary event requires reason")
	}
}
