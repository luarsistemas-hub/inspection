package core

import (
	reportcore "inspection/services/inspection/internal/features/reports/core"
)

// ComparisonView is the customer-safe, presented comparison of one issued
// report version: baseline as issued, outcome counts over the full baseline
// denominator, the displayed items and their paired evidence references.
type ComparisonView struct {
	BaselineVersionNumber              int
	Purpose, PurposeLabel, ActivatedAt string
	Status, StatusLabel                string
	Counts                             reportcore.ComparisonCounts
	Displayed                          int
	Items                              []ComparisonItemView
	OutsideBaseline                    []ComparisonObservationView
	Disclaimer                         string
	BaselineLine, CountsLine           string
}

// ComparisonItemView is one baseline item for a reader: no item identity,
// type code, model text or staff backstage.
type ComparisonItemView struct {
	Room, Name, Outcome, OutcomeLabel   string
	BaselineCondition, CurrentCondition string
	Limitations                         []string
	Pairs                               []ComparisonPairView
}

// ComparisonPairView references both sides of a pair by report-local evidence
// numbers and states whether each image can be shown.
type ComparisonPairView struct {
	BaselineEvidence, CurrentEvidence         string
	BaselineAvailability, CurrentAvailability string
}

// ComparisonObservationView is an informational object seen outside the baseline.
type ComparisonObservationView struct {
	Room, Name string
}

// FilterComparison applies the customer allowlist to a v3 comparison while
// keeping its stable codes, counts and evidence IDs: the baseline loses its
// internal ID, items lose identity, type code, model text and staff markers
// (the room key, which the pair validation needs, stays and is never
// presented), and informational observations keep only room and name. Presentation is
// applied once, by ProjectComparison or the PDF renderer.
func FilterComparison(snapshot reportcore.Snapshot) *reportcore.ComparisonSection {
	if snapshot.SchemaVersion < 3 || snapshot.Comparison == nil {
		return nil
	}
	source := snapshot.Comparison
	out := &reportcore.ComparisonSection{Baseline: reportcore.ComparisonBaseline{VersionNumber: source.Baseline.VersionNumber, Purpose: source.Baseline.Purpose, ActivatedAt: source.Baseline.ActivatedAt}, Status: source.Status, Counts: source.Counts, Items: make([]reportcore.ComparisonItem, 0, len(source.Items)), OutsideBaseline: make([]reportcore.ComparisonObservation, 0, len(source.OutsideBaseline))}
	for _, item := range source.Items {
		out.Items = append(out.Items, reportcore.ComparisonItem{Room: item.Room, RoomKey: item.RoomKey, Name: item.Name, Outcome: item.Outcome, BaselineCondition: item.BaselineCondition, CurrentCondition: item.CurrentCondition, Limitations: customerLimitations(item.Limitations), Pairs: append([]reportcore.ComparisonPair(nil), item.Pairs...)})
	}
	for _, observation := range source.OutsideBaseline {
		out.OutsideBaseline = append(out.OutsideBaseline, reportcore.ComparisonObservation{Room: observation.Room, Name: observation.Name})
	}
	return out
}

// customerLimitations replaces limitations that describe the internal pairing
// work (a pair of another room rejected, an incoherent analysis answer) with
// the neutral reader text of the same gap; the others are kept as issued.
func customerLimitations(limitations []string) []string {
	if limitations == nil {
		return nil
	}
	out := make([]string, 0, len(limitations))
	for _, limitation := range limitations {
		switch limitation {
		case reportcore.LimitationPairRejected:
			limitation = reportcore.LimitationNoCurrentView
		case reportcore.LimitationInconsistentClaim:
			limitation = reportcore.LimitationComparisonNoResult
		}
		if !containsText(out, limitation) {
			out = append(out, limitation)
		}
	}
	return out
}

