// Package coordinator owns the cross-slice onboarding state machine.
package coordinator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/apperror"
)

const (
	StateAgencySaved      = "AGENCY_SAVED"
	StatePropertySaved    = "PROPERTY_SAVED"
	StateOriginPending    = "ORIGIN_PENDING"
	StateOriginReady      = "ORIGIN_READY"
	StateParticipantSaved = "PARTICIPANT_SAVED"
	StateReadyToSubmit    = "READY_TO_SUBMIT"
	StateDeliveryPending  = "DELIVERY_PENDING"
	StateSubmitted        = "SUBMITTED"
	StateFailed           = "FAILED"

	NextActionWaitForOrigin = "WAIT_FOR_ORIGIN"
	NextActionRetryDelivery = "RETRY_DELIVERY"
	NextActionOpenCapture   = "OPEN_CAPTURE"
)

// StepPayload is the canonical, already decoded checkpoint document.
type StepPayload map[string]any

// SubmitInput is the coordinator's immutable submission boundary.
type SubmitInput struct {
	Now            time.Time
	Steps          map[string]StepPayload
	OriginStatus   string
	DeliveryState  string
	TemplateMode   catalog.ComparisonMode
	IdempotencyKey string
}

// SubmitResult is safe to project to GraphQL while dependencies are pending.
type SubmitResult struct {
	State, NextAction, OriginStatus, DeliveryStatus string
	Digest                                          string
}

// ValidateProperty validates the fields owned by the property checkpoint.
func ValidateProperty(payload StepPayload, now time.Time) error {
	address := stringValue(payload, "address")
	if address == "" || utf8.RuneCountInString(address) > catalog.MaxTextCodePoints {
		return apperror.New(apperror.InvalidInput, "address", "address is required and must be within the text limit")
	}
	deadline, ok := propertyDeadline(payload)
	if !ok || !deadline.After(now) {
		return apperror.New(apperror.InvalidInput, "deadline", "deadline must be in the future")
	}
	purpose := strings.ToUpper(stringValue(payload, "purpose"))
	switch purpose {
	case "SALE", "RENTAL", "MAINTENANCE", "INSURANCE":
	default:
		return apperror.New(apperror.InvalidInput, "purpose", "invalid property purpose")
	}
	rooms, exists := payload["rooms"]
	if !exists || !positiveInteger(rooms) {
		return apperror.New(apperror.InvalidInput, "rooms", "rooms must be a positive integer")
	}
	return nil
}

// ValidateDelegate enforces participant input before any domain write.
func ValidateDelegate(payload StepPayload) error {
	if stringValue(payload, "name") == "" {
		return apperror.New(apperror.InvalidInput, "name", "participant name is required")
	}
	email := stringValue(payload, "email")
	parsed, err := mail.ParseAddress(strings.ToLower(strings.TrimSpace(email)))
	if err != nil || parsed.Address != strings.ToLower(strings.TrimSpace(email)) {
		return apperror.New(apperror.InvalidInput, "email", "invalid email")
	}
	return nil
}

// ValidateOriginMedia applies the private-origin media contract at the
// coordinator boundary, before an upload can be submitted for activation.
func ValidateOriginMedia(contentType string, size int64, description string) error {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "image/") {
		return apperror.New(apperror.InvalidInput, "contentType", "origin media must be an image")
	}
	if size <= 0 || size > catalog.MaxOriginalBytes {
		return apperror.New(apperror.InvalidInput, "sizeBytes", "origin media exceeds the size limit")
	}
	if strings.TrimSpace(description) == "" {
		return apperror.New(apperror.InvalidInput, "description", "origin media description is required")
	}
	return nil
}

// ValidateCheckpointPayload validates the payload owned by a checkpoint
// before it can be persisted or trigger any external provisioning work.
func ValidateCheckpointPayload(step string, payload StepPayload, now time.Time) error {
	switch strings.ToUpper(strings.TrimSpace(step)) {
	case "AGENCY":
		name := stringValue(payload, "agencyName")
		if name == "" {
			name = stringValue(payload, "name")
		}
		if name == "" || utf8.RuneCountInString(name) > catalog.MaxTextCodePoints {
			return apperror.New(apperror.InvalidInput, "name", "agency name is required and must be within the text limit")
		}
	case "PROPERTY":
		return ValidateProperty(payload, now)
	case "ORIGIN":
		mode := strings.ToUpper(stringValue(payload, "mode"))
		if mode != "CHECKLIST_ONLY" && mode != "FIXED_ORIGIN" {
			return apperror.New(apperror.InvalidInput, "mode", "invalid origin mode")
		}
	case "PARTICIPANT":
		mode := strings.ToUpper(stringValue(payload, "mode"))
		switch mode {
		case "SELF":
			return nil
		case "DELEGATE":
			return ValidateDelegate(payload)
		default:
			return apperror.New(apperror.InvalidInput, "mode", "invalid participant mode")
		}
	default:
		return apperror.New(apperror.InvalidInput, "step", "invalid step")
	}
	return nil
}

