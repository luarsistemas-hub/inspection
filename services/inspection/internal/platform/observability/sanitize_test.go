package observability

import (
	"bytes"
	"encoding/base64"
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizerContractsUT054UT055(t *testing.T) {
	got := Sanitize(map[string]any{"correlation_id": "c", "notification_id": "n", "code": "INTERNAL", "tenant_id": "tenant", "Authorization": "Bearer x", "otp": "123456", "provider_response": "secret", "image_bytes": "x", "email": "a@b", "recipient": "+5511", "capture_url": "https://x/capture/token", "ciphertext": "encrypted"})
	if got["correlation_id"] != "c" || got["code"] != "INTERNAL" || got["tenant_hash"] == nil {
		t.Fatalf("safe fields missing: %#v", got)
	}
	for _, key := range []string{"Authorization", "otp", "provider_response", "image_bytes", "email", "recipient", "capture_url", "ciphertext", "tenant_id"} {
		if _, ok := got[key]; ok {
			t.Fatalf("field leaked: %s", key)
		}
	}
}

func TestIT048StructuredLogRedactsSensitiveNotificationFields(t *testing.T) {
	var output bytes.Buffer
	if err := Log(&output, "notification_attempt", map[string]any{"correlation_id": "corr-1", "notification_id": "notification-1", "code": "provider_rejected", "recipient": "+5511999999999", "capture_token": "opaque", "rendered_body": "sensitive"}); err != nil {
		t.Fatal(err)
	}
	value := output.String()
	if !strings.Contains(value, "corr-1") || !strings.Contains(value, "notification-1") || !strings.Contains(value, "provider_rejected") {
		t.Fatalf("safe evidence missing: %s", value)
	}
	for _, secret := range []string{"+5511", "opaque", "sensitive"} {
		if strings.Contains(value, secret) {
			t.Fatalf("log leaked %q: %s", secret, value)
		}
	}
}

func TestSanitizeErrorRedactsSensitivePatternsAndPreservesDiagnostics(t *testing.T) {
	input := `password: x; url https://example.test/path?token=secret; token=abcdefghijklmnopqrstuvwxyz0123456789; code 654321; postgres://u:pw@localhost/db; invalid input value for enum status: "secret-value"; invalid input syntax for type text: "escaped \" secret"; constraint "inspection_tenant_id_fkey" relation "inspection" column "tenant_id" table "inspections" index "idx_inspections_tenant"; UUID 550e8400-e29b-41d4-a716-446655440000`
	got := SanitizeError(input)
	for _, secret := range []string{"password: x", "token=secret", "abcdefghijklmnopqrstuvwxyz0123456789", "654321", ":pw@localhost", `"secret-value"`, `escaped \\" secret`} {
		if strings.Contains(got, secret) {
			t.Errorf("sensitive value %q remained in %q", secret, got)
		}
	}
	for _, diagnostic := range []string{`constraint "inspection_tenant_id_fkey"`, `relation "inspection"`, `column "tenant_id"`, `table "inspections"`, `index "idx_inspections_tenant"`, "550e8400-e29b-41d4-a716-446655440000"} {
		if !strings.Contains(got, diagnostic) {
			t.Errorf("diagnostic %q was removed from %q", diagnostic, got)
		}
	}
}

func TestSanitizeErrorBoundsUTF8BeforeAndAfterRedaction(t *testing.T) {
	input := strings.Repeat("á", 3000) + " password=secret " + strings.Repeat("z", 3000)
	got := SanitizeError(input)
	if !utf8.ValidString(got) || len(got) > 4099 {
		t.Fatalf("sanitized error is invalid UTF-8 or unbounded: bytes=%d valid=%v", len(got), utf8.ValidString(got))
	}
	if strings.Contains(got, "secret") {
		t.Fatalf("password remained in bounded error: %q", got)
	}
}

func TestSanitizeErrorRedactsBearerAndSixDigitOTPWithoutLabel(t *testing.T) {
	got := SanitizeError("Bearer abcdefghijklmnopqrstuvwxyz0123456789 otp is 123456")
	if strings.Contains(got, "abcdefghijklmnopqrstuvwxyz0123456789") || strings.Contains(got, "123456") {
		t.Fatalf("credential or standalone OTP remained: %q", got)
	}
}

func TestSanitizeErrorRedactsHighEntropyHexBase64AndKnownPrefixes(t *testing.T) {
	for _, token := range []string{
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"QmFzZTY0VG9rZW5TdHJpbmdXVGhpZ2hFbnRyb3B5MTIzNDU2Nzg5",
		"sk_live_NOT_A_REAL_TOKEN",
	} {
		if got := SanitizeError("credential=" + token); strings.Contains(got, token) {
			t.Errorf("high-entropy or prefixed token remained: %q", got)
		}
	}
}

func TestSanitizeErrorRedactsOpaqueBase64URLTokensAndPreservesIdentifiers(t *testing.T) {
	token := "Xk3_9aQ2vB7nM4pL8sR1tY6uW0zC5eF2gH9jK3mN7pQ"
	pathToken := "r_8sXk3QaZ9vB7nM4pL8sR1tY6uW0zC5eF2gH9jK3mN"
	got := SanitizeError("tok " + token + " /capture/" + pathToken + " " + token + "," + pathToken)
	for _, secret := range []string{token, pathToken} {
		if strings.Contains(got, secret) {
			t.Errorf("opaque token remained: %q in %q", secret, got)
		}
	}
	for _, safe := range []string{"inspection_id", "uq_contacts_tenant_id", "inspection_source_version_tenant_idempotency_constraint_idx", "word ordinary text", "snake_case"} {
		if got := SanitizeError(safe); !strings.Contains(got, safe) {
			t.Errorf("ordinary identifier or text was redacted: input=%q output=%q", safe, got)
		}
	}
	for _, id := range []string{
		"550e8400-e29b-41d4-a716-446655440000",
		"inspection_id=550e8400-e29b-41d4-a716-446655440000",
		"/inspections/550e8400-e29b-41d4-a716-446655440000/media",
	} {
		if got := SanitizeError(id); !strings.Contains(got, "550e8400-e29b-41d4-a716-446655440000") {
			t.Errorf("UUID diagnostic was redacted: input=%q output=%q", id, got)
		}
	}
}

func TestSanitizeErrorRedactsCompositeSecretsAndPhoneNumbers(t *testing.T) {
	input := `access_token=accessSensitiveTokenVALUE_7281&ok=yes client_secret=clientSensitiveValue_7281 api_key=apiSensitiveValue_7281 {"password":"hunter2","mode":"safe"} password: minha senha longa; phone +5511999998888 to 14155552671 from: 442079460123 recipient=+14155552671 whatsapp 5511987654321`
	got := SanitizeError(input)
	for _, secret := range []string{"accessSensitiveTokenVALUE_7281", "clientSensitiveValue_7281", "apiSensitiveValue_7281", "hunter2", "minha senha longa", "+5511999998888", "14155552671", "442079460123", "+14155552671", "5511987654321"} {
		if strings.Contains(got, secret) {
			t.Errorf("secret or phone remained: %q in %q", secret, got)
		}
	}
	for _, safe := range []string{"ok=yes", `"mode":"safe"`} {
		if !strings.Contains(got, safe) {
			t.Errorf("unrelated value was removed: %q from %q", safe, got)
		}
	}
}

func TestSanitizeErrorRedactsDSNBasicAuthorizationAndKeepsUUID(t *testing.T) {
	input := "redis://:supersecret@dragonfly:6379/0 postgres://app:p@ss@db/x Authorization: Basic dXNlcjpwYXNzd29yZA== inspection_id=550e8400-e29b-41d4-a716-446655440000"
	got := SanitizeError(input)
	for _, secret := range []string{"supersecret", "p@ss", "dXNlcjpwYXNzd29yZA=="} {
		if strings.Contains(got, secret) {
			t.Errorf("credential remained: %q in %q", secret, got)
		}
	}
	if !strings.Contains(got, "inspection_id=550e8400-e29b-41d4-a716-446655440000") {
		t.Fatalf("UUID next to equals was not preserved: %q", got)
	}
}

func TestSanitizeErrorRedactsPhonesAndPreservesTimestamps(t *testing.T) {
	for _, input := range []string{
		`{"to":"5511999998888"}`,
		`{"recipient":"11999998888"}`,
		`telefone 11999998888`,
		`phone_number: 11999998888`,
		`celular (11) 99999-8888`,
	} {
		got := SanitizeError(input)
		for _, digit := range []string{"5511999998888", "11999998888", "(11) 99999-8888"} {
			if strings.Contains(got, digit) {
				t.Errorf("phone remained for %q: %q", input, got)
			}
		}
	}
	for _, timestamp := range []string{"2026-11-01 10:00:00", "created_at=2026-11-01T10:00:00Z", "retrying from 2026-11-01 10:00:00", "scheduled to 2026-11-01 10:00:00"} {
		if got := SanitizeError(timestamp); !strings.Contains(got, timestamp) {
			t.Errorf("timestamp was redacted: %q -> %q", timestamp, got)
		}
	}
}

func TestSanitizeErrorPreservesUsefulIdentifiersAndKnownTokenCauses(t *testing.T) {
	for _, identifier := range []string{
		"ResponsibleEmailCorrectionPayload",
		"UpdateOnboardingV2PropertyCheckpointPayload",
		"idx_inspections_tenant_id_created_at_v2",
		"000123_create_onboarding_requests_table",
		"real-estate-onboarding-checkpoint-conflict-error",
		"capture-link-delivery",
	} {
		if got := SanitizeError("type " + identifier + " failed"); !strings.Contains(got, identifier) {
			t.Errorf("identifier was redacted: %q -> %q", identifier, got)
		}
	}
	for _, token := range []string{
		"ZXlKaGJHY2lPaUpJVXpJMU5pSXNJblI1Y0NJNklrcFhWQ0o5",
		"AbCdEf0123456789abcdef0123456789",
		"xk3_9aq2vb7nm4pl8sr1ty6uw0zc5ef2gh9jk3mn7pq",
	} {
		if got := SanitizeError("capture link " + token + " expired"); strings.Contains(got, token) || !strings.Contains(got, "expired") {
			t.Errorf("opaque token was kept or diagnostic lost: %q -> %q", token, got)
		}
	}
	for _, cause := range []string{"invalid token: context deadline exceeded", "refresh token: expired at 2026-11-01"} {
		if got := SanitizeError(cause); got != cause {
			t.Errorf("token cause was lost: %q -> %q", cause, got)
		}
	}
	if got := SanitizeError("access_token=abc123XYZ rejected by provider"); strings.Contains(got, "abc123XYZ") || !strings.Contains(got, "rejected by provider") {
		t.Errorf("secret-looking token word not redacted or cause lost: %q", got)
	}
	if got := SanitizeError(`{"access_token":"abc def","mode":"safe"}`); strings.Contains(got, "abc def") || !strings.Contains(got, `"mode":"safe"`) {
		t.Errorf("quoted token value must always be redacted: %q", got)
	}
}

func TestSanitizeErrorRedactsAdditionalCredentialForms(t *testing.T) {
	for _, secret := range []string{
		`secret_key=abcXYZ0987654321secret`,
		`Cookie: session=abcXYZ0987654321secret`,
		`Cookie: sid=abcXYZ0987654321secret; session=abcXYZ0987654321secret`,
		`Basic dXNlcjpwYXNzd29yZDEyMzQ=`,
		`postgres://app:pass/with/slash@db.example.test/inspection`,
		`postgres://app:pass/with/slash@abcXYZ0987654321secret@db.example.test/inspection`,
	} {
		got := SanitizeError(secret)
		for _, marker := range []string{"abcXYZ0987654321secret", "dXNlcjpwYXNzd29yZDEyMzQ=", "pass/with/slash"} {
			if strings.Contains(got, marker) {
				t.Errorf("credential remained for %q: %q", secret, got)
			}
		}
	}
}

func TestSanitizeErrorDSNKeepsHostDiagnostics(t *testing.T) {
	got := SanitizeError("dial postgres://user:secret@db.example.test:5432/inspection failed")
	if strings.Contains(got, "secret") || !strings.Contains(got, "@db.example.test:5432/inspection failed") {
		t.Fatalf("DSN password not hidden or host lost: %q", got)
	}
}

func TestSanitizeErrorScansTheRestOfTokenDiagnosticPhrases(t *testing.T) {
	for input, want := range map[string]string{
		"invalid token: context deadline exceeded password=hunter2": "invalid token: context deadline exceeded password=[REDACTED]",
		"token: invalid client_secret=abc123":                       "token: invalid client_secret=[REDACTED]",
		"invalid token: Bearer opaque123":                           "invalid token: Bearer [REDACTED]",
	} {
		if got := SanitizeError(input); got != want {
			t.Errorf("SanitizeError(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSanitizeErrorKeepsCommonWordOnlyForColonTokenPhrases(t *testing.T) {
	for _, input := range []string{"access_token=abcdefghijklmnop", "access_token=expired", "refresh_token=invalid"} {
		if got := SanitizeError(input); !strings.HasSuffix(got, "=[REDACTED]") {
			t.Errorf("token assigned with '=' must always be redacted: %q -> %q", input, got)
		}
	}
	if got := SanitizeError("refresh token: expired at 2026-11-01"); got != "refresh token: expired at 2026-11-01" {
		t.Errorf("colon diagnostic phrase was changed: %q", got)
	}
}

func TestSanitizeErrorRedactsOnlyCredentialKeyNames(t *testing.T) {
	for _, secret := range []string{"api_key=k1", "x-api-key: k1", "apikey=k1", "apiKey: k1", "secret_key=k1", "privateKey: k1", "access-key=k1", "signing_key=k1", "encryption_key=k1", "AWS_SECRET_ACCESS_KEY=k1"} {
		if got := SanitizeError(secret); strings.Contains(got, "k1") {
			t.Errorf("credential key value remained: %q -> %q", secret, got)
		}
	}
	for _, safe := range []string{
		"idempotency_key: abc-123",
		"idempotencyKey=req-2026-11-01-0001",
		"cache key: inspection:tenant:550e8400-e29b-41d4-a716-446655440000",
		"storage key: tenants/550e8400-e29b-41d4-a716-446655440000/media/photo.jpg",
		"object_key=tenants/x/photo.jpg",
		"monkey: banana",
		"primary key violated",
	} {
		if got := SanitizeError(safe); got != safe {
			t.Errorf("non-credential key was redacted: %q -> %q", safe, got)
		}
	}
}

func TestSanitizeErrorRedactsJWTWithShortHeader(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	if got := SanitizeError("jwt " + jwt + " rejected"); got != "jwt [REDACTED] rejected" {
		t.Fatalf("JWT was not fully redacted: %q", got)
	}
}

// TestSanitizeErrorOpaqueTokenMonteCarlo mirrors security.NewOpaqueToken
// (base64url of 32 random bytes) with a fixed seed, so the run is reproducible.
func TestSanitizeErrorOpaqueTokenMonteCarlo(t *testing.T) {
	random := rand.New(rand.NewChaCha8([32]byte{'i', 'n', 's', 'p', 'e', 'c', 't', 'i', 'o', 'n'}))
	raw := make([]byte, 32)
	leaks := 0
	for range 5000 {
		for index := range raw {
			raw[index] = byte(random.Uint32())
		}
		token := base64.RawURLEncoding.EncodeToString(raw)
		if got := SanitizeError("capture link " + token + " expired"); strings.Contains(got, token) || !strings.Contains(got, "expired") {
			leaks++
			if leaks <= 3 {
				t.Errorf("opaque token leaked or diagnostic lost: %q", got)
			}
		}
	}
	if leaks != 0 {
		t.Fatalf("leaked %d of 5000 opaque tokens", leaks)
	}
	for _, identifier := range []string{
		"UpdateOnboardingPropertyCheckpointPayload",
		"OnboardingRequestCheckpointConflictError",
		"onboarding_requests_tenant_id_idempotency_key_key",
		"ResolverForOnboardingRequestCheckpointMutation",
		"MarshalOnboardingRequestCheckpointConflictErrorPayload2",
		"HTTPServerTimeoutExceededWhileAwaitingHeaders",
		"ParseGraphQLRequestBodyFromHTTPResponse",
		"aaaa_bbbb_cccc_dddd_eeee_ffff_gggg_hhhh_1",
		"INSPECTION_MIGRATION_DATABASE_URL",
		"INSPECTION_TEST_AUTH_ENABLED=true",
	} {
		if got := SanitizeError("missing " + identifier); got != "missing "+identifier {
			t.Errorf("identifier was redacted: %q -> %q", identifier, got)
		}
	}
}
