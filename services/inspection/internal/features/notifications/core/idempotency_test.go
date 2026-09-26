package core

import (
	"strings"
	"testing"

	"inspection/libs/identity"
)

func TestRecipientIdempotencyKeyAcceptsEmailAndPhone(t *testing.T) {
	id := identity.NewID()
	email := RecipientIdempotencyKey(id, ChannelEmail, "person@example.test")
	if !idempotencyPattern.MatchString(email) || strings.Contains(email, "person") || email != RecipientIdempotencyKey(id, ChannelEmail, "person@example.test") {
		t.Fatalf("unstable or invalid email key: %q", email)
	}
	phone := RecipientIdempotencyKey(id, ChannelSMS, "+5511999999999")
	if !idempotencyPattern.MatchString(phone) || phone == email {
		t.Fatalf("invalid or colliding phone key: %q", phone)
	}
	if email == RecipientIdempotencyKey(id, ChannelEmail, "other@example.test") {
		t.Fatal("different recipients share an idempotency key")
	}
}
