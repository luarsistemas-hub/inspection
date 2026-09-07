package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

type testRefs struct{}

func (testRefs) SegmentExists(value string) bool         { return value == "segment-v1" }
func (testRefs) AnalysisProfileExists(value string) bool { return value == "profile-v1" }

func validDocument() TemplateDocument {
	return TemplateDocument{SchemaVersion: 1, SegmentVersionID: "segment-v1", ParticipantRoles: []string{"TENANT_PARTICIPANT"}, ComparisonMode: FixedOrigin, Requirements: []CaptureRequirement{{Key: "front", Section: "outside", Label: "Front", EvidenceKind: "PHOTO", MinimumCount: 1, MaximumCount: 2, Required: true, DescriptionRequired: true, CaptureSourcePolicy: "CAMERA_DEFAULT", ComparisonTarget: FixedOrigin, Applicability: `asset.kind == "house"`}}, ReportMode: "HISTORICAL", AnalysisProfile: "profile-v1", Policy: Policy{GPSRequired: true, GeofenceMeters: 150, AllowGallery: true}}
}
func encode(t *testing.T, v any) []byte {
	t.Helper()
	payload, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestTemplateCompilerUT024(t *testing.T) {
	payload := encode(t, validDocument())
	first, err := Compile(payload, testRefs{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compile(payload, testRefs{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest == "" || first.Digest != second.Digest || string(first.Canonical) != string(second.Canonical) {
		t.Fatal("canonical compilation is not deterministic")
	}
}

func TestTemplateCompilerUT025(t *testing.T) {
	cases := map[string]func(*TemplateDocument){"no requirements": func(v *TemplateDocument) { v.Requirements = nil }, "unsafe expression": func(v *TemplateDocument) { v.Requirements[0].Applicability = "system.exec{}" }, "unknown segment": func(v *TemplateDocument) { v.SegmentVersionID = "unknown" }, "incompatible mode": func(v *TemplateDocument) { v.Requirements[0].ComparisonTarget = ChecklistOnly }, "requirement limit": func(v *TemplateDocument) { v.Requirements = make([]CaptureRequirement, MaxRequirements+1) }}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			doc := validDocument()
			mutate(&doc)
			if _, err := Compile(encode(t, doc), testRefs{}); err == nil {
				t.Fatal("invalid template accepted")
			}
		})
	}
}

func TestPolicyResolverUT026UT027(t *testing.T) {
	optional := false
	radius := 250
	snapshot, err := ResolvePolicy(Policy{GPSRequired: true, GeofenceMeters: 150, AllowGallery: true}, PolicyOverride{GPSRequired: &optional, GeofenceMeters: &radius}, FixedOrigin, "origin-v1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.GPSRequired || snapshot.GeofenceMeters != 250 || snapshot.ReferenceID != "origin-v1" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if _, err := ResolvePolicy(Policy{GPSRequired: true, GeofenceMeters: 150}, PolicyOverride{}, FixedOrigin, ""); err == nil {
		t.Fatal("missing reference accepted")
	}
	if _, err := ResolvePolicy(Policy{}, PolicyOverride{}, ComparisonMode("UNKNOWN"), "ref"); err == nil {
		t.Fatal("conflicting mode accepted")
	}
}

func TestCapacityBoundaryUT062(t *testing.T) {
	if err := ValidateCapacity(MaxAssetsPerTenant, MaxInspections, MaxActivePhotos, MaxOriginalBytes); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		assets, inspections, photos int
		bytes                       int64
	}{{MaxAssetsPerTenant + 1, 0, 0, 0}, {0, MaxInspections + 1, 0, 0}, {0, 0, MaxActivePhotos + 1, 0}, {0, 0, 0, MaxOriginalBytes + 1}} {
		if ValidateCapacity(test.assets, test.inspections, test.photos, test.bytes) == nil {
			t.Fatal("over-capacity value accepted")
		}
	}
}

func TestUnicodeAndDocumentLimitsUT072(t *testing.T) {
	if !ValidName(strings.Repeat("á", MaxNameCodePoints)) || ValidName(strings.Repeat("á", MaxNameCodePoints+1)) {
		t.Fatal("name boundary incorrect")
	}
	if !ValidDescription(strings.Repeat("ç", MaxTextCodePoints)) || ValidDescription(strings.Repeat("ç", MaxTextCodePoints+1)) {
		t.Fatal("description boundary incorrect")
	}
	payload := make([]byte, MaxTemplateBytes)
	if _, err := Compile(payload, testRefs{}); err == nil || !strings.Contains(err.Error(), "invalid template") {
		t.Fatal("one MiB malformed payload must fail schema validation")
	}
	if _, err := Compile(make([]byte, MaxTemplateBytes+1), testRefs{}); err == nil {
		t.Fatal("oversized template accepted")
	}
}

func TestStoragePolicyUT073(t *testing.T) {
	meter := StorageMeter{}
	if err := meter.Add(1 << 40); err != nil {
		t.Fatal("commercial byte quota imposed")
	}
	if meter.Bytes != 1<<40 {
		t.Fatal("meter did not retain bytes")
	}
	if err := meter.Add(-1); err == nil {
		t.Fatal("negative usage accepted")
	}
	if err := ValidateCapacity(MaxAssetsPerTenant, MaxInspections, MaxActivePhotos, MaxOriginalBytes); err != nil {
		t.Fatal(err)
	}
}

func TestCuratedSeedsPropertyConstructionCleaning(t *testing.T) {
	seeds := CuratedSeeds("profile-v1", "segment-v1", "segment-v1", "segment-v1")
	if err := ValidateSeeds(seeds, testRefs{}); err != nil {
		t.Fatal(err)
	}
	if seeds[0].Document.MultiStage || !seeds[1].Document.MultiStage || !seeds[2].Document.MultiStage {
		t.Fatal("multi-stage flags are not independent")
	}
	if seeds[1].Document.ReportMode == seeds[2].Document.ReportMode {
		t.Fatal("report mode seeds must demonstrate both modes")
	}
}
