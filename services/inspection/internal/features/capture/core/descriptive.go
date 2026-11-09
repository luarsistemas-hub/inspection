package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
)

var componentStates = map[string]bool{"OBSERVED": true, "PARTIAL": true, "NOT_OBSERVED": true, "NOT_APPLICABLE": true, "IMPOSSIBLE": true}

// ComponentAnswerInput is one participant's answer for a pinned component or view.
type ComponentAnswerInput struct {
	TenantID, ResponsibilityID                           identity.ID
	RequirementKey, ComponentKey, ViewKey, State, Reason string
	EvidenceIDs                                          []identity.ID
	ExpectedVersion                                      int64
	ActorID                                              *identity.ID
}

// HandoverInput is a typed declaration attached to the capture link's inspection.
type HandoverInput struct {
	TenantID, ResponsibilityID identity.ID
	RecordKey, Kind, Declarant string
	Payload                    json.RawMessage
	MediaIDs                   []identity.ID
	ExpectedVersion            int64
}

// CoverageSummary contains deterministic coverage and recapture guidance.
type CoverageSummary struct {
	Expected          int                   `json:"expected"`
	FullyAssessed     int                   `json:"fullyAssessed"`
	PartiallyAssessed int                   `json:"partiallyAssessed"`
	NotObserved       int                   `json:"notObserved"`
	NotApplicable     int                   `json:"notApplicable"`
	Impossible        int                   `json:"impossible"`
	UnknownScope      bool                  `json:"unknownScope"`
	Suggestions       []RecaptureSuggestion `json:"suggestions"`
	PendingRecords    []string              `json:"pendingRecords"`
}

// RecaptureSuggestion points to a missing pinned view without making a condition claim.
type RecaptureSuggestion struct {
	RequirementKey string `json:"requirementKey"`
	RoomKey        string `json:"roomKey"`
	RoomName       string `json:"roomName"`
	ComponentKey   string `json:"componentKey"`
	ComponentLabel string `json:"componentLabel"`
	MissingView    string `json:"missingView"`
}

// ComponentCoverageAnswer is the deterministic input for coverage projection.
type ComponentCoverageAnswer struct{ RequirementKey, ComponentKey, ViewKey, State string }

type pinnedScope struct {
	Rooms           []catalog.ExpectedRoom   `json:"rooms"`
	HandoverRecords []catalog.HandoverRecord `json:"handoverRecords"`
}

