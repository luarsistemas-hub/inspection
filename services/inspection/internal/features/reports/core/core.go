// Package core owns immutable, internal advisory report snapshot construction.
package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ErrInvalidSnapshot marks report validation failures that cannot succeed on retry.
var ErrInvalidSnapshot = fmt.Errorf("invalid report snapshot")

// mutableURL matches a URL scheme with whatever follows up to a delimiter,
// including a bare "https://": the validation rejects every scheme occurrence,
// so the sanitizer must remove every one too.
var mutableURL = regexp.MustCompile(`(?i)https?://[^\s\"'<>]*`)

var mutableURLBytes = regexp.MustCompile(`(?i)https?:(?://|\\/\\/)[^\s\"'<>]*`)

// PresentationLabel returns the shared Portuguese label for report states,
// review decisions and visual conditions. Unknown codes stay readable.
func PresentationLabel(value string) string {
	if value == "" {
		return ""
	}
	labels := map[string]string{
		"OBSERVED": "Observado", "PARTIAL": "Parcial", "NOT_OBSERVED": "Não observado", "NOT_APPLICABLE": "Não aplicável", "IMPOSSIBLE": "Impossível de capturar", "NOT_REVIEWED": "Não revisado", "ACCEPTED": "Aceito", "EDITED": "Editado", "REJECTED": "Rejeitado", "NOT_AUTHORIZED": "Não autorizada", "PENDING": "Pendente", "COMPLETED": "Concluída", "FAILED": "Falha na análise", "INCONCLUSIVE": "Análise inconclusiva", "DECLARED": "Declarado", "NEW": "Novo", "GOOD": "Bom", "FAIR": "Regular", "POOR": "Ruim", "NOT_ASSESSABLE": "Não avaliável", "PROPERTY_COMPONENTS": "Componentes do imóvel",
	}
	if label, ok := labels[value]; ok {
		return label
	}
	return reportLabel(nil, value)
}

// ScopeProfileLabel returns a customer-safe label, including an explicit
// fallback when the snapshot has no usable scope profile.
func ScopeProfileLabel(snapshot Snapshot) string {
	if snapshot.Scope == nil || snapshot.Scope.Unknown || strings.TrimSpace(snapshot.Scope.Profile) == "" {
		return "Escopo não informado"
	}
	return PresentationLabel(snapshot.Scope.Profile)
}

// ExpectedRoomCount returns the number of environments in the pinned scope.
func ExpectedRoomCount(snapshot Snapshot) (int, bool) {
	if snapshot.Scope == nil || snapshot.Scope.Unknown || len(snapshot.Scope.Document) == 0 {
		return 0, false
	}
	var scope struct {
		Rooms []json.RawMessage `json:"rooms"`
	}
	if json.Unmarshal(snapshot.Scope.Document, &scope) != nil || len(scope.Rooms) == 0 {
		return 0, false
	}
	return len(scope.Rooms), true
}

// ChecklistGroupLabel returns a stable editorial heading for a visual item.
func ChecklistGroupLabel(typeCode string) string {
	switch typeCode {
	case "WALL", "FLOOR", "CEILING", "ROOF":
		return "Superfícies"
	case "DOOR", "WINDOW":
		return "Aberturas"
	case "CABINET", "WARDROBE", "COUNTERTOP":
		return "Marcenaria e bancadas"
	case "SINK", "FAUCET", "TOILET", "SHOWER":
		return "Instalações hidráulicas"
	case "APPLIANCE", "APPLIANCE_OTHER", "LIGHTING", "SOCKET_SWITCH":
		return "Instalações e equipamentos"
	case "FURNITURE", "SOFA", "TABLE", "CHAIR":
		return "Mobiliário"
	default:
		return "Outros componentes"
	}
}

// ChecklistGroupOrder returns the shared presentation order for editorial groups.
func ChecklistGroupOrder(label string) int {
	order := map[string]int{
		"Superfícies": 0, "Aberturas": 1, "Marcenaria e bancadas": 2,
		"Instalações hidráulicas": 3, "Instalações e equipamentos": 4,
		"Mobiliário": 5, "Outros componentes": 6,
	}
	if index, ok := order[label]; ok {
		return index
	}
	return len(order)
}

// EvidenceReference gives one stable, human-readable number to evidence in a snapshot.
func EvidenceReference(snapshot Snapshot, evidenceID string) string {
	for index, evidence := range snapshot.Evidence {
		if evidence.ID == evidenceID {
			return fmt.Sprintf("E%03d", index+1)
		}
	}
	return "Evidência sem índice"
}

// EvidenceReferences converts evidence IDs to their report-local references.
func EvidenceReferences(snapshot Snapshot, ids []string) []string {
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		result = append(result, EvidenceReference(snapshot, id))
	}
	return result
}

// Snapshot is the canonical report representation. JSON and HTML derive from
// exactly this immutable value; PDF rendering may fail without changing it.
type Snapshot struct {
	SchemaVersion      int               `json:"schemaVersion"`
	ReportID           string            `json:"reportId"`
	InspectionID       string            `json:"inspectionId"`
	ProjectID          string            `json:"projectId,omitempty"`
	TemplateVersionID  string            `json:"templateVersionId"`
	ReferenceVersionID string            `json:"referenceVersionId"`
	VideoPlanID        string            `json:"videoPlanId,omitempty"`
	PromptDigest       string            `json:"promptDigest"`
	Mode               string            `json:"mode"`
	Classification     string            `json:"classification"`
	ReasonCodes        []string          `json:"reasonCodes"`
	Timeline           []TimelineEntry   `json:"timeline"`
	Coverage           map[string]string `json:"coverage"`
	Context            Context           `json:"context"`
	Requirements       []Requirement     `json:"requirements,omitempty"`
	Findings           []Finding         `json:"findings,omitempty"`
	FindingCounts      *FindingCounts    `json:"findingCounts,omitempty"`
	Evidence           []Evidence        `json:"evidence,omitempty"`
	Inventory          *InventorySection `json:"inventory,omitempty"`
	Checklist          *ChecklistSection `json:"checklist,omitempty"`
	Processing         *ProcessingState  `json:"processing,omitempty"`
	Scope              *ScopeSnapshot    `json:"scope,omitempty"`
	Handover           *HandoverSection  `json:"handover,omitempty"`
	Provenance         *Provenance       `json:"provenance,omitempty"`
	// Comparison is the complete v3 comparison against the pinned baseline.
	Comparison       *ComparisonSection `json:"comparison,omitempty"`
	VerificationCode string             `json:"verificationCode,omitempty"`
	// ScopeStatements bound video conclusions to the observed scope.
	ScopeStatements []string `json:"scopeStatements,omitempty"`
	Advisory        string   `json:"advisory"`
}

// FindingCounts contains aggregate severities safe to present without the finding details.
type FindingCounts struct {
	Total         int `json:"total"`
	Critical      int `json:"critical"`
	High          int `json:"high"`
	Medium        int `json:"medium"`
	Low           int `json:"low"`
	Informational int `json:"informational"`
}

// CountFindings returns an aggregate suitable for a customer-facing summary.
func CountFindings(findings []Finding) FindingCounts {
	counts := FindingCounts{Total: len(findings)}
	for _, finding := range findings {
		switch finding.Severity {
		case "CRITICAL":
			counts.Critical++
		case "HIGH":
			counts.High++
		case "MEDIUM":
			counts.Medium++
		case "LOW":
			counts.Low++
		default:
			counts.Informational++
		}
	}
	return counts
}

// ChecklistSection contains descriptive room and component facts in report v3.
type ChecklistSection struct {
	Rooms    []ChecklistRoom   `json:"rooms"`
	Coverage ChecklistCoverage `json:"coverage"`
}

type ChecklistRoom struct {
	Key        string               `json:"key"`
	TypeCode   string               `json:"typeCode"`
	Name       string               `json:"name"`
	Components []ChecklistComponent `json:"components"`
}

