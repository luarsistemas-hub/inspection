package video

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxScopeBatchFrames = 24
	scopeBatchOverlap   = 2
	maxScopeLabelRunes  = 120
	maxScopeItems       = 200
	selectionVersion    = 1
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
	Candidate     Candidate
	Role, Quality string
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

// ScopeObservation is a model label bound to an existing screened frame id.
type ScopeObservation struct {
	FrameID, EnvironmentCandidate, ComponentLabel, ViewLabel, Visibility string
	Landmarks                                                            []string
	IdentityStatus                                                       string `json:"identityStatus,omitempty"`
}

// ValidateScope rejects hallucinated frame membership and overlarge or unsafe labels.
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
	for _, item := range observations {
		if _, ok := allowed[item.FrameID]; !ok {
			return fmt.Errorf("%w: frame outside request", ErrModelOutputInvalid)
		}
		if _, ok := seen[item.FrameID]; ok {
			return fmt.Errorf("%w: repeated frame", ErrModelOutputInvalid)
		}
		seen[item.FrameID] = struct{}{}
		for _, label := range []string{item.EnvironmentCandidate, item.ComponentLabel, item.ViewLabel, item.Visibility} {
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

// DiscoverScope validates each bounded response and retries malformed output once.
// It returns no partial scope when any batch remains invalid.
func DiscoverScope(ctx context.Context, frames []string, invoke func(context.Context, []string) ([]ScopeObservation, []string, error)) ([]ScopeObservation, error) {
	if invoke == nil {
		return nil, fmt.Errorf("video: missing scope gateway")
	}
	var all []ScopeObservation
	for _, batch := range ScopeBatches(frames) {
		var accepted []ScopeObservation
		valid := false
		for attempt := 0; attempt < 2; attempt++ {
			observations, unresolved, err := invoke(ctx, batch)
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
	ordered := append([]ScopeObservation(nil), observations...)
	sort.SliceStable(ordered, func(i, j int) bool { return positions[ordered[i].FrameID] < positions[ordered[j].FrameID] })
	seenFrames := make(map[string]ScopeObservation, len(ordered))
	grouped := make([]ScopeObservation, 0, min(len(ordered), maxScopeItems))
	groupEnvironments := make([]string, 0, min(len(ordered), maxScopeItems))
	groupIndexes := make(map[string]int)
	occurrences := make(map[string]int)
	previousEnvironment := ""
	environmentOccurrences := 0
	for _, observation := range ordered {
		if _, ok := positions[observation.FrameID]; !ok {
			return nil, ErrModelOutputInvalid
		}
		if earlier, ok := seenFrames[observation.FrameID]; ok {
			if earlier.EnvironmentCandidate != observation.EnvironmentCandidate || earlier.ComponentLabel != observation.ComponentLabel || earlier.ViewLabel != observation.ViewLabel || earlier.Visibility != observation.Visibility || strings.Join(earlier.Landmarks, "\x00") != strings.Join(observation.Landmarks, "\x00") {
				return nil, ErrModelOutputInvalid
			}
			continue
		}
		seenFrames[observation.FrameID] = observation
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
			if grouped[index].Visibility != "VISIBLE" && observation.Visibility == "VISIBLE" {
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
