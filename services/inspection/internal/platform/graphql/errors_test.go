package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestPresentErrorLogsInternalCauseWithoutLeakingIt(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{CorrelationID: "corr-qa"})
	cause := errors.New(`connection refused; password=secret; contact ana@example.test; otp=654321; invalid input syntax for type uuid: "postgres-secret-value"`)
	public := PresentError(ctx, apperror.Wrap(apperror.Internal, apperror.Wrap(apperror.Internal, cause)))
	if public.Message != "Não foi possível concluir a operação." || public.Extensions["correlationId"] != "corr-qa" || public.Extensions["code"] != string(apperror.Internal) {
		t.Fatalf("unexpected public error: %+v", public)
	}
	logged := output.String()
	if !strings.Contains(logged, "corr-qa") || !strings.Contains(logged, "errorCode=INTERNAL") || !strings.Contains(logged, "connection refused") {
		t.Fatalf("internal error log omitted safe correlation fields: %s", logged)
	}
	for _, secret := range []string{"password=secret", "ana@example.test", "654321", "postgres-secret-value"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("internal error log leaked %q: %s", secret, logged)
		}
	}
	if strings.Contains(public.Message, "postgres") || strings.Contains(public.Message, "password") || strings.Contains(public.Message, "secret") {
		t.Fatalf("internal detail leaked: %s", public.Message)
	}
	response, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(response), "postgres") || strings.Contains(string(response), "password") || strings.Contains(string(response), "secret") {
		t.Fatalf("internal cause leaked in serialized GraphQL response: %s", response)
	}
}

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
	if unknown.Code != apperror.Internal || unknown.Message != "Não foi possível concluir a operação." {
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