type ChecklistComponent struct {
	Key              string            `json:"key"`
	TypeCode         string            `json:"typeCode,omitempty"`
	Label            string            `json:"label"`
	Status           string            `json:"status"`
	Description      string            `json:"description,omitempty"`
	Condition        string            `json:"condition,omitempty"`
	ReviewStatus     string            `json:"reviewStatus,omitempty"`
	AssessedScope    string            `json:"assessedScope,omitempty"`
	NotAssessed      []string          `json:"notAssessed,omitempty"`
	EvidenceIDs      []string          `json:"evidenceIds,omitempty"`
	AttributeSources []AttributeSource `json:"attributeSources,omitempty"`
	Limitations      []string          `json:"limitations,omitempty"`
	Conflict         bool              `json:"conflict,omitempty"`
	OutsideScope     bool              `json:"outsideScope,omitempty"`
}

// AttributeSource records provenance categories without embedding transcript text or private note identifiers.
type AttributeSource struct {
	Attribute   string   `json:"attribute"`
	Source      string   `json:"source"`
	EvidenceIDs []string `json:"evidenceIds,omitempty"`
}

type ChecklistCoverage struct {
	Expected          int  `json:"expected"`
	FullyAssessed     int  `json:"fullyAssessed"`
	PartiallyAssessed int  `json:"partiallyAssessed"`
	NotObserved       int  `json:"notObserved"`
	NotApplicable     int  `json:"notApplicable"`
	Impossible        int  `json:"impossible"`
	UnknownScope      bool `json:"unknownScope,omitempty"`
}

type ProcessingState struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type ScopeSnapshot struct {
	Digest   string          `json:"digest,omitempty"`
	Version  int             `json:"version,omitempty"`
	Profile  string          `json:"profile,omitempty"`
	Document json.RawMessage `json:"document,omitempty"`
	Unknown  bool            `json:"unknown,omitempty"`
}

