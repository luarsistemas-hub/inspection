package identity

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestID(t *testing.T) {
	id := NewID()
	assert.False(t, IsEmpty(&id))
}
