package core

import (
	"testing"

	"inspection/libs/identity"
)

func TestParticipantContactContractsIT021IT023IT027(t *testing.T) {
	for _, tc := range []struct{ channel, value string }{{"EMAIL", "bad"}, {"SMS", "123"}, {"WHATSAPP", "+0123456789"}, {"FAX", "+5511999999999"}} {
		if _, err := Normalize(tc.channel, tc.value); err == nil {
			t.Fatalf("invalid contact accepted: %#v", tc)
		}
	}
	email, err := Normalize("EMAIL", "Person@Example.COM")
	if err != nil {
		t.Fatal(err)
	}
	again, err := Normalize("email", "Person@Example.COM")
	if err != nil {
		t.Fatal(err)
	}
	if email != again || email != "person@example.com" {
		t.Fatal("equivalent contact is not reusable")
	}
	phone, err := Normalize("SMS", "+55 (11) 99999-9999")
	if err != nil {
		t.Fatal(err)
	}
	if phone != "+5511999999999" {
		t.Fatalf("phone not normalized: %s", phone)
	}
	if MaxContacts != 10 {
		t.Fatalf("unexpected participant contact limit: %d", MaxContacts)
	}
}

func TestParticipantUpsertMarksOnlyCreatesAndMovesAsNewWork(t *testing.T) {
	participantID := identity.NewID()
	currentUnit, otherUnit := identity.NewID(), identity.NewID()
	for _, tc := range []struct {
		name, want string
		id         *identity.ID
		current    identity.ID
	}{
		{name: "create", want: "new", current: currentUnit},
		{name: "same unit update", want: "existing", id: &participantID, current: currentUnit},
		{name: "move to another unit", want: "new", id: &participantID, current: currentUnit},
	} {
		target := currentUnit
		if tc.name == "move to another unit" {
			target = otherUnit
		}
		got := participantUpsertNewWork(tc.id, tc.current, target)
		if (got && tc.want != "new") || (!got && tc.want != "existing") {
			t.Errorf("%s newWork=%t, want %s", tc.name, got, tc.want)
		}
	}
}