type HandoverSection struct {
	Entries []HandoverEntry `json:"entries"`
}
type HandoverEntry struct {
	Key         string          `json:"key"`
	Kind        string          `json:"kind"`
	Status      string          `json:"status"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	EvidenceIDs []string        `json:"evidenceIds,omitempty"`
	Declarant   string          `json:"declarant,omitempty"`
}

type Provenance struct {
	AnalysisMode   string   `json:"analysisMode,omitempty"`
	PromptRevision string   `json:"promptRevision,omitempty"`
	ModelRevision  string   `json:"modelRevision,omitempty"`
	ScopeDigest    string   `json:"scopeDigest,omitempty"`
	EvidenceIDs    []string `json:"evidenceIds,omitempty"`
}

// InventorySection is the exception-first inventory projection in snapshot v2.
type InventorySection struct {
	Reference          InventoryReference           `json:"reference"`
	Coverage           *InventoryCoverage           `json:"coverage,omitempty"`
	CatalogDiagnostics *InventoryCatalogDiagnostics `json:"catalogDiagnostics,omitempty"`
	Problems           []InventoryEntry             `json:"problems"`
	Informational      []InventoryEntry             `json:"informational"`
}

// InventoryCatalogDiagnostics records bounded model-output and evidence loss.
type InventoryCatalogDiagnostics struct {
	ItemsTruncated  int `json:"itemsTruncated"`
	ImagesTruncated int `json:"imagesTruncated"`
}

type InventoryReference struct {
	VersionID     string `json:"versionId"`
	VersionNumber int    `json:"versionNumber"`
	Purpose       string `json:"purpose"`
	ActivatedAt   string `json:"activatedAt"`
}

type InventoryCoverage struct {
	ExpectedItems int `json:"expectedItems"`
	AssessedItems int `json:"assessedItems"`
	NotObserved   int `json:"notObserved"`
	// NoBasis counts baseline items without a usable baseline view (report v3
	// only); they are never counted as not observed.
	NoBasis                        int      `json:"noBasis,omitempty"`
	EnvironmentsNotCaptured        []string `json:"environmentsNotCaptured"`
	EnvironmentsAddedDuringCapture []string `json:"environmentsAddedDuringCapture"`
}

type InventoryEntry struct {
	ItemIdentityID  string                  `json:"itemIdentityId,omitempty"`
	Environment     string                  `json:"environment"`
	Type            string                  `json:"type"`
	Name            string                  `json:"name"`
	Status          string                  `json:"status"`
	ConditionBefore string                  `json:"conditionBefore,omitempty"`
	ConditionAfter  string                  `json:"conditionAfter,omitempty"`
	Reason          string                  `json:"reason,omitempty"`
	Pairs           []InventoryEvidencePair `json:"pairs,omitempty"`
}

type InventoryEvidencePair struct {
	Origin      string `json:"origin,omitempty"`
	Current     string `json:"current,omitempty"`
	Decision    string `json:"decision"`
	TimestampMS *int64 `json:"timestampMs,omitempty"`
}

// Context is the immutable, reader-friendly identity of the inspection at the
// moment its report is generated. It deliberately contains no contact details
// or precise location coordinates.
type Context struct {
	Asset       AssetContext       `json:"asset"`
	Participant ParticipantContext `json:"participant"`
	Template    TemplateContext    `json:"template"`
	Inspection  InspectionContext  `json:"inspection"`
}

type AssetContext struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ExternalKey string `json:"externalKey"`
	Address     string `json:"address"`
}

type ParticipantContext struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type TemplateContext struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int    `json:"version"`
}

type InspectionContext struct {
	ProjectID   string `json:"projectId,omitempty"`
	StageID     string `json:"stageId,omitempty"`
	StageLabel  string `json:"stageLabel,omitempty"`
	DueAt       string `json:"dueAt,omitempty"`
	SubmittedAt string `json:"submittedAt,omitempty"`
	GeneratedAt string `json:"generatedAt"`
}

// Requirement preserves the labels that make a captured image meaningful to
// a reader even when its source template changes later.
type Requirement struct {
	Key                   string `json:"key"`
	Section               string `json:"section"`
	Label                 string `json:"label"`
	PresentationLocalized bool   `json:"presentationLocalized,omitempty"`
	Instructions          string `json:"instructions,omitempty"`
	Coverage              string `json:"coverage,omitempty"`
	ImpossibilityReason   string `json:"impossibilityReason,omitempty"`
	NoRelevantChange      *bool  `json:"noRelevantChange"`
	AnalysisMode          string `json:"analysisMode"`
	AnalysisStatus        string `json:"analysisStatus"`
}

// Finding is an immutable, neutral observation included in the internal
// report. It deliberately carries no fault, cost, liability, or automatic
// consequence.
type Finding struct {
	ID                string   `json:"id,omitempty"`
	Category          string   `json:"category"`
	Title             string   `json:"title"`
	Description       string   `json:"description"`
	Severity          string   `json:"severity"`
	Confidence        float64  `json:"confidence"`
	EvidenceIDs       []string `json:"evidenceIds"`
	Quality           string   `json:"quality"`
	RecommendedAction string   `json:"recommendedAction"`
}

// Evidence is a report-safe view of current evidence metadata. Object keys,
// signed URLs, and media bytes never enter a snapshot.
type Evidence struct {
	ID                 string   `json:"id"`
	RequirementKey     string   `json:"requirementKey"`
	Role               string   `json:"role"`
	EvidenceKind       string   `json:"evidenceKind,omitempty"`
	FrameID            string   `json:"frameId,omitempty"`
	SourceMediaID      string   `json:"sourceMediaId,omitempty"`
	PreparationID      string   `json:"preparationId,omitempty"`
	PTSMicros          *int64   `json:"ptsMicros,omitempty"`
	TimestampMS        *int64   `json:"timestampMs,omitempty"`
	FrameDigest        string   `json:"frameDigest,omitempty"`
	SourceAvailability string   `json:"sourceAvailability,omitempty"`
	Description        string   `json:"description,omitempty"`
	CaptureSource      string   `json:"captureSource,omitempty"`
	CapturedAt         string   `json:"capturedAt,omitempty"`
	DisplayDigest      string   `json:"displayDigest,omitempty"`
	Availability       string   `json:"availability,omitempty"`
	Flags              []string `json:"flags,omitempty"`
}

// EvidenceAvailable reports whether a snapshot marks media as usable. Empty
// values are retained as available for historical v1/v2 snapshots, which did
// not persist availability; explicit unknown states fail closed.
func EvidenceAvailable(availability string) bool {
	return availability == "" || availability == "AVAILABLE"
}

// SanitizeURLs removes mutable URLs from free-text snapshot fields before the
// report is validated and rendered. User-entered text must not block issuance:
// it removes exactly what the validation rejects (any "http://" or "https://",
// in any letter case, even without a host). The caller's snapshot is never
// modified; maps, slices and sections are copied before they change.
func SanitizeURLs(snapshot Snapshot) Snapshot {
	sanitize := func(value string) string { return mutableURL.ReplaceAllString(value, "[endereço removido]") }
	// sanitizeJSON never runs the expression over serialized JSON: it decodes
	// the document, sanitizes decoded keys and string values (numbers are kept
	// verbatim) and re-encodes only when something changed.
	sanitizeJSON := func(raw json.RawMessage) json.RawMessage {
		if len(raw) == 0 {
			return raw
		}
		value, ok := decodeJSONValue(raw)
		if !ok {
			return mutableURLBytes.ReplaceAll(append(json.RawMessage(nil), raw...), []byte("[endereço removido]"))
		}
		changed := false
		value = sanitizeJSONValue(value, func(text string) string {
			cleaned := sanitize(text)
			if cleaned != text {
				changed = true
			}
			return cleaned
		})
		if !changed {
			return raw
		}
		var encoded bytes.Buffer
		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(false)
		if encoder.Encode(value) != nil {
			return raw
		}
		return bytes.TrimRight(encoded.Bytes(), "\n")
	}
	if snapshot.Coverage != nil {
		coverage := make(map[string]string, len(snapshot.Coverage))
		for key, value := range snapshot.Coverage {
			coverage[key] = sanitize(value)
		}
		snapshot.Coverage = coverage
	}
	if snapshot.Scope != nil {
		scope := *snapshot.Scope
		scope.Document = sanitizeJSON(scope.Document)
		snapshot.Scope = &scope
	}
	if snapshot.Handover != nil {
		handover := HandoverSection{Entries: append([]HandoverEntry(nil), snapshot.Handover.Entries...)}
		if snapshot.Handover.Entries != nil && handover.Entries == nil {
			handover.Entries = []HandoverEntry{}
		}
		for i := range handover.Entries {
			handover.Entries[i].Payload = sanitizeJSON(handover.Entries[i].Payload)
			handover.Entries[i].Declarant = sanitize(handover.Entries[i].Declarant)
		}
		snapshot.Handover = &handover
	}
	if snapshot.Evidence != nil {
		evidence := append([]Evidence(nil), snapshot.Evidence...)
		for i := range evidence {
			evidence[i].Description = sanitize(evidence[i].Description)
			evidence[i].Flags = sanitizeStrings(evidence[i].Flags, sanitize)
		}
		snapshot.Evidence = evidence
	}
	if snapshot.Findings != nil {
		findings := append([]Finding(nil), snapshot.Findings...)
		for i := range findings {
			findings[i].Title = sanitize(findings[i].Title)
			findings[i].Description = sanitize(findings[i].Description)
			findings[i].RecommendedAction = sanitize(findings[i].RecommendedAction)
		}
		snapshot.Findings = findings
	}
	if snapshot.Checklist != nil {
		checklist := *snapshot.Checklist
		checklist.Rooms = append([]ChecklistRoom(nil), snapshot.Checklist.Rooms...)
		if snapshot.Checklist.Rooms != nil && checklist.Rooms == nil {
			checklist.Rooms = []ChecklistRoom{}
		}
		for i := range checklist.Rooms {
			components := checklist.Rooms[i].Components
			checklist.Rooms[i].Components = append([]ChecklistComponent(nil), components...)
			if components != nil && checklist.Rooms[i].Components == nil {
				checklist.Rooms[i].Components = []ChecklistComponent{}
			}
			for j := range checklist.Rooms[i].Components {
				component := &checklist.Rooms[i].Components[j]
				component.Description, component.Condition, component.AssessedScope = sanitize(component.Description), sanitize(component.Condition), sanitize(component.AssessedScope)
				component.Limitations = sanitizeStrings(component.Limitations, sanitize)
			}
		}
		snapshot.Checklist = &checklist
	}
	snapshot.Comparison = sanitizeComparison(snapshot.Comparison, sanitize)
	return snapshot
}

// sanitizeStrings returns a sanitized copy of values, preserving nil.
func sanitizeStrings(values []string, sanitize func(string) string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = sanitize(value)
	}
	return out
}

// decodeJSONValue decodes one JSON document keeping numbers verbatim.
func decodeJSONValue(raw json.RawMessage) (any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, false
	}
	return value, true
}

// containsMutableURL is the single definition of a mutable URL shared by the
// sanitizer and the validation.
func containsMutableURL(value string) bool { return mutableURL.MatchString(value) }

// jsonContainsMutableURL applies containsMutableURL to the decoded keys and
// string values of a JSON document (so escaped forms are seen as the reader
// sees them) and to the raw bytes when the document cannot be decoded.
func jsonContainsMutableURL(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	value, ok := decodeJSONValue(raw)
	if !ok {
		return mutableURLBytes.Match(raw)
	}
	found := false
	sanitizeJSONValue(value, func(text string) string {
		if containsMutableURL(text) {
			found = true
		}
		return text
	})
	return found || mutableURLBytes.Match(raw)
}

func sanitizeJSONValue(value any, sanitize func(string) string) any {
	switch typed := value.(type) {
	case string:
		return sanitize(typed)
	case []any:
		for i := range typed {
			typed[i] = sanitizeJSONValue(typed[i], sanitize)
		}
		return typed
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[sanitize(key)] = sanitizeJSONValue(item, sanitize)
		}
		return out
	default:
		return value
	}
}

type TimelineEntry struct {
	StageID        string `json:"stageId"`
	Classification string `json:"classification"`
	Status         string `json:"status"`
	Position       int    `json:"position"`
}

// CanonicalJSON returns stable JSON and its content digest for immutable
// storage. Map encoding is deterministic in encoding/json.
func CanonicalJSON(snapshot Snapshot) ([]byte, string, error) {
	if err := Validate(snapshot); err != nil {
		return nil, "", err
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

func Validate(snapshot Snapshot) error {
	return validateSnapshot(snapshot, false)
}

// ValidateCustomerSnapshot validates a customer projection of a report. It
// applies every rule of Validate except the presence of the scope and
// provenance sections, which the customer allowlist removes on purpose; the
// checklist, processing and handover sections and their codes, keys and
// evidence references must still be valid. As defense in depth, a v3
// projection that still carries a field outside the customer allowlist
// (attribute sources, notes, type codes, internal limitations, item
// identities, catalog diagnostics) is rejected.
func ValidateCustomerSnapshot(snapshot Snapshot) error {
	return validateSnapshot(snapshot, true)
}

func validateSnapshot(snapshot Snapshot, customer bool) error {
	if snapshot.SchemaVersion <= 0 || strings.TrimSpace(snapshot.ReportID) == "" || strings.TrimSpace(snapshot.InspectionID) == "" || strings.TrimSpace(snapshot.TemplateVersionID) == "" || strings.TrimSpace(snapshot.ReferenceVersionID) == "" || strings.TrimSpace(snapshot.PromptDigest) == "" {
		return fmt.Errorf("report snapshot identity is required")
	}
	if snapshot.Mode != "CONSOLIDATED" && snapshot.Mode != "HISTORICAL" {
		return fmt.Errorf("report mode is invalid")
	}
	if snapshot.Classification != "NORMAL" && snapshot.Classification != "ATTENTION" && snapshot.Classification != "CRITICAL" {
		return fmt.Errorf("report classification is invalid")
	}
	if snapshot.Advisory == "" {
		return fmt.Errorf("report advisory is required")
	}
	if snapshot.Inventory != nil {
		ref := snapshot.Inventory.Reference
		if snapshot.SchemaVersion < 2 || strings.TrimSpace(ref.VersionID) == "" || ref.VersionNumber < 1 {
			return fmt.Errorf("inventory report reference is invalid")
		}
		// A comparative report references an activated baseline; a cataloging
		// report references its own draft and carries no coverage.
		if snapshot.Inventory.Coverage != nil && (strings.TrimSpace(ref.Purpose) == "" || strings.TrimSpace(ref.ActivatedAt) == "") {
			return fmt.Errorf("inventory report reference is invalid")
		}
		known := make(map[string]bool, len(snapshot.Evidence))
		for _, evidence := range snapshot.Evidence {
			known[evidence.ID] = true
		}
		entries := append(append([]InventoryEntry{}, snapshot.Inventory.Problems...), snapshot.Inventory.Informational...)
		for _, entry := range entries {
			if strings.TrimSpace(entry.Environment) == "" || strings.TrimSpace(entry.Type) == "" || strings.TrimSpace(entry.Name) == "" {
				return fmt.Errorf("inventory report entry is invalid")
			}
			for _, pair := range entry.Pairs {
				if pair.Decision != "SAME_VIEW" && pair.Decision != "NOT_COMPARABLE" && pair.Decision != "UNCERTAIN" {
					return fmt.Errorf("inventory pair decision is invalid")
				}
				if (pair.Origin == "" && pair.Current == "") || (pair.Origin != "" && !known[pair.Origin]) || (pair.Current != "" && !known[pair.Current]) {
					return fmt.Errorf("inventory pair references unknown evidence")
				}
			}
		}
	} else if snapshot.SchemaVersion == 2 {
		return fmt.Errorf("inventory report v2 requires inventory section")
	}
	if err := validateComparison(snapshot, customer); err != nil {
		return err
	}
	if snapshot.SchemaVersion >= 3 {
		if snapshot.Checklist == nil || snapshot.Processing == nil || snapshot.Handover == nil || !customer && (snapshot.Scope == nil || snapshot.Provenance == nil) {
			return fmt.Errorf("report v3 sections are required")
		}
		if customer && (snapshot.Scope != nil || snapshot.Provenance != nil || len(snapshot.Findings) > 0) {
			return fmt.Errorf("customer report contains internal sections")
		}
		if !validProcessingStatus(snapshot.Processing.Status) {
			return fmt.Errorf("report processing status is invalid")
		}
		if customer {
			if err := validateCustomerAllowlist(snapshot); err != nil {
				return err
			}
		}
		if snapshot.Scope != nil && jsonContainsMutableURL(snapshot.Scope.Document) {
			return fmt.Errorf("report scope cannot contain mutable media URLs")
		}
		known := make(map[string]bool, len(snapshot.Evidence))
		for _, evidence := range snapshot.Evidence {
			known[evidence.ID] = true
		}
		for _, room := range snapshot.Checklist.Rooms {
			if strings.TrimSpace(room.Key) == "" || strings.TrimSpace(room.Name) == "" {
				return fmt.Errorf("report checklist room is invalid")
			}
			for _, component := range room.Components {
				if strings.TrimSpace(component.Label) == "" || strings.TrimSpace(component.Status) == "" {
					return fmt.Errorf("report checklist component is invalid")
				}
				if !validComponentState(component.Status) || !validReviewStatus(component.ReviewStatus) {
					return fmt.Errorf("report checklist status is invalid")
				}
				if component.OutsideScope && component.Condition != "" {
					return fmt.Errorf("out of scope component cannot carry condition")
				}
				for _, id := range component.EvidenceIDs {
					if !known[id] {
						return fmt.Errorf("report checklist evidence is unknown")
					}
				}
			}
		}
		for _, entry := range snapshot.Handover.Entries {
			if strings.TrimSpace(entry.Key) == "" || (entry.Kind != "KEY" && entry.Kind != "CONTROL" && entry.Kind != "METER" && entry.Kind != "NOTE") || (entry.Status != "DECLARED" && entry.Status != "PENDING") {
				return fmt.Errorf("report handover entry is invalid")
			}
			if jsonContainsMutableURL(entry.Payload) || containsMutableURL(entry.Declarant) {
				return fmt.Errorf("report handover cannot contain mutable media URLs")
			}
			for _, id := range entry.EvidenceIDs {
				if !known[id] {
					return fmt.Errorf("report handover evidence is unknown")
				}
			}
		}
	}
	if strings.TrimSpace(snapshot.Context.Asset.ID) == "" || strings.TrimSpace(snapshot.Context.Asset.Name) == "" || strings.TrimSpace(snapshot.Context.Asset.Address) == "" || strings.TrimSpace(snapshot.Context.Participant.ID) == "" || strings.TrimSpace(snapshot.Context.Participant.Name) == "" || strings.TrimSpace(snapshot.Context.Template.ID) == "" || strings.TrimSpace(snapshot.Context.Template.Name) == "" || snapshot.Context.Template.Version < 1 || strings.TrimSpace(snapshot.Context.Inspection.GeneratedAt) == "" {
		return fmt.Errorf("report context is required")
	}
	for key, value := range snapshot.Coverage {
		if strings.TrimSpace(key) == "" || containsMutableURL(value) {
			return fmt.Errorf("report snapshot cannot persist mutable media URLs")
		}
	}
	for _, finding := range snapshot.Findings {
		if strings.TrimSpace(finding.Category) == "" || strings.TrimSpace(finding.Title) == "" || strings.TrimSpace(finding.Description) == "" || strings.TrimSpace(finding.Quality) == "" || strings.TrimSpace(finding.RecommendedAction) == "" || len(finding.EvidenceIDs) == 0 || finding.Confidence < 0 || finding.Confidence > 1 {
			return fmt.Errorf("report finding is invalid")
		}
		if finding.Category != "CONSERVATION" && finding.Category != "INVENTORY" && finding.Category != "CLEANLINESS" && finding.Category != "OBSTRUCTION" && finding.Category != "EVIDENCE_QUALITY" {
			return fmt.Errorf("report finding category is invalid")
		}
		for _, evidenceID := range finding.EvidenceIDs {
			if strings.TrimSpace(evidenceID) == "" {
				return fmt.Errorf("report finding evidence is required")
			}
		}
	}
	for _, evidence := range snapshot.Evidence {
		if !customer && evidence.Availability == EvidenceWithheld {
			return fmt.Errorf("report evidence availability is audience-only")
		}
		if strings.TrimSpace(evidence.ID) == "" || strings.TrimSpace(evidence.RequirementKey) == "" || (evidence.Role != "" && evidence.Role != "CURRENT" && evidence.Role != "REFERENCE") {
			return fmt.Errorf("report evidence identity is required")
		}
		if evidence.EvidenceKind != "" && evidence.EvidenceKind != "PHOTO" && evidence.EvidenceKind != "VIDEO_FRAME" {
			return fmt.Errorf("report evidence kind is invalid")
		}
		if evidence.EvidenceKind == "VIDEO_FRAME" && (evidence.FrameID != evidence.ID || strings.TrimSpace(evidence.SourceMediaID) == "" || strings.TrimSpace(evidence.PreparationID) == "" || evidence.TimestampMS == nil || *evidence.TimestampMS < 0 || len(evidence.FrameDigest) != 64) {
			return fmt.Errorf("video frame provenance is incomplete")
		}
		if evidence.EvidenceKind == "VIDEO_FRAME" && evidence.PTSMicros != nil && (*evidence.PTSMicros < 0 || *evidence.PTSMicros/1000 != *evidence.TimestampMS) {
			return fmt.Errorf("video frame timestamp is inconsistent")
		}
		for _, flag := range evidence.Flags {
			if containsMutableURL(flag) {
				return fmt.Errorf("report evidence cannot persist mutable media URLs")
			}
		}
	}
	for _, requirement := range snapshot.Requirements {
		if strings.TrimSpace(requirement.Key) == "" || strings.TrimSpace(requirement.Section) == "" || strings.TrimSpace(requirement.Label) == "" {
			return fmt.Errorf("report requirement is invalid")
		}
		if requirement.AnalysisMode != "CURRENT_ONLY" && requirement.AnalysisMode != "COMPARE_ORIGIN_CURRENT" {
			return fmt.Errorf("report requirement analysis mode is invalid")
		}
		if requirement.AnalysisStatus != "PENDING" && requirement.AnalysisStatus != "COMPLETED" && requirement.AnalysisStatus != "INCONCLUSIVE" && requirement.AnalysisStatus != "FAILED" {
			return fmt.Errorf("report requirement analysis status is invalid")
		}
		if requirement.AnalysisMode == "CURRENT_ONLY" && requirement.NoRelevantChange != nil {
			return fmt.Errorf("current-only report must have null noRelevantChange")
		}
	}
	return nil
}

// validateCustomerAllowlist rejects, as defense in depth, a v3 customer
// projection that still carries any field outside the customer allowlist.
func validateCustomerAllowlist(snapshot Snapshot) error {
	internal := fmt.Errorf("customer report contains internal fields")
	if message := snapshot.Processing.Message; message != "" && message != CustomerProcessingMessage(snapshot.Processing.Status) {
		return internal
	}
	for _, room := range snapshot.Checklist.Rooms {
		if room.TypeCode != "" {
			return internal
		}
		for _, component := range room.Components {
			if component.TypeCode != "" || component.AssessedScope != "" || len(component.NotAssessed) > 0 || len(component.AttributeSources) > 0 || component.Conflict || component.OutsideScope {
				return internal
			}
			if component.ReviewStatus != "" && component.ReviewStatus != "NOT_REVIEWED" {
				return internal
			}
			for _, limitation := range component.Limitations {
				if IsInternalLimitation(limitation) {
					return internal
				}
			}
		}
	}
	for _, entry := range snapshot.Handover.Entries {
		allowed := CustomerHandoverPayloadFields[entry.Kind]
		if len(allowed) == 0 && (len(entry.Payload) > 0 || entry.Declarant != "") {
			return internal
		}
		if len(entry.Payload) == 0 {
			continue
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(entry.Payload, &fields) != nil {
			return internal
		}
		for field := range fields {
			permitted := false
			for _, name := range allowed {
				permitted = permitted || name == field
			}
			if !permitted {
				return internal
			}
		}
	}
	if snapshot.Inventory != nil {
		if snapshot.Inventory.CatalogDiagnostics != nil {
			return internal
		}
		for _, entry := range append(append([]InventoryEntry{}, snapshot.Inventory.Problems...), snapshot.Inventory.Informational...) {
			if entry.ItemIdentityID != "" {
				return internal
			}
		}
	}
	return nil
}

func validProcessingStatus(status string) bool {
	return status == "COMPLETED" || status == "FAILED" || status == "INCONCLUSIVE" || status == "NOT_AUTHORIZED"
}
func validComponentState(status string) bool {
	return status == "OBSERVED" || status == "PARTIAL" || status == "NOT_OBSERVED" || status == "NOT_APPLICABLE" || status == "IMPOSSIBLE"
}
func validReviewStatus(status string) bool {
	return status == "" || status == "NOT_REVIEWED" || status == "ACCEPTED" || status == "EDITED" || status == "REJECTED"
}

// HTML renders a safe internal view. It intentionally cannot include public
// media URLs, storage credentials, or an external-session link.
func HTML(snapshot Snapshot) ([]byte, error) {
	return HTMLForPDF(snapshot, nil, true)
}

// HTMLForPDF renders a self-contained document whose image paths are local
// asset names supplied to the private renderer. Availability is evaluated at
// render time so a permanently missing derivative receives a clear marker.
func HTMLForPDF(snapshot Snapshot, availability map[string]bool, internal bool) ([]byte, error) {
	if err := Validate(snapshot); err != nil {
		return nil, err
	}
	originalScopeStatements := append([]string(nil), snapshot.ScopeStatements...)
	snapshot = LocalizeVideoPresentation(snapshot)
	const source = `<!doctype html><html lang="pt-BR"><head><meta charset="utf-8"><style>body{font:14px Arial,sans-serif;color:#172033;margin:32px}h1{margin-bottom:4px}.muted{color:#526078}.badge{display:inline-block;padding:4px 8px;background:#e8edf8;border-radius:999px}.requirement{break-inside:avoid;margin:24px 0}.gallery{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}.evidence{border:1px solid #d7deea;padding:8px}.evidence img{width:100%;max-height:260px;object-fit:contain}.marker{padding:32px 12px;background:#f2f4f8;color:#526078;text-align:center}.finding{border-left:3px solid #516fc4;padding-left:10px;margin:10px 0}</style></head><body><h1>{{if .Internal}}Laudo interno de vistoria{{else}}Laudo de vistoria{{end}}</h1><p class="muted">{{.Snapshot.Context.Asset.Name}} · {{.Snapshot.Context.Asset.Address}}</p><p>Responsável: {{.Snapshot.Context.Participant.Name}} · Modelo: {{.Snapshot.Context.Template.Name}}</p><p><span class="badge">{{classificationForSnapshot .Snapshot}}</span> · {{mode .Snapshot.Mode}}</p><p>{{.Snapshot.Advisory}}</p>{{if .Snapshot.ScopeStatements}}<h2>Escopo da análise</h2><ul>{{range $index, $statement := .Snapshot.ScopeStatements}}<li data-source-statement="{{index $.OriginalScopeStatements $index}}">{{$statement}}</li>{{end}}</ul>{{end}}<h2>Motivos</h2><ul>{{range .Snapshot.ReasonCodes}}<li>{{reason .}}</li>{{end}}</ul>{{if and .Snapshot.Inventory (not .Snapshot.Comparison)}}<section id="inventory-summary"><p>{{if .Snapshot.Inventory.Reference.Purpose}}Referência: {{inventoryPurpose .Snapshot.Inventory.Reference.Purpose}}{{else}}Inventário catalogado nesta vistoria{{end}}{{if .Snapshot.Inventory.Coverage}} · {{.Snapshot.Inventory.Coverage.AssessedItems}} de {{.Snapshot.Inventory.Coverage.ExpectedItems}} itens avaliados · {{.Snapshot.Inventory.Coverage.NotObserved}} não observados{{end}}</p></section>{{if .Snapshot.Inventory.Problems}}<section id="inventory-problems"><h2>Problemas do inventário</h2>{{range .Snapshot.Inventory.Problems}}<article><h3>{{.Environment}} · {{.Name}}</h3><p>{{.Type}} · {{inventoryStatus .Status}}{{if .ConditionBefore}} · {{condition .ConditionBefore}} → {{condition .ConditionAfter}}{{end}}</p>{{if .Reason}}<p>{{.Reason}}</p>{{end}}{{range .Pairs}}<div class="gallery"><figure class="evidence">{{if available .Origin}}<img src="{{assetName .Origin}}" alt="Evidência da referência">{{else}}<div class="marker">Imagem indisponível</div>{{end}}<figcaption>Referência</figcaption></figure><figure class="evidence">{{if available .Current}}<img src="{{assetName .Current}}" alt="Evidência atual">{{else}}<div class="marker">Imagem indisponível</div>{{end}}<figcaption>Vistoria atual{{if .TimestampMS}} · {{.TimestampMS}} ms{{end}}</figcaption></figure></div>{{end}}</article>{{end}}</section>{{end}}{{end}}<h2>Evidências</h2>{{with evidenceNotice .Snapshot}}<p>{{.}}</p>{{end}}{{range .EvidenceGroups}}<section class="requirement"><h3>{{.Label}}</h3><p class="muted">{{.Instructions}}</p><p class="muted">{{analysisMode .AnalysisMode}} · {{analysisStatus .AnalysisStatus}}{{if .HasNoRelevantChange}} · {{noRelevantChange .NoRelevantChange}}{{end}}</p>{{if .Coverage}}<p class="muted">{{.Coverage}}</p>{{end}}<div class="gallery">{{range .Evidence}}<figure class="evidence" data-evidence-id="{{.ID}}">{{if available .ID}}<img src="{{assetName .ID}}" alt="{{if $.V3}}{{evidenceRole .Role}}{{else}}{{.Role}}{{end}} · {{.Description}}">{{else}}<div class="marker">Imagem indisponível</div>{{end}}<figcaption><strong>{{if $.V3}}{{evidenceReference .ID}} · {{end}}{{evidenceRole .Role}}</strong>{{if .Description}} · {{.Description}}{{end}}{{if .CapturedAt}} · {{.CapturedAt}}{{end}}{{range .Flags}} · {{flag .}}{{end}}</figcaption></figure>{{end}}</div></section>{{end}}{{if .Internal}}<h2>Constatações</h2>{{if .Snapshot.Findings}}{{range .Snapshot.Findings}}<section class="finding"><strong>{{category .Category}} · {{.Title}}</strong><p>{{.Description}}</p><p>Severidade: {{severity .Severity}} · Confiança: {{.Confidence}} · Ação recomendada: {{action .RecommendedAction}}</p></section>{{end}}{{else}}<p>{{noFindings .Snapshot}}</p>{{end}}{{end}}</body></html>`
	// Report v3 presents flags and analysis statuses through the shared
	// vocabulary (vocabulary.go); v1/v2 keep their historical presentation.
	v3 := snapshot.SchemaVersion >= 3
	flag, analysisStatus := presentFlag, presentAnalysisStatus
	if v3 {
		flag, analysisStatus = FlagLabel, AnalysisStatusLabel
	}
	t, err := template.New("report").Funcs(template.FuncMap{
		"classification": presentClassification, "mode": presentMode, "reason": presentReason,
		"requirement": presentRequirement, "captureSource": presentCaptureSource, "flag": flag, "severity": presentSeverity, "action": presentAction,
		"evidenceRole": presentEvidenceRole, "assetName": evidenceAssetName, "analysisMode": presentAnalysisMode, "analysisStatus": analysisStatus, "noRelevantChange": presentNoRelevantChange, "hasNoRelevantChange": hasNoRelevantChange, "category": presentCategory, "noFindings": noFindings,
		"available":                 func(id string) bool { return availability == nil || availability[id] },
		"classificationForSnapshot": classificationForSnapshot,
		"evidenceNotice":            EvidenceNotice,
		"evidenceReference":         func(id string) string { return EvidenceReference(snapshot, id) },
		"join":                      strings.Join,
		"inventoryPurpose": func(value string) string {
			return reportLabel(map[string]string{"MOVE_IN": "Entrada no imóvel", "CONSTRUCTION_HANDOVER": "Entrega da obra", "POST_RENOVATION": "Pós-reforma", "INVENTORY_UPDATE": "Atualização do inventário", "CONTESTATION_ADJUSTMENT": "Ajuste de contestação"}, value)
		},
		"inventoryStatus": func(value string) string {
			if v3 {
				return InventoryStatusLabel(value)
			}
			return reportLabel(map[string]string{"MISSING": "Item ausente", "CONDITION_CHANGED": "Condição alterada", "NOT_OBSERVED": "Não observado", "ENVIRONMENT_NOT_CAPTURED": "Ambiente não capturado", InventoryNoBasis: "Sem base para comparar"}, value)
		},
		"condition": func(value string) string {
			if v3 {
				return PresentationLabel(value)
			}
			return reportLabel(map[string]string{"NEW": "Novo", "GOOD": "Bom", "FAIR": "Regular", "POOR": "Ruim", "NOT_ASSESSABLE": "Não avaliável"}, value)
		},
	}).Parse(source)
	if err != nil {
		return nil, err
	}
	var rendered strings.Builder
	if err := t.Execute(&rendered, htmlModel{Snapshot: snapshot, OriginalScopeStatements: originalScopeStatements, EvidenceGroups: groupEvidence(snapshot), Internal: internal, V3: v3}); err != nil {
		return nil, err
	}
	if snapshot.SchemaVersion >= 3 {
		section, err := descriptiveHTMLTemplate(snapshot, internal)
		if err != nil {
			return nil, err
		}
		var v3 strings.Builder
		if err := section.Execute(&v3, snapshot); err != nil {
			return nil, err
		}
		if snapshot.Comparison != nil {
			comparison, err := comparisonHTMLTemplate(snapshot)
			if err != nil {
				return nil, err
			}
			if err := comparison.Execute(&v3, *snapshot.Comparison); err != nil {
				return nil, err
			}
		}
		html := strings.Replace(rendered.String(), "</body>", v3.String()+"</body>", 1)
		return []byte(html), nil
	}
	return []byte(rendered.String()), nil
}

func descriptiveHTMLTemplate(snapshot Snapshot, internal bool) (*template.Template, error) {
	funcs := template.FuncMap{
		"join":     strings.Join,
		"internal": func() bool { return internal },
		"status": func(value string) string {
			return PresentationLabel(value)
		},
		"review": func(value string) string {
			return PresentationLabel(value)
		},
		"condition": func(value string) string {
			return PresentationLabel(value)
		},
		"evidenceRefs": func(ids []string) []string {
			return EvidenceReferences(snapshot, ids)
		},
		"groupedRooms": func() []checklistHTMLRoom { return groupChecklistRooms(snapshot, internal) },
		"handover": func(entry HandoverEntry) string {
			return FormatHandoverForSnapshot(snapshot, entry, internal)
		},
		"source": func(value AttributeSource) string {
			return FormatAttributeSourceForSnapshot(snapshot, value)
		},
		"timestamp": func(value string) string {
			parsed, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return "não informada"
			}
			return parsed.UTC().Format("02/01/2006 15:04 UTC")
		},
		"purpose": func() string {
			if snapshot.Context.Inspection.StageLabel != "" {
				return snapshot.Context.Inspection.StageLabel
			}
			return "Laudo de vistoria"
		},
		"scopeProfile": func() string { return ScopeProfileLabel(snapshot) },
		"scopeVersion": func() string {
			if snapshot.Scope == nil || snapshot.Scope.Version == 0 {
				return "não informada"
			}
			return fmt.Sprintf("v%d", snapshot.Scope.Version)
		},
		"expectedRoomCount": func() string {
			if count, known := ExpectedRoomCount(snapshot); known {
				return fmt.Sprintf("%d", count)
			}
			return "não informado"
		},
		"findingSeveritySummary": func() string {
			counts := CountFindings(snapshot.Findings)
			if snapshot.FindingCounts != nil {
				counts = *snapshot.FindingCounts
			}
			parts := make([]string, 0, 5)
			for _, item := range []struct {
				label string
				count int
			}{{"críticas", counts.Critical}, {"altas", counts.High}, {"moderadas", counts.Medium}, {"baixas", counts.Low}, {"informativas", counts.Informational}} {
				if item.count > 0 {
					parts = append(parts, fmt.Sprintf("%d %s", item.count, item.label))
				}
			}
			if len(parts) == 0 {
				return "Nenhuma constatação registrada"
			}
			return "Constatações por gravidade: " + strings.Join(parts, " · ")
		},
		"pendingHandoverCount": func() int {
			count := 0
			if snapshot.Handover != nil {
				for _, entry := range snapshot.Handover.Entries {
					if entry.Status == "PENDING" {
						count++
					}
				}
			}
			return count
		},
	}
	const source = `{{if .SchemaVersion}}{{if ge .SchemaVersion 3}}<section id="executive-summary"><h2>Identificação e síntese</h2><p>Finalidade: {{purpose}} · Data declarada: {{if .Context.Inspection.SubmittedAt}}{{timestamp .Context.Inspection.SubmittedAt}}{{else}}não informada{{end}} · Emissão: {{timestamp .Context.Inspection.GeneratedAt}} · Revisão do escopo {{scopeVersion}}</p><p>Perfil de bens: {{scopeProfile}} · Ambientes previstos: {{expectedRoomCount}} · {{len .Checklist.Rooms}} registrado(s)</p><p>{{findingSeveritySummary}} · {{pendingHandoverCount}} registro(s) de entrega pendente(s)</p></section>{{end}}{{end}}<section id="descriptive-checklist"><h2>Checklist descritivo</h2>{{if .Checklist.Coverage.UnknownScope}}<p>Escopo não definido; não há percentual de cobertura.</p>{{else}}<p>{{.Checklist.Coverage.Expected}} componentes esperados · {{.Checklist.Coverage.FullyAssessed}} avaliados · {{.Checklist.Coverage.PartiallyAssessed}} parcialmente avaliados · {{.Checklist.Coverage.NotObserved}} não observados · {{.Checklist.Coverage.NotApplicable}} não aplicáveis · {{.Checklist.Coverage.Impossible}} impossíveis de capturar</p>{{end}}{{range groupedRooms}}<article><h3>{{.Name}}</h3>{{range .Groups}}<h4>{{.Label}}</h4>{{range .Components}}<p><strong>{{.Label}}</strong> · {{status .Status}}{{if .Description}} · {{.Description}}{{end}}{{if .Condition}} · {{condition .Condition}}{{end}}{{if .ReviewStatus}} · {{review .ReviewStatus}}{{end}}{{if .AssessedScope}}<br>Extensão avaliada: {{.AssessedScope}}{{end}}{{if .NotAssessed}}<br>Partes não avaliadas: {{join .NotAssessed ", "}}{{end}}{{if and (internal) .OutsideScope}}<br>Observado fora do escopo; não compõe a cobertura nem o inventário de referência.{{end}}{{if .EvidenceIDs}}<br>Evidência: {{range $index, $ref := evidenceRefs .EvidenceIDs}}{{if $index}}, {{end}}{{$ref}}{{end}}{{end}}{{range .AttributeSources}}<br>{{source .}}{{end}}{{range .Limitations}}<br>Limitação: {{.}}{{end}}</p>{{end}}{{else}}<p>Nenhum componente observado.</p>{{end}}</article>{{else}}<p>Nenhuma evidência registrada; isso não significa ausência de problemas.</p>{{end}}</section>{{if .Processing}}<p>Estado da análise: {{status .Processing.Status}}{{if .Processing.Message}} · {{.Processing.Message}}{{end}}</p>{{end}}{{if .Handover}}<section id="handover"><h2>Chaves, controles e medidores</h2>{{range .Handover.Entries}}<p>{{handover .}}</p>{{else}}<p>Nenhum registro adicional.</p>{{end}}</section>{{end}}`
	return template.New("descriptive").Funcs(funcs).Parse(source)
}

