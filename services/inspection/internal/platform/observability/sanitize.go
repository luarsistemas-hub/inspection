package observability

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	// Only credential-like key names are matched: "idempotency_key", "cache key"
	// and "object_key" are diagnostics, not secrets.
	errorFieldPattern    = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])("?)([A-Za-z0-9_-]*(?:password|passwd|token|secret|otp|(?:api|secret|private|access|signing|encryption)[_-]?key|apikey))("?)\s*([:=])\s*("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\n&;,]+)`)
	errorEmailPattern    = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	errorURLQueryPattern = regexp.MustCompile(`(?i)(https?://[^\s?#"'<>]+)\?[^\s"'<>]+`)
	// The password segment is greedy up to the last "@" so passwords containing "/" or "@" are fully hidden.
	errorDSNPasswordPattern   = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://[^:/@\s]*:)\S*(@[^@/\s?#]+)`)
	errorBearerPattern        = regexp.MustCompile(`(?i)\bBearer\s+[^\s,;]+`)
	errorBasicAuthPattern     = regexp.MustCompile(`(?i)(Authorization\s*:\s*Basic\s+)[A-Za-z0-9+/=_-]+`)
	errorBasicPattern         = regexp.MustCompile(`(?i)\bBasic\s+[A-Za-z0-9+/=_-]{8,}`)
	errorCookieSessionPattern = regexp.MustCompile(`(?i)(\bCookie\s*:\s*)[^\s;,=]+=[^\s;,]*(?:\s*;\s*[^\s;,=]+=[^\s;,]*)*`)
	errorPostgresValuePattern = regexp.MustCompile(`(?i)((?:invalid input (?:syntax|value) for [^:]+):\s*)"(?:\\.|[^"\\])*"`)
	// Six-digit values are redacted globally because an unlabeled OTP is indistinguishable from an ordinary number.
	errorOTPPattern          = regexp.MustCompile(`\b[0-9]{6}\b`)
	errorPhoneContextPattern = regexp.MustCompile(`(?i)((?:["']?\b(?:phone(?:_number)?|to|from|recipient|whatsapp|telefone|celular)["']?\s*[:=]\s*["']?|\b(?:phone(?:_number)?|to|from|recipient|whatsapp|telefone|celular)\s+))\+?(?:[0-9]{1,3}[ .-]?)?(?:\(?[0-9]{2,3}\)?[ .-]?)?[0-9]{4,5}[ .-]?[0-9]{4}(["']?)`)
	errorE164Pattern         = regexp.MustCompile(`\+[1-9][0-9]{9,14}\b`)
	errorKnownTokenPattern   = regexp.MustCompile(`(?i)\b(?:sk[-_](?:live[-_])?[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[baprs]-[A-Za-z0-9-]{16,}|(?-i:eyJ)[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]*(?:\.[A-Za-z0-9_-]*)?)\b`)
	errorLongTokenPattern    = regexp.MustCompile(`[A-Za-z0-9_+=-]{32,}`)
	errorUUIDPattern         = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	identifierWordPattern    = regexp.MustCompile(`[A-Z]?[a-z]+|[A-Z]+|[0-9]+`)
)

// errorRedactions runs Bearer/Basic credentials and known token formats before
// credential fields, so "invalid token: Bearer x" never survives as a diagnostic
// phrase and a JWT is removed before the OTP pattern splits it. DSN credentials
// run before e-mail so "user:secret@db.example.test" keeps the host.
//
// Accepted limitations (not redacted on purpose): phone numbers without a label
// or "+" prefix, JSON embedded with double escaping (\"password\"), and 128-bit
// hex digests, which are indistinguishable from content hashes.
var errorRedactions = []*regexp.Regexp{
	errorBearerPattern,
	errorBasicAuthPattern,
	errorBasicPattern,
	errorKnownTokenPattern,
	errorFieldPattern,
	errorDSNPasswordPattern,
	errorEmailPattern,
	errorURLQueryPattern,
	errorCookieSessionPattern,
	errorPostgresValuePattern,
	errorOTPPattern,
	errorPhoneContextPattern,
	errorE164Pattern,
	errorLongTokenPattern,
}

