// Package coordinator owns the cross-slice onboarding state machine.
package coordinator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"inspection/libs/identity"
	assetaddress "inspection/services/inspection/internal/features/assets/address"
	onboardingcatalog "inspection/services/inspection/internal/features/onboarding/real_estate_catalog"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/apperror"
)

const (
	StateAgencySaved      = "AGENCY_SAVED"
	StatePropertySaved    = "PROPERTY_SAVED"
	StateParticipantSaved = "PARTICIPANT_SAVED"
	StateReadyToSubmit    = "READY_TO_SUBMIT"
	StateDeliveryPending  = "DELIVERY_PENDING"
	StateSubmitted        = "SUBMITTED"
	StateFailed           = "FAILED"

	NextActionRetryDelivery = "RETRY_DELIVERY"
	NextActionOpenCapture   = "OPEN_CAPTURE"
)

// StepPayload is the canonical, already decoded checkpoint document.
type StepPayload map[string]any

// SubmitInput is the coordinator's immutable submission boundary.
type SubmitInput struct {
	Now            time.Time
	Steps          map[string]StepPayload
	DeliveryState  string
	IdempotencyKey string
}

// SubmitResult is safe to project to GraphQL while dependencies are pending.
type SubmitResult struct {
	State, NextAction, DeliveryStatus string
	Digest                            string
}

const (
	// maxNameCodePoints bounds every person, agency, and unit name.
	maxNameCodePoints = catalog.MaxNameCodePoints
	// maxEmailLength is the RFC 5321 address limit.
	maxEmailLength = 320
	// maxUnitCodeCodePoints matches the business unit code column.
	maxUnitCodeCodePoints = 200
)

// ValidateProperty validates the fields owned by the property checkpoint.
func ValidateProperty(payload StepPayload, now time.Time) error {
	_, err := canonicalProperty(payload, now)
	return err
}

// canonicalProperty validates the property checkpoint and returns the only
// document that may be stored: catalog keys, NFC text, uppercase enums.
func canonicalProperty(payload StepPayload, now time.Time) (StepPayload, error) {
	result := StepPayload{}
	if raw, ok := payload["addressDetails"].(map[string]any); ok {
		details := assetaddress.Details{PostalCode: mapString(raw, "postalCode"), Street: mapString(raw, "street"), Number: mapString(raw, "number"), WithoutNumber: raw["withoutNumber"] == true, Complement: mapString(raw, "complement"), District: mapString(raw, "district"), City: mapString(raw, "city"), State: mapString(raw, "state"), MunicipalityCode: mapString(raw, "municipalityCode"), Reference: mapString(raw, "reference")}
		value, err := assetaddress.Validate(details)
		if err != nil {
			return nil, apperror.New(apperror.InvalidInput, "addressDetails", "Confira os dados do endereço informado.")
		}
		result["addressDetails"] = map[string]any{"countryCode": value.CountryCode, "postalCode": value.PostalCode, "street": value.Street, "number": value.Number, "withoutNumber": value.WithoutNumber, "complement": value.Complement, "district": value.District, "city": value.City, "state": value.State, "municipalityCode": value.MunicipalityCode, "reference": value.Reference}
	} else {
		legacyAddress, ok := assetaddress.CleanText(stringValue(payload, "address"))
		if !ok || legacyAddress == "" || utf8.RuneCountInString(legacyAddress) > catalog.MaxTextCodePoints {
			return nil, apperror.New(apperror.InvalidInput, "address", "Informe um endereço válido para o imóvel.")
		}
		result["address"] = legacyAddress
	}
	deadline, ok := propertyDeadline(payload)
	if !ok || !deadline.After(now) {
		return nil, apperror.New(apperror.InvalidInput, "deadline", "Informe um prazo futuro para a vistoria.")
	}
	if value := stringValue(payload, "deadline"); value != "" {
		result["deadline"] = value
	} else {
		result["deadlineAt"] = stringValue(payload, "deadlineAt")
	}
	purpose, ok := catalogOption("purpose", stringValue(payload, "purpose"))
	if !ok {
		return nil, apperror.New(apperror.InvalidInput, "purpose", "Selecione uma finalidade válida para a vistoria.")
	}
	result["purpose"] = purpose
	propertyType, ok := catalogOption("propertyType", stringValue(payload, "propertyType"))
	if !ok {
		return nil, apperror.New(apperror.InvalidInput, "propertyType", "Selecione um tipo de imóvel válido.")
	}
	result["propertyType"] = propertyType
	return result, nil
}