// FormatHandover renders a supplemental record without exposing its internal key.
func FormatHandover(entry HandoverEntry) string {
	return formatHandover(entry, entry.EvidenceIDs, false)
}

// FormatHandoverForSnapshot renders a supplemental record with report-local evidence references.
func FormatHandoverForSnapshot(snapshot Snapshot, entry HandoverEntry, includeNote bool) string {
	return formatHandover(entry, EvidenceReferences(snapshot, entry.EvidenceIDs), includeNote)
}

// FormatAttributeSourceForSnapshot renders provenance with report-local evidence references.
func FormatAttributeSourceForSnapshot(snapshot Snapshot, source AttributeSource) string {
	copy := source
	copy.EvidenceIDs = EvidenceReferences(snapshot, source.EvidenceIDs)
	return FormatAttributeSource(copy)
}

func formatHandover(entry HandoverEntry, evidenceReferences []string, includeNote bool) string {
	presentation := PresentHandover(entry)
	parts := []string{presentation.Label, reportLabel(map[string]string{"PENDING": "Pendente", "DECLARED": "Declarado"}, entry.Status)}
	if presentation.Type != "" {
		parts = append(parts, presentation.Type)
	}
	if presentation.Value != "" {
		parts = append(parts, presentation.Value)
	}
	if presentation.Unit != "" {
		parts = append(parts, presentation.Unit)
	}
	if presentation.Date != "" {
		parts = append(parts, "Data: "+presentation.Date)
	}
	if presentation.Declarant != "" {
		parts = append(parts, "Declarante: "+presentation.Declarant)
	}
	if includeNote && presentation.Note != "" {
		parts = append(parts, "Observação: "+presentation.Note)
	}
	if len(evidenceReferences) > 0 {
		parts = append(parts, "Evidência: "+strings.Join(evidenceReferences, ", "))
	}
	return strings.Join(parts, " · ")
}

