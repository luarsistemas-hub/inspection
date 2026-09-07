package security

import (
	"testing"
	"time"

	"inspection/libs/identity"
)

func TestUT068HashesAndConstantTimeComparison(t *testing.T) {
	pepper := []byte("01234567890123456789012345678901")
	a, _ := HashOTP("123456", pepper)
	b, _ := HashOTP("123456", pepper)
	if !Equal(a, b) || HashToken("a") == HashToken("b") {
		t.Fatal("hash contract failed")
	}
}

func TestScopedTokenCarriesOnlyTenantLocator(t *testing.T) {
	tenantID := identity.NewID()
	token, err := NewScopedToken(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseScopedToken(token)
	if err != nil || parsed != tenantID {
		t.Fatalf("scope: %v %v", parsed, err)
	}
	if _, err := ParseScopedToken(tenantID.String() + ".short"); err == nil {
		t.Fatal("weak scoped token accepted")
	}
}
func TestUT069CSRFAndExpiry(t *testing.T) {
	pepper := []byte("01234567890123456789012345678901")
	session := HashToken("session")
	a, _ := CSRFProof(session, "nonce", pepper)
	b, _ := CSRFProof(session, "other", pepper)
	if Equal(a, b) {
		t.Fatal("csrf replay accepted")
	}
	now := time.Now()
	if Active(now, now, nil) || Active(now, now.Add(time.Second), &now) {
		t.Fatal("expiry or revocation boundary accepted")
	}
}
func TestUT019ExactSessionBoundary(t *testing.T) {
	created := time.Unix(100, 0)
	expires := created.Add(SessionTTL)
	if !Active(expires.Add(-time.Nanosecond), expires, nil) || Active(expires, expires, nil) {
		t.Fatal("session boundary incorrect")
	}
}
func TestUT021TerminalRevocation(t *testing.T) {
	now := time.Now()
	if Active(now, now.Add(time.Hour), &now) {
		t.Fatal("revoked session active")
	}
}
