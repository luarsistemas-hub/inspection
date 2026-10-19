package prepare_video_comparisons

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"inspection/libs/identity"
)

func testID(value string) identity.ID {
	return identity.NewDeterministicID("prepare-video-comparisons-test", value)
}

func matchedPlan() (Plan, []Frame) {
	tenant, asset := testID("tenant"), testID("asset")
	originPrep, currentPrep := testID("origin-prep"), testID("current-prep")
	component, originFrame, currentFrame := testID("sink"), testID("F01"), testID("F11")
	frames := []Frame{{ID: originFrame, TenantID: tenant, AssetID: asset, PreparationID: originPrep, Generation: 1, Digest: digest}, {ID: currentFrame, TenantID: tenant, AssetID: asset, PreparationID: currentPrep, Generation: 1, Digest: digest}}
	plan := Plan{ID: testID("plan"), TenantID: tenant, AssetID: asset, InspectionID: testID("inspection"), SubmissionID: testID("submission"), Generation: 1, ReferenceVersionID: ptr(testID("origin-version")), OriginPreparationID: &originPrep, CurrentPreparationID: currentPrep, CurrentGeneration: 1, Items: []PlanItem{{ComponentID: component, Outcome: Matched, InputDigest: componentDigest([]Frame{frames[0]}, []Frame{frames[1]}), OriginFrames: []identity.ID{originFrame}, CurrentFrames: []identity.ID{currentFrame}, CoreGroups: [][]Pair{{{ComponentID: component, OriginFrameID: originFrame, CurrentFrameID: currentFrame, Decision: SameView}}}}}}
	return plan, frames
}

const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func ptr(id identity.ID) *identity.ID { return &id }

type proposerStub []Pair

func (p proposerStub) Propose(context.Context, []Frame, []Frame) ([]Pair, error) {
	return []Pair(p), nil
}

type verifierStub []Pair

func (v verifierStub) Verify(context.Context, []Frame, []Frame, []Pair) ([]Pair, error) {
	return []Pair(v), nil
}

func TestMatchRequiresBothPassesToSupportSameView(t *testing.T) {
	component, origin, current := testID("sink"), testID("F01"), testID("F11")
	candidate := Pair{ComponentID: component, OriginFrameID: origin, CurrentFrameID: current, Decision: SameView, Landmarks: []string{"cabinet edge"}}
	originFrames := []Frame{{ID: origin}}
	currentFrames := []Frame{{ID: current}}
	accepted, err := Match(context.Background(), originFrames, currentFrames, proposerStub{candidate}, verifierStub{{ComponentID: component, OriginFrameID: origin, CurrentFrameID: current, Decision: Uncertain}})
	if err != nil || len(accepted) != 0 {
		t.Fatalf("uncertain verification must abstain: accepted=%v err=%v", accepted, err)
	}
	accepted, err = Match(context.Background(), originFrames, currentFrames, proposerStub{candidate}, verifierStub{candidate})
	if err != nil || len(accepted) != 1 {
		t.Fatalf("supported location pair should be accepted: accepted=%v err=%v", accepted, err)
	}
}

func TestMatchRejectsVerifierMembershipOutsideProposedCandidate(t *testing.T) {
	component, origin, current := testID("sink"), testID("F01"), testID("F11")
	originFrames, currentFrames := []Frame{{ID: origin}}, []Frame{{ID: current}}
	proposed := Pair{ComponentID: component, OriginFrameID: origin, CurrentFrameID: current, Decision: SameView}
	forged := Pair{ComponentID: testID("other-component"), OriginFrameID: origin, CurrentFrameID: current, Decision: SameView, Landmarks: []string{"counter edge"}}
	if _, err := Match(context.Background(), originFrames, currentFrames, proposerStub{proposed}, verifierStub{forged}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("forged verifier membership accepted: %v", err)
	}
}

