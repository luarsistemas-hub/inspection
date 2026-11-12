package core

import (
	"errors"
	"fmt"
	"html/template"
	"regexp"
	"strings"
	"time"
)

// Comparison outcome codes of a report v3 comparison. Each baseline item has
// exactly one outcome; the historical change is kept apart from the item's
// current condition.
const (
	ComparisonUnchanged   = "UNCHANGED"
	ComparisonChanged     = "CHANGED"
	ComparisonImproved    = "IMPROVED"
	ComparisonMissing     = "MISSING"
	ComparisonNotObserved = "NOT_OBSERVED"
	ComparisonNoBasis     = "NO_BASIS"
)

// InventoryNoBasis is the inventory entry status of a baseline item without a
// usable baseline view (report v3); it is listed apart from the problems.
const InventoryNoBasis = "NO_BASIS"

// ComparisonOutcomes lists the outcomes in their stable presentation and
// filter order.
var ComparisonOutcomes = []string{ComparisonChanged, ComparisonMissing, ComparisonImproved, ComparisonNotObserved, ComparisonNoBasis, ComparisonUnchanged}

// Neutral limitation texts of comparison items. They describe the evidence,
// never a responsibility, wear or cost.
const (
	LimitationNoBaselineView = "A referência aprovada não mostra o item o suficiente; não há base para comparar."
	// LimitationNoBaselineCondition explains an item the baseline shows
	// without an assessable condition.
	LimitationNoBaselineCondition = "A referência aprovada mostra o item, mas sem condição avaliável; não há base para comparar a condição."
	LimitationNoCurrentView       = "Não foi encontrada uma vista atual equivalente à da referência."
	LimitationPairRejected        = "Um par de evidências de outro ambiente foi rejeitado; o item não foi comparado."
	LimitationInconsistentClaim   = "A conclusão da análise não era coerente com as condições observadas; o item não foi comparado."
	LimitationRoomCoverage        = "A ausência não foi confirmada: o ambiente não foi registrado por inteiro e o item pode ter mudado de lugar."
	// LimitationRoomCoverageUndefinedScope explains a movable item reported
	// absent in an inspection whose scope defines no rooms (template v1/v2):
	// whole-room coverage cannot be established, so absence is not proven.
	LimitationRoomCoverageUndefinedScope = "A ausência não foi confirmada: escopo não definido nesta vistoria, então não há como confirmar que o ambiente foi registrado por inteiro e o item pode ter mudado de lugar."
	LimitationEnvironmentMissing         = "O ambiente não foi capturado nesta vistoria."
	// LimitationEnvironmentUnmatched explains the items of a baseline room no
	// room of this inspection's scope corresponds to (none, or more than one).
	LimitationEnvironmentUnmatched = "Ambiente não correspondido à referência: nenhum ambiente desta vistoria corresponde, de forma única, a este ambiente da referência; o item não foi comparado."
	LimitationComparisonFailed     = "Falha na comparação; o item não foi avaliado."
	LimitationObstructed           = "Visão obstruída na vistoria atual."
	LimitationFraming              = "Enquadramento insuficiente na vistoria atual."
	LimitationQuality              = "Qualidade da evidência atual insuficiente."
	LimitationUnsupportedOutcome   = "A conclusão não tinha evidência pareada da referência e da vistoria atual."
	LimitationComparisonNoResult   = "A comparação não registrou resultado para este item."
)

// ErrInvalidComparisonFilter rejects an unknown comparison outcome filter.
var ErrInvalidComparisonFilter = errors.New("invalid comparison outcome filter")

// ComparisonSection is the complete, evidence-backed comparison of a v3 report
// against the approved baseline pinned by the inspection. Every included
// baseline item appears once, whatever its outcome.
type ComparisonSection struct {
	Baseline        ComparisonBaseline      `json:"baseline"`
	Status          string                  `json:"status"`
	Counts          ComparisonCounts        `json:"counts"`
	Items           []ComparisonItem        `json:"items"`
	OutsideBaseline []ComparisonObservation `json:"outsideBaseline"`
}

// ComparisonBaseline identifies the approved baseline version as issued.
type ComparisonBaseline struct {
	VersionID     string `json:"versionId,omitempty"`
	VersionNumber int    `json:"versionNumber"`
	Purpose       string `json:"purpose"`
	ActivatedAt   string `json:"activatedAt"`
}

