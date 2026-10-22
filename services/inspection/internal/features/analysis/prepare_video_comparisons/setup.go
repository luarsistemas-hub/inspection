// Package prepare_video_comparisons validates semantic correspondence and seals
// the immutable frame membership consumed by the existing condition core.
package prepare_video_comparisons

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/observability"
	"inspection/services/inspection/internal/platform/video"
)

var ErrInvalidPlan = errors.New("video analysis plan is invalid")

const (
	Matched          = "MATCHED"
	CurrentOnly      = "CURRENT_ONLY"
	Inconclusive     = "INCONCLUSIVE"
	TechnicalFailure = "TECHNICAL_FAILURE"
	SameView         = "SAME_VIEW"
	NotComparable    = "NOT_COMPARABLE"
	Uncertain        = "UNCERTAIN"
)

// Frame is an authorized frame reference loaded from a sealed preparation.
// Asset and tenant ownership are copied from authoritative database rows.
type Frame struct {
	ID, TenantID, AssetID, PreparationID         identity.ID
	Generation                                   int
	Digest                                       string
	PTS                                          int64
	Environment, Component, ComponentLabel, View string
	Visibility, IdentityStatus, Quality          string
	Visible                                      bool
}

// Pair is one proposed relationship between two exact frames.
type Pair struct {
	ComponentID    identity.ID `json:"componentId"`
	OriginFrameID  identity.ID `json:"originFrameId"`
	CurrentFrameID identity.ID `json:"currentFrameId"`
	Decision       string      `json:"decision"`
	Reason         string      `json:"reason,omitempty"`
	Landmarks      []string    `json:"visibleLandmarks,omitempty"`
}

// PlanItem is an immutable component result in a sealed plan.
type PlanItem struct {
	ComponentID        identity.ID     `json:"componentId"`
	Outcome            string          `json:"outcome"`
	Reason             string          `json:"reason,omitempty"`
	InputDigest        string          `json:"inputDigest,omitempty"`
	OriginFrames       []identity.ID   `json:"originFrames,omitempty"`
	CurrentFrames      []identity.ID   `json:"currentFrames,omitempty"`
	CoreGroups         [][]Pair        `json:"coreGroups,omitempty"`
	CurrentGroups      [][]identity.ID `json:"currentGroups,omitempty"`
	CarriedResultJobID *identity.ID    `json:"carriedResultJobId,omitempty"`
}

// Plan binds a submission to the exact current and optional pinned-origin banks.
type Plan struct {
	ID, TenantID, AssetID, InspectionID, SubmissionID identity.ID
	ReferenceVersionID                                *identity.ID
	ParentPlanID                                      *identity.ID
	CurrentPreparationID                              identity.ID
	SupplementPreparationID                           *identity.ID
	Generation, CurrentGeneration                     int
	OriginPreparationID                               *identity.ID
	LeaseToken                                        *identity.ID
	Items                                             []PlanItem
	Digest                                            string
}

// Proposer makes the first semantic pass. Verifier independently checks the
// proposed pairs with their scene context. Both passes must agree on identity.
type Proposer interface {
	Propose(context.Context, []Frame, []Frame) ([]Pair, error)
}
type Verifier interface {
	Verify(context.Context, []Frame, []Frame, []Pair) ([]Pair, error)
}

// Dependencies documents the external boundaries required by the durable
// slice. The pure validators below intentionally do not construct adapters.
type Dependencies struct {
	Frames   FrameReader
	Matcher  Proposer
	Verifier Verifier
	Plans    PlanWriter
	Metrics  *observability.Metrics
	MaxCalls int
}

type FrameReader interface {
	LoadPreparationFrames(context.Context, identity.ID, identity.ID) ([]Frame, error)
}
type PlanWriter interface {
	SealPlan(context.Context, Plan) error
}

// Component is an expected origin or discovered current-only component.
type Component struct {
	// ID is the originating VideoScopeItem UUID for expected reference
	// components, and the current preparation's VideoScopeItem UUID for
	// CURRENT_ONLY discoveries. It is persisted unchanged as video:<ID>.
	ID        identity.ID
	GapReason string
	// PriorFrameIDs are the parent plan's current frames of this component.
	// A child plan compares its supplement against them.
	PriorFrameIDs []identity.ID
}
type PlanRequest struct {
	Plan         Plan
	Components   []Component
	CarriedItems []PlanItem
}