func TestParseMatchResultRejectsForgedFrameAndSanitizesLabels(t *testing.T) {
	component, origin, current := testID("sink"), testID("F01"), testID("F11")
	framesO, framesC := []Frame{{ID: origin, Component: component.String()}}, []Frame{{ID: current, Component: component.String()}}
	valid := `{"pairs":[{"componentId":"` + component.String() + `","originFrameId":"` + origin.String() + `","currentFrameId":"` + current.String() + `","decision":"SAME_VIEW","visibleLandmarks":["` + strings.Repeat("é", 121) + `"],"reason":"ok"}]}`
	pairs, err := parseMatchResult([]byte(valid), framesO, framesC, nil, false)
	if err != nil || len(pairs) != 1 || utf8.RuneCountInString(pairs[0].Landmarks[0]) != 120 {
		t.Fatalf("valid result should be sanitized to 120 code points: pairs=%v err=%v", pairs, err)
	}
	forged := strings.Replace(valid, origin.String(), testID("forged").String(), 1)
	if _, err := parseMatchResult([]byte(forged), framesO, framesC, nil, false); err == nil {
		t.Fatal("forged frame ID was accepted")
	}
}

func TestValidatePlanAllowsDifferentTimestampsForSameComponent(t *testing.T) {
	plan, frames := matchedPlan()
	frames[0].PTS = 10_000_000
	frames[1].PTS = 70_000_000
	if err := ValidatePlanWithFrames(plan, frames); err != nil {
		t.Fatalf("different route timestamps must not invalidate location pair: %v", err)
	}
}

func TestValidatePlanRejectsMixedCurrentGeneration(t *testing.T) {
	plan, frames := matchedPlan()
	frames[1].Generation = 2
	if err := ValidatePlanWithFrames(plan, frames); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("expected generation mismatch rejection, got %v", err)
	}
}

func TestGroupPairsExposesExcessInsteadOfDispatchingOverLimit(t *testing.T) {
	component := testID("sink")
	pairs := make([]Pair, 4)
	for i := range pairs {
		pairs[i] = Pair{ComponentID: component, OriginFrameID: testID("O" + string(rune('1'+i))), CurrentFrameID: testID("C" + string(rune('1'+i))), Decision: SameView}
	}
	groups, excess := GroupPairs(pairs)
	if len(groups) != 1 || len(groups[0]) != 3 || len(excess) != 1 {
		t.Fatalf("expected three-pair group and one explicit excess pair, groups=%v excess=%v", groups, excess)
	}
}

func TestValidatePlanKeepsLocationPairWhenObjectIsAbsent(t *testing.T) {
	plan, frames := matchedPlan()
	frames[0].Visible, frames[1].Visible = true, true
	plan.Items[0].CoreGroups[0][0].Landmarks = []string{"sink location", "counter edge"}
	if err := ValidatePlanWithFrames(plan, frames); err != nil {
		t.Fatalf("visible corresponding location remains a valid pair when inventory differs: %v", err)
	}
}

func TestSetupRejectsOutOfScopeFrameBanksBeforeMatcherIT185(t *testing.T) {
	plan, frames := matchedPlan()
	originPrep := *plan.OriginPreparationID
	component := plan.Items[0].ComponentID
	cases := []struct {
		name   string
		mutate func([]Frame) []Frame
	}{
		{name: "foreign tenant", mutate: func(input []Frame) []Frame { input[0].TenantID = testID("tenant-b"); return input }},
		{name: "foreign asset", mutate: func(input []Frame) []Frame { input[0].AssetID = testID("asset-y"); return input }},
		{name: "unpinned O2", mutate: func(input []Frame) []Frame { input[0].PreparationID = testID("origin-O2"); return input }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bank := tc.mutate(append([]Frame(nil), frames...))
			reader := fakeFrames{values: map[identity.ID][]Frame{originPrep: {bank[0]}, plan.CurrentPreparationID: {bank[1]}}}
			called := false
			matcher := proposerFunc(func(context.Context, []Frame, []Frame) ([]Pair, error) { called = true; return nil, nil })
			verifier := verifierFunc(func(context.Context, []Frame, []Frame, []Pair) ([]Pair, error) { called = true; return nil, nil })
			writer := &fakeWriter{}
			build, err := Setup(Dependencies{Frames: reader, Matcher: matcher, Verifier: verifier, Plans: writer})
			if err != nil {
				t.Fatal(err)
			}
			_, err = build(context.Background(), PlanRequest{Plan: plan, Components: []Component{{ID: component}}})
			if !errors.Is(err, ErrInvalidPlan) || called || writer.plan.ID != (identity.ID{}) {
				t.Fatalf("out-of-scope frame must be rejected before gateway/writer: err=%v called=%v", err, called)
			}
		})
	}
}

