package resolvers

import (
	"encoding/json"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
)

func mapComponentCaptureAnswer(answer database.ComponentAnswer) *graphql1.ComponentCaptureAnswer {
	var evidence []identity.ID
	_ = json.Unmarshal(answer.EvidenceIDs, &evidence)
	ids := make([]string, 0, len(evidence))
	for _, id := range evidence {
		ids = append(ids, id.String())
	}
	// A component-level answer is stored with an empty view key; the API
	// exposes it as null, the same value the capture client sends for it.
	var view *string
	if answer.ViewKey != "" {
		value := answer.ViewKey
		view = &value
	}
	return &graphql1.ComponentCaptureAnswer{RequirementKey: answer.RequirementKey, ComponentKey: answer.ComponentKey, ViewKey: view, State: answer.State, Reason: answer.Reason, EvidenceIds: ids, Version: int(answer.Version)}
}

func mapHandoverCaptureRecord(record database.HandoverRecord) (*graphql1.HandoverCaptureRecord, error) {
	var payload map[string]any
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return nil, err
	}
	var ids []identity.ID
	if err := json.Unmarshal(record.MediaIDs, &ids); err != nil {
		return nil, err
	}
	media := make([]string, 0, len(ids))
	for _, id := range ids {
		media = append(media, id.String())
	}
	return &graphql1.HandoverCaptureRecord{RecordKey: record.RecordKey, Kind: record.Kind, Payload: payload, MediaIds: media, Declarant: record.Declarant, Version: int(record.Version)}, nil
}

func mapInspectionCoverage(summary capturecore.CoverageSummary) *graphql1.InspectionCoverage {
	suggestions := make([]*graphql1.RecaptureSuggestion, 0, len(summary.Suggestions))
	for _, item := range summary.Suggestions {
		suggestions = append(suggestions, &graphql1.RecaptureSuggestion{RequirementKey: item.RequirementKey, RoomKey: item.RoomKey, RoomName: item.RoomName, ComponentKey: item.ComponentKey, ComponentLabel: item.ComponentLabel, MissingView: item.MissingView})
	}
	pendingRecords := append([]string(nil), summary.PendingRecords...)
	if pendingRecords == nil {
		pendingRecords = []string{}
	}
	return &graphql1.InspectionCoverage{Expected: summary.Expected, FullyAssessed: summary.FullyAssessed, PartiallyAssessed: summary.PartiallyAssessed, NotObserved: summary.NotObserved, NotApplicable: summary.NotApplicable, Impossible: summary.Impossible, UnknownScope: summary.UnknownScope, Suggestions: suggestions, PendingRecords: pendingRecords}
}

func projectDescriptiveCapture(response *graphql1.ExternalCapture, view capturecore.Bootstrap) error {
	if view.ConfirmationOnly {
		response.Coverage = mapInspectionCoverage(capturecore.CoverageSummary{UnknownScope: true, Suggestions: []capturecore.RecaptureSuggestion{}, PendingRecords: []string{}})
		return nil
	}
	if len(view.Scope) == 0 {
		response.Coverage = mapInspectionCoverage(capturecore.CoverageSummary{UnknownScope: true, Suggestions: []capturecore.RecaptureSuggestion{}, PendingRecords: []string{}})
	} else {
		var scope map[string]any
		if err := json.Unmarshal(view.Scope, &scope); err != nil {
			return err
		}
		response.Scope = scope
		response.Coverage = mapInspectionCoverage(view.Coverage)
	}
	response.ComponentAnswers = make([]*graphql1.ComponentCaptureAnswer, 0, len(view.ComponentAnswers))
	for _, answer := range view.ComponentAnswers {
		response.ComponentAnswers = append(response.ComponentAnswers, mapComponentCaptureAnswer(answer))
	}
	response.HandoverRecords = make([]*graphql1.HandoverCaptureRecord, 0, len(view.HandoverRecords))
	for _, record := range view.HandoverRecords {
		mapped, err := mapHandoverCaptureRecord(record)
		if err != nil {
			return err
		}
		response.HandoverRecords = append(response.HandoverRecords, mapped)
	}
	return nil
}
