package identity

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestID(t *testing.T) {
	id := NewID()
	assert.False(t, IsEmpty(&id))
}

func TestNewDeterministicID(t *testing.T) {
	first := NewDeterministicID("oidc", "issuer\\x00subject")
	if first != NewDeterministicID("oidc", "issuer\\x00subject") || first == NewDeterministicID("oidc", "issuer\\x00other") {
		t.Fatal("deterministic identity contract violated")
	}
}
