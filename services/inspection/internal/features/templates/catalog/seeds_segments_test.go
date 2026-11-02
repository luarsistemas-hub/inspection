package catalog

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// segmentSeeds returns the curated seeds of the three segments, which share
// one segment version in these tests so that only their own contract differs.
func segmentSeeds() []Seed {
	return CuratedSeeds("REAL_ESTATE", "segment-v1", "segment-v1", "segment-v1")
}

func mustCompileSeed(t *testing.T, document TemplateDocument) error {
	t.Helper()
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compile(payload, testRefs{})
	return err
}

// IT-321 (construction) and IT-331 (cleaning): invalid evidence limits never
// compile into a template, so no segment can publish a requirement that cannot
// be satisfied; the valid seed itself compiles.
func TestAIP_IT321IT331SeedsRejectInvalidEvidenceLimits(t *testing.T) {
	for _, seed := range segmentSeeds() {
		t.Run(seed.SegmentKey, func(t *testing.T) {
			if err := mustCompileSeed(t, seed.Document); err != nil {
				t.Fatalf("curated seed does not compile: %v", err)
			}
			mutations := map[string]func(*TemplateDocument){
				"minimum above maximum": func(d *TemplateDocument) {
					d.EnvironmentCapture.Photo.Minimum, d.EnvironmentCapture.Photo.Maximum = 5, 2
				},
				"photos above the cap": func(d *TemplateDocument) { d.EnvironmentCapture.Photo.Maximum = MaxEnvironmentPhotos + 1 },
				"negative minimum":     func(d *TemplateDocument) { d.EnvironmentCapture.Photo.Minimum = -1 },
				"clips above the cap":  func(d *TemplateDocument) { d.EnvironmentCapture.Video.MaximumClips = MaxEnvironmentClips + 1 },
				"unknown environment type": func(d *TemplateDocument) {
					d.SuggestedEnvironmentTypes = append(d.SuggestedEnvironmentTypes, "MOON_BASE")
				},
				"duplicate environment": func(d *TemplateDocument) {
					d.SuggestedEnvironmentTypes = append(d.SuggestedEnvironmentTypes, d.SuggestedEnvironmentTypes[0])
				},
				"no participant role":     func(d *TemplateDocument) { d.ParticipantRoles = nil },
				"unknown comparison mode": func(d *TemplateDocument) { d.DefaultComparisonMode = "GUESS" },
				"geofence out of range":   func(d *TemplateDocument) { d.Policy.GeofenceMeters = MaxGeofence + 1 },
				"free-form requirements":  func(d *TemplateDocument) { d.Requirements = []CaptureRequirement{{Key: "k"}} },
				"layout missing":          func(d *TemplateDocument) { d.CaptureLayout = "" },
			}
			for name, mutate := range mutations {
				copy := seed.Document
				limits := *seed.Document.EnvironmentCapture
				copy.EnvironmentCapture = &limits
				copy.SuggestedEnvironmentTypes = append([]string(nil), seed.Document.SuggestedEnvironmentTypes...)
				copy.ParticipantRoles = append([]string(nil), seed.Document.ParticipantRoles...)
				mutate(&copy)
				if err := mustCompileSeed(t, copy); err == nil {
					t.Errorf("%s: invalid template compiled", name)
				}
			}
		})
	}
}