var forbidden = []string{"authorization", "cookie", "token", "otp", "password", "secret", "presigned", "contact", "email", "phone", "recipient", "destination", "address", "body", "html", "url", "image", "ciphertext", "nonce", "credential", "bearer", "prompt", "response", "graphql.variables"}

// Sanitize removes sensitive and unbounded fields while keeping correlation.
func Sanitize(fields map[string]any) map[string]any {
	clean := make(map[string]any, len(fields))
	for key, value := range fields {
		lower := strings.ToLower(key)
		blocked := false
		for _, term := range forbidden {
			if strings.Contains(lower, term) {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		if lower == "tenant_id" || lower == "tenantid" {
			sum := sha256.Sum256([]byte(toString(value)))
			clean["tenant_hash"] = hex.EncodeToString(sum[:8])
			continue
		}
		clean[key] = value
	}
	return clean
}

// SanitizeError redacts common credentials and personal data from bounded error text.
func SanitizeError(value string) string {
	value = truncateUTF8(value, 4096)
	uuids := make([]string, 0)
	value = errorUUIDPattern.ReplaceAllStringFunc(value, func(uuid string) string {
		marker := "\x00UUID" + strconv.Itoa(len(uuids)) + "\x00"
		uuids = append(uuids, uuid)
		return marker
	})
	for _, pattern := range errorRedactions {
		switch pattern {
		case errorFieldPattern:
			value = pattern.ReplaceAllStringFunc(value, redactCredentialField)
		case errorEmailPattern, errorOTPPattern, errorKnownTokenPattern:
			value = pattern.ReplaceAllString(value, "[REDACTED]")
		case errorURLQueryPattern:
			value = pattern.ReplaceAllString(value, "$1?[REDACTED]")
		case errorDSNPasswordPattern:
			value = pattern.ReplaceAllString(value, "$1[REDACTED]$2")
		case errorBearerPattern:
			value = pattern.ReplaceAllString(value, "Bearer [REDACTED]")
		case errorBasicAuthPattern:
			value = pattern.ReplaceAllString(value, "$1[REDACTED]")
		case errorBasicPattern:
			value = pattern.ReplaceAllString(value, "Basic [REDACTED]")
		case errorCookieSessionPattern:
			value = pattern.ReplaceAllString(value, "$1[REDACTED]")
		case errorPostgresValuePattern:
			value = pattern.ReplaceAllString(value, "$1\"[REDACTED]\"")
		case errorPhoneContextPattern:
			value = pattern.ReplaceAllString(value, "$1[REDACTED]$2")
		case errorE164Pattern:
			value = pattern.ReplaceAllString(value, "[REDACTED]")
		case errorLongTokenPattern:
			value = redactLongTokens(value, pattern)
		}
	}
	for index, uuid := range uuids {
		value = strings.ReplaceAll(value, "\x00UUID"+strconv.Itoa(index)+"\x00", uuid)
	}
	return truncateUTF8(value, 4096)
}

// redactCredentialField hides the value of a credential-like key. Unquoted
// "token: phrase" values are often diagnostic sentences ("invalid token:
// context deadline exceeded"), so only a secret-looking first word is hidden
// for them; "token=value" always hides the first word. The remaining phrase is
// scanned again because it may carry another credential field.
func redactCredentialField(match string) string {
	loc := errorFieldPattern.FindStringSubmatchIndex(match)
	if len(loc) != 14 || loc[12] < 0 {
		return "[REDACTED]"
	}
	head, raw := match[:loc[12]], match[loc[12]:loc[13]]
	key, separator := match[loc[6]:loc[7]], match[loc[10]:loc[11]]
	quoted := strings.HasPrefix(raw, `"`) || strings.HasPrefix(raw, "'")
	if quoted || !strings.Contains(strings.ToLower(key), "token") {
		return head + "[REDACTED]"
	}
	word, rest := raw, ""
	if end := strings.IndexAny(raw, " \t\r"); end >= 0 {
		word, rest = raw[:end], raw[end:]
	}
	rest = errorFieldPattern.ReplaceAllStringFunc(rest, redactCredentialField)
	if strings.HasPrefix(raw, "Bearer [REDACTED]") || strings.HasPrefix(raw, "Basic [REDACTED]") {
		return head + word + rest
	}
	if separator == ":" && isPlainLowercaseWord(word) {
		return head + word + rest
	}
	return head + "[REDACTED]" + rest
}

// isPlainLowercaseWord reports words such as "expired" or "context" that carry
// a diagnostic cause instead of a credential.
func isPlainLowercaseWord(word string) bool {
	word = strings.TrimRight(word, ".:")
	if word == "" || len(word) > 24 {
		return false
	}
	for _, char := range word {
		if char < 'a' || char > 'z' {
			return false
		}
	}
	return true
}

func redactLongTokens(value string, pattern *regexp.Regexp) string {
	matches := pattern.FindAllStringIndex(value, -1)
	if len(matches) == 0 {
		return value
	}
	var output strings.Builder
	last := 0
	for _, match := range matches {
		start, end := match[0], match[1]
		if (start > 0 && isOpaqueTokenByte(value[start-1])) || (end < len(value) && isOpaqueTokenByte(value[end])) {
			continue
		}
		output.WriteString(value[last:start])
		output.WriteString(redactHighEntropyToken(value[start:end]))
		last = end
	}
	if last == 0 {
		return value
	}
	output.WriteString(value[last:])
	return output.String()
}

func isOpaqueTokenByte(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '_' || value == '+' || value == '=' || value == '-'
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for remaining := 0; remaining < 3 && len(value) > 0; remaining++ {
		start := len(value) - 1
		for start >= 0 && !utf8.RuneStart(value[start]) && len(value)-start < 4 {
			start--
		}
		if start < 0 || utf8.FullRuneInString(value[start:]) {
			break
		}
		value = value[:start]
	}
	return value + "…"
}

func redactHighEntropyToken(token string) string {
	if looksLikeIdentifier(token) || !hasHighEntropy(token) {
		return token
	}
	return "[REDACTED]"
}

// looksLikeIdentifier keeps snake_case, kebab-case, migration names and
// PascalCase Go/gqlgen type names readable. Every "_"/"-"/"=" separated part must
// be short or made of readable words; random base64/hex chunks fail this test.
func looksLikeIdentifier(token string) bool {
	parts := strings.FieldsFunc(token, func(char rune) bool { return char == '_' || char == '-' || char == '=' })
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if !looksLikeIdentifierPart(part) {
			return false
		}
	}
	return true
}

func looksLikeIdentifierPart(part string) bool {
	if len(part) <= 6 {
		return true
	}
	// A long all-caps run is random when it is mixed with lowercase letters
	// (base64) or fills a long part (base32). Short SCREAMING_SNAKE parts such
	// as INSPECTION_DATABASE_URL remain readable.
	strictCaps := strings.ToUpper(part) != part || len(part) > 16
	letters, letterWords, digitGroups := 0, 0, 0
	for _, word := range identifierWordPattern.FindAllString(part, -1) {
		if word[0] >= '0' && word[0] <= '9' {
			// Identifiers carry few short numbers (V2, Sha256); random
			// base64/hex chunks scatter digits across the part.
			digitGroups++
			if len(word) > 3 || digitGroups > 2 {
				return false
			}
			continue
		}
		if strictCaps && len(word) > 5 && strings.ToUpper(word) == word {
			return false
		}
		letters += len(word)
		letterWords++
	}
	return letterWords > 0 && letters >= 4*letterWords
}

func hasHighEntropy(token string) bool {
	unique := make(map[rune]struct{})
	counts := make(map[rune]int)
	categories := 0
	for _, char := range token {
		unique[char] = struct{}{}
		counts[char]++
		switch {
		case char >= 'A' && char <= 'Z':
			categories |= 1
		case char >= 'a' && char <= 'z':
			categories |= 2
		case char >= '0' && char <= '9':
			categories |= 4
		default:
			categories |= 8
		}
	}
	entropy := 0.0
	for _, count := range counts {
		probability := float64(count) / float64(len(token))
		entropy -= probability * math.Log2(probability)
	}
	categoryCount := 0
	for bits := categories; bits > 0; bits &= bits - 1 {
		categoryCount++
	}
	return len(unique) >= 12 && categoryCount >= 2 && entropy >= 3.5
}

func toString(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return "redacted-type"
}