type proposerFunc func(context.Context, []Frame, []Frame) ([]Pair, error)

func (f proposerFunc) Propose(ctx context.Context, origin, current []Frame) ([]Pair, error) {
	return f(ctx, origin, current)
}

type verifierFunc func(context.Context, []Frame, []Frame, []Pair) ([]Pair, error)

func (f verifierFunc) Verify(ctx context.Context, origin, current []Frame, proposed []Pair) ([]Pair, error) {
	return f(ctx, origin, current, proposed)
}

func TestRemovedObjectStillDispatchesVerifiedLocationPairUT044(t *testing.T) {
	plan, frames := matchedPlan()
	component := plan.Items[0].ComponentID
	frames[0].Component, frames[1].Component = component.String(), component.String()
	frames[0].Visible, frames[1].Visible = true, true
	pair := plan.Items[0].CoreGroups[0][0]
	pair.Reason = "sink object absent from current view; counter edge and plumbing location correspond"
	pair.Landmarks = []string{"counter edge", "plumbing location"}
	reader := fakeFrames{values: map[identity.ID][]Frame{*plan.OriginPreparationID: {frames[0]}, plan.CurrentPreparationID: {frames[1]}}}
	writer := &fakeWriter{}
	build, err := Setup(Dependencies{Frames: reader, Matcher: proposerStub{pair}, Verifier: verifierStub{pair}, Plans: writer})
	if err != nil {
		t.Fatal(err)
	}
	result, err := build(context.Background(), PlanRequest{Plan: plan, Components: []Component{{ID: component}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Outcome != Matched || len(result.Items[0].CoreGroups) != 1 || len(result.Items[0].CoreGroups[0]) != 1 {
		t.Fatalf("verified location with missing object did not reach condition core: %#v", result.Items)
	}
	if result.Items[0].CoreGroups[0][0].OriginFrameID != frames[0].ID || result.Items[0].CoreGroups[0][0].CurrentFrameID != frames[1].ID || writer.plan.Digest != result.Digest {
		t.Fatalf("location pair or sealed plan lost exact frames: %#v", result.Items[0])
	}
}

func TestSetupValidatesEveryDependencyBeforeRegistration(t *testing.T) {
	// Explicit small fakes keep this test focused on setup's dependency gate.
	reader := fakeFrames{}
	matcher := proposerStub{}
	verifier := verifierStub{}
	writer := &fakeWriter{}
	valid := Dependencies{Frames: reader, Matcher: matcher, Verifier: verifier, Plans: writer}
	if _, err := Setup(valid); err != nil {
		t.Fatal(err)
	}
	for name, deps := range map[string]Dependencies{
		"reader":   {Matcher: matcher, Verifier: verifier, Plans: writer},
		"matcher":  {Frames: reader, Verifier: verifier, Plans: writer},
		"verifier": {Frames: reader, Matcher: matcher, Plans: writer},
		"writer":   {Frames: reader, Matcher: matcher, Verifier: verifier},
	} {
		if _, err := Setup(deps); err == nil {
			t.Errorf("missing %s accepted", name)
		}
	}
}

type fakeFrames struct{ values map[identity.ID][]Frame }

func (f fakeFrames) LoadPreparationFrames(_ context.Context, _, preparation identity.ID) ([]Frame, error) {
	return append([]Frame(nil), f.values[preparation]...), nil
}

type fakeWriter struct{ plan Plan }

func (f *fakeWriter) SealPlan(_ context.Context, plan Plan) error { f.plan = plan; return nil }

func TestSetupBuildsAndSealsVerifiedTimestampIndependentPlan(t *testing.T) {
	plan, frames := matchedPlan()
	originPrep, currentPrep := *plan.OriginPreparationID, plan.CurrentPreparationID
	originFrames, currentFrames := frames[0], frames[1]
	originFrames.PTS, currentFrames.PTS = 10_000_000, 70_000_000
	component := plan.Items[0].ComponentID
	pair := plan.Items[0].CoreGroups[0][0]
	pair.Landmarks = []string{"counter edge"}
	reader := fakeFrames{values: map[identity.ID][]Frame{originPrep: {originFrames}, currentPrep: {currentFrames}}}
	writer := &fakeWriter{}
	build, err := Setup(Dependencies{Frames: reader, Matcher: proposerStub{pair}, Verifier: verifierStub{pair}, Plans: writer})
	if err != nil {
		t.Fatal(err)
	}
	result, err := build(context.Background(), PlanRequest{Plan: plan, Components: []Component{{ID: component}}})
	if err != nil {
		t.Fatalf("build video plan: %v", err)
	}
	if result.Digest == "" || writer.plan.Digest != result.Digest || len(result.Items) != 1 || result.Items[0].Outcome != Matched {
		t.Fatalf("plan was not sealed with the verified pair: %#v", result)
	}
}

func TestSetupDoesNotMatchCurrentFramesWithNamedVisualLimitations(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		mutate       func(*Frame)
	}{
		{"obstructed", "OBSTRUCTED_VIEW", func(frame *Frame) { frame.Visibility = "OBSCURED" }},
		{"blurred", "BLURRED_VIEW", func(frame *Frame) { frame.Quality = "LOW_QUALITY" }},
		{"ambiguous", "AMBIGUOUS_IDENTITY", func(frame *Frame) { frame.IdentityStatus = "AMBIGUOUS" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, frames := matchedPlan()
			component := plan.Items[0].ComponentID
			originPrep, currentPrep := *plan.OriginPreparationID, plan.CurrentPreparationID
			origin, current := frames[0], frames[1]
			origin.Component, current.Component = component.String(), component.String()
			origin.ComponentLabel, current.ComponentLabel = "sink", "sink"
			origin.Environment, current.Environment = "kitchen", "kitchen"
			origin.Visible, current.Visible = true, true
			tc.mutate(&current)
			pair := Pair{ComponentID: component, OriginFrameID: origin.ID, CurrentFrameID: current.ID, Decision: SameView, Landmarks: []string{"counter edge"}}
			writer := &fakeWriter{}
			build, err := Setup(Dependencies{
				Frames:  fakeFrames{values: map[identity.ID][]Frame{originPrep: {origin}, currentPrep: {current}}},
				Matcher: proposerStub{pair}, Verifier: verifierStub{pair}, Plans: writer,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := build(context.Background(), PlanRequest{Plan: plan, Components: []Component{{ID: component}}})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Items) != 1 || result.Items[0].Outcome != Inconclusive || result.Items[0].Reason != tc.reason {
				t.Fatalf("visual limitation was matched: %+v", result.Items)
			}
		})
	}
}

func TestSetupKeepsGoodFrameWhenAnotherViewIsBlurred(t *testing.T) {
	plan, frames := matchedPlan()
	component := plan.Items[0].ComponentID
	originPrep, currentPrep := *plan.OriginPreparationID, plan.CurrentPreparationID
	origin, good := frames[0], frames[1]
	origin.Component, good.Component = component.String(), component.String()
	origin.ComponentLabel, good.ComponentLabel = "sink", "sink"
	origin.Environment, good.Environment = "kitchen", "kitchen"
	origin.Visible, good.Visible = true, true
	origin.Visibility, good.Visibility = "VISIBLE", "VISIBLE"
	blurred := good
	blurred.ID, blurred.Quality = testID("current-blurred"), "LOW_QUALITY"
	pair := Pair{ComponentID: component, OriginFrameID: origin.ID, CurrentFrameID: good.ID, Decision: SameView, Landmarks: []string{"counter edge"}}
	writer := &fakeWriter{}
	build, err := Setup(Dependencies{
		Frames:  fakeFrames{values: map[identity.ID][]Frame{originPrep: {origin}, currentPrep: {good, blurred}}},
		Matcher: proposerStub{pair}, Verifier: verifierStub{pair}, Plans: writer,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := build(context.Background(), PlanRequest{Plan: plan, Components: []Component{{ID: component}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Outcome != Matched {
		t.Fatalf("one blurred frame discarded the good evidence: %+v", result.Items)
	}
	for _, id := range result.Items[0].CurrentFrames {
		if id == blurred.ID {
			t.Fatalf("blurred frame %s entered the comparison", blurred.ID)
		}
	}
}

func TestSetupExhaustedMatchBudgetSealsPriorResultsAndMarksRemainingTechnicalFailureIT184(t *testing.T) {
	plan, _ := matchedPlan()
	originPrep, currentPrep := *plan.OriginPreparationID, plan.CurrentPreparationID
	components := []Component{{ID: testID("kitchen")}, {ID: testID("bathroom")}, {ID: testID("wall")}}
	var origin, current []Frame
	for i, component := range components {
		label := []string{"sink", "shower", "wall"}[i]
		origin = append(origin, Frame{ID: testID("origin-" + label), TenantID: plan.TenantID, AssetID: plan.AssetID, PreparationID: originPrep, Generation: 1, Digest: digest, Component: component.ID.String(), ComponentLabel: label})
		current = append(current, Frame{ID: testID("current-" + label), TenantID: plan.TenantID, AssetID: plan.AssetID, PreparationID: currentPrep, Generation: 1, Digest: digest, Component: component.ID.String(), ComponentLabel: label})
	}
	matcher := &budgetPlanMatcher{}
	writer := &fakeWriter{}
	build, err := Setup(Dependencies{Frames: fakeFrames{values: map[identity.ID][]Frame{originPrep: origin, currentPrep: current}}, Matcher: matcher, Verifier: matcher, Plans: writer, MaxCalls: 3})
	if err != nil {
		t.Fatal(err)
	}
	result, err := build(context.Background(), PlanRequest{Plan: plan, Components: components})
	if err != nil {
		t.Fatalf("build should seal after budget exhaustion: %v", err)
	}
	if len(result.Items) != 3 || result.Items[0].Outcome != Matched || result.Items[1].Outcome != TechnicalFailure || result.Items[1].Reason != "MATCH_BUDGET_EXHAUSTED" || result.Items[2].Outcome != TechnicalFailure || result.Items[2].Reason != "MATCH_BUDGET_EXHAUSTED" {
		t.Fatalf("unexpected checkpointed plan outcomes: %+v", result.Items)
	}
	if writer.plan.Digest != result.Digest || matcher.calls != 4 {
		t.Fatalf("plan not sealed or call limit not enforced: sealedDigest=%q resultDigest=%q calls=%d", writer.plan.Digest, result.Digest, matcher.calls)
	}
}

type budgetPlanMatcher struct{ calls int }

func (m *budgetPlanMatcher) Propose(ctx context.Context, origin, current []Frame) ([]Pair, error) {
	m.calls++
	if err := ConsumeMatchCall(ctx); err != nil {
		return nil, err
	}
	if len(origin) == 0 || len(current) == 0 {
		return nil, nil
	}
	component, err := identity.ParseID(origin[0].Component)
	if err != nil {
		return nil, err
	}
	return []Pair{{ComponentID: component, OriginFrameID: origin[0].ID, CurrentFrameID: current[0].ID, Decision: SameView}}, nil
}

func (m *budgetPlanMatcher) Verify(ctx context.Context, _, _ []Frame, proposed []Pair) ([]Pair, error) {
	m.calls++
	if err := ConsumeMatchCall(ctx); err != nil {
		return nil, err
	}
	for i := range proposed {
		proposed[i].Landmarks = []string{"verified edge"}
	}
	return proposed, nil
}

func TestG2ChangesBathroomAndCarriesKitchenUT054(t *testing.T) {
	plan, frames := matchedPlan()
	plan.ID = identity.NewID()
	plan.Generation = 2
	parentID := identity.NewID()
	plan.ParentPlanID = &parentID
	// The kitchen result is a terminal G1 job; only bathroom is sent through matching.
	kitchenID, bathroomID, kitchenJobID := identity.NewID(), plan.Items[0].ComponentID, identity.NewID()
	plan.Items = nil
	carried := PlanItem{ComponentID: kitchenID, Outcome: Matched, InputDigest: componentDigest(nil, nil), CarriedResultJobID: &kitchenJobID}
	origin, prior, supplement := *plan.OriginPreparationID, plan.CurrentPreparationID, identity.NewID()
	plan.SupplementPreparationID = &supplement
	plan.CurrentPreparationID = prior
	plan.CurrentGeneration = 2
	priorFrame, supplementFrame := frames[1], frames[1]
	supplementFrame.ID, supplementFrame.PreparationID, supplementFrame.Generation = identity.NewID(), supplement, 2
	priorFrame.Component, supplementFrame.Component = bathroomID.String(), bathroomID.String()
	pair := Pair{ComponentID: bathroomID, OriginFrameID: priorFrame.ID, CurrentFrameID: supplementFrame.ID, Decision: SameView, Landmarks: []string{"bathroom doorway"}}
	writer := &fakeWriter{}
	build, err := Setup(Dependencies{Frames: fakeFrames{values: map[identity.ID][]Frame{origin: {frames[0]}, prior: {priorFrame}, supplement: {supplementFrame}}}, Matcher: proposerStub{pair}, Verifier: verifierStub{pair}, Plans: writer})
	if err != nil {
		t.Fatal(err)
	}
	result, err := build(context.Background(), PlanRequest{Plan: plan, Components: []Component{{ID: bathroomID}}, CarriedItems: []PlanItem{carried}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("G2 items=%d, want bathroom plus carried kitchen", len(result.Items))
	}
	byComponent := map[identity.ID]PlanItem{}
	for _, item := range result.Items {
		byComponent[item.ComponentID] = item
	}
	if byComponent[bathroomID].Outcome != Matched || byComponent[bathroomID].CarriedResultJobID != nil {
		t.Fatalf("bathroom was not recomputed: %+v", byComponent[bathroomID])
	}
	if got := byComponent[kitchenID].CarriedResultJobID; got == nil || *got != kitchenJobID {
		t.Fatalf("kitchen carried job=%v, want %s", got, kitchenJobID)
	}
	if writer.plan.Digest != result.Digest {
		t.Fatal("G2 was not sealed")
	}
}

func TestVerifierUncertainSealsInconclusiveItemUT041(t *testing.T) {
	plan, frames := matchedPlan()
	component := plan.Items[0].ComponentID
	frames[0].Component, frames[1].Component = component.String(), component.String()
	pair := plan.Items[0].CoreGroups[0][0]
	pair.Landmarks = []string{"counter edge"}
	reader := fakeFrames{values: map[identity.ID][]Frame{*plan.OriginPreparationID: {frames[0]}, plan.CurrentPreparationID: {frames[1]}}}
	writer := &fakeWriter{}
	build, err := Setup(Dependencies{Frames: reader, Matcher: proposerStub{pair}, Verifier: verifierStub{{ComponentID: component, OriginFrameID: pair.OriginFrameID, CurrentFrameID: pair.CurrentFrameID, Decision: Uncertain}}, Plans: writer})
	if err != nil {
		t.Fatal(err)
	}
	result, err := build(context.Background(), PlanRequest{Plan: plan, Components: []Component{{ID: component}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Outcome != Inconclusive || result.Items[0].Reason != "NO_VERIFIED_CORRESPONDENCE" || len(result.Items[0].CoreGroups) != 0 || len(result.Items[0].OriginFrames) != 1 || len(result.Items[0].CurrentFrames) != 1 {
		t.Fatalf("uncertain verification produced unsupported comparison: %#v", result.Items)
	}
	if writer.plan.Digest != result.Digest {
		t.Fatal("inconclusive result was not sealed")
	}
}

func TestSevenUniqueImagesBecomeExplicitLimitItemUT043(t *testing.T) {
	plan, base := matchedPlan()
	component := plan.Items[0].ComponentID
	origin := []Frame{base[0]}
	current := []Frame{base[1]}
	for i := range origin {
		origin[i].Component = component.String()
	}
	for i := range current {
		current[i].Component = component.String()
	}
	for i := 1; i < 3; i++ {
		frame := base[0]
		frame.ID = testID("origin-extra-" + string(rune('0'+i)))
		frame.Component = component.String()
		origin = append(origin, frame)
	}
	for i := 1; i < 4; i++ {
		frame := base[1]
		frame.ID = testID("current-extra-" + string(rune('0'+i)))
		frame.Component = component.String()
		current = append(current, frame)
	}
	pairs := []Pair{
		{ComponentID: component, OriginFrameID: origin[0].ID, CurrentFrameID: current[0].ID, Decision: SameView, Landmarks: []string{"edge"}},
		{ComponentID: component, OriginFrameID: origin[1].ID, CurrentFrameID: current[1].ID, Decision: SameView, Landmarks: []string{"edge"}},
		{ComponentID: component, OriginFrameID: origin[2].ID, CurrentFrameID: current[2].ID, Decision: SameView, Landmarks: []string{"edge"}},
		{ComponentID: component, OriginFrameID: origin[0].ID, CurrentFrameID: current[3].ID, Decision: SameView, Landmarks: []string{"edge"}},
	}
	reader := fakeFrames{values: map[identity.ID][]Frame{*plan.OriginPreparationID: origin, plan.CurrentPreparationID: current}}
	writer := &fakeWriter{}
	build, err := Setup(Dependencies{Frames: reader, Matcher: proposerStub(pairs), Verifier: verifierStub(pairs), Plans: writer})
	if err != nil {
		t.Fatal(err)
	}
	result, err := build(context.Background(), PlanRequest{Plan: plan, Components: []Component{{ID: component}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Outcome != TechnicalFailure || result.Items[0].Reason != "CORE_IMAGE_LIMIT_EXCEEDED" || len(result.Items[0].CoreGroups) != 0 || len(result.Items[0].OriginFrames) != 3 || len(result.Items[0].CurrentFrames) != 4 {
		t.Fatalf("seven-image proposal was dispatched or truncated: %#v", result.Items)
	}
	if writer.plan.Digest != result.Digest {
		t.Fatal("limit outcome was not sealed")
	}
}

func TestValidatePlanRejectsDuplicateMembershipAndCrossTenantFrames(t *testing.T) {
	plan, frames := matchedPlan()
	frames = append(frames, frames[1])
	if err := ValidatePlanWithFrames(plan, frames); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("expected duplicate frame rejection, got %v", err)
	}
	plan, frames = matchedPlan()
	frames[1].TenantID = testID("other-tenant")
	if err := ValidatePlanWithFrames(plan, frames); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("expected cross-tenant frame rejection, got %v", err)
	}
	plan, frames = matchedPlan()
	frames[1].Digest = "not-a-digest"
	if err := ValidatePlanWithFrames(plan, frames); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("expected invalid digest rejection, got %v", err)
	}
}

func TestValidatePlanRejectsItemDigestThatDoesNotMatchPinnedFrames(t *testing.T) {
	plan, frames := matchedPlan()
	plan.Items[0].InputDigest = digest
	if componentDigest([]Frame{frames[0]}, []Frame{frames[1]}) == digest {
		t.Fatal("fixture digest must differ from the canonical membership digest")
	}
	if err := ValidatePlanWithFrames(plan, frames); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("tampered item digest accepted: %v", err)
	}
}

func TestValidatePlanAllowsSharedOriginButRejectsRepeatedCurrentView(t *testing.T) {
	plan, _ := matchedPlan()
	item := &plan.Items[0]
	origin, component := item.OriginFrames[0], item.ComponentID
	second := testID("F12")
	item.CurrentFrames = append(item.CurrentFrames, second)
	item.CoreGroups[0] = append(item.CoreGroups[0], Pair{ComponentID: component, OriginFrameID: origin, CurrentFrameID: second, Decision: SameView})
	if err := ValidatePlan(plan); err != nil {
		t.Fatalf("one origin view anchoring two current views was rejected: %v", err)
	}
	repeated, _ := matchedPlan()
	repeated.Items[0].CoreGroups[0] = append(repeated.Items[0].CoreGroups[0], repeated.Items[0].CoreGroups[0][0])
	if err := ValidatePlan(repeated); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("repeated current view accepted: %v", err)
	}
	crossRole, _ := matchedPlan()
	crossItem := &crossRole.Items[0]
	crossItem.OriginFrames = append(crossItem.OriginFrames, crossItem.CurrentFrames[0])
	crossItem.CurrentFrames = append(crossItem.CurrentFrames, testID("F13"))
	crossItem.CoreGroups[0] = append(crossItem.CoreGroups[0], Pair{ComponentID: crossItem.ComponentID, OriginFrameID: crossItem.CurrentFrames[0], CurrentFrameID: testID("F13"), Decision: SameView})
	if err := ValidatePlan(crossRole); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("frame used as both origin and current accepted: %v", err)
	}
}

func TestChildPlanComparesSupplementWithParentCurrentFramesOfComponent(t *testing.T) {
	plan, frames := matchedPlan()
	plan.ID, plan.Generation = identity.NewID(), 2
	parentID := identity.NewID()
	plan.ParentPlanID = &parentID
	bathroomID := plan.Items[0].ComponentID
	plan.Items = nil
	origin, prior, supplement := *plan.OriginPreparationID, plan.CurrentPreparationID, identity.NewID()
	plan.SupplementPreparationID, plan.CurrentGeneration = &supplement, 2
	// In production the parent's current frame carries its own scope ID.
	priorFrame, supplementFrame := frames[1], frames[1]
	priorFrame.Component, priorFrame.ComponentLabel, priorFrame.View, priorFrame.Visible = identity.NewID().String(), "shower", "wide", true
	supplementFrame.ID, supplementFrame.PreparationID, supplementFrame.Generation = identity.NewID(), supplement, 2
	supplementFrame.Component, supplementFrame.ComponentLabel, supplementFrame.View, supplementFrame.Visible = identity.NewID().String(), "shower", "wide", true
	pair := Pair{ComponentID: bathroomID, OriginFrameID: priorFrame.ID, CurrentFrameID: supplementFrame.ID, Decision: SameView, Landmarks: []string{"bathroom doorway"}}
	build, err := Setup(Dependencies{Frames: fakeFrames{values: map[identity.ID][]Frame{origin: {frames[0]}, prior: {priorFrame}, supplement: {supplementFrame}}}, Matcher: proposerStub{pair}, Verifier: verifierStub{pair}, Plans: &fakeWriter{}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := build(context.Background(), PlanRequest{Plan: plan, Components: []Component{{ID: bathroomID, PriorFrameIDs: []identity.ID{priorFrame.ID}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Outcome != Matched || !containsID(result.Items[0].OriginFrames, priorFrame.ID) || !containsID(result.Items[0].CurrentFrames, supplementFrame.ID) {
		t.Fatalf("child plan did not compare the supplement with the parent's component frame: %+v", result.Items)
	}
	// Without the parent's frames the component has no comparison base.
	unlinkedBuild, err := Setup(Dependencies{Frames: fakeFrames{values: map[identity.ID][]Frame{origin: {frames[0]}, prior: {priorFrame}, supplement: {supplementFrame}}}, Matcher: proposerStub{}, Verifier: verifierStub{}, Plans: &fakeWriter{}})
	if err != nil {
		t.Fatal(err)
	}
	unlinked, err := unlinkedBuild(context.Background(), PlanRequest{Plan: plan, Components: []Component{{ID: bathroomID}}})
	if err != nil {
		t.Fatal(err)
	}
	if unlinked.Items[0].Outcome == Matched {
		t.Fatalf("child plan matched without the parent frame link: %+v", unlinked.Items)
	}
}