// RecordComponentAnswer validates participant input against the immutable inspection scope.
func (s Service) RecordComponentAnswer(ctx context.Context, in ComponentAnswerInput) (database.ComponentAnswer, error) {
	in.State = strings.ToUpper(strings.TrimSpace(in.State))
	in.Reason = strings.TrimSpace(in.Reason)
	if err := ValidateComponentAnswer(in.State, in.Reason, len(in.EvidenceIDs)); err != nil {
		return database.ComponentAnswer{}, err
	}
	var out database.ComponentAnswer
	err := s.within(ctx, in.TenantID, func(tx *gorm.DB) error {
		var draft database.CaptureDraft
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=?", in.TenantID, in.ResponsibilityID).Take(&draft).Error; err != nil || draft.Status != "OPEN" {
			return apperror.New(apperror.InvalidState, "responsibility", "A captura não está disponível.")
		}
		if in.ActorID == nil {
			var responsibility database.Responsibility
			if err := tx.Select("participant_id").Where("tenant_id=? AND id=?", in.TenantID, in.ResponsibilityID).Take(&responsibility).Error; err != nil {
				return err
			}
			actor := responsibility.ParticipantID
			if actor == (identity.ID{}) {
				actor = in.ResponsibilityID
			}
			in.ActorID = &actor
		}
		scope, err := loadPinnedScope(tx, in.TenantID, in.ResponsibilityID)
		if err != nil {
			return err
		}
		_, component, ok := findComponent(scope, in.RequirementKey, in.ComponentKey)
		if !ok {
			return apperror.New(apperror.InvalidInput, "componentKey", "O componente não pertence ao escopo desta vistoria.")
		}
		if in.ViewKey != "" && !containsString(component.Views, in.ViewKey) {
			return apperror.New(apperror.InvalidInput, "viewKey", "A vista não pertence ao componente selecionado.")
		}
		if len(in.EvidenceIDs) > 0 {
			var media []database.MediaObject
			if err := tx.Where("tenant_id=? AND responsibility_id=? AND id IN ? AND requirement_key=?", in.TenantID, in.ResponsibilityID, in.EvidenceIDs, in.RequirementKey).Find(&media).Error; err != nil {
				return err
			}
			if len(media) != len(in.EvidenceIDs) {
				return apperror.New(apperror.InvalidInput, "evidenceIds", "A evidência precisa pertencer ao mesmo cômodo e à vistoria.")
			}
			for _, m := range media {
				if m.Status != "READY" && m.Status != "SCREENED" {
					return apperror.New(apperror.InvalidState, "evidenceIds", "A verificação da mídia precisa terminar antes de vinculá-la.")
				}
			}
		}
		var existing database.ComponentAnswer
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND draft_id=? AND requirement_key=? AND component_key=? AND view_key=?", in.TenantID, draft.ID, in.RequirementKey, in.ComponentKey, in.ViewKey).Take(&existing)
		if query.Error != nil && query.Error != gorm.ErrRecordNotFound {
			return query.Error
		}
		if query.Error == gorm.ErrRecordNotFound {
			if in.ExpectedVersion > 0 {
				return apperror.New(apperror.Conflict, "expectedVersion", "A resposta foi alterada. Atualize a captura e tente novamente.")
			}
			existing = database.ComponentAnswer{ID: identity.NewID(), TenantID: in.TenantID, DraftID: draft.ID, RequirementKey: in.RequirementKey, ComponentKey: in.ComponentKey, ViewKey: in.ViewKey, Version: 1, CreatedAt: s.now()}
		} else {
			if existing.State == in.State && existing.Reason == in.Reason && jsonEqual(existing.EvidenceIDs, mustJSON(in.EvidenceIDs)) {
				out = existing
				return nil
			}
			if in.ExpectedVersion != existing.Version {
				return apperror.New(apperror.Conflict, "expectedVersion", "A resposta foi alterada. Atualize a captura e tente novamente.")
			}
			existing.Version++
		}
		evidence, _ := json.Marshal(in.EvidenceIDs)
		existing.State, existing.Reason, existing.EvidenceIDs, existing.ActorID, existing.UpdatedAt = in.State, in.Reason, evidence, in.ActorID, s.now()
		if existing.CreatedAt.IsZero() {
			existing.CreatedAt = s.now()
		}
		if query.Error == gorm.ErrRecordNotFound {
			if err := tx.Create(&existing).Error; err != nil {
				return err
			}
		} else if err := tx.Save(&existing).Error; err != nil {
			return err
		}
		actorID := in.ResponsibilityID
		if in.ActorID != nil {
			actorID = *in.ActorID
		}
		if err := tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: in.TenantID, ActorID: actorID, Action: "capture.component_answer_recorded", TargetType: "component_answer", TargetID: existing.ID.String(), Outcome: existing.State, Reason: existing.Reason, CorrelationID: "capture-component-answer-" + existing.ID.String(), OccurredAt: existing.UpdatedAt}).Error; err != nil {
			return err
		}
		out = existing
		return nil
	})
	return out, err
}