type evidenceGroup struct {
	Label, Instructions, Coverage, AnalysisMode, AnalysisStatus string
	NoRelevantChange                                            *bool
	HasNoRelevantChange                                         bool
	Evidence                                                    []Evidence
}
type htmlModel struct {
	Snapshot                Snapshot
	OriginalScopeStatements []string
	EvidenceGroups          []evidenceGroup
	Internal                bool
	// V3 enables the report-local evidence references of snapshot v3.
	V3 bool
}

type checklistHTMLGroup struct {
	Label      string
	Components []ChecklistComponent
}

type checklistHTMLRoom struct {
	Name   string
	Groups []checklistHTMLGroup
}

func groupChecklistRooms(snapshot Snapshot, internal bool) []checklistHTMLRoom {
	if snapshot.SchemaVersion < 3 || snapshot.Checklist == nil {
		return nil
	}
	rooms := make([]checklistHTMLRoom, 0, len(snapshot.Checklist.Rooms))
	for _, room := range snapshot.Checklist.Rooms {
		groups := make(map[string][]ChecklistComponent)
		for _, component := range room.Components {
			if component.OutsideScope && !internal {
				continue
			}
			label := ChecklistGroupLabel(component.TypeCode)
			groups[label] = append(groups[label], component)
		}
		labels := make([]string, 0, len(groups))
		for label := range groups {
			labels = append(labels, label)
		}
		sort.Slice(labels, func(i, j int) bool { return ChecklistGroupOrder(labels[i]) < ChecklistGroupOrder(labels[j]) })
		grouped := make([]checklistHTMLGroup, 0, len(labels))
		for _, label := range labels {
			grouped = append(grouped, checklistHTMLGroup{Label: label, Components: groups[label]})
		}
		rooms = append(rooms, checklistHTMLRoom{Name: room.Name, Groups: grouped})
	}
	return rooms
}