// catalogOption resolves value against the closed property options and
// returns the catalog's canonical (upper-case) spelling.
func catalogOption(field, value string) (string, bool) {
	value = strings.ToUpper(strings.TrimSpace(value))
	for _, option := range onboardingcatalog.FieldOptions("property", field) {
		if option == value {
			return option, true
		}
	}
	return "", false
}

func mapString(value map[string]any, key string) string {
	result, _ := value[key].(string)
	return strings.TrimSpace(result)
}

// ValidateDelegate enforces participant input before any domain write.
func ValidateDelegate(payload StepPayload) error {
	_, err := canonicalDelegate(payload)
	return err
}

func canonicalDelegate(payload StepPayload) (StepPayload, error) {
	name, ok := assetaddress.CleanText(stringValue(payload, "name"))
	if name == "" {
		return nil, apperror.New(apperror.InvalidInput, "name", "Informe o nome da pessoa responsável.")
	}
	if !ok || utf8.RuneCountInString(name) > maxNameCodePoints {
		return nil, apperror.New(apperror.InvalidInput, "name", "O nome da pessoa responsável deve ter até 200 caracteres e não pode conter caracteres especiais ocultos.")
	}
	email, err := canonicalEmail(stringValue(payload, "email"))
	if err != nil {
		return nil, apperror.New(apperror.InvalidInput, "email", "Informe um e-mail válido.")
	}
	confirmation, err := canonicalEmail(stringValue(payload, "emailConfirmation"))
	if err != nil || confirmation != email {
		return nil, apperror.New(apperror.InvalidInput, "emailConfirmation", "Os e-mails precisam ser iguais.")
	}
	return StepPayload{"mode": "DELEGATE", "name": name, "email": email, "emailConfirmation": confirmation}, nil
}

func canonicalEmail(value string) (string, error) {
	normalized, err := participantcore.Normalize("EMAIL", value)
	if err != nil {
		return "", err
	}
	if len(normalized) > maxEmailLength || !assetaddress.SafeText(normalized) {
		return "", fmt.Errorf("invalid email")
	}
	return normalized, nil
}

func canonicalAgency(payload StepPayload) (StepPayload, error) {
	name := stringValue(payload, "agencyName")
	if name == "" {
		name = stringValue(payload, "name")
	}
	name, ok := assetaddress.CleanText(name)
	if name == "" || !ok || utf8.RuneCountInString(name) > maxNameCodePoints {
		return nil, apperror.New(apperror.InvalidInput, "name", "Informe um nome válido para a imobiliária, com até 200 caracteres.")
	}
	result := StepPayload{"name": name}
	if _, present := payload["agencyName"]; present {
		result["agencyName"] = name
	}
	for _, field := range []struct{ key, label string }{{"businessUnitCode", "o código da unidade"}, {"businessUnitName", "o nome da unidade"}} {
		raw := stringValue(payload, field.key)
		if raw == "" {
			continue
		}
		value, ok := assetaddress.CleanText(raw)
		if !ok || utf8.RuneCountInString(value) > maxUnitCodeCodePoints {
			return nil, apperror.New(apperror.InvalidInput, field.key, "Confira "+field.label+": use até 200 caracteres, sem caracteres especiais ocultos.")
		}
		result[field.key] = value
	}
	if raw := stringValue(payload, "existingAgencyId"); raw != "" {
		id, err := identity.ParseID(raw)
		if err != nil {
			return nil, apperror.New(apperror.InvalidInput, "existingAgencyId", "Confirme a imobiliária vinculada a este e-mail.")
		}
		result["existingAgencyId"] = id.String()
	}
	return result, nil
}

// ValidateCheckpointPayload validates the payload owned by a checkpoint
// before it can be persisted or trigger any external provisioning work.
func ValidateCheckpointPayload(step string, payload StepPayload, now time.Time) error {
	_, err := CanonicalCheckpointPayload(step, payload, now)
	return err
}