// UpsertHandoverRecord persists a declaration separately from visual room coverage.
func (s Service) UpsertHandoverRecord(ctx context.Context, in HandoverInput) (database.HandoverRecord, error) {
	in.RecordKey, in.Kind, in.Declarant = strings.TrimSpace(in.RecordKey), strings.ToUpper(strings.TrimSpace(in.Kind)), strings.TrimSpace(in.Declarant)
	if in.Declarant == "" {
		in.Declarant = "Participante da vistoria"
	}
	if utf8.RuneCountInString(in.Declarant) > 200 {
		return database.HandoverRecord{}, apperror.New(apperror.InvalidInput, "declarant", "O nome do declarante é muito longo.")
	}
	var payload map[string]any
	if err := json.Unmarshal(in.Payload, &payload); err != nil || payload == nil {
		return database.HandoverRecord{}, apperror.New(apperror.InvalidInput, "payload", "Informe os dados da declaração.")
	}
	if in.Kind != "KEY" && in.Kind != "CONTROL" && in.Kind != "METER" && in.Kind != "NOTE" {
		return database.HandoverRecord{}, apperror.New(apperror.InvalidInput, "kind", "O tipo de declaração é inválido.")
	}
	if err := validateHandoverPayload(in.Kind, payload); err != nil {
		return database.HandoverRecord{}, err
	}
	var out database.HandoverRecord
	err := s.within(ctx, in.TenantID, func(tx *gorm.DB) error {
		var draft database.CaptureDraft
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=?", in.TenantID, in.ResponsibilityID).Take(&draft).Error; err != nil || (draft.Status != "OPEN" && draft.Status != "SUBMITTED") {
			return apperror.New(apperror.InvalidState, "responsibility", "A captura não está disponível.")
		}
		scope, err := loadPinnedScope(tx, in.TenantID, in.ResponsibilityID)
		if err != nil {
			return err
		}
		valid := false
		for _, def := range scope.HandoverRecords {
			if def.Key == in.RecordKey && strings.EqualFold(def.Kind, in.Kind) {
				valid = true
				break
			}
		}
		if !valid {
			return apperror.New(apperror.InvalidInput, "recordKey", "O registro não pertence ao escopo desta vistoria.")
		}
		if len(in.MediaIDs) > 0 {
			var count int64
			if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=? AND id IN ? AND status IN ?", in.TenantID, in.ResponsibilityID, in.MediaIDs, []string{"READY", "SCREENED"}).Count(&count).Error; err != nil {
				return err
			}
			if count != int64(len(in.MediaIDs)) {
				return apperror.New(apperror.InvalidInput, "mediaIds", "A mídia precisa pertencer à mesma vistoria e estar verificada.")
			}
		}
		var current database.HandoverRecord
		q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND draft_id=? AND record_key=?", in.TenantID, draft.ID, in.RecordKey).Order("version DESC").Take(&current)
		if q.Error != nil && q.Error != gorm.ErrRecordNotFound {
			return q.Error
		}
		if q.Error == gorm.ErrRecordNotFound {
			if in.ExpectedVersion > 0 || draft.Status == "SUBMITTED" {
				return apperror.New(apperror.Conflict, "expectedVersion", "A declaração foi alterada. Atualize e tente novamente.")
			}
			current = database.HandoverRecord{ID: identity.NewID(), TenantID: in.TenantID, DraftID: draft.ID, RecordKey: in.RecordKey, Kind: in.Kind, Version: 1, CreatedAt: s.now()}
		} else {
			if draft.Status == "SUBMITTED" {
				var issued int64
				var responsibility database.Responsibility
				if err := tx.Select("inspection_id").Where("tenant_id=? AND id=?", in.TenantID, in.ResponsibilityID).Take(&responsibility).Error; err != nil {
					return err
				}
				if err := tx.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", in.TenantID, responsibility.InspectionID).Count(&issued).Error; err != nil {
					return err
				}
				if issued == 0 {
					return apperror.New(apperror.InvalidState, "responsibility", "A declaração só pode ser corrigida após a emissão do laudo.")
				}
			}
			if current.Kind == in.Kind && current.Declarant == in.Declarant && jsonEqual(current.Payload, in.Payload) && jsonEqual(current.MediaIDs, mustJSON(in.MediaIDs)) {
				out = current
				return nil
			}
			if current.Version != in.ExpectedVersion {
				return apperror.New(apperror.Conflict, "expectedVersion", "A declaração foi alterada. Atualize e tente novamente.")
			}
			current = database.HandoverRecord{ID: identity.NewID(), TenantID: in.TenantID, DraftID: draft.ID, RecordKey: in.RecordKey, Kind: in.Kind, Version: current.Version + 1, CreatedAt: s.now()}
		}
		media, _ := json.Marshal(in.MediaIDs)
		current.Payload = append(json.RawMessage(nil), in.Payload...)
		current.MediaIDs = media
		current.Declarant = in.Declarant
		current.UpdatedAt = s.now()
		out = current
		return tx.Create(&current).Error
	})
	return out, err
}

