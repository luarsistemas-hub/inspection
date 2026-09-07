package core

import "testing"

func TestFirstCriticalNeverReturnsExternalRecipients(t *testing.T) {
	recipients := FirstCritical("ATTENTION", "CRITICAL", []Recipient{{Internal: false, Verified: true, Selected: true, Destination: "external@example.test"}, {Internal: true, Verified: true, Selected: true, Channel: "EMAIL", Destination: "reviewer@example.test"}})
	if len(recipients) != 1 || recipients[0].Destination != "reviewer@example.test" {
		t.Fatalf("unsafe recipients: %#v", recipients)
	}
	if FirstCritical("CRITICAL", "CRITICAL", recipients) != nil {
		t.Fatal("duplicate critical alert")
	}
}
