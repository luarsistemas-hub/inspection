package core

import "testing"

func TestFanOutFiltersAndDeduplicates(t *testing.T) {
	recipients := FanOut("event", "published", "safe", "body", []Recipient{
		{MembershipID: "m1", Channel: "EMAIL", Verified: true, Selected: true, Active: true},
		{MembershipID: "m1", Channel: "EMAIL", Verified: true, Selected: true, Active: true},
		{MembershipID: "m2", Channel: "EMAIL", Verified: false, Selected: true, Active: true},
	})
	if len(recipients) != 1 || recipients[0].MembershipID != "m1" {
		t.Fatalf("fanout: %+v", recipients)
	}
}

func TestMarkReadIsIdempotentAndPrivate(t *testing.T) {
	at := int64(3)
	n, err := MarkRead(Notification{MembershipID: "m1", ReadAt: &at}, "m1", 9)
	if err != nil || n.ReadAt == nil || *n.ReadAt != 3 {
		t.Fatalf("read: %+v %v", n, err)
	}
	if _, err := MarkRead(Notification{MembershipID: "m1"}, "m2", 9); err == nil {
		t.Fatal("foreign notification accepted")
	}
}