func latestHandoverRecords(records []database.HandoverRecord) []database.HandoverRecord {
	latest := make([]database.HandoverRecord, 0, len(records))
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		if _, exists := seen[record.RecordKey]; exists {
			continue
		}
		seen[record.RecordKey] = struct{}{}
		latest = append(latest, record)
	}
	return latest
}

// Coverage returns component status, pending handover records, and view-based recapture suggestions.
func (s Service) Coverage(ctx context.Context, tenantID, responsibilityID identity.ID) (CoverageSummary, error) {
	var summary CoverageSummary
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var err error
		summary, err = coverageInTx(tx, tenantID, responsibilityID)
		return err
	})
	return summary, err
}

func loadPinnedScope(tx *gorm.DB, tenantID, responsibilityID identity.ID) (pinnedScope, error) {
	var responsibility database.Responsibility
	if err := tx.Where("tenant_id=? AND id=?", tenantID, responsibilityID).Take(&responsibility).Error; err != nil {
		return pinnedScope{}, apperror.New(apperror.NotFound, "responsibility", "A captura não foi encontrada.")
	}
	var row database.InspectionScopeSnapshot
	if err := tx.Where("tenant_id=? AND inspection_id=?", tenantID, responsibility.InspectionID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pinnedScope{}, nil
		}
		return pinnedScope{}, apperror.New(apperror.InvalidState, "scope", "O escopo fixado não está disponível.")
	}
	var scope pinnedScope
	if err := json.Unmarshal(row.Document, &scope); err != nil {
		return pinnedScope{}, apperror.New(apperror.InvalidState, "scope", "O escopo fixado não está disponível.")
	}
	return scope, nil
}