// Setup validates dependencies before exposing a plan builder.
func Setup(deps Dependencies) (func(context.Context, PlanRequest) (Plan, error), error) {
	if deps.Frames == nil {
		return nil, fmt.Errorf("analysis/prepare_video_comparisons: missing frame reader")
	}
	if deps.Matcher == nil {
		return nil, fmt.Errorf("analysis/prepare_video_comparisons: missing matcher")
	}
	if deps.Verifier == nil {
		return nil, fmt.Errorf("analysis/prepare_video_comparisons: missing verifier")
	}
	if deps.Plans == nil {
		return nil, fmt.Errorf("analysis/prepare_video_comparisons: missing plan writer")
	}
	return func(ctx context.Context, request PlanRequest) (Plan, error) {
		plan := request.Plan
		ctx = WithMatchCorrelation(ctx, plan.InspectionID, plan.TenantID)
		maxCalls := deps.MaxCalls
		if maxCalls <= 0 {
			maxCalls = DefaultMaxCalls
		}
		ctx = WithMatchCallBudget(ctx, maxCalls)
		if plan.Generation < 1 {
			plan.Generation = 1
		}
		if plan.ID == (identity.ID{}) && plan.SubmissionID != (identity.ID{}) {
			plan.ID = identity.NewDeterministicID("inspection-video-plan", plan.TenantID.String()+":"+plan.SubmissionID.String()+":"+fmt.Sprint(plan.Generation))
		}
		currentPreparationID := plan.CurrentPreparationID
		if plan.ParentPlanID != nil {
			if plan.SupplementPreparationID == nil {
				return Plan{}, fmt.Errorf("%w: child plan is missing supplement preparation", ErrInvalidPlan)
			}
			currentPreparationID = *plan.SupplementPreparationID
		}
		current, err := deps.Frames.LoadPreparationFrames(ctx, plan.TenantID, currentPreparationID)
		if err != nil {
			return Plan{}, err
		}
		var origin []Frame
		if plan.ReferenceVersionID != nil {
			if plan.OriginPreparationID == nil || *plan.OriginPreparationID == (identity.ID{}) {
				return Plan{}, fmt.Errorf("%w: missing pinned origin preparation", ErrInvalidPlan)
			}
			origin, err = deps.Frames.LoadPreparationFrames(ctx, plan.TenantID, *plan.OriginPreparationID)
			if err != nil {
				return Plan{}, err
			}
		}
		if err := validateLoadedBank(plan, origin, current); err != nil {
			return Plan{}, err
		}
		comparisonOrigin := origin
		if plan.ParentPlanID != nil {
			comparisonOrigin, err = deps.Frames.LoadPreparationFrames(ctx, plan.TenantID, plan.CurrentPreparationID)
			if err != nil {
				return Plan{}, err
			}
		}
		plan.Items = append([]PlanItem(nil), request.CarriedItems...)
		if plan.ReferenceVersionID == nil {
			for _, component := range request.Components {
				frames := framesForComponent(current, component.ID)
				usable, named := usableComponentFrames(frames)
				reason := component.GapReason
				if reason == "" {
					reason = named
				}
				if reason != "" {
					plan.Items = append(plan.Items, PlanItem{ComponentID: component.ID, Outcome: Inconclusive, Reason: reason, CurrentFrames: frameIDs(frames), InputDigest: componentDigest(nil, frames)})
					continue
				}
				frames = usable
				usableFrames := frames[:0]
				for _, frame := range frames {
					if frame.Visible {
						usableFrames = append(usableFrames, frame)
					}
				}
				frames = usableFrames
				if len(frames) == 0 {
					reason := component.GapReason
					if reason == "" {
						reason = "CURRENT_VIEW_UNAVAILABLE"
					}
					plan.Items = append(plan.Items, PlanItem{ComponentID: component.ID, Outcome: Inconclusive, Reason: reason, InputDigest: componentDigest(nil, nil)})
					continue
				}
				ids := frameIDs(frames)
				plan.Items = append(plan.Items, PlanItem{ComponentID: component.ID, Outcome: CurrentOnly, CurrentFrames: ids, CurrentGroups: currentFrameGroups(ids), InputDigest: componentDigest(nil, frames)})
			}
		} else {
			for componentIndex, component := range request.Components {
				expectedOrigin := framesForComponent(comparisonOrigin, component.ID)
				if plan.ParentPlanID != nil && len(component.PriorFrameIDs) > 0 {
					expectedOrigin = framesWithIDs(comparisonOrigin, component.PriorFrameIDs)
				}
				componentOrigin := matchingFramesForComponent(comparisonOrigin, component.ID, expectedOrigin)
				componentCurrent := matchingFramesForComponent(current, component.ID, expectedOrigin)
				usableCurrent, named := usableComponentFrames(componentCurrent)
				reason := component.GapReason
				if reason == "" {
					reason = named
				}
				if reason != "" {
					plan.Items = append(plan.Items, PlanItem{ComponentID: component.ID, Outcome: Inconclusive, Reason: reason, OriginFrames: frameIDs(componentOrigin), CurrentFrames: frameIDs(componentCurrent), InputDigest: componentDigest(componentOrigin, componentCurrent)})
					continue
				}
				componentCurrent = usableCurrent
				pairs, err := Match(ctx, componentOrigin, componentCurrent, deps.Matcher, deps.Verifier)
				if errors.Is(err, ErrMatchBudgetExhausted) {
					for _, unprocessed := range request.Components[componentIndex:] {
						originFrames := framesForComponent(comparisonOrigin, unprocessed.ID)
						currentFrames := framesForComponent(current, unprocessed.ID)
						plan.Items = append(plan.Items, PlanItem{ComponentID: unprocessed.ID, Outcome: TechnicalFailure, Reason: "MATCH_BUDGET_EXHAUSTED", OriginFrames: frameIDs(originFrames), CurrentFrames: frameIDs(currentFrames), InputDigest: componentDigest(originFrames, currentFrames)})
					}
					break
				}
				if err != nil {
					return Plan{}, err
				}
				componentPairs := make([]Pair, 0)
				for _, pair := range pairs {
					if pair.ComponentID == component.ID {
						componentPairs = append(componentPairs, pair)
					}
				}
				groups, excess := GroupPairs(componentPairs)
				if len(excess) > 0 {
					originFrames := pairFrames(componentPairs, comparisonOrigin, true)
					currentFrames := pairFrames(componentPairs, current, false)
					plan.Items = append(plan.Items, PlanItem{ComponentID: component.ID, Outcome: TechnicalFailure, Reason: "CORE_IMAGE_LIMIT_EXCEEDED", OriginFrames: frameIDs(originFrames), CurrentFrames: frameIDs(currentFrames), InputDigest: componentDigest(originFrames, currentFrames)})
					continue
				}
				if len(groups) == 0 {
					reason := component.GapReason
					if reason == "" {
						if missingExpectedView(component.ID, comparisonOrigin, current) {
							reason = "NO_CURRENT_VIEW"
						} else {
							reason = "NO_VERIFIED_CORRESPONDENCE"
						}
					}
					originFrames := framesForComponent(comparisonOrigin, component.ID)
					currentFrames := framesForComponent(current, component.ID)
					plan.Items = append(plan.Items, PlanItem{ComponentID: component.ID, Outcome: Inconclusive, Reason: reason, OriginFrames: frameIDs(originFrames), CurrentFrames: frameIDs(currentFrames), InputDigest: componentDigest(originFrames, currentFrames)})
					continue
				}
				originIDs, currentIDs := pairsFrameIDs(groups)
				acceptedPairs := flattenPairs(groups)
				plan.Items = append(plan.Items, PlanItem{ComponentID: component.ID, Outcome: Matched, OriginFrames: originIDs, CurrentFrames: currentIDs, CoreGroups: groups, InputDigest: componentDigest(pairFrames(acceptedPairs, comparisonOrigin, true), pairFrames(acceptedPairs, current, false))})
			}
		}
		supplied := append([]Frame(nil), origin...)
		if plan.ParentPlanID != nil {
			supplied = append(supplied, comparisonOrigin...)
		}
		supplied = append(supplied, current...)
		if err := ValidatePlanWithFrames(plan, supplied); err != nil {
			return Plan{}, err
		}
		plan.Digest = PlanDigest(plan)
		if err := deps.Plans.SealPlan(ctx, plan); err != nil {
			return Plan{}, err
		}
		if deps.Metrics != nil {
			counts := make(map[string]int, 4)
			for _, item := range plan.Items {
				counts[item.Outcome]++
			}
			deps.Metrics.VideoPlanSeal(counts)
		}
		return plan, nil
	}, nil
}

