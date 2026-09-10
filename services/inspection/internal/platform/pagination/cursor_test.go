package pagination

import (
	"testing"
	"time"

	"inspection/libs/identity"
)

func TestCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 9, 9, 12, 0, 0, 123000000, time.FixedZone("BRT", -3*60*60))
	id := identity.NewID()

	decodedAt, decodedID, err := After(Encode(at, id))
	if err != nil {
		t.Fatal(err)
	}
	if !decodedAt.Equal(at.UTC()) || decodedID != id {
		t.Fatalf("cursor changed value: got %s/%s", decodedAt, decodedID)
	}
}

func TestAfterRejectsRawIDAndMalformedTimestamp(t *testing.T) {
	if _, _, err := After(identity.NewID().String()); err == nil {
		t.Fatal("expected raw id cursor to be rejected")
	}
	if _, _, err := After("eyJ0IjoiYmFkIiwiaWQiOiI="); err == nil {
		t.Fatal("expected malformed cursor to be rejected")
	}
}
