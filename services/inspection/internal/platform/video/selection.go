package video

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxScopeBatchFrames = 24
	scopeBatchOverlap   = 2
	maxScopeLabelRunes  = 120
	maxScopeItems       = 200
	// maxKnownEnvironments bounds the labels carried into later scope prompts.
	maxKnownEnvironments    = 32
	selectionVersion        = 1
	maxRetainedFrames       = 7200
	maxRepresentativeFrames = 12
)

// Candidate is a frame retained during the bounded per-second selection pass.
type Candidate struct {
	ID            string
	PTSMicros     int64
	Sharpness     float64
	DHash         uint64
	DistinctScene bool
	CloseView     bool
}

// SelectedFrame marks the baseline/detail role and any explicit quality warning.
type SelectedFrame struct {
	Candidate      Candidate
	Role, Quality  string
	Representative bool
}

// SelectFrames chooses the sharpest frame per second and at most one detail
// candidate whose image differs from the baseline or represents a close view.
func SelectFrames(candidates []Candidate, lowQualityBelow float64) []SelectedFrame {
	if lowQualityBelow <= 0 {
		lowQualityBelow = 1
	}
	buckets := map[int64][]Candidate{}
	order := make([]int64, 0)
	for _, candidate := range candidates {
		if candidate.ID == "" || candidate.PTSMicros < 0 {
			continue
		}
		bucket := candidate.PTSMicros / 1_000_000
		if _, ok := buckets[bucket]; !ok {
			order = append(order, bucket)
		}
		buckets[bucket] = append(buckets[bucket], candidate)
	}
	result := make([]SelectedFrame, 0, len(order)*2)
	for _, bucket := range order {
		values := buckets[bucket]
		baseline := values[0]
		for _, candidate := range values[1:] {
			if candidate.Sharpness > baseline.Sharpness || candidate.Sharpness == baseline.Sharpness && candidate.PTSMicros < baseline.PTSMicros {
				baseline = candidate
			}
		}
		quality := ""
		if baseline.Sharpness < lowQualityBelow {
			quality = "LOW_QUALITY"
		}
		result = append(result, SelectedFrame{Candidate: baseline, Role: "BASELINE", Quality: quality})
		var detail *Candidate
		for i := range values {
			candidate := values[i]
			if candidate.ID == baseline.ID || candidate.PTSMicros == baseline.PTSMicros {
				continue
			}
			if hammingDistance(candidate.DHash, baseline.DHash) <= 8 && !candidate.DistinctScene && !candidate.CloseView {
				continue
			}
			if detail == nil || candidate.Sharpness > detail.Sharpness || candidate.Sharpness == detail.Sharpness && candidate.PTSMicros < detail.PTSMicros {
				copy := candidate
				detail = &copy
			}
		}
		if detail != nil {
			result = append(result, SelectedFrame{Candidate: *detail, Role: "DETAIL"})
		}
	}
	return result
}

// SelectFramesV2 applies per-second sampling followed by cross-time dedup.
// Similar runs retain their sharpest candidate. Distinct-scene and close-view
// candidates always survive, and every non-empty clip retains at least one frame.
func SelectFramesV2(candidates []Candidate, lowQualityBelow float64) ([]SelectedFrame, error) {
	if lowQualityBelow <= 0 {
		lowQualityBelow = 1
	}
	ordered := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.ID != "" && candidate.PTSMicros >= 0 {
			ordered = append(ordered, candidate)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].PTSMicros != ordered[j].PTSMicros {
			return ordered[i].PTSMicros < ordered[j].PTSMicros
		}
		return ordered[i].ID < ordered[j].ID
	})
	if len(ordered) == 0 {
		return nil, nil
	}

	// Preserve the v1 per-second candidate selection as the sampling stage.
	sampled := SelectFrames(ordered, 0)
	result := make([]SelectedFrame, 0, min(len(sampled), maxRetainedFrames))
	appendFrame := func(frame SelectedFrame) error {
		if len(result) == maxRetainedFrames {
			return ErrResourceLimit
		}
		if frame.Candidate.Sharpness < lowQualityBelow {
			frame.Quality = "LOW_QUALITY"
		}
		result = append(result, frame)
		return nil
	}

	var runBest *SelectedFrame
	var runAnchor *Candidate
	var lastRetained *Candidate
	flushRun := func() error {
		if runBest == nil {
			return nil
		}
		if err := appendFrame(*runBest); err != nil {
			return err
		}
		candidate := runBest.Candidate
		lastRetained = &candidate
		runBest = nil
		runAnchor = nil
		return nil
	}
	for _, frame := range sampled {
		candidate := frame.Candidate
		if candidate.DistinctScene || candidate.CloseView {
			if err := flushRun(); err != nil {
				return nil, err
			}
			if err := appendFrame(frame); err != nil {
				return nil, err
			}
			copy := candidate
			lastRetained = &copy
			runAnchor = nil
			continue
		}
		var reference *Candidate
		if runAnchor != nil {
			reference = runAnchor
		} else {
			reference = lastRetained
		}
		if reference == nil || hammingDistance(candidate.DHash, reference.DHash) <= 10 {
			if runAnchor == nil {
				copy := candidate
				runAnchor = &copy
			}
			if runBest == nil || sharper(candidate, runBest.Candidate) {
				copy := frame
				runBest = &copy
			}
			continue
		}
		if err := flushRun(); err != nil {
			return nil, err
		}
		copy := frame
		runBest = &copy
		anchor := candidate
		runAnchor = &anchor
	}
	if err := flushRun(); err != nil {
		return nil, err
	}
	return result, nil
}

