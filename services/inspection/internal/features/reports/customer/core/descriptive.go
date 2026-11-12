package core

import (
	"encoding/json"
	"slices"

	reportcore "inspection/services/inspection/internal/features/reports/core"
)

// ChecklistView is the customer-safe allowlist for descriptive report facts.
type ChecklistView struct {
	Rooms               []ChecklistRoomView
	Coverage            ChecklistCoverageView
	ClassificationLabel string
	Limited             bool
	Processing          string
	Handover            []HandoverView
}

type ChecklistRoomView struct {
	Name       string                   `json:"name"`
	Components []ChecklistComponentView `json:"components"`
}
type ChecklistComponentView struct {
	Label        string   `json:"label"`
	DisplayGroup string   `json:"displayGroup"`
	Status       string   `json:"status"`
	Description  string   `json:"description"`
	Condition    string   `json:"condition"`
	ReviewLabel  string   `json:"reviewLabel"`
	EvidenceIDs  []string `json:"evidenceIds"`
	Limitations  []string `json:"limitations"`
}
type ChecklistCoverageView struct {
	Expected          int  `json:"expected"`
	FullyAssessed     int  `json:"fullyAssessed"`
	PartiallyAssessed int  `json:"partiallyAssessed"`
	NotObserved       int  `json:"notObserved"`
	NotApplicable     int  `json:"notApplicable"`
	Impossible        int  `json:"impossible"`
	UnknownScope      bool `json:"unknownScope"`
}
type HandoverView struct {
	Label, Kind, Status, Type, Value, Unit, Date, Declarant string
	EvidenceIDs                                             []string
}

// FilterDescriptive applies the customer allowlist to the v3 checklist and
// handover sections while keeping their stable codes, keys and evidence IDs.
// Presentation (Portuguese labels and E### references) is applied once, by the
// screen projection or the PDF renderer, never here.
func FilterDescriptive(snapshot reportcore.Snapshot) (*reportcore.ChecklistSection, *reportcore.HandoverSection) {
	if snapshot.SchemaVersion < 3 || snapshot.Checklist == nil {
		return nil, nil
	}
	checklist := &reportcore.ChecklistSection{Coverage: snapshot.Checklist.Coverage, Rooms: make([]reportcore.ChecklistRoom, 0, len(snapshot.Checklist.Rooms))}
	for _, room := range snapshot.Checklist.Rooms {
		out := reportcore.ChecklistRoom{Key: room.Key, Name: room.Name, Components: make([]reportcore.ChecklistComponent, 0, len(room.Components))}
		for _, component := range room.Components {
			if component.OutsideScope {
				continue
			}
			description, condition := component.Description, component.Condition
			limitations := make([]string, 0, len(component.Limitations)+1)
			conflict := component.Conflict || slices.ContainsFunc(component.Limitations, reportcore.IsInternalLimitation)
			if conflict {
				description, condition = "", ""
				limitations = append(limitations, reportcore.LimitationConditionUndetermined)
			}
			// Conflict explanations name internal inputs (catalog, findings,
			// voice and visual sources); the customer reads only the neutral
			// statement that the condition was not determined.
			for _, limitation := range component.Limitations {
				if reportcore.IsInternalLimitation(limitation) {
					limitation = reportcore.LimitationConditionUndetermined
				}
				if !slices.Contains(limitations, limitation) {
					limitations = append(limitations, limitation)
				}
			}
			reviewStatus := ""
			if component.ReviewStatus == "NOT_REVIEWED" || component.ReviewStatus == "PENDING" {
				reviewStatus = "NOT_REVIEWED"
			}
			out.Components = append(out.Components, reportcore.ChecklistComponent{Key: component.Key, Label: component.Label, Status: component.Status, Description: description, Condition: condition, ReviewStatus: reviewStatus, EvidenceIDs: append([]string(nil), component.EvidenceIDs...), Limitations: limitations})
		}
		checklist.Rooms = append(checklist.Rooms, out)
	}
	handover := &reportcore.HandoverSection{Entries: []reportcore.HandoverEntry{}}
	if snapshot.Handover != nil {
		for _, entry := range snapshot.Handover.Entries {
			out := reportcore.HandoverEntry{Key: entry.Key, Kind: entry.Kind, Status: entry.Status, EvidenceIDs: append([]string(nil), entry.EvidenceIDs...)}
			if entry.Kind == "KEY" || entry.Kind == "CONTROL" || entry.Kind == "METER" {
				out.Declarant = entry.Declarant
			}
			out.Payload = filterHandoverPayload(entry.Kind, entry.Payload)
			handover.Entries = append(handover.Entries, out)
		}
	}
	return checklist, handover
}