func groupEvidence(snapshot Snapshot) []evidenceGroup {
	byKey := make(map[string]*evidenceGroup, len(snapshot.Requirements))
	groups := make([]evidenceGroup, 0, len(snapshot.Requirements))
	for _, requirement := range snapshot.Requirements {
		groups = append(groups, evidenceGroup{Label: requirement.Label, Instructions: requirement.Instructions, Coverage: requirement.Coverage, AnalysisMode: requirement.AnalysisMode, AnalysisStatus: requirement.AnalysisStatus, NoRelevantChange: requirement.NoRelevantChange, HasNoRelevantChange: requirement.AnalysisMode == "COMPARE_ORIGIN_CURRENT" && requirement.NoRelevantChange != nil})
		byKey[requirement.Key] = &groups[len(groups)-1]
	}
	for _, item := range snapshot.Evidence {
		group, ok := byKey[item.RequirementKey]
		if !ok {
			groups = append(groups, evidenceGroup{Label: presentRequirement(item.RequirementKey)})
			group = &groups[len(groups)-1]
			byKey[item.RequirementKey] = group
		}
		group.Evidence = append(group.Evidence, item)
	}
	return groups
}

func evidenceAssetName(id string) string {
	return "evidence-" + strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return -1
	}, id) + ".jpg"
}