// ValidateSubmit enforces ordering and truthful pending states. It is pure so
// the GraphQL and worker boundaries can share the same transition contract.
func ValidateSubmit(in SubmitInput) (SubmitResult, error) {
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return SubmitResult{}, apperror.New(apperror.InvalidInput, "clientMutationId", "idempotency key is required")
	}
	for _, step := range []string{"AGENCY", "PROPERTY", "ORIGIN", "PARTICIPANT"} {
		if _, ok := in.Steps[step]; !ok {
			return SubmitResult{}, apperror.New(apperror.InvalidState, "step", "incomplete step: "+strings.ToLower(step))
		}
	}
	if err := ValidateProperty(in.Steps["PROPERTY"], in.Now); err != nil {
		return SubmitResult{}, err
	}
	participant := in.Steps["PARTICIPANT"]
	mode := strings.ToUpper(stringValue(participant, "mode"))
	if mode == "SELF" || (mode == "" && boolValue(participant, "self")) {
		// Self capture has no delegate contact fields.
	} else if mode == "DELEGATE" || mode == "" {
		if err := ValidateDelegate(participant); err != nil {
			return SubmitResult{}, err
		}
	} else {
		return SubmitResult{}, apperror.New(apperror.InvalidInput, "mode", "invalid participant mode")
	}
	if in.TemplateMode == catalog.FixedOrigin && in.OriginStatus != "ACTIVE" {
		return SubmitResult{State: StateOriginPending, NextAction: NextActionWaitForOrigin, OriginStatus: "PENDING", DeliveryStatus: in.DeliveryState}, nil
	}
	if in.TemplateMode == catalog.ChecklistOnly && in.OriginStatus == "ACTIVE" {
		return SubmitResult{}, apperror.New(apperror.InvalidInput, "originMode", "checklist-only cannot include an origin")
	}
	state, action := StateReadyToSubmit, NextActionOpenCapture
	if in.DeliveryState == "PENDING" || in.DeliveryState == "FAILED" {
		state, action = StateDeliveryPending, NextActionRetryDelivery
	}
	return SubmitResult{State: state, NextAction: action, OriginStatus: in.OriginStatus, DeliveryStatus: in.DeliveryState, Digest: digest(in)}, nil
}

func digest(in SubmitInput) string {
	raw, _ := json.Marshal(struct {
		Steps map[string]StepPayload `json:"steps"`
		Mode  catalog.ComparisonMode `json:"mode"`
	}{in.Steps, in.TemplateMode})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func stringValue(payload StepPayload, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func boolValue(payload StepPayload, key string) bool {
	value, _ := payload[key].(bool)
	return value
}

func positiveInteger(value any) bool {
	switch value := value.(type) {
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		return err == nil && parsed > 0
	case json.Number:
		parsed, err := strconv.ParseInt(string(value), 10, 64)
		return err == nil && parsed > 0
	case int:
		return value > 0
	case int8:
		return value > 0
	case int16:
		return value > 0
	case int32:
		return value > 0
	case int64:
		return value > 0
	case uint:
		return value > 0
	case uint8:
		return value > 0
	case uint16:
		return value > 0
	case uint32:
		return value > 0
	case uint64:
		return value > 0
	case float32:
		return value > 0 && !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0) && float32(math.Trunc(float64(value))) == value
	case float64:
		return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0) && math.Trunc(value) == value
	default:
		return false
	}
}
func timeValue(payload StepPayload, key string) (time.Time, bool) {
	value := stringValue(payload, key)
	if value == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, value)
	return t, err == nil
}

// propertyDeadline accepts the catalog's date field and the legacy instant
// field so checkpoint validation remains aligned with the public definition
// while previously stored submissions continue to validate.
func propertyDeadline(payload StepPayload) (time.Time, bool) {
	if value := stringValue(payload, "deadline"); value != "" {
		date, err := time.Parse("2006-01-02", value)
		if err != nil {
			return time.Time{}, false
		}
		// A catalog date represents the whole selected calendar day, rather than
		// midnight at the start of that day.
		return date.Add(24*time.Hour - time.Nanosecond), true
	}
	return timeValue(payload, "deadlineAt")
}

// ValidateCheckpointOrder reports the first prerequisite that is missing.
func ValidateCheckpointOrder(current, requested string) error {
	ranks := map[string]int{"AGENCY": 1, "PROPERTY": 2, "ORIGIN": 3, "PARTICIPANT": 4, "READY_TO_SUBMIT": 5}
	if ranks[strings.ToUpper(requested)] == 0 {
		return fmt.Errorf("unknown onboarding step %q", requested)
	}
	if ranks[strings.ToUpper(requested)] > ranks[strings.ToUpper(current)]+1 {
		return apperror.New(apperror.InvalidState, "property", "onboarding prerequisites are incomplete")
	}
	return nil
}