// ComparisonCounts are the stable denominators of the comparison filters.
type ComparisonCounts struct {
	Total       int `json:"total"`
	Unchanged   int `json:"unchanged"`
	Changed     int `json:"changed"`
	Improved    int `json:"improved"`
	Missing     int `json:"missing"`
	NotObserved int `json:"notObserved"`
	NoBasis     int `json:"noBasis"`
}

// ComparisonItem is one baseline item: its historical outcome, its baseline
// and current conditions as separate facts, and the evidence pairs that
// support the outcome.
type ComparisonItem struct {
	ItemIdentityID string `json:"itemIdentityId,omitempty"`
	Room           string `json:"room"`
	// RoomKey is the capture requirement key of the item's baseline room in
	// this inspection: every current evidence paired with the item belongs to
	// it. It is never presented.
	RoomKey           string           `json:"roomKey,omitempty"`
	TypeCode          string           `json:"typeCode,omitempty"`
	Name              string           `json:"name"`
	Outcome           string           `json:"outcome"`
	BaselineCondition string           `json:"baselineCondition,omitempty"`
	CurrentCondition  string           `json:"currentCondition,omitempty"`
	Description       string           `json:"description,omitempty"`
	Limitations       []string         `json:"limitations,omitempty"`
	Pairs             []ComparisonPair `json:"pairs,omitempty"`
	PairingRevised    bool             `json:"pairingRevised,omitempty"`
}

// ComparisonPair links one baseline evidence to the current evidence of the
// same item and room.
type ComparisonPair struct {
	BaselineEvidenceID string `json:"baselineEvidenceId"`
	CurrentEvidenceID  string `json:"currentEvidenceId"`
}