// usableComponentFrames drops frames whose view is ambiguous, obstructed or
// blurred. It names that limitation only when no labelled usable frame of the
// component remains, so one poor frame never discards good evidence.
func usableComponentFrames(frames []Frame) ([]Frame, string) {
	usable := make([]Frame, 0, len(frames))
	flagged := make([]Frame, 0)
	labelled := 0
	for _, frame := range frames {
		if frame.IdentityStatus == "AMBIGUOUS" || frame.Visibility == video.VisibilityObscured || frame.Quality == "LOW_QUALITY" {
			flagged = append(flagged, frame)
			continue
		}
		if frame.Visibility != "" {
			labelled++
		}
		usable = append(usable, frame)
	}
	if len(flagged) > 0 && labelled == 0 {
		return usable, namedGapReason(flagged)
	}
	return usable, ""
}

func namedGapReason(frames []Frame) string {
	for _, frame := range frames {
		if frame.IdentityStatus == "AMBIGUOUS" {
			return "AMBIGUOUS_IDENTITY"
		}
	}
	for _, frame := range frames {
		if frame.Visibility == video.VisibilityObscured {
			return "OBSTRUCTED_VIEW"
		}
	}
	for _, frame := range frames {
		if frame.Quality == "LOW_QUALITY" {
			return "BLURRED_VIEW"
		}
	}
	return ""
}

