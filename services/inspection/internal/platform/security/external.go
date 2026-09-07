package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"time"

	"inspection/libs/identity"
)

const SessionTTL = 2 * time.Hour
const OTPTTL = 10 * time.Minute

var sixDigits = regexp.MustCompile(`^[0-9]{6}$`)

func NewOpaqueToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

// NewScopedToken adds a non-secret tenant locator to an opaque 256-bit token.
// The complete value is still hashed before persistence.
func NewScopedToken(tenantID identity.ID) (string, error) {
	if tenantID == (identity.ID{}) {
		return "", errors.New("invalid token scope")
	}
	secret, err := NewOpaqueToken()
	if err != nil {
		return "", err
	}
	return tenantID.String() + "." + secret, nil
}

func ParseScopedToken(value string) (identity.ID, error) {
	tenant, secret, ok := strings.Cut(value, ".")
	if !ok || len(secret) != 43 {
		return identity.ID{}, errors.New("invalid scoped token")
	}
	tenantID, err := identity.ParseID(tenant)
	if err != nil {
		return identity.ID{}, errors.New("invalid scoped token")
	}
	if raw, err := base64.RawURLEncoding.DecodeString(secret); err != nil || len(raw) != 32 {
		return identity.ID{}, errors.New("invalid scoped token")
	}
	return tenantID, nil
}

func HashToken(value string) [32]byte { return sha256.Sum256([]byte(value)) }
func HashOTP(code string, pepper []byte) ([32]byte, error) {
	if !sixDigits.MatchString(code) || len(pepper) < 32 {
		return [32]byte{}, errors.New("invalid otp material")
	}
	h := hmac.New(sha256.New, pepper)
	_, _ = h.Write([]byte(code))
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result, nil
}
func Equal(actual, expected [32]byte) bool { return hmac.Equal(actual[:], expected[:]) }

func CSRFProof(sessionDigest [32]byte, nonce string, pepper []byte) ([32]byte, error) {
	if nonce == "" || len(pepper) < 32 {
		return [32]byte{}, errors.New("invalid csrf material")
	}
	h := hmac.New(sha256.New, pepper)
	_, _ = h.Write(sessionDigest[:])
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(nonce))
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result, nil
}

func Active(now, expiresAt time.Time, revokedAt *time.Time) bool {
	return revokedAt == nil && now.Before(expiresAt)
}
