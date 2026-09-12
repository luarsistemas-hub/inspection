package apperror

import "errors"

// Code is a stable public failure classification.
type Code string

const (
	Unauthenticated          Code = "UNAUTHENTICATED"
	Forbidden                Code = "FORBIDDEN"
	NotFound                 Code = "NOT_FOUND"
	InvalidInput             Code = "INVALID_INPUT"
	InvalidState             Code = "INVALID_STATE"
	Conflict                 Code = "CONFLICT"
	RateLimited              Code = "RATE_LIMITED"
	SessionExpired           Code = "SESSION_EXPIRED"
	DependencyUnavailable    Code = "DEPENDENCY_UNAVAILABLE"
	UnsupportedSchemaVersion Code = "UNSUPPORTED_SCHEMA_VERSION"
	Internal                 Code = "INTERNAL"
)

// Error retains a private cause while exposing only stable metadata.
type Error struct {
	Code  Code
	Field string
	Safe  string
	Cause error
}

func (e *Error) Error() string {
	if e.Safe != "" {
		return e.Safe
	}
	return string(e.Code)
}

func (e *Error) Unwrap() error { return e.Cause }

// New constructs an application error.
func New(code Code, field, safe string) *Error { return &Error{Code: code, Field: field, Safe: safe} }

// Wrap retains the private cause for diagnostics.
func Wrap(code Code, cause error) *Error { return &Error{Code: code, Cause: cause} }

// Public extracts safe data and maps unknown failures to INTERNAL.
func Public(err error) (Code, string, string) {
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr.Code, appErr.Field, appErr.Safe
	}
	return Internal, "", "internal error"
}