func coverageInTx(tx *gorm.DB, tenantID, responsibilityID identity.ID) (CoverageSummary, error) {
	var summary CoverageSummary
	scope, err := loadPinnedScope(tx, tenantID, responsibilityID)
	if err != nil {
		return summary, err
	}
	if len(scope.Rooms) == 0 {
		summary.UnknownScope = true
	}
	var draft database.CaptureDraft
	if err := tx.Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).Take(&draft).Error; err != nil {
		return summary, err
	}
	var answers []database.ComponentAnswer
	if err := tx.Where("tenant_id=? AND draft_id=?", tenantID, draft.ID).Find(&answers).Error; err != nil {
		return summary, err
	}
	var records []database.HandoverRecord
	if err := tx.Where("tenant_id=? AND draft_id=?", tenantID, draft.ID).Find(&records).Error; err != nil {
		return summary, err
	}
	coverageAnswers := make([]ComponentCoverageAnswer, 0, len(answers))
	validEvidence := make(map[identity.ID]bool)
	for _, a := range answers {
		var evidenceIDs []identity.ID
		if err := json.Unmarshal(a.EvidenceIDs, &evidenceIDs); err != nil {
			return summary, err
		}
		if len(evidenceIDs) > 0 {
			var media []database.MediaObject
			if err := tx.Where("tenant_id=? AND responsibility_id=? AND id IN ? AND status = ?", tenantID, responsibilityID, evidenceIDs, "READY").Find(&media).Error; err != nil {
				return summary, err
			}
			for _, item := range media {
				validEvidence[item.ID] = true
			}
		}
	}
	var requirementAnswers []database.RequirementAnswer
	if err := tx.Where("tenant_id=? AND draft_id=? AND impossibility_reason <> ''", tenantID, draft.ID).Find(&requirementAnswers).Error; err != nil {
		return summary, err
	}
	roomImpossible := make(map[string]bool, len(requirementAnswers))
	for _, answer := range requirementAnswers {
		roomImpossible[answer.RequirementKey] = true
	}
	for _, a := range answers {
		var ids []identity.ID
		_ = json.Unmarshal(a.EvidenceIDs, &ids)
		usable := false
		for _, id := range ids {
			if validEvidence[id] {
				usable = true
				break
			}
		}
		state := UsableAnswerState(a.State, usable)
		coverageAnswers = append(coverageAnswers, ComponentCoverageAnswer{RequirementKey: a.RequirementKey, ComponentKey: a.ComponentKey, ViewKey: a.ViewKey, State: state})
	}
	for i := range scope.Rooms {
		if roomImpossible["env:"+scope.Rooms[i].Key] {
			for j := range scope.Rooms[i].Components {
				key := "env:" + scope.Rooms[i].Key + "|" + scope.Rooms[i].Components[j].Key + "|"
				found := false
				for k := range coverageAnswers {
					if coverageAnswers[k].RequirementKey+"|"+coverageAnswers[k].ComponentKey+"|" == key {
						coverageAnswers[k].State = "IMPOSSIBLE"
						found = true
					}
				}
				if !found {
					coverageAnswers = append(coverageAnswers, ComponentCoverageAnswer{RequirementKey: "env:" + scope.Rooms[i].Key, ComponentKey: scope.Rooms[i].Components[j].Key, State: "IMPOSSIBLE"})
				}
			}
		}
	}
	summary = EvaluateCoverage(scope.Rooms, coverageAnswers)
	for _, record := range scope.HandoverRecords {
		if record.Required && !recordByKey(records, record.Key) {
			summary.PendingRecords = append(summary.PendingRecords, record.Key)
		}
	}
	return summary, nil
}

// EvaluateCoverage calculates component coverage and missing-view suggestions from a pinned scope.
func EvaluateCoverage(rooms []catalog.ExpectedRoom, answers []ComponentCoverageAnswer) CoverageSummary {
	summary := CoverageSummary{UnknownScope: len(rooms) == 0}
	answerBy := make(map[string]ComponentCoverageAnswer, len(answers))
	for _, answer := range answers {
		answerBy[answer.RequirementKey+"|"+answer.ComponentKey+"|"+answer.ViewKey] = answer
	}
	for _, room := range rooms {
		req := "env:" + room.Key
		for _, component := range room.Components {
			summary.Expected++
			state, missingViews := evaluateComponentState(req, component, answerBy)
			switch state {
			case "OBSERVED":
				summary.FullyAssessed++
			case "PARTIAL":
				summary.PartiallyAssessed++
			case "NOT_APPLICABLE":
				summary.NotApplicable++
			case "IMPOSSIBLE":
				summary.Impossible++
			default:
				summary.NotObserved++
			}
			if state == "PARTIAL" || state == "NOT_OBSERVED" {
				if len(missingViews) == 0 {
					missingViews = []string{"general"}
				}
				for _, view := range missingViews {
					summary.Suggestions = append(summary.Suggestions, RecaptureSuggestion{RequirementKey: req, RoomKey: room.Key, RoomName: room.Name, ComponentKey: component.Key, ComponentLabel: component.Label, MissingView: view})
				}
			}
		}
	}
	return summary
}