// ComparisonObservation is a current object outside the baseline. It is
// informational only: it is not counted as a baseline item nor called new.
type ComparisonObservation struct {
	Room        string `json:"room"`
	TypeCode    string `json:"typeCode,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// blameTerms are statements a comparison must never make, in Portuguese or
// English: responsibility or blame, who caused or must pay, misuse, wear or
// deterioration, repair, price or cost (PRD F6). They are matched against the
// text folded to lower case without accents, at word starts, so inflections
// ("responsável", "cobrado", "custará") are caught too.
var blameTerms = regexp.MustCompile(`\b(responsab\w*|responsav\w*|culpa\w*|cobra\w*|cobranc\w*|consert\w*|repar\w*|preco\w*|valor\w*|reais|r\$|custo\w*|custa\w*|custou|pag(ar|o|a|os|as|ou|ara|arao|amento|uem)\b|mau uso|mal uso|uso indevido|uso inadequado|desgast\w*|deterior\w*|orcament\w*|indeniz\w*|ressarc\w*|multa\w*|reembols\w*|locatari\w*|inquilin\w*|ocupante\w*|morador\w*|prejuizo\w*|causad\w*|responsib\w*|liab\w*|fault\w*|blame\w*|cost\w*|charg\w*|pric\w*|pay\w*|paid\b|misuse\w*|wear\w*|tear\b|tenant\w*|occupant\w*|repair\w*|caused\b)`)

// accentFolding maps the Portuguese accented letters to their base letter.
var accentFolding = strings.NewReplacer("á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a", "é", "e", "è", "e", "ê", "e", "ë", "e", "í", "i", "ì", "i", "î", "i", "ï", "i", "ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o", "ú", "u", "ù", "u", "û", "u", "ü", "u", "ç", "c", "ñ", "n")

// ComparisonTextAllowed reports whether a comparison text is neutral: it
// asserts no responsibility, misuse, wear, repair, price or cost. Report v3
// comparisons hold only controlled texts; this is the validation backstop.
func ComparisonTextAllowed(text string) bool {
	return !blameTerms.MatchString(accentFolding.Replace(strings.ToLower(text)))
}

// ComparisonOutcomeLabel is the single Portuguese vocabulary of comparison
// outcomes for every surface (screen, HTML and PDF).
func ComparisonOutcomeLabel(outcome string) string {
	switch outcome {
	case ComparisonUnchanged:
		return "Sem alteração"
	case ComparisonChanged:
		return "Alterado"
	case ComparisonImproved:
		return "Melhorou"
	case ComparisonMissing:
		return "Ausência comprovada"
	case ComparisonNotObserved:
		return "Não observado"
	case ComparisonNoBasis:
		return "Sem base para comparar"
	default:
		return unrecognizedLabel
	}
}

// inventoryOutcomes maps the per-item statuses of a report v3 inventory
// section to the comparison outcome they state.
var inventoryOutcomes = map[string]string{
	"PRESENT_UNCHANGED": ComparisonUnchanged,
	"CONDITION_CHANGED": ComparisonChanged,
	"IMPROVED":          ComparisonImproved,
	"MISSING":           ComparisonMissing,
	"NOT_OBSERVED":      ComparisonNotObserved,
	InventoryNoBasis:    ComparisonNoBasis,
}

// InventoryStatusLabel presents an inventory entry status of a report v3 with
// the comparison vocabulary (ComparisonOutcomeLabel), so the inventory
// section, the comparison, HTML, PDF, GraphQL and the Dashboard name each
// outcome the same way. Room-level statuses keep their own label. Reports
// v1/v2 keep their historical labels and never use it.
func InventoryStatusLabel(status string) string {
	if outcome, ok := inventoryOutcomes[status]; ok {
		return ComparisonOutcomeLabel(outcome)
	}
	return CodeLabel(status)
}

// ComparisonStatusLabel presents the processing state of the comparison.
func ComparisonStatusLabel(status string) string {
	switch status {
	case "COMPLETED":
		return "Comparação concluída"
	case "FAILED":
		return "Falha na comparação"
	case "INCONCLUSIVE":
		return "Comparação inconclusiva"
	default:
		return unrecognizedLabel
	}
}

// CountComparison derives the filter counts from the items, so every count
// is exactly the number of items carrying that outcome.
func CountComparison(items []ComparisonItem) ComparisonCounts {
	counts := ComparisonCounts{Total: len(items)}
	for _, item := range items {
		switch item.Outcome {
		case ComparisonUnchanged:
			counts.Unchanged++
		case ComparisonChanged:
			counts.Changed++
		case ComparisonImproved:
			counts.Improved++
		case ComparisonMissing:
			counts.Missing++
		case ComparisonNotObserved:
			counts.NotObserved++
		case ComparisonNoBasis:
			counts.NoBasis++
		}
	}
	return counts
}

// ComparisonCount returns the count of one outcome.
func (c ComparisonCounts) ComparisonCount(outcome string) int {
	switch outcome {
	case ComparisonUnchanged:
		return c.Unchanged
	case ComparisonChanged:
		return c.Changed
	case ComparisonImproved:
		return c.Improved
	case ComparisonMissing:
		return c.Missing
	case ComparisonNotObserved:
		return c.NotObserved
	case ComparisonNoBasis:
		return c.NoBasis
	default:
		return 0
	}
}

// ValidComparisonOutcome reports whether outcome is a known comparison outcome.
func ValidComparisonOutcome(outcome string) bool {
	for _, known := range ComparisonOutcomes {
		if known == outcome {
			return true
		}
	}
	return false
}

// FilterComparison returns a copy of the section whose items have the given
// outcome. The counts keep the total denominator of the whole comparison; an
// empty outcome returns every item. An unknown outcome is rejected.
func FilterComparison(section ComparisonSection, outcome string) (ComparisonSection, error) {
	if outcome == "" {
		return section, nil
	}
	if !ValidComparisonOutcome(outcome) {
		return ComparisonSection{}, ErrInvalidComparisonFilter
	}
	filtered := section
	filtered.Items = make([]ComparisonItem, 0, section.Counts.ComparisonCount(outcome))
	for _, item := range section.Items {
		if item.Outcome == outcome {
			filtered.Items = append(filtered.Items, item)
		}
	}
	return filtered, nil
}

// ComparisonLimited reports whether a comparison leaves items unresolved or
// failed, so a report cannot read as "everything unchanged".
func ComparisonLimited(section *ComparisonSection) bool {
	if section == nil {
		return false
	}
	return section.Status != "COMPLETED" || section.Counts.NotObserved > 0 || section.Counts.NoBasis > 0
}

// outcomeNeedsPair lists the outcomes that state a visual fact and therefore
// need baseline and current evidence of the same item.
func outcomeNeedsPair(outcome string) bool {
	return outcome == ComparisonUnchanged || outcome == ComparisonChanged || outcome == ComparisonImproved || outcome == ComparisonMissing
}

func validComparisonCondition(value string) bool {
	switch value {
	case "", "NEW", "GOOD", "FAIR", "POOR", "NOT_ASSESSABLE":
		return true
	}
	return false
}

func validateComparison(snapshot Snapshot, customer bool) error {
	section := snapshot.Comparison
	if section == nil {
		return nil
	}
	if snapshot.SchemaVersion < 3 {
		return fmt.Errorf("report comparison requires schema v3")
	}
	if section.Status != "COMPLETED" && section.Status != "FAILED" && section.Status != "INCONCLUSIVE" {
		return fmt.Errorf("report comparison status is invalid")
	}
	baseline := section.Baseline
	if baseline.VersionNumber < 1 || strings.TrimSpace(baseline.Purpose) == "" || strings.TrimSpace(baseline.ActivatedAt) == "" || (!customer && strings.TrimSpace(baseline.VersionID) == "") {
		return fmt.Errorf("report comparison baseline is invalid")
	}
	if customer && (baseline.VersionID != "") {
		return fmt.Errorf("customer report contains internal fields")
	}
	// A pair links a baseline (REFERENCE) evidence of the item to a distinct
	// CURRENT evidence captured in the item's own room. The room is the item's
	// (RoomKey): a baseline photo keeps the key it was first listed with (an
	// origin key, for instance), so only the current side is bound to a room.
	known := make(map[string]Evidence, len(snapshot.Evidence))
	for _, evidence := range snapshot.Evidence {
		if _, seen := known[evidence.ID]; !seen {
			known[evidence.ID] = evidence
		}
	}
	for _, item := range section.Items {
		if strings.TrimSpace(item.Room) == "" || strings.TrimSpace(item.Name) == "" || !ValidComparisonOutcome(item.Outcome) {
			return fmt.Errorf("report comparison item is invalid")
		}
		if !validComparisonCondition(item.BaselineCondition) || !validComparisonCondition(item.CurrentCondition) {
			return fmt.Errorf("report comparison condition is invalid")
		}
		if outcomeNeedsPair(item.Outcome) && len(item.Pairs) == 0 {
			return fmt.Errorf("report comparison outcome lacks paired evidence")
		}
		if (item.Outcome == ComparisonNotObserved || item.Outcome == ComparisonNoBasis) && len(item.Limitations) == 0 {
			return fmt.Errorf("report comparison gap lacks its limitation")
		}
		room := item.RoomKey
		for _, pair := range item.Pairs {
			baselineEvidence, hasBaseline := known[pair.BaselineEvidenceID]
			currentEvidence, hasCurrent := known[pair.CurrentEvidenceID]
			if !hasBaseline || !hasCurrent {
				return fmt.Errorf("report comparison pair references unknown evidence")
			}
			if pair.BaselineEvidenceID == pair.CurrentEvidenceID || baselineEvidence.Role != "REFERENCE" || currentEvidence.Role != "CURRENT" {
				return fmt.Errorf("report comparison pair does not link baseline and current evidence")
			}
			if item.RoomKey == "" {
				// A snapshot issued before items carried their room key keeps
				// the rule it was issued with: both sides share one room key.
				if baselineEvidence.RequirementKey == "" || baselineEvidence.RequirementKey != currentEvidence.RequirementKey || (room != "" && room != baselineEvidence.RequirementKey) {
					return fmt.Errorf("report comparison pair crosses rooms")
				}
				room = baselineEvidence.RequirementKey
				continue
			}
			if currentEvidence.RequirementKey != item.RoomKey {
				return fmt.Errorf("report comparison pair crosses rooms")
			}
		}
		for _, text := range append([]string{item.Room, item.Name, item.Description}, item.Limitations...) {
			if containsMutableURL(text) {
				return fmt.Errorf("report comparison cannot contain mutable media URLs")
			}
		}
		for _, text := range append([]string{item.Description}, item.Limitations...) {
			if !ComparisonTextAllowed(text) {
				return fmt.Errorf("report comparison states responsibility, wear or cost")
			}
		}
		if customer && (item.ItemIdentityID != "" || item.TypeCode != "" || item.Description != "" || item.PairingRevised) {
			return fmt.Errorf("customer report contains internal fields")
		}
	}
	for _, observation := range section.OutsideBaseline {
		if strings.TrimSpace(observation.Room) == "" || strings.TrimSpace(observation.Name) == "" {
			return fmt.Errorf("report comparison observation is invalid")
		}
		if containsMutableURL(observation.Name) || containsMutableURL(observation.Description) || !ComparisonTextAllowed(observation.Description) {
			return fmt.Errorf("report comparison observation is invalid")
		}
		if customer && (observation.TypeCode != "" || observation.Description != "") {
			return fmt.Errorf("customer report contains internal fields")
		}
	}
	if section.Counts != CountComparison(section.Items) {
		return fmt.Errorf("report comparison counts differ from its items")
	}
	return nil
}

// sanitizeComparison removes mutable URLs from comparison free text.
func sanitizeComparison(section *ComparisonSection, sanitize func(string) string) *ComparisonSection {
	if section == nil {
		return nil
	}
	out := *section
	out.Items = append([]ComparisonItem(nil), section.Items...)
	if section.Items != nil && out.Items == nil {
		out.Items = []ComparisonItem{}
	}
	for i := range out.Items {
		item := &out.Items[i]
		item.Room, item.Name, item.Description = sanitize(item.Room), sanitize(item.Name), sanitize(item.Description)
		item.Limitations = sanitizeStrings(item.Limitations, sanitize)
		item.Pairs = append([]ComparisonPair(nil), item.Pairs...)
	}
	out.OutsideBaseline = append([]ComparisonObservation(nil), section.OutsideBaseline...)
	if section.OutsideBaseline != nil && out.OutsideBaseline == nil {
		out.OutsideBaseline = []ComparisonObservation{}
	}
	for i := range out.OutsideBaseline {
		out.OutsideBaseline[i].Room, out.OutsideBaseline[i].Name, out.OutsideBaseline[i].Description = sanitize(out.OutsideBaseline[i].Room), sanitize(out.OutsideBaseline[i].Name), sanitize(out.OutsideBaseline[i].Description)
	}
	return &out
}

// ComparisonGroup is the presentation of one outcome: its label, its count
// over the total denominator and its items.
type ComparisonGroup struct {
	Outcome, Label string
	Count, Total   int
	Items          []ComparisonItem
}

// GroupComparison groups items by outcome in the stable presentation order.
// Every group carries the same total denominator, so screen, HTML and PDF show
// "N de M" consistently.
func GroupComparison(section ComparisonSection) []ComparisonGroup {
	groups := make([]ComparisonGroup, 0, len(ComparisonOutcomes))
	for _, outcome := range ComparisonOutcomes {
		group := ComparisonGroup{Outcome: outcome, Label: ComparisonOutcomeLabel(outcome), Count: section.Counts.ComparisonCount(outcome), Total: section.Counts.Total}
		for _, item := range section.Items {
			if item.Outcome == outcome {
				group.Items = append(group.Items, item)
			}
		}
		groups = append(groups, group)
	}
	return groups
}

// ComparisonCountsLine summarizes the counts of a comparison in one sentence.
func ComparisonCountsLine(counts ComparisonCounts) string {
	return fmt.Sprintf("%d itens da referência · %d sem alteração · %d alterados · %d melhoraram · %d com ausência comprovada · %d não observados · %d sem base para comparar", counts.Total, counts.Unchanged, counts.Changed, counts.Improved, counts.Missing, counts.NotObserved, counts.NoBasis)
}

// ComparisonBaselineLine presents the baseline version as issued.
func ComparisonBaselineLine(baseline ComparisonBaseline) string {
	line := fmt.Sprintf("Referência aprovada: versão %d", baseline.VersionNumber)
	if purpose := CodeLabel(baseline.Purpose); purpose != "" && purpose != unrecognizedLabel {
		line += " · " + purpose
	}
	if date := comparisonDate(baseline.ActivatedAt); date != "" {
		line += " · aprovada em " + date
	}
	return line
}

// ComparisonItemLine presents the conditions of one item, keeping the current
// condition apart from the historical outcome.
func ComparisonItemLine(item ComparisonItem) string {
	parts := []string{item.Room + " · " + item.Name, ComparisonOutcomeLabel(item.Outcome)}
	// "No basis to compare" never states a baseline condition.
	if item.BaselineCondition != "" && item.Outcome != ComparisonNoBasis {
		parts = append(parts, "Condição na referência: "+PresentationLabel(item.BaselineCondition))
	}
	if item.CurrentCondition != "" {
		parts = append(parts, "Condição atual: "+PresentationLabel(item.CurrentCondition))
	}
	return strings.Join(parts, " · ")
}

// ComparisonPairLine presents one evidence pair with report-local references.
func ComparisonPairLine(snapshot Snapshot, pair ComparisonPair) string {
	return "Referência " + EvidenceReference(snapshot, pair.BaselineEvidenceID) + "; vistoria atual " + EvidenceReference(snapshot, pair.CurrentEvidenceID)
}

// ComparisonDisclaimer is shown with every comparison.
const ComparisonDisclaimer = "Esta comparação descreve diferenças visuais entre as evidências e não atribui responsabilidade, desgaste natural ou custo de reparo."

func comparisonDate(value string) string {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return ""
	}
	return parsed.UTC().Format("02/01/2006")
}

func comparisonHTMLTemplate(snapshot Snapshot) (*template.Template, error) {
	funcs := template.FuncMap{
		"baselineLine": ComparisonBaselineLine,
		"statusLabel":  ComparisonStatusLabel,
		"countsLine":   ComparisonCountsLine,
		"itemLine":     ComparisonItemLine,
		"pairLine":     func(pair ComparisonPair) string { return ComparisonPairLine(snapshot, pair) },
		"groups":       GroupComparison,
	}
	const source = `<section id="comparison"><h2>Comparação com a referência</h2><p>{{baselineLine .Baseline}}</p><p>Estado: {{statusLabel .Status}}</p><p>{{countsLine .Counts}}</p>{{range groups .}}{{if .Count}}<h3>{{.Label}} · {{.Count}} de {{.Total}} itens</h3>{{range .Items}}<p data-outcome="{{.Outcome}}"><strong>{{itemLine .}}</strong>{{if .Description}}<br>{{.Description}}{{end}}{{range .Pairs}}<br>{{pairLine .}}{{end}}{{range .Limitations}}<br>Limitação: {{.}}{{end}}</p>{{end}}{{end}}{{end}}{{if .OutsideBaseline}}<h3>Observado fora da referência (informativo)</h3>{{range .OutsideBaseline}}<p>{{.Room}} · {{.Name}}</p>{{end}}{{end}}<p>` + ComparisonDisclaimer + `</p></section>`
	return template.New("comparison").Funcs(funcs).Parse(source)
}

// EvidenceIndexLabel is the text of one evidence in the report v3 evidence
// index: its description, else a neutral label naming its role ("Referência"
// or "Vistoria atual") and, when the snapshot knows it, its room. A capture
// requirement key ("env:…", "origin:…") is internal and never presented.
func EvidenceIndexLabel(snapshot Snapshot, evidence Evidence) string {
	if description := strings.TrimSpace(evidence.Description); description != "" {
		return description
	}
	label := "Vistoria atual"
	if evidence.Role == "REFERENCE" {
		label = "Referência"
	}
	if room := evidenceRoomName(snapshot, evidence); room != "" {
		label += " · " + room
	}
	return label
}

// evidenceRoomName is the presentable room of one evidence: the room of the
// comparison item or inventory entry that cites it, else the label of its
// capture requirement. It is empty when the snapshot does not name it.
func evidenceRoomName(snapshot Snapshot, evidence Evidence) string {
	if comparison := snapshot.Comparison; comparison != nil {
		for _, item := range comparison.Items {
			for _, pair := range item.Pairs {
				if (pair.BaselineEvidenceID == evidence.ID || pair.CurrentEvidenceID == evidence.ID) && strings.TrimSpace(item.Room) != "" {
					return strings.TrimSpace(item.Room)
				}
			}
		}
	}
	if inventory := snapshot.Inventory; inventory != nil {
		for _, entry := range append(append([]InventoryEntry{}, inventory.Problems...), inventory.Informational...) {
			for _, pair := range entry.Pairs {
				if (pair.Origin == evidence.ID || pair.Current == evidence.ID) && strings.TrimSpace(entry.Environment) != "" {
					return strings.TrimSpace(entry.Environment)
				}
			}
		}
	}
	for _, requirement := range snapshot.Requirements {
		if requirement.Key == evidence.RequirementKey && strings.TrimSpace(requirement.Label) != "" {
			return strings.TrimSpace(requirement.Label)
		}
	}
	if checklist := snapshot.Checklist; checklist != nil {
		for _, room := range checklist.Rooms {
			if "env:"+room.Key == evidence.RequirementKey && strings.TrimSpace(room.Name) != "" {
				return strings.TrimSpace(room.Name)
			}
		}
	}
	return ""
}
