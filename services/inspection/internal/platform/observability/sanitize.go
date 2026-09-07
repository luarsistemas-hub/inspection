package observability

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

var forbidden = []string{"authorization", "cookie", "token", "otp", "password", "secret", "presigned", "contact", "email", "phone", "image", "prompt", "response", "graphql.variables"}

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

func toString(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return "redacted-type"
}