func matchingFramesForComponent(frames []Frame, componentID identity.ID, expected []Frame) []Frame {
	result := make([]Frame, 0)
	labels := make(map[string]bool, len(expected))
	for _, frame := range expected {
		labels[frame.ComponentLabel+"\x00"+frame.View] = true
	}
	for _, frame := range frames {
		if frame.Component == componentID.String() || frame.ComponentLabel == "" || labels[frame.ComponentLabel+"\x00"+frame.View] {
			result = append(result, frame)
		}
	}
	return result
}

// validateLoadedBank rejects frame-reader scope violations before any matcher
// gateway can receive images from an untrusted or unpinned preparation.
func validateLoadedBank(plan Plan, origin, current []Frame) error {
	for _, frame := range current {
		if frame.ID == (identity.ID{}) || !validDigest(frame.Digest) || frame.TenantID != plan.TenantID || frame.AssetID != plan.AssetID || frame.PreparationID != expectedCurrentPreparation(plan) || frame.Generation != plan.CurrentGeneration {
			return fmt.Errorf("%w: current frame is outside submitted bank", ErrInvalidPlan)
		}
	}
	if plan.ReferenceVersionID == nil {
		if len(origin) != 0 {
			return fmt.Errorf("%w: unpinned origin bank supplied", ErrInvalidPlan)
		}
		return nil
	}
	expected := plan.OriginPreparationID
	if expected == nil {
		return fmt.Errorf("%w: missing pinned origin preparation", ErrInvalidPlan)
	}
	for _, frame := range origin {
		if frame.ID == (identity.ID{}) || !validDigest(frame.Digest) || frame.TenantID != plan.TenantID || frame.AssetID != plan.AssetID || frame.PreparationID != *expected {
			return fmt.Errorf("%w: origin frame is outside pinned bank", ErrInvalidPlan)
		}
	}
	return nil
}

func missingExpectedView(componentID identity.ID, origin, current []Frame) bool {
	expected := false
	for _, reference := range origin {
		if reference.Component != componentID.String() || reference.ComponentLabel == "" {
			continue
		}
		expected = true
		for _, candidate := range current {
			if candidate.ComponentLabel == reference.ComponentLabel {
				return false
			}
		}
	}
	return expected
}

func flattenPairs(groups [][]Pair) []Pair {
	var pairs []Pair
	for _, group := range groups {
		pairs = append(pairs, group...)
	}
	return pairs
}

func framesForComponent(frames []Frame, component identity.ID) []Frame {
	result := make([]Frame, 0)
	for _, frame := range frames {
		if frame.Component == component.String() {
			result = append(result, frame)
		}
	}
	return result
}