func presentClassification(value string) string {
	return reportLabel(map[string]string{"NORMAL": "Sem alertas identificados", "ATTENTION": "Requer atenção", "CRITICAL": "Crítica"}, value)
}

func classificationForSnapshot(snapshot Snapshot) string {
	if ClassificationLimitedForSnapshot(snapshot) {
		return "Resultado com limitações"
	}
	return ClassificationLabel(snapshot.Classification)
}

// ClassificationLimitedForSnapshot reports whether the v3 evidence and scope
// constraints require an uncertainty label despite a NORMAL classifier result.
func ClassificationLimitedForSnapshot(snapshot Snapshot) bool {
	if snapshot.SchemaVersion >= 3 && snapshot.Classification == "NORMAL" {
		limited := !hasUsableEvidence(snapshot) || snapshot.Processing == nil || snapshot.Processing.Status != "COMPLETED"
		if snapshot.Checklist == nil || snapshot.Checklist.Coverage.UnknownScope || snapshot.Checklist.Coverage.Expected == 0 || snapshot.Checklist.Coverage.FullyAssessed < snapshot.Checklist.Coverage.Expected {
			limited = true
		}
		// An unresolved or failed comparison never reads as "no alerts".
		if ComparisonLimited(snapshot.Comparison) {
			limited = true
		}
		return limited
	}
	return false
}