func sharper(candidate, other Candidate) bool {
	return candidate.Sharpness > other.Sharpness || candidate.Sharpness == other.Sharpness && (candidate.PTSMicros < other.PTSMicros || candidate.PTSMicros == other.PTSMicros && candidate.ID < other.ID)
}

// SelectRepresentatives marks up to limit frames using greedy max-min dHash
// diversity, with sharpness as a tie-breaker. The returned slice retains every input frame;
// callers can persist all frames and send only entries with Representative=true.
func SelectRepresentatives(frames []SelectedFrame, limit int) []SelectedFrame {
	result := slices.Clone(frames)
	for i := range result {
		result[i].Representative = false
	}
	limit = min(max(limit, 0), maxRepresentativeFrames, len(result))
	if limit == 0 {
		return result
	}
	selected := make([]bool, len(result))
	first := 0
	for i := 1; i < len(result); i++ {
		if sharper(result[i].Candidate, result[first].Candidate) {
			first = i
		}
	}
	selected[first] = true
	result[first].Representative = true
	for count := 1; count < limit; count++ {
		best, bestDistance, bestSharpness := -1, -1, -1.0
		for i, frame := range result {
			if selected[i] {
				continue
			}
			minDistance := 64
			for chosen, isSelected := range selected {
				if isSelected {
					distance := hammingDistance(frame.Candidate.DHash, result[chosen].Candidate.DHash)
					if distance < minDistance {
						minDistance = distance
					}
				}
			}
			sharpness := max(frame.Candidate.Sharpness, 0)
			if minDistance > bestDistance || minDistance == bestDistance && (sharpness > bestSharpness || sharpness == bestSharpness && (best < 0 || sharper(frame.Candidate, result[best].Candidate))) {
				best, bestDistance, bestSharpness = i, minDistance, sharpness
			}
		}
		selected[best] = true
		result[best].Representative = true
	}
	return result
}

// Visibility values recorded for a scope observation.
const (
	VisibilityVisible  = "VISIBLE"
	VisibilityObscured = "OBSCURED"
)

// ScopeObservation is a model label bound to an existing screened frame id.
type ScopeObservation struct {
	FrameID                string   `json:"frameId"`
	EnvironmentCandidate   string   `json:"environmentCandidate"`
	ComponentLabel         string   `json:"componentLabel"`
	ViewLabel              string   `json:"viewLabel"`
	EnvironmentDisplayName string   `json:"environmentDisplayName,omitempty"`
	ComponentDisplayName   string   `json:"componentDisplayName,omitempty"`
	ViewDisplayName        string   `json:"viewDisplayName,omitempty"`
	Visibility             string   `json:"visibility"`
	Landmarks              []string `json:"landmarks"`
	IdentityStatus         string   `json:"identityStatus,omitempty"`
}

// KnownEnvironment carries stable matching identity and its Portuguese label
// into later model batches.
type KnownEnvironment struct {
	Candidate   string `json:"candidate"`
	DisplayName string `json:"displayName"`
}