func evaluateComponentState(requirementKey string, component catalog.ExpectedComponent, answers map[string]ComponentCoverageAnswer) (string, []string) {
	if answer, ok := answers[requirementKey+"|"+component.Key+"|"]; ok {
		return answer.State, missingComponentViews(requirementKey, component, answers)
	}
	if len(component.Views) == 0 {
		return "NOT_OBSERVED", nil
	}
	observed, applicable := 0, 0
	partial, impossible, notApplicable := false, false, false
	missing := make([]string, 0, len(component.Views))
	for _, view := range component.Views {
		answer, ok := answers[requirementKey+"|"+component.Key+"|"+view]
		if !ok {
			applicable++
			missing = append(missing, view)
			continue
		}
		switch answer.State {
		case "OBSERVED":
			applicable++
			observed++
		case "PARTIAL":
			applicable++
			partial = true
			missing = append(missing, view)
		case "IMPOSSIBLE":
			impossible = true
		case "NOT_APPLICABLE":
			notApplicable = true
		default:
			applicable++
			missing = append(missing, view)
		}
	}
	if applicable > 0 && observed == applicable && !partial {
		return "OBSERVED", nil
	}
	if observed > 0 || partial {
		return "PARTIAL", missing
	}
	if impossible {
		return "IMPOSSIBLE", nil
	}
	if notApplicable {
		return "NOT_APPLICABLE", nil
	}
	return "NOT_OBSERVED", missing
}

func missingComponentViews(requirementKey string, component catalog.ExpectedComponent, answers map[string]ComponentCoverageAnswer) []string {
	var missing []string
	for _, view := range component.Views {
		if answer, ok := answers[requirementKey+"|"+component.Key+"|"+view]; !ok || (answer.State != "OBSERVED" && answer.State != "NOT_APPLICABLE" && answer.State != "IMPOSSIBLE") {
			missing = append(missing, view)
		}
	}
	return missing
}

func recordByKey(records []database.HandoverRecord, key string) bool {
	for _, record := range records {
		if record.RecordKey == key {
			return true
		}
	}
	return false
}
func findComponent(scope pinnedScope, requirementKey, componentKey string) (catalog.ExpectedRoom, catalog.ExpectedComponent, bool) {
	for _, room := range scope.Rooms {
		if requirementKey != "env:"+room.Key {
			continue
		}
		for _, component := range room.Components {
			if component.Key == componentKey {
				return room, component, true
			}
		}
	}
	return catalog.ExpectedRoom{}, catalog.ExpectedComponent{}, false
}
func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// ValidateComponentAnswer enforces valid states, evidence, and explanations for exception states.
func ValidateComponentAnswer(state, reason string, evidenceCount int) error {
	state, reason = strings.ToUpper(strings.TrimSpace(state)), strings.TrimSpace(reason)
	if !componentStates[state] {
		return apperror.New(apperror.InvalidInput, "state", "Selecione um estado válido para o componente.")
	}
	if (state == "NOT_APPLICABLE" || state == "IMPOSSIBLE") && reason == "" {
		return apperror.New(apperror.InvalidInput, "reason", "Informe o motivo para este estado.")
	}
	if (state == "OBSERVED" || state == "PARTIAL") && evidenceCount == 0 {
		return apperror.New(apperror.InvalidInput, "evidenceIds", "Vincule uma foto válida para registrar a cobertura.")
	}
	if err := ValidateDescription(reason, false); err != nil {
		return apperror.New(apperror.InvalidInput, "reason", "O motivo informado é inválido.")
	}
	return nil
}

func mustJSON(value any) []byte { encoded, _ := json.Marshal(value); return encoded }

func jsonEqual(left, right []byte) bool {
	var a, b any
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		return bytes.Equal(bytes.TrimSpace(left), bytes.TrimSpace(right))
	}
	return reflect.DeepEqual(a, b)
}

