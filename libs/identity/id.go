package identity

import "github.com/google/uuid"

type ID = uuid.UUID

func NewID() ID {
	return ID(uuid.New())
}

// NewDeterministicID derives a stable UUID for an idempotent domain identity.
// Callers should use a namespaced value and must not treat this as a secret.
func NewDeterministicID(namespace string, value string) ID {
	return ID(uuid.NewSHA1(uuid.NameSpaceURL, []byte(namespace+"\x00"+value)))
}

func ParseID(s string) (ID, error) {
	id, err := uuid.Parse(s)
	return ID(id), err
}

func IsEmpty(id *ID) bool {
	return id == nil || id.String() == ""
}