func framesWithIDs(frames []Frame, ids []identity.ID) []Frame {
	wanted := make(map[identity.ID]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	result := make([]Frame, 0, len(ids))
	for _, frame := range frames {
		if wanted[frame.ID] {
			result = append(result, frame)
		}
	}
	return result
}

func frameIDs(frames []Frame) []identity.ID {
	ids := make([]identity.ID, 0, len(frames))
	for _, frame := range frames {
		ids = append(ids, frame.ID)
	}
	return ids
}

func currentFrameGroups(ids []identity.ID) [][]identity.ID {
	var groups [][]identity.ID
	for start := 0; start < len(ids); start += 6 {
		groups = append(groups, append([]identity.ID(nil), ids[start:min(start+6, len(ids))]...))
	}
	return groups
}

func pairsFrameIDs(groups [][]Pair) ([]identity.ID, []identity.ID) {
	var origin, current []identity.ID
	seenOrigin, seenCurrent := map[identity.ID]struct{}{}, map[identity.ID]struct{}{}
	for _, group := range groups {
		for _, pair := range group {
			if _, ok := seenOrigin[pair.OriginFrameID]; !ok {
				origin = append(origin, pair.OriginFrameID)
				seenOrigin[pair.OriginFrameID] = struct{}{}
			}
			if _, ok := seenCurrent[pair.CurrentFrameID]; !ok {
				current = append(current, pair.CurrentFrameID)
				seenCurrent[pair.CurrentFrameID] = struct{}{}
			}
		}
	}
	return origin, current
}

func pairFrames(pairs []Pair, frames []Frame, origin bool) []Frame {
	wanted := map[identity.ID]struct{}{}
	for _, pair := range pairs {
		id := pair.CurrentFrameID
		if origin {
			id = pair.OriginFrameID
		}
		wanted[id] = struct{}{}
	}
	var result []Frame
	for _, frame := range frames {
		if _, ok := wanted[frame.ID]; ok {
			result = append(result, frame)
		}
	}
	return result
}

func componentDigest(origin, current []Frame) string {
	ids := make([]string, 0, len(origin)+len(current))
	for _, frame := range origin {
		ids = append(ids, "O:"+frame.ID.String()+":"+frame.Digest)
	}
	for _, frame := range current {
		ids = append(ids, "C:"+frame.ID.String()+":"+frame.Digest)
	}
	sort.Strings(ids)
	encoded, _ := json.Marshal(ids)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// Match performs two independent correspondence passes and abstains unless
// both identify the same component and supported view. Timestamps and route
// order are intentionally absent from the acceptance decision.
func Match(ctx context.Context, origin, current []Frame, proposer Proposer, verifier Verifier) ([]Pair, error) {
	if proposer == nil || verifier == nil {
		return nil, fmt.Errorf("%w: missing match pass", ErrInvalidPlan)
	}
	candidates, err := proposer.Propose(ctx, origin, current)
	if err != nil {
		return nil, err
	}
	originIDs, currentIDs := map[identity.ID]struct{}{}, map[identity.ID]struct{}{}
	for _, frame := range origin {
		originIDs[frame.ID] = struct{}{}
	}
	for _, frame := range current {
		currentIDs[frame.ID] = struct{}{}
	}
	candidateKeys := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate.ComponentID == (identity.ID{}) {
			return nil, fmt.Errorf("%w: missing candidate component", ErrInvalidPlan)
		}
		if candidate.Decision != SameView && candidate.Decision != NotComparable && candidate.Decision != Uncertain {
			return nil, fmt.Errorf("%w: unsupported match decision", ErrInvalidPlan)
		}
		if _, ok := originIDs[candidate.OriginFrameID]; !ok {
			return nil, fmt.Errorf("%w: candidate origin frame is outside pinned bank", ErrInvalidPlan)
		}
		if _, ok := currentIDs[candidate.CurrentFrameID]; !ok {
			return nil, fmt.Errorf("%w: candidate current frame is outside submission", ErrInvalidPlan)
		}
		if _, duplicate := candidateKeys[pairKey(candidate)]; duplicate {
			return nil, fmt.Errorf("%w: duplicate proposed pair", ErrInvalidPlan)
		}
		candidateKeys[pairKey(candidate)] = struct{}{}
	}
	verified, err := verifier.Verify(ctx, origin, current, candidates)
	if err != nil {
		return nil, err
	}
	verifiedByKey := make(map[string]Pair, len(verified))
	for _, pair := range verified {
		key := pairKey(pair)
		if _, ok := candidateKeys[key]; !ok {
			return nil, fmt.Errorf("%w: verifier returned an unproposed pair", ErrInvalidPlan)
		}
		if pair.Decision != SameView && pair.Decision != NotComparable && pair.Decision != Uncertain {
			return nil, fmt.Errorf("%w: unsupported verifier decision", ErrInvalidPlan)
		}
		if _, exists := verifiedByKey[key]; exists {
			return nil, fmt.Errorf("%w: duplicate verifier pair", ErrInvalidPlan)
		}
		verifiedByKey[key] = pair
	}
	accepted := make([]Pair, 0, len(candidates))
	for _, candidate := range candidates {
		check, ok := verifiedByKey[pairKey(candidate)]
		if !ok || candidate.Decision != SameView || check.Decision != SameView || len(check.Landmarks) == 0 {
			continue
		}
		accepted = append(accepted, Pair{ComponentID: candidate.ComponentID, OriginFrameID: candidate.OriginFrameID, CurrentFrameID: candidate.CurrentFrameID, Decision: SameView, Landmarks: append([]string(nil), check.Landmarks...)})
	}
	return accepted, nil
}

func pairKey(pair Pair) string {
	return pair.ComponentID.String() + ":" + pair.OriginFrameID.String() + ":" + pair.CurrentFrameID.String()
}

// GroupPairs keeps each condition-core request to three pairs and six unique
// images. It preserves pair order and never duplicates an image in a group.
func GroupPairs(pairs []Pair) ([][]Pair, []Pair) {
	var groups [][]Pair
	var excess []Pair
	group := make([]Pair, 0, 3)
	images := map[identity.ID]struct{}{}
	for _, pair := range pairs {
		newCount := 0
		if _, ok := images[pair.OriginFrameID]; !ok {
			newCount++
		}
		if _, ok := images[pair.CurrentFrameID]; !ok {
			newCount++
		}
		if len(group) == 3 || len(images)+newCount > 6 {
			excess = append(excess, pair)
			continue
		}
		group = append(group, pair)
		images[pair.OriginFrameID] = struct{}{}
		images[pair.CurrentFrameID] = struct{}{}
	}
	if len(group) > 0 {
		groups = append(groups, group)
	}
	return groups, excess
}

// ValidatePlan rejects mixed generations, cross-scope frames, duplicate
// component membership, invalid outcomes and groups that exceed the core cap.
func ValidatePlan(plan Plan) error {
	if plan.ID == (identity.ID{}) || plan.TenantID == (identity.ID{}) || plan.AssetID == (identity.ID{}) || plan.InspectionID == (identity.ID{}) || plan.SubmissionID == (identity.ID{}) || plan.CurrentPreparationID == (identity.ID{}) || plan.Generation < 1 || plan.CurrentGeneration < 1 {
		return fmt.Errorf("%w: missing plan identity", ErrInvalidPlan)
	}
	for _, item := range plan.Items {
		for _, frame := range append(append([]identity.ID{}, item.OriginFrames...), item.CurrentFrames...) {
			if frame == (identity.ID{}) {
				return fmt.Errorf("%w: empty frame membership", ErrInvalidPlan)
			}
		}
	}
	seenComponents := map[identity.ID]struct{}{}
	for _, item := range plan.Items {
		if item.ComponentID == (identity.ID{}) {
			return fmt.Errorf("%w: missing component", ErrInvalidPlan)
		}
		if !validDigest(item.InputDigest) {
			return fmt.Errorf("%w: invalid item input digest", ErrInvalidPlan)
		}
		if _, exists := seenComponents[item.ComponentID]; exists {
			return fmt.Errorf("%w: duplicate component membership", ErrInvalidPlan)
		}
		seenComponents[item.ComponentID] = struct{}{}
		if item.CarriedResultJobID != nil {
			if plan.ParentPlanID == nil || *item.CarriedResultJobID == (identity.ID{}) {
				return fmt.Errorf("%w: carried result lacks parent lineage", ErrInvalidPlan)
			}
			continue
		}
		switch item.Outcome {
		case Matched:
			if plan.ReferenceVersionID == nil || plan.OriginPreparationID == nil || len(item.OriginFrames) == 0 || len(item.CurrentFrames) == 0 || item.Reason != "" || !validDigest(item.InputDigest) {
				return fmt.Errorf("%w: invalid matched item", ErrInvalidPlan)
			}
		case CurrentOnly:
			if plan.ReferenceVersionID != nil || plan.OriginPreparationID != nil || len(item.OriginFrames) != 0 || len(item.CurrentFrames) == 0 {
				return fmt.Errorf("%w: invalid current-only item", ErrInvalidPlan)
			}
			if len(item.CurrentGroups) == 0 {
				return fmt.Errorf("%w: current-only item has no core group", ErrInvalidPlan)
			}
			for _, group := range item.CurrentGroups {
				if len(group) == 0 || len(group) > 6 {
					return fmt.Errorf("%w: current-only image limit", ErrInvalidPlan)
				}
				for _, id := range group {
					if !containsID(item.CurrentFrames, id) {
						return fmt.Errorf("%w: current-only group outside membership", ErrInvalidPlan)
					}
				}
			}
		case Inconclusive, TechnicalFailure:
			if strings.TrimSpace(item.Reason) == "" {
				return fmt.Errorf("%w: missing limitation reason", ErrInvalidPlan)
			}
		default:
			return fmt.Errorf("%w: unsupported outcome", ErrInvalidPlan)
		}
		if item.Outcome == Matched {
			if len(item.CoreGroups) == 0 {
				return fmt.Errorf("%w: matched item has no core group", ErrInvalidPlan)
			}
			for _, group := range item.CoreGroups {
				if len(group) == 0 || len(group) > 3 {
					return fmt.Errorf("%w: core group pair limit", ErrInvalidPlan)
				}
				unique := map[identity.ID]struct{}{}
				seenCurrent := map[identity.ID]struct{}{}
				origins := map[identity.ID]struct{}{}
				for _, pair := range group {
					if pair.Decision != SameView || pair.ComponentID != item.ComponentID || pair.OriginFrameID == (identity.ID{}) || pair.CurrentFrameID == (identity.ID{}) {
						return fmt.Errorf("%w: unsupported pair membership", ErrInvalidPlan)
					}
					if !containsID(item.OriginFrames, pair.OriginFrameID) || !containsID(item.CurrentFrames, pair.CurrentFrameID) {
						return fmt.Errorf("%w: pair outside item membership", ErrInvalidPlan)
					}
					// One origin view may anchor several current views of the same
					// component; each current view and frame role stays unique.
					if _, duplicate := seenCurrent[pair.CurrentFrameID]; duplicate {
						return fmt.Errorf("%w: duplicate evidence in core request", ErrInvalidPlan)
					}
					if _, crossRole := origins[pair.CurrentFrameID]; crossRole {
						return fmt.Errorf("%w: duplicate evidence in core request", ErrInvalidPlan)
					}
					if _, crossRole := seenCurrent[pair.OriginFrameID]; crossRole {
						return fmt.Errorf("%w: duplicate evidence in core request", ErrInvalidPlan)
					}
					seenCurrent[pair.CurrentFrameID] = struct{}{}
					origins[pair.OriginFrameID] = struct{}{}
					unique[pair.OriginFrameID] = struct{}{}
					unique[pair.CurrentFrameID] = struct{}{}
				}
				if len(unique) > 6 {
					return fmt.Errorf("%w: core group image limit", ErrInvalidPlan)
				}
			}
		}
	}
	return nil
}

func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

// ValidatePlanWithFrames additionally checks that every member is present in
// the pinned preparation and belongs to the plan tenant, asset and generation.
func ValidatePlanWithFrames(plan Plan, supplied []Frame) error {
	if err := ValidatePlan(plan); err != nil {
		return err
	}
	byID := make(map[identity.ID]Frame, len(supplied))
	for _, frame := range supplied {
		if frame.ID == (identity.ID{}) || !validDigest(frame.Digest) {
			return fmt.Errorf("%w: incomplete frame", ErrInvalidPlan)
		}
		if _, exists := byID[frame.ID]; exists {
			return fmt.Errorf("%w: duplicate frame row", ErrInvalidPlan)
		}
		if frame.TenantID != plan.TenantID || frame.AssetID != plan.AssetID {
			return fmt.Errorf("%w: cross-scope frame", ErrInvalidPlan)
		}
		byID[frame.ID] = frame
	}
	used := map[identity.ID]struct{}{}
	for _, item := range plan.Items {
		if item.CarriedResultJobID != nil {
			continue
		}
		for _, id := range append(append([]identity.ID{}, item.OriginFrames...), item.CurrentFrames...) {
			frame, ok := byID[id]
			if !ok {
				return fmt.Errorf("%w: unknown frame", ErrInvalidPlan)
			}
			if _, duplicate := used[id]; duplicate {
				return fmt.Errorf("%w: duplicate membership", ErrInvalidPlan)
			}
			used[id] = struct{}{}
			if item.Outcome == CurrentOnly || containsID(item.CurrentFrames, id) {
				if frame.PreparationID != expectedCurrentPreparation(plan) || frame.Generation != plan.CurrentGeneration {
					return fmt.Errorf("%w: mixed current generation", ErrInvalidPlan)
				}
			} else if expectedOriginPreparation(plan) == nil || frame.PreparationID != *expectedOriginPreparation(plan) {
				return fmt.Errorf("%w: frame outside pinned origin", ErrInvalidPlan)
			}
		}
		originFrames := make([]Frame, 0, len(item.OriginFrames))
		currentFrames := make([]Frame, 0, len(item.CurrentFrames))
		for _, id := range item.OriginFrames {
			originFrames = append(originFrames, byID[id])
		}
		for _, id := range item.CurrentFrames {
			currentFrames = append(currentFrames, byID[id])
		}
		if componentDigest(originFrames, currentFrames) != item.InputDigest {
			return fmt.Errorf("%w: item input digest does not match frame membership", ErrInvalidPlan)
		}
		for _, group := range item.CoreGroups {
			for _, pair := range group {
				if _, ok := byID[pair.OriginFrameID]; !ok {
					return fmt.Errorf("%w: unknown origin pair frame", ErrInvalidPlan)
				}
				if _, ok := byID[pair.CurrentFrameID]; !ok {
					return fmt.Errorf("%w: unknown current pair frame", ErrInvalidPlan)
				}
			}
		}
		for _, group := range item.CurrentGroups {
			for _, id := range group {
				if _, ok := byID[id]; !ok {
					return fmt.Errorf("%w: unknown current-only frame", ErrInvalidPlan)
				}
				if !containsID(item.CurrentFrames, id) {
					return fmt.Errorf("%w: current-only frame outside membership", ErrInvalidPlan)
				}
			}
		}
	}
	return nil
}

func containsID(values []identity.ID, target identity.ID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func expectedOriginPreparation(plan Plan) *identity.ID {
	if plan.ParentPlanID != nil {
		return &plan.CurrentPreparationID
	}
	return plan.OriginPreparationID
}

func expectedCurrentPreparation(plan Plan) identity.ID {
	if plan.ParentPlanID != nil && plan.SupplementPreparationID != nil {
		return *plan.SupplementPreparationID
	}
	return plan.CurrentPreparationID
}

// PlanDigest hashes canonical membership and input digests, not mutable status.
func PlanDigest(plan Plan) string {
	items := append([]PlanItem(nil), plan.Items...)
	sort.Slice(items, func(i, j int) bool { return items[i].ComponentID.String() < items[j].ComponentID.String() })
	canonical := struct {
		ID, TenantID, AssetID, InspectionID, SubmissionID, CurrentPreparationID        string
		Generation, CurrentGeneration                                                  int
		ReferenceVersionID, OriginPreparationID, SupplementPreparationID, ParentPlanID string
		Items                                                                          []PlanItem
	}{ID: plan.ID.String(), TenantID: plan.TenantID.String(), AssetID: plan.AssetID.String(), InspectionID: plan.InspectionID.String(), SubmissionID: plan.SubmissionID.String(), CurrentPreparationID: plan.CurrentPreparationID.String(), Generation: plan.Generation, CurrentGeneration: plan.CurrentGeneration, Items: items}
	if plan.ReferenceVersionID != nil {
		canonical.ReferenceVersionID = plan.ReferenceVersionID.String()
	}
	if plan.OriginPreparationID != nil {
		canonical.OriginPreparationID = plan.OriginPreparationID.String()
	}
	if plan.SupplementPreparationID != nil {
		canonical.SupplementPreparationID = plan.SupplementPreparationID.String()
	}
	if plan.ParentPlanID != nil {
		canonical.ParentPlanID = plan.ParentPlanID.String()
	}
	data, _ := json.Marshal(canonical)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