func containsText(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

// ProjectComparison presents the customer comparison of an authorized report
// version. An optional outcome filter selects the displayed items; the counts
// always keep the whole baseline denominator. An unknown filter is rejected
// with reportcore.ErrInvalidComparisonFilter and returns no data.
func ProjectComparison(snapshot reportcore.Snapshot, outcome string) (*ComparisonView, error) {
	section := FilterComparison(snapshot)
	if section == nil {
		if outcome != "" && !reportcore.ValidComparisonOutcome(outcome) {
			return nil, reportcore.ErrInvalidComparisonFilter
		}
		return nil, nil
	}
	filtered, err := reportcore.FilterComparison(*section, outcome)
	if err != nil {
		return nil, err
	}
	evidence := make(map[string]reportcore.Evidence, len(snapshot.Evidence))
	for _, item := range snapshot.Evidence {
		evidence[item.ID] = item
	}
	view := &ComparisonView{BaselineVersionNumber: section.Baseline.VersionNumber, Purpose: reportcore.CodeLabel(section.Baseline.Purpose), PurposeLabel: reportcore.CodeLabel(section.Baseline.Purpose), ActivatedAt: section.Baseline.ActivatedAt, Status: section.Status, StatusLabel: reportcore.ComparisonStatusLabel(section.Status), Counts: section.Counts, Displayed: len(filtered.Items), Items: make([]ComparisonItemView, 0, len(filtered.Items)), OutsideBaseline: make([]ComparisonObservationView, 0, len(section.OutsideBaseline)), Disclaimer: reportcore.ComparisonDisclaimer, BaselineLine: reportcore.ComparisonBaselineLine(section.Baseline), CountsLine: reportcore.ComparisonCountsLine(section.Counts)}
	for _, item := range filtered.Items {
		baselineCondition := reportcore.PresentationLabel(item.BaselineCondition)
		if item.Outcome == reportcore.ComparisonNoBasis {
			baselineCondition = ""
		}
		projected := ComparisonItemView{Room: item.Room, Name: item.Name, Outcome: item.Outcome, OutcomeLabel: reportcore.ComparisonOutcomeLabel(item.Outcome), BaselineCondition: baselineCondition, CurrentCondition: reportcore.PresentationLabel(item.CurrentCondition), Limitations: append([]string{}, item.Limitations...), Pairs: make([]ComparisonPairView, 0, len(item.Pairs))}
		for _, pair := range item.Pairs {
			projected.Pairs = append(projected.Pairs, ComparisonPairView{BaselineEvidence: reportcore.EvidenceReference(snapshot, pair.BaselineEvidenceID), CurrentEvidence: reportcore.EvidenceReference(snapshot, pair.CurrentEvidenceID), BaselineAvailability: PairAvailability(evidence[pair.BaselineEvidenceID]), CurrentAvailability: PairAvailability(evidence[pair.CurrentEvidenceID])})
		}
		view.Items = append(view.Items, projected)
	}
	for _, observation := range section.OutsideBaseline {
		view.OutsideBaseline = append(view.OutsideBaseline, ComparisonObservationView{Room: observation.Room, Name: observation.Name})
	}
	return view, nil
}

// PairAvailability tells whether a paired image can be shown to the customer.
// An image that is missing, expired or blocked (by the snapshot or by the
// screening triage applied to the customer copy) is labeled unavailable; the
// issued conclusion does not change. It is the single rule of the customer
// GraphQL view and the customer PDF.
func PairAvailability(evidence reportcore.Evidence) string {
	if EvidenceBlocked(evidence) {
		return "UNAVAILABLE"
	}
	if source := evidence.SourceAvailability; source != "" && source != "AVAILABLE" {
		return "UNAVAILABLE"
	}
	if evidence.Availability == "" || evidence.Availability == "AVAILABLE" {
		return "AVAILABLE"
	}
	return "UNAVAILABLE"
}

// PairAvailabilityNote is the customer PDF wording of PairAvailability for one
// pair: empty when both images can be shown.
func PairAvailabilityNote(snapshot reportcore.Snapshot, pair reportcore.ComparisonPair) string {
	evidence := make(map[string]reportcore.Evidence, len(snapshot.Evidence))
	for _, item := range snapshot.Evidence {
		if _, seen := evidence[item.ID]; !seen {
			evidence[item.ID] = item
		}
	}
	baseline := PairAvailability(evidence[pair.BaselineEvidenceID]) == "UNAVAILABLE"
	current := PairAvailability(evidence[pair.CurrentEvidenceID]) == "UNAVAILABLE"
	switch {
	case baseline && current:
		return "Imagens indisponíveis"
	case baseline:
		return "Imagem da referência indisponível"
	case current:
		return "Imagem atual indisponível"
	}
	return ""
}
