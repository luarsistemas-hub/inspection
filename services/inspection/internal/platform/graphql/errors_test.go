package graphql

import (
	"errors"
	"inspection/services/inspection/internal/platform/apperror"
	"strings"
	"testing"
)

func TestErrorMapperContractsUT042UT043(t *testing.T) {
	codes := []apperror.Code{apperror.Unauthenticated, apperror.Forbidden, apperror.NotFound, apperror.InvalidInput, apperror.InvalidState, apperror.Conflict, apperror.RateLimited, apperror.SessionExpired, apperror.DependencyUnavailable, apperror.Internal}
	for _, code := range codes {
		e := MapError(&apperror.Error{Code: code, Field: "field", Safe: "safe", Cause: errors.New("sql token prompt john@example.com")}, "corr")
		if e.Code != code || e.Field != "field" || e.CorrelationID != "corr" {
			t.Fatalf("bad map: %+v", e)
		}
		if strings.Contains(e.Message, "sql") || strings.Contains(e.Message, "token") || strings.Contains(e.Message, "@") {
			t.Fatalf("leak: %s", e.Message)
		}
	}
	unknown := MapError(errors.New("postgres password=secret"), "corr")
	if unknown.Code != apperror.Internal || unknown.Message != "internal error" {
		t.Fatalf("unknown leak: %+v", unknown)
	}
}
func TestCursorAndPaginationIT599(t *testing.T) {
	encoded := EncodeCursor(Cursor{Time: "2026-01-01T00:00:00Z", ID: "id"})
	if _, err := DecodeCursor(encoded); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeCursor("tampered"); err == nil {
		t.Fatal("tampered cursor accepted")
	}
	if n, _ := PageSize(500); n != 100 {
		t.Fatalf("cap=%d", n)
	}
}
