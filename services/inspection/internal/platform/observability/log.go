package observability

import (
	"encoding/json"
	"io"
)

// Log writes one structured, redacted operational event. Callers provide safe
// identifiers and normalized codes; sensitive fields are dropped centrally.
func Log(w io.Writer, event string, fields map[string]any) error {
	if w == nil {
		return io.ErrClosedPipe
	}
	clean := Sanitize(fields)
	clean["event"] = safeCode(event)
	return json.NewEncoder(w).Encode(clean)
}
