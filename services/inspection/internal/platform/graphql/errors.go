package graphql

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"

	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/requestctx"

	gqlgen "github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// PublicError is the stable GraphQL extension contract.
type PublicError struct {
	Code          apperror.Code `json:"code"`
	Field         string        `json:"field,omitempty"`
	CorrelationID string        `json:"correlationId"`
	Message       string        `json:"message"`
}

// PresentError maps every resolver failure to the stable public GraphQL error
// contract without exposing wrapped database or provider details.
func PresentError(ctx context.Context, err error) *gqlerror.Error {
	correlationID := "unavailable"
	if metadata, ok := requestctx.FromContext(ctx); ok && metadata.CorrelationID != "" {
		correlationID = metadata.CorrelationID
	}
	public := MapError(err, correlationID)
	if public.Code == apperror.Internal {
		operation := graphqlOperationName(ctx)
		slog.ErrorContext(ctx, "graphql resolver failure", "operation", operation, "correlationId", correlationID, "error", err)
	}
	return &gqlerror.Error{Message: public.Message, Extensions: map[string]any{"code": string(public.Code), "field": public.Field, "correlationId": public.CorrelationID}}
}

func graphqlOperationName(ctx context.Context) (operation string) {
	operation = "unknown"
	defer func() {
		if recover() != nil {
			operation = "unknown"
		}
	}()
	if operationContext := gqlgen.GetOperationContext(ctx); operationContext != nil && operationContext.Operation != nil && operationContext.Operation.Name != "" {
		return operationContext.Operation.Name
	}
	return operation
}

func MapError(err error, correlationID string) PublicError {
	code, field, safe := apperror.Public(err)
	if safe == "" {
		safe = publicMessage(code)
	}
	return PublicError{Code: code, Field: field, CorrelationID: correlationID, Message: safe}
}

func publicMessage(code apperror.Code) string {
	if code == apperror.Internal {
		return "internal error"
	}
	return "request failed"
}

type Cursor struct {
	Time string `json:"t"`
	ID   string `json:"id"`
}

func EncodeCursor(c Cursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func DecodeCursor(value string) (Cursor, error) {
	var c Cursor
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return c, apperror.New(apperror.InvalidInput, "after", "invalid cursor")
	}
	if err := json.Unmarshal(raw, &c); err != nil || c.Time == "" || c.ID == "" {
		return c, apperror.New(apperror.InvalidInput, "after", "invalid cursor")
	}
	return c, nil
}
func PageSize(requested int) (int, error) {
	if requested < 0 {
		return 0, fmt.Errorf("page size must be positive")
	}
	if requested == 0 {
		return 25, nil
	}
	if requested > 100 {
		return 100, nil
	}
	return requested, nil
}
