package notifications

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

// PayloadCipher encrypts execution-only notification material. Key values are
// base64-encoded AES-256 keys so configuration never needs a second format.
type PayloadCipher struct {
	active string
	keys   map[string][]byte
}

// NewPayloadCipher validates the versioned payload-key ring.
func NewPayloadCipher(active string, encoded map[string]string) (*PayloadCipher, error) {
	active = strings.TrimSpace(active)
	if active == "" {
		return nil, fmt.Errorf("notification payload: missing active key")
	}
	keys := make(map[string][]byte, len(encoded))
	for id, value := range encoded {
		key, err := base64.StdEncoding.DecodeString(value)
		if err != nil || len(key) != 32 || strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("notification payload: invalid key ring")
		}
		keys[id] = key
	}
	if _, ok := keys[active]; !ok {
		return nil, fmt.Errorf("notification payload: active key is unavailable")
	}
	return &PayloadCipher{active: active, keys: keys}, nil
}

// Encrypt returns the key version, nonce, and AES-GCM ciphertext. Callers own
// the associated-data composition and must bind tenant and notification IDs.
func (c *PayloadCipher) Encrypt(plain, additionalData []byte) (string, []byte, []byte, error) {
	if c == nil {
		return "", nil, nil, fmt.Errorf("notification payload: cipher is unavailable")
	}
	block, err := aes.NewCipher(c.keys[c.active])
	if err != nil {
		return "", nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", nil, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", nil, nil, err
	}
	return c.active, nonce, gcm.Seal(nil, nonce, plain, additionalData), nil
}

// Decrypt authenticates all persisted metadata before exposing plaintext.
func (c *PayloadCipher) Decrypt(keyID string, nonce, ciphertext, additionalData []byte) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("notification payload: cipher is unavailable")
	}
	key, ok := c.keys[keyID]
	if !ok {
		return nil, fmt.Errorf("notification payload: unknown key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("notification payload: authentication failed")
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, additionalData)
	if err != nil {
		return nil, fmt.Errorf("notification payload: authentication failed")
	}
	return plain, nil
}