func validateHandoverPayload(kind string, payload map[string]any) error {
	allowed := map[string]map[string]bool{
		"KEY":     {"keyType": true, "quantity": true, "note": true, "observation": true},
		"CONTROL": {"controlType": true, "quantity": true, "note": true, "observation": true},
		"METER":   {"meterType": true, "identifier": true, "reading": true, "unit": true, "date": true, "note": true, "observation": true},
		"NOTE":    {"note": true},
	}
	for key := range payload {
		if !allowed[kind][key] {
			return apperror.New(apperror.InvalidInput, "payload", "O campo informado não é permitido para este tipo de declaração.")
		}
	}
	if kind == "NOTE" {
		if value, ok := payload["note"].(string); !ok || strings.TrimSpace(value) == "" {
			return apperror.New(apperror.InvalidInput, "note", "Informe a observação da declaração.")
		}
	}
	switch kind {
	case "KEY", "CONTROL":
		typeField := "keyType"
		typeLabel := "tipo de chave"
		if kind == "CONTROL" {
			typeField, typeLabel = "controlType", "tipo de controle"
		}
		if value, ok := payload[typeField].(string); !ok || strings.TrimSpace(value) == "" {
			return apperror.New(apperror.InvalidInput, typeField, "Informe o "+typeLabel+" entregue.")
		}
		v, ok := payload["quantity"]
		if kind == "KEY" && !ok {
			return apperror.New(apperror.InvalidInput, "quantity", "Informe a quantidade entregue.")
		}
		if ok {
			n, ok := v.(float64)
			if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || math.Trunc(n) != n {
				return apperror.New(apperror.InvalidInput, "quantity", "Informe uma quantidade igual ou maior que zero.")
			}
		}
	case "METER":
		if value, ok := payload["meterType"].(string); !ok || strings.TrimSpace(value) == "" {
			return apperror.New(apperror.InvalidInput, "meterType", "Informe o tipo do medidor.")
		}
		if value, ok := payload["identifier"].(string); !ok || strings.TrimSpace(value) == "" {
			return apperror.New(apperror.InvalidInput, "identifier", "Informe a identificação do medidor.")
		}
		v, ok := payload["reading"]
		if !ok {
			return apperror.New(apperror.InvalidInput, "reading", "Informe uma leitura válida.")
		}
		reading, ok := v.(float64)
		if !ok || math.IsNaN(reading) || math.IsInf(reading, 0) || reading < 0 {
			return apperror.New(apperror.InvalidInput, "reading", "Informe uma leitura numérica válida.")
		}
		if value, ok := payload["unit"].(string); !ok || strings.TrimSpace(value) == "" {
			return apperror.New(apperror.InvalidInput, "unit", "Informe a unidade da leitura.")
		}
		if date, ok := payload["date"].(string); !ok || strings.TrimSpace(date) == "" {
			return apperror.New(apperror.InvalidInput, "date", "Informe a data da leitura.")
		} else if _, err := time.Parse("2006-01-02", date); err != nil {
			return apperror.New(apperror.InvalidInput, "date", "Informe uma data válida para a leitura.")
		}
	}
	limit := 4096
	if kind == "NOTE" {
		limit = 16 * 1024
	}
	if len(mustJSON(payload)) > limit {
		if kind == "NOTE" {
			return apperror.New(apperror.InvalidInput, "note", "A observação excede o limite permitido de 2.000 caracteres.")
		}
		return apperror.New(apperror.InvalidInput, "payload", "Os dados da declaração excedem o limite permitido.")
	}
	for _, key := range []string{"note", "observation"} {
		if note, ok := payload[key].(string); ok && utf8.RuneCountInString(note) > 2000 {
			if kind == "NOTE" {
				return apperror.New(apperror.InvalidInput, "note", "A observação excede o limite permitido de 2.000 caracteres.")
			}
			return apperror.New(apperror.InvalidInput, key, "A observação excede o limite permitido.")
		}
	}
	return nil
}