// ValidateScope rejects hallucinated frame membership and overlarge or unsafe
// labels. It normalizes each observation's visibility in place to
// VisibilityVisible or VisibilityObscured.
func ValidateScope(frameIDs []string, observations []ScopeObservation, unresolved []string) error {
	allowed := make(map[string]struct{}, len(frameIDs))
	for _, id := range frameIDs {
		if id == "" {
			return fmt.Errorf("%w: empty frame id", ErrModelOutputInvalid)
		}
		allowed[id] = struct{}{}
	}
	if len(observations) > maxScopeItems || len(unresolved) > len(frameIDs) {
		return fmt.Errorf("%w: scope limit", ErrResourceLimit)
	}
	seen := map[string]struct{}{}
	for _, id := range unresolved {
		if _, ok := allowed[id]; !ok {
			return fmt.Errorf("%w: unknown unresolved frame", ErrModelOutputInvalid)
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("%w: repeated frame", ErrModelOutputInvalid)
		}
		seen[id] = struct{}{}
	}
	for i := range observations {
		item := &observations[i]
		if _, ok := allowed[item.FrameID]; !ok {
			return fmt.Errorf("%w: frame outside request", ErrModelOutputInvalid)
		}
		if _, ok := seen[item.FrameID]; ok {
			return fmt.Errorf("%w: repeated frame", ErrModelOutputInvalid)
		}
		seen[item.FrameID] = struct{}{}
		item.Visibility = normalizeVisibility(item.Visibility)
		item.EnvironmentDisplayName = normalizeDisplayName(item.EnvironmentDisplayName, item.EnvironmentCandidate)
		item.ComponentDisplayName = normalizeDisplayName(item.ComponentDisplayName, item.ComponentLabel)
		item.ViewDisplayName = normalizeDisplayName(item.ViewDisplayName, item.ViewLabel)
		for _, label := range []string{item.EnvironmentCandidate, item.ComponentLabel, item.ViewLabel, item.EnvironmentDisplayName, item.ComponentDisplayName, item.ViewDisplayName} {
			if label == "" {
				continue
			}
			if !validSceneLabel(label) {
				return fmt.Errorf("%w: invalid scene label", ErrModelOutputInvalid)
			}
		}
		if len(item.Landmarks) > 32 {
			return fmt.Errorf("%w: too many landmarks", ErrResourceLimit)
		}
		for _, landmark := range item.Landmarks {
			if !validSceneLabel(landmark) {
				return fmt.Errorf("%w: invalid landmark", ErrModelOutputInvalid)
			}
		}
	}
	if len(seen) != len(allowed) {
		return fmt.Errorf("%w: response omitted requested frame", ErrModelOutputInvalid)
	}
	return nil
}

func normalizeDisplayName(displayName, canonical string) string {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" || strings.Contains(displayName, "_") || !validSceneLabel(displayName) {
		return ""
	}
	normalize := func(value string) string {
		value = strings.ToLower(strings.TrimSpace(value))
		value = strings.ReplaceAll(value, "-", " ")
		value = strings.ReplaceAll(value, "_", " ")
		return strings.Join(strings.Fields(value), " ")
	}
	if normalize(displayName) == normalize(canonical) {
		return ""
	}
	return displayName
}

// normalizeVisibility accepts the schema value and common model synonyms for a
// clear view. Any other value is treated as obscured, the conservative state
// that downstream analysis reports as a named gap.
func normalizeVisibility(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case VisibilityVisible, "FULL_VIEW", "FULLY_VISIBLE", "CLEAR_VIEW":
		return VisibilityVisible
	default:
		return VisibilityObscured
	}
}

// DiscoverScope validates each bounded response and retries malformed output once.
// It returns no partial scope when any batch remains invalid. Each call receives
// the distinct environment labels accepted from earlier batches, so the model
// can keep naming a continuing room consistently across batch boundaries.
func DiscoverScope(ctx context.Context, frames []string, invoke func(ctx context.Context, batch []string, knownEnvironments []KnownEnvironment) ([]ScopeObservation, []string, error)) ([]ScopeObservation, error) {
	if invoke == nil {
		return nil, fmt.Errorf("video: missing scope gateway")
	}
	var all []ScopeObservation
	var known []KnownEnvironment
	knownKeys := map[string]int{}
	for _, batch := range ScopeBatches(frames) {
		var accepted []ScopeObservation
		valid := false
		for attempt := 0; attempt < 2; attempt++ {
			observations, unresolved, err := invoke(ctx, batch, slices.Clone(known))
			if err != nil {
				if errors.Is(err, ErrResourceLimit) {
					return nil, err
				}
				if errors.Is(err, ErrModelOutputInvalid) {
					continue
				}
				return nil, err
			}
			if err = ValidateScope(batch, observations, unresolved); errors.Is(err, ErrResourceLimit) {
				return nil, err
			} else if err == nil {
				accepted, valid = observations, true
				break
			}
		}
		if !valid {
			return nil, ErrModelOutputInvalid
		}
		for i := range accepted {
			observation := &accepted[i]
			label := strings.TrimSpace(observation.EnvironmentCandidate)
			key := strings.ToLower(label)
			if index, ok := knownKeys[key]; ok {
				// The first display name accepted for an environment is the
				// canonical one; the prompt asks for reuse but cannot enforce it.
				if known[index].DisplayName == "" {
					known[index].DisplayName = observation.EnvironmentDisplayName
				} else {
					observation.EnvironmentDisplayName = known[index].DisplayName
				}
				continue
			}
			if len(known) == maxKnownEnvironments {
				continue
			}
			knownKeys[key] = len(known)
			known = append(known, KnownEnvironment{Candidate: label, DisplayName: observation.EnvironmentDisplayName})
		}
		all = append(all, accepted...)
	}
	return all, nil
}