// IT-322 (planned construction stage) and IT-332/IT-338 (cleaning): a stage
// that depends on an origin cannot start without a reference. The curated
// seeds are explicitly reference-free (CHECKLIST_ONLY, ADR-021), every other
// comparison mode needs one.
func TestAIP_IT322IT332OnlyReferenceFreeSeedsStartWithoutAReference(t *testing.T) {
	for _, seed := range segmentSeeds() {
		t.Run(seed.SegmentKey, func(t *testing.T) {
			snapshot, err := ResolvePolicy(seed.Document.Policy, PolicyOverride{}, seed.Document.DefaultComparisonMode, "")
			if err != nil || snapshot.ComparisonMode != ChecklistOnly || snapshot.ReferenceID != "" {
				t.Fatalf("reference-free seed policy = %+v err=%v", snapshot, err)
			}
			for _, mode := range []ComparisonMode{PlannedStage, FixedOrigin, PreviousInspection, BeforeAfter} {
				if _, err := ResolvePolicy(seed.Document.Policy, PolicyOverride{}, mode, ""); err == nil || !strings.Contains(err.Error(), "reference") {
					t.Errorf("%s without a reference err=%v, want a reference requirement", mode, err)
				}
				if resolved, err := ResolvePolicy(seed.Document.Policy, PolicyOverride{}, mode, "origin-v1"); err != nil || resolved.ReferenceID != "origin-v1" {
					t.Errorf("%s with a reference = %+v err=%v", mode, resolved, err)
				}
			}
		})
	}
}

// IT-323 (construction) and IT-333 (cleaning): the evidence limits that every
// seed freezes are the platform ceilings, and anything above the aggregate
// ceiling is rejected.
func TestAIP_IT323IT333SeedEvidenceLimitsAreThePlatformCeilings(t *testing.T) {
	for _, seed := range segmentSeeds() {
		t.Run(seed.SegmentKey, func(t *testing.T) {
			compiled, err := Compile(mustMarshal(t, seed.Document), testRefs{})
			if err != nil {
				t.Fatal(err)
			}
			limits := compiled.Document.EnvironmentCapture
			if limits.Photo.Minimum != 1 || limits.Photo.Maximum != MaxEnvironmentPhotos || limits.Video.MaximumClips != MaxEnvironmentClips {
				t.Fatalf("seed evidence limits = %+v, want 1..%d photos and %d clips", limits, MaxEnvironmentPhotos, MaxEnvironmentClips)
			}
			if err := ValidateCaptureCapacity(compiled.Document.SchemaVersion, 1, 1, MaxEnvironmentInspectionPhotos, 1); err != nil {
				t.Fatalf("aggregate ceiling rejected: %v", err)
			}
			err = ValidateCaptureCapacity(compiled.Document.SchemaVersion, 1, 1, MaxEnvironmentInspectionPhotos+1, 1)
			if err == nil || !strings.Contains(err.Error(), "capacity") {
				t.Fatalf("evidence above the aggregate ceiling err=%v, want a capacity rejection", err)
			}
			if ValidateCaptureCapacity(compiled.Document.SchemaVersion, 1, 1, 1, MaxOriginalBytes+1) == nil {
				t.Fatal("an original above the byte ceiling was accepted")
			}
		})
	}
}

// The three segments share one workflow contract and differ only by their own
// participant roles and suggested environments.
func TestAIP_SegmentSeedsDifferOnlyByRolesAndEnvironments(t *testing.T) {
	seeds := segmentSeeds()
	if len(seeds) != 3 {
		t.Fatalf("seeds = %d, want property, construction and cleaning", len(seeds))
	}
	neutral := func(document TemplateDocument) TemplateDocument {
		document.ParticipantRoles, document.SuggestedEnvironmentTypes = nil, nil
		return document
	}
	for _, seed := range seeds[1:] {
		if !reflect.DeepEqual(neutral(seeds[0].Document), neutral(seed.Document)) {
			t.Fatalf("%s differs from %s beyond roles and environments: %+v vs %+v", seed.SegmentKey, seeds[0].SegmentKey, seed.Document, seeds[0].Document)
		}
		if reflect.DeepEqual(seeds[0].Document.ParticipantRoles, seed.Document.ParticipantRoles) {
			t.Fatalf("%s kept the roles of %s", seed.SegmentKey, seeds[0].SegmentKey)
		}
	}
	for _, seed := range seeds {
		if seed.Document.AnalysisType != "REAL_ESTATE" || seed.Document.CaptureLayout != EnvironmentsLayout || seed.Document.SchemaVersion != EnvironmentSchemaVersion {
			t.Fatalf("%s left the shared analysis contract: %+v", seed.SegmentKey, seed.Document)
		}
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