// filterHandoverPayload keeps only the allowlisted payload fields with their
// original JSON values, so numbers and codes are presented exactly once later.
func filterHandoverPayload(kind string, payload json.RawMessage) json.RawMessage {
	fields := reportcore.CustomerHandoverPayloadFields[kind]
	if len(fields) == 0 || len(payload) == 0 {
		return nil
	}
	var decoded map[string]json.RawMessage
	if json.Unmarshal(payload, &decoded) != nil {
		return nil
	}
	allowed := make(map[string]json.RawMessage, len(fields))
	for _, field := range fields {
		if value, ok := decoded[field]; ok {
			allowed[field] = value
		}
	}
	if len(allowed) == 0 {
		return nil
	}
	encoded, err := json.Marshal(allowed)
	if err != nil {
		return nil
	}
	return encoded
}

// ProjectChecklist copies only approved customer fields from a v3 snapshot and
// presents them with Portuguese labels and report-local evidence references.
func ProjectChecklist(snapshot reportcore.Snapshot) *ChecklistView {
	checklist, handover := FilterDescriptive(snapshot)
	if checklist == nil {
		return nil
	}
	coverage := checklist.Coverage
	view := &ChecklistView{Rooms: make([]ChecklistRoomView, 0, len(checklist.Rooms)), Coverage: ChecklistCoverageView{Expected: coverage.Expected, FullyAssessed: coverage.FullyAssessed, PartiallyAssessed: coverage.PartiallyAssessed, NotObserved: coverage.NotObserved, NotApplicable: coverage.NotApplicable, Impossible: coverage.Impossible, UnknownScope: coverage.UnknownScope}, ClassificationLabel: reportcore.ClassificationLabelForSnapshot(snapshot), Limited: reportcore.ClassificationLimitedForSnapshot(snapshot)}
	if snapshot.Processing != nil {
		view.Processing = reportcore.PresentationLabel(snapshot.Processing.Status)
	}
	for _, room := range checklist.Rooms {
		out := ChecklistRoomView{Name: room.Name, Components: make([]ChecklistComponentView, 0, len(room.Components))}
		typeCodes := make(map[string]string, len(room.Components))
		for _, sourceRoom := range snapshot.Checklist.Rooms {
			if sourceRoom.Key != room.Key {
				continue
			}
			for _, component := range sourceRoom.Components {
				if !component.OutsideScope {
					typeCodes[component.Key] = component.TypeCode
				}
			}
			break
		}
		for _, component := range room.Components {
			out.Components = append(out.Components, ChecklistComponentView{Label: component.Label, DisplayGroup: reportcore.ChecklistGroupLabel(typeCodes[component.Key]), Status: reportcore.PresentationLabel(component.Status), Description: component.Description, Condition: reportcore.PresentationLabel(component.Condition), ReviewLabel: reportcore.PresentationLabel(component.ReviewStatus), EvidenceIDs: reportcore.EvidenceReferences(snapshot, component.EvidenceIDs), Limitations: component.Limitations})
		}
		view.Rooms = append(view.Rooms, out)
	}
	for _, entry := range handover.Entries {
		projected := projectHandover(entry)
		projected.Status = reportcore.PresentationLabel(projected.Status)
		projected.EvidenceIDs = reportcore.EvidenceReferences(snapshot, projected.EvidenceIDs)
		view.Handover = append(view.Handover, projected)
	}
	return view
}

func projectHandover(entry reportcore.HandoverEntry) HandoverView {
	presentation := reportcore.PresentHandover(entry)
	return HandoverView{Label: presentation.Label, Kind: entry.Kind, Status: entry.Status, Type: presentation.Type, Value: presentation.Value, Unit: presentation.Unit, Date: presentation.Date, Declarant: presentation.Declarant, EvidenceIDs: append([]string(nil), entry.EvidenceIDs...)}
}