// ConsolidateScope keeps one representative per observed component/view within
// a continuous environment occurrence. A later occurrence of the same room
// label remains distinct, since a repeated label cannot establish identity.
func ConsolidateScope(frameIDs []string, observations []ScopeObservation) ([]ScopeObservation, error) {
	positions := make(map[string]int, len(frameIDs))
	for i, id := range frameIDs {
		positions[id] = i
	}
	ordered := make([]ScopeObservation, 0, len(observations))
	frameIndexes := make(map[string]int, len(observations))
	for _, observation := range observations {
		if _, ok := positions[observation.FrameID]; !ok {
			return nil, ErrModelOutputInvalid
		}
		// Adjacent batches share two frames and describe them in independent
		// model calls. Keep the earlier description unless only the later one
		// saw the component clearly.
		if index, ok := frameIndexes[observation.FrameID]; ok {
			if ordered[index].Visibility != VisibilityVisible && observation.Visibility == VisibilityVisible {
				ordered[index] = observation
			}
			continue
		}
		frameIndexes[observation.FrameID] = len(ordered)
		ordered = append(ordered, observation)
	}
	sort.SliceStable(ordered, func(i, j int) bool { return positions[ordered[i].FrameID] < positions[ordered[j].FrameID] })
	grouped := make([]ScopeObservation, 0, min(len(ordered), maxScopeItems))
	groupEnvironments := make([]string, 0, min(len(ordered), maxScopeItems))
	groupIndexes := make(map[string]int)
	occurrences := make(map[string]int)
	previousEnvironment := ""
	environmentOccurrences := 0
	for _, observation := range ordered {
		environment := strings.ToLower(strings.TrimSpace(observation.EnvironmentCandidate))
		if environment != previousEnvironment {
			environmentOccurrences++
			if environmentOccurrences > maxScopeItems {
				return nil, ErrResourceLimit
			}
			previousEnvironment = environment
			occurrences[environment]++
			groupIndexes = make(map[string]int)
		}
		key := strings.ToLower(strings.TrimSpace(observation.ComponentLabel)) + "\x00" + strings.ToLower(strings.TrimSpace(observation.ViewLabel)) + "\x00" + strings.ToLower(strings.Join(observation.Landmarks, "\x00"))
		if index, ok := groupIndexes[key]; ok {
			if grouped[index].Visibility != VisibilityVisible && observation.Visibility == VisibilityVisible {
				grouped[index] = observation
			}
			continue
		}
		if len(grouped) == maxScopeItems {
			return nil, ErrResourceLimit
		}
		groupIndexes[key] = len(grouped)
		groupEnvironments = append(groupEnvironments, environment)
		grouped = append(grouped, observation)
	}
	for i := range grouped {
		grouped[i].IdentityStatus = "RESOLVED"
		if occurrences[groupEnvironments[i]] > 1 {
			grouped[i].IdentityStatus = "AMBIGUOUS"
		}
	}
	return grouped, nil
}

// ScopeBatches maintains temporal order and two-frame overlap between requests.
func ScopeBatches(frames []string) [][]string {
	if len(frames) == 0 {
		return nil
	}
	var batches [][]string
	for start := 0; start < len(frames); {
		end := min(start+maxScopeBatchFrames, len(frames))
		batches = append(batches, append([]string(nil), frames[start:end]...))
		if end == len(frames) {
			break
		}
		start = end - scopeBatchOverlap
	}
	return batches
}

func validSceneLabel(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > maxScopeLabelRunes {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func hammingDistance(a, b uint64) int {
	x := a ^ b
	count := 0
	for x != 0 {
		x &= x - 1
		count++
	}
	return count
}

var (
	ErrModelOutputInvalid = fmt.Errorf("video: MODEL_OUTPUT_INVALID")
	ErrResourceLimit      = fmt.Errorf("video: RESOURCE_LIMIT")
)