// CanonicalCheckpointPayload validates a checkpoint and returns the canonical
// document to persist. Browser-supplied keys outside the catalog are dropped,
// text is NFC without control or format code points, and enums use their
// catalog spelling.
func CanonicalCheckpointPayload(step string, payload StepPayload, now time.Time) (StepPayload, error) {
	switch strings.ToUpper(strings.TrimSpace(step)) {
	case "AGENCY":
		return canonicalAgency(payload)
	case "PROPERTY":
		return canonicalProperty(payload, now)
	case "PARTICIPANT":
		switch strings.ToUpper(stringValue(payload, "mode")) {
		case "SELF":
			return StepPayload{"mode": "SELF"}, nil
		case "DELEGATE":
			return canonicalDelegate(payload)
		default:
			return nil, apperror.New(apperror.InvalidInput, "mode", "Selecione quem realizará a vistoria.")
		}
	default:
		return nil, apperror.New(apperror.InvalidInput, "step", "Esta etapa não está disponível. Atualize a página e tente novamente.")
	}
}

// ValidateSubmit enforces ordering and truthful pending states. It is pure so
// the GraphQL and worker boundaries can share the same transition contract.
func ValidateSubmit(in SubmitInput) (SubmitResult, error) {
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return SubmitResult{}, apperror.New(apperror.InvalidInput, "clientMutationId", "Não foi possível concluir a solicitação. Tente novamente.")
	}
	for _, step := range []string{"AGENCY", "PROPERTY", "PARTICIPANT"} {
		if _, ok := in.Steps[step]; !ok {
			return SubmitResult{}, missingStepError(step)
		}
	}
	if err := ValidateProperty(in.Steps["PROPERTY"], in.Now); err != nil {
		if deadline, ok := propertyDeadline(in.Steps["PROPERTY"]); ok && !deadline.After(in.Now) {
			// The checkpoint is immutable, so a deadline that was valid when it was
			// confirmed can lapse before the user submits. Say so truthfully and
			// point to the recovery path instead of a misleading input error.
			return SubmitResult{}, apperror.New(apperror.InvalidState, "deadline", "O prazo escolhido para a vistoria já passou. Inicie um novo cadastro para informar outro prazo.")
		}
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
		return SubmitResult{}, apperror.New(apperror.InvalidInput, "mode", "Selecione quem realizará a vistoria.")
	}
	state, action := StateReadyToSubmit, NextActionOpenCapture
	if in.DeliveryState == "PENDING" || in.DeliveryState == "FAILED" {
		state, action = StateDeliveryPending, NextActionRetryDelivery
	}
	return SubmitResult{State: state, NextAction: action, DeliveryStatus: in.DeliveryState, Digest: digest(in)}, nil
}

var stepLabels = map[string]string{"AGENCY": "Imobiliária", "PROPERTY": "Imóvel", "PARTICIPANT": "Responsável pela vistoria"}

func missingStepError(step string) error {
	return apperror.New(apperror.InvalidState, strings.ToLower(step), "Preencha e confirme a etapa "+stepLabels[step]+" antes de continuar.")
}

// ValidateCheckpointPrerequisites requires a stored checkpoint for every step
// that precedes the requested one. The session's current step alone is not
// proof: a verified session already points at AGENCY before it is confirmed.
func ValidateCheckpointPrerequisites(step string, completed map[string]bool) error {
	step = strings.ToUpper(strings.TrimSpace(step))
	for _, prior := range []string{"AGENCY", "PROPERTY", "PARTICIPANT"} {
		if prior == step {
			return nil
		}
		if !completed[prior] {
			return missingStepError(prior)
		}
	}
	return nil
}

func digest(in SubmitInput) string {
	raw, _ := json.Marshal(struct {
		Steps map[string]StepPayload `json:"steps"`
	}{in.Steps})
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
	ranks := map[string]int{"AGENCY": 1, "PROPERTY": 2, "PARTICIPANT": 3, "READY_TO_SUBMIT": 4}
	if ranks[strings.ToUpper(requested)] == 0 {
		return fmt.Errorf("unknown onboarding step %q", requested)
	}
	if ranks[strings.ToUpper(requested)] > ranks[strings.ToUpper(current)]+1 {
		return apperror.New(apperror.InvalidState, "property", "Preencha e confirme todas as etapas antes de continuar.")
	}
	return nil
}
