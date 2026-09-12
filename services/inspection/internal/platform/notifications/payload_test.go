package notifications

import (
	"encoding/base64"
	"testing"
)

func TestPayloadCipherAuthenticatesTenantAndNotification(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	cipher, err := NewPayloadCipher("key-2", map[string]string{"key-2": base64.StdEncoding.EncodeToString(key)})
	if err != nil {
		t.Fatal(err)
	}
	keyID, nonce, ciphertext, err := cipher.Encrypt([]byte("capture-token"), []byte("tenant-a:notification-a"))
	if err != nil {
		t.Fatal(err)
	}
	if keyID != "key-2" || len(nonce) == 0 || len(ciphertext) == 0 {
		t.Fatalf("encrypted payload metadata missing")
	}
	plain, err := cipher.Decrypt(keyID, nonce, ciphertext, []byte("tenant-a:notification-a"))
	if err != nil || string(plain) != "capture-token" {
		t.Fatalf("round trip: %q %v", plain, err)
	}
	if _, err := cipher.Decrypt(keyID, nonce, ciphertext, []byte("tenant-b:notification-a")); err == nil {
		t.Fatal("tenant mismatch authenticated")
	}
	ciphertext[0] ^= 1
	if _, err := cipher.Decrypt(keyID, nonce, ciphertext, []byte("tenant-a:notification-a")); err == nil {
		t.Fatal("modified ciphertext authenticated")
	}
}
