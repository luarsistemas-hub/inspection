package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"sort"
	"strings"

	"inspection/libs/identity"
)

// Channel is an operational-notification delivery channel.
type Channel string

const (
	ChannelEmail    Channel = "EMAIL"
	ChannelSMS      Channel = "SMS"
	ChannelWhatsApp Channel = "WHATSAPP"
)

// State is the durable lifecycle state of one notification request.
type State string

const (
	StateQueued     State = "QUEUED"
	StateProcessing State = "PROCESSING"
	StateAccepted   State = "ACCEPTED"
	StateSent       State = "SENT"
	StateDelivered  State = "DELIVERED"
	StateFailed     State = "FAILED"
	StateUnknown    State = "UNKNOWN"
	StateCanceled   State = "CANCELED"
)

// TemplateRef identifies an immutable, logical message template revision.
type TemplateRef struct {
	Name    string
	Version string
}

// String returns the stable catalog lookup key.
func (t TemplateRef) String() string { return t.Name + ":" + t.Version }

// Notification is the provider-neutral business request. It deliberately has
// no provider ID, rendered content, credential, or ORM dependency.
type Notification struct {
	TenantID       identity.ID
	Recipient      Recipient
	Channel        Channel
	Template       TemplateRef
	Variables      map[string]string
	CorrelationID  string
	IdempotencyKey string
	// Execution holds token-bearing data needed only when the worker renders a
	// sensitive link. It is encrypted before persistence and never enters work
	// variables, outbox, or broker messages.
	Execution *ExecutionPayload
}

// ExecutionPayload is the minimal execution-only input for a sensitive link.
type ExecutionPayload struct {
	InvitationID identity.ID
	Token        string
	URLVariable  string
	BaseURL      string
	ExpiresAt    int64
}

// NotificationResult is the stable result of accepting a request.
type NotificationResult struct {
	ID     identity.ID
	State  State
	Reused bool
}

// NotificationService accepts operational notification requests.
type NotificationService interface {
	Send(context.Context, Notification) (NotificationResult, error)
}

// ErrorCode classifies safe caller-visible validation and idempotency errors.
type ErrorCode string

const (
	InvalidInput          ErrorCode = "INVALID_INPUT"
	UnknownChannel        ErrorCode = "UNKNOWN_CHANNEL"
	UnknownTemplate       ErrorCode = "UNKNOWN_TEMPLATE"
	InvalidRecipient      ErrorCode = "INVALID_RECIPIENT"
	MissingVariable       ErrorCode = "MISSING_VARIABLE"
	IncompatibleVariables ErrorCode = "INCOMPATIBLE_VARIABLES"
	IdempotencyConflict   ErrorCode = "IDEMPOTENCY_CONFLICT"
)

// Error never includes recipient destinations, rendered content, or variables.
type Error struct {
	Code  ErrorCode
	Field string
}

func (e *Error) Error() string {
	if e.Field == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Field
}

var (
	correlationPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,200}$`)
	idempotencyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,200}$`)
	phonePattern       = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)
)

// Validate verifies all fields that can be checked without persistence.
func Validate(notification Notification, catalog Catalog) error {
	if notification.TenantID == (identity.ID{}) {
		return &Error{Code: InvalidInput, Field: "tenantId"}
	}
	if notification.Channel != ChannelEmail && notification.Channel != ChannelSMS && notification.Channel != ChannelWhatsApp {
		return &Error{Code: UnknownChannel, Field: "channel"}
	}
	if err := validateRecipient(notification.Channel, notification.Recipient.Destination); err != nil {
		return err
	}
	if !correlationPattern.MatchString(notification.CorrelationID) {
		return &Error{Code: InvalidInput, Field: "correlationId"}
	}
	if !idempotencyPattern.MatchString(notification.IdempotencyKey) {
		return &Error{Code: InvalidInput, Field: "idempotencyKey"}
	}
	template, err := catalog.Resolve(notification.Template, notification.Channel)
	if err != nil {
		return err
	}
	variables := cloneVariables(notification.Variables)
	if notification.Execution != nil {
		if notification.Execution.InvitationID == (identity.ID{}) || notification.Execution.Token == "" || notification.Execution.URLVariable == "" || notification.Execution.BaseURL == "" {
			return &Error{Code: InvalidInput, Field: "execution"}
		}
		variables[notification.Execution.URLVariable] = "execution-only"
	}
	return template.ValidateVariables(variables)
}

func validateRecipient(channel Channel, destination string) error {
	if destination == "" || len(destination) > 320 {
		return &Error{Code: InvalidRecipient, Field: "recipient"}
	}
	if channel == ChannelEmail {
		address, err := mail.ParseAddress(destination)
		if err != nil || address.Address != destination || !strings.Contains(address.Address, "@") {
			return &Error{Code: InvalidRecipient, Field: "recipient"}
		}
		return nil
	}
	if !phonePattern.MatchString(destination) {
		return &Error{Code: InvalidRecipient, Field: "recipient"}
	}
	return nil
}

// CanonicalDigest gives idempotency a deterministic representation independent
// of Go map iteration order. It does not persist or expose input values.
func CanonicalDigest(notification Notification) (string, error) {
	variables := make([]canonicalVariable, 0, len(notification.Variables))
	for key, value := range notification.Variables {
		variables = append(variables, canonicalVariable{Key: key, Value: value})
	}
	sort.Slice(variables, func(i, j int) bool { return variables[i].Key < variables[j].Key })
	payload := canonicalNotification{
		TenantID: notification.TenantID.String(), RecipientID: notification.Recipient.ID,
		Destination: notification.Recipient.Destination, Channel: string(notification.Channel),
		Template: notification.Template.String(), Variables: variables, CorrelationID: notification.CorrelationID,
	}
	if notification.Execution != nil {
		sum := sha256.Sum256([]byte(notification.Execution.Token))
		payload.Execution = &canonicalExecution{InvitationID: notification.Execution.InvitationID.String(), URLVariable: notification.Execution.URLVariable, BaseURL: notification.Execution.BaseURL, ExpiresAt: notification.Execution.ExpiresAt, TokenDigest: hex.EncodeToString(sum[:])}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("canonical notification: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

type canonicalVariable struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type canonicalNotification struct {
	TenantID      string              `json:"tenantId"`
	RecipientID   string              `json:"recipientId"`
	Destination   string              `json:"destination"`
	Channel       string              `json:"channel"`
	Template      string              `json:"template"`
	Variables     []canonicalVariable `json:"variables"`
	CorrelationID string              `json:"correlationId"`
	Execution     *canonicalExecution `json:"execution,omitempty"`
}

type canonicalExecution struct {
	InvitationID string `json:"invitationId"`
	URLVariable  string `json:"urlVariable"`
	BaseURL      string `json:"baseUrl"`
	ExpiresAt    int64  `json:"expiresAt"`
	TokenDigest  string `json:"tokenDigest"`
}

func cloneVariables(values map[string]string) map[string]string {
	copy := make(map[string]string, len(values)+1)
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

// Is reports whether err is a notification error with the supplied code.
func Is(err error, code ErrorCode) bool {
	var typed *Error
	return errors.As(err, &typed) && typed.Code == code
}