// ClassificationLabel returns a Portuguese label for the persisted classifier code.
func ClassificationLabel(value string) string {
	return reportLabel(map[string]string{"NORMAL": "Sem alertas identificados", "ATTENTION": "Requer atenção", "CRITICAL": "Crítica"}, value)
}

// ClassificationLabelForSnapshot returns a safe localized label that reflects v3 limitations.
func ClassificationLabelForSnapshot(snapshot Snapshot) string {
	return classificationForSnapshot(snapshot)
}

// hasUsableEvidence reports whether the issued report has usable evidence.
// Evidence withheld from one audience is still usable evidence of the report,
// so every audience derives the same classification label from the same facts.
func hasUsableEvidence(snapshot Snapshot) bool {
	for _, item := range snapshot.Evidence {
		if item.Availability == "AVAILABLE" || item.Availability == EvidenceWithheld {
			return true
		}
	}
	return false
}

// HasUsableEvidence reports whether an issued snapshot includes at least one
// available image, counting images withheld only from the current audience.
func HasUsableEvidence(snapshot Snapshot) bool { return hasUsableEvidence(snapshot) }
func presentMode(value string) string {
	return reportLabel(map[string]string{"CONSOLIDATED": "Consolidado", "HISTORICAL": "Histórico"}, value)
}

// reasonLabels is the single Portuguese vocabulary for classification reason
// codes. The HTML report, the PDF and the dashboard (apps/dashboard
// triage-journey.tsx) must show the same text for the same code.
var reasonLabels = map[string]string{
	// Codes the deterministic classifier emits.
	"NO_COMPARISONS": "Sem comparações disponíveis", "PENDING_COMPARISON": "Análise pendente", "INCONCLUSIVE": "Análise inconclusiva",
	"ANALYSIS_FAILED": "Falha técnica na análise", "MISSING_EVIDENCE": "Evidência ausente", "SKIPPED_STAGE": "Etapa pulada",
	"EVIDENCE_FLAG": "Evidência com alerta", "UNCORRECTED_RECAPTURE": "Complemento não corrigido",
	"OBSERVED_CHANGE": "Alteração observada", "CRITICAL_FINDING": "Achado crítico",
	"INVENTORY_ITEM_MISSING": "Item do inventário ausente", "INVENTORY_CONDITION_WORSENED": "Condição de item do inventário piorou",
	"INVENTORY_ITEM_NOT_OBSERVED": "Item do inventário não observado", "INVENTORY_ENVIRONMENT_NOT_CAPTURED": "Ambiente do inventário não capturado",
	"INVENTORY_EMPTY_BASELINE": "Inventário de referência vazio",
	// Codes persisted by earlier report versions; the classifier no longer emits them.
	"QUALITY_LOW": "Qualidade insuficiente", "CLASSIFICATION_CRITICAL": "Classificação crítica",
}

// ReasonLabel returns the Portuguese explanation of a classification reason
// code, or "Situação não reconhecida" for an unknown code: a raw code is never
// shown to the reader.
func ReasonLabel(code string) string { return reportLabel(reasonLabels, code) }

func presentReason(value string) string { return ReasonLabel(value) }
func presentRequirement(value string) string {
	if value == "overview" || value == "Property overview" {
		return "Visão geral do imóvel"
	}
	return "Requisito não reconhecido"
}
func presentCaptureSource(value string) string {
	return reportLabel(map[string]string{"CAMERA": "Câmera", "CAMERA_ONLY": "Câmera obrigatória", "GALLERY": "Galeria", "CAMERA_DEFAULT": "Câmera ou galeria conforme a política"}, value)
}

// presentFlag is the historical v1/v2 flag presentation; v3 uses FlagLabel.
func presentFlag(value string) string {
	return reportLabel(map[string]string{"GPS_MISSING": "Localização ausente", "LOW_QUALITY": "Qualidade insuficiente", "SENSITIVE_DETECTION": "Detecção sensível"}, value)
}
func presentSeverity(value string) string {
	return reportLabel(map[string]string{"NONE": "Insuficiente", "LOW": "Baixa", "MEDIUM": "Média", "HIGH": "Alta", "CRITICAL": "Crítica"}, value)
}
func presentAnalysisMode(value string) string {
	return reportLabel(map[string]string{"CURRENT_ONLY": "Análise atual", "COMPARE_ORIGIN_CURRENT": "Comparação com referência"}, value)
}

// presentAnalysisStatus is the historical v1/v2 presentation; v3 uses AnalysisStatusLabel.
func presentAnalysisStatus(value string) string {
	return reportLabel(map[string]string{"PENDING": "Pendente", "COMPLETED": "Concluída", "INCONCLUSIVE": "Inconclusiva", "FAILED": "Falha técnica"}, value)
}
func presentNoRelevantChange(value *bool) string {
	if value == nil {
		return "Comparação inconclusiva"
	}
	if *value {
		return "Sem alteração relevante identificada"
	}
	return "Alteração relevante identificada"
}
func hasNoRelevantChange(value evidenceGroup) bool { return value.HasNoRelevantChange }
func presentCategory(value string) string {
	return reportLabel(map[string]string{"CONSERVATION": "Conservação", "INVENTORY": "Inventário", "CLEANLINESS": "Limpeza", "OBSTRUCTION": "Obstrução", "EVIDENCE_QUALITY": "Qualidade da evidência"}, value)
}
func presentAction(value string) string {
	if label := reportLabel(map[string]string{"REVIEW": "Revisar evidência", "RECOVER": "Solicitar complemento", "NO_ACTION": "Nenhuma ação adicional"}, value); label != "Situação não reconhecida" {
		return label
	}
	return value
}
func presentEvidenceRole(value string) string {
	return reportLabel(map[string]string{"REFERENCE": "Referência", "CURRENT": "Vistoria atual"}, value)
}
func noFindings(snapshot Snapshot) string {
	return "Nenhuma constatação foi registrada; isso não confirma a ausência de problemas."
}
func reportLabel(labels map[string]string, value string) string {
	if label, ok := labels[value]; ok {
		return label
	}
	return "Situação não reconhecida"
}
