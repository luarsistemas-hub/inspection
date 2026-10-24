package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTemplateSchemaV2_UT090ToUT095(t *testing.T) {
	refs := testRefs{}
	t.Run("UT090 schema v2 defaults", func(t *testing.T) {
		doc := validDocument()
		doc.SchemaVersion = 2
		doc.Requirements = nil
		doc.CaptureLayout = "ENVIRONMENTS"
		compiled, err := Compile(encode(t, doc), refs)
		if err != nil {
			t.Fatal(err)
		}
		if compiled.Document.EnvironmentCapture == nil || compiled.Document.EnvironmentCapture.Photo.Minimum != 1 || compiled.Document.EnvironmentCapture.Photo.Maximum != 20 || compiled.Document.EnvironmentCapture.Video.MaximumClips != 3 {
			t.Fatalf("unexpected defaults: %+v", compiled.Document.EnvironmentCapture)
		}
	})
	t.Run("UT091 v2 rejects requirements", func(t *testing.T) {
		doc := validDocument()
		doc.SchemaVersion = 2
		doc.CaptureLayout = "ENVIRONMENTS"
		if _, err := Compile(encode(t, doc), refs); err == nil || !strings.Contains(err.Error(), "requirements") {
			t.Fatalf("wanted requirements error, got %v", err)
		}
	})
	t.Run("UT092 capture limits", func(t *testing.T) {
		for name, mutate := range map[string]func(*TemplateDocument){"photo maximum": func(d *TemplateDocument) {
			d.EnvironmentCapture = DefaultEnvironmentCapture()
			d.EnvironmentCapture.Photo.Maximum = 21
		}, "video maximum": func(d *TemplateDocument) {
			d.EnvironmentCapture = DefaultEnvironmentCapture()
			d.EnvironmentCapture.Video.MaximumClips = 4
		}} {
			t.Run(name, func(t *testing.T) {
				doc := validDocument()
				doc.SchemaVersion = 2
				doc.Requirements = nil
				doc.CaptureLayout = "ENVIRONMENTS"
				mutate(&doc)
				if _, err := Compile(encode(t, doc), refs); err == nil {
					t.Fatal("invalid limit accepted")
				}
			})
		}
	})
	t.Run("v1 rejects v2 fields and partial v2 limits use defaults", func(t *testing.T) {
		v1 := validDocument()
		v1.CaptureLayout = "ENVIRONMENTS"
		if _, err := Compile(encode(t, v1), refs); err == nil {
			t.Fatal("v1 accepted v2 capture fields")
		}
		v2 := validDocument()
		v2.SchemaVersion, v2.Requirements, v2.CaptureLayout = 2, nil, "ENVIRONMENTS"
		limits := &EnvironmentCapture{}
		limits.Photo.Maximum = 10
		v2.EnvironmentCapture = limits
		compiled, err := Compile(encode(t, v2), refs)
		if err != nil {
			t.Fatal(err)
		}
		if compiled.Document.EnvironmentCapture.Photo.Minimum != 1 || compiled.Document.EnvironmentCapture.Photo.Maximum != 10 || compiled.Document.EnvironmentCapture.Video.MaximumClips != 3 {
			t.Fatalf("partial defaults not applied: %+v", compiled.Document.EnvironmentCapture)
		}
	})
	t.Run("UT093 v1 canonical digest remains stable", func(t *testing.T) {
		doc := validDocument()
		got, err := Compile(encode(t, doc), refs)
		if err != nil {
			t.Fatal(err)
		}
		if got.Digest != "0ef69bb09272476a66d8f0a7a456a7ef1711485e4738effc04fc2ac00b55df35" {
			t.Fatalf("v1 canonical digest changed: %s", got.Digest)
		}
	})
	t.Run("UT094 unknown suggested environment rejected", func(t *testing.T) {
		doc := validDocument()
		doc.SchemaVersion = 2
		doc.Requirements = nil
		doc.CaptureLayout = "ENVIRONMENTS"
		doc.SuggestedEnvironmentTypes = []string{"GARDEN_X"}
		if _, err := Compile(encode(t, doc), refs); err == nil {
			t.Fatal("unknown type accepted")
		}
	})
	t.Run("UT095 curated v2 seeds compile", func(t *testing.T) {
		seeds := CuratedSeeds("REAL_ESTATE", "property-v1", "construction-v1", "cleaning-v1")
		if err := ValidateSeeds(seeds, refs); err != nil {
			t.Fatal(err)
		}
		for _, seed := range seeds {
			if seed.Document.SchemaVersion != 2 || seed.Document.CaptureLayout != "ENVIRONMENTS" {
				t.Errorf("seed %s is not v2", seed.TemplateKey)
			}
		}
	})
}

type testRefs struct{}

func (testRefs) SegmentExists(value string) bool {
	return value == "segment-v1" || value == "property-v1" || value == "construction-v1" || value == "cleaning-v1"
}
func (testRefs) AnalysisTypeExists(value string) bool { return value == "REAL_ESTATE" }

func validDocument() TemplateDocument {
	return TemplateDocument{SchemaVersion: 1, SegmentVersionID: "segment-v1", ParticipantRoles: []string{"TENANT_PARTICIPANT"}, DefaultComparisonMode: ChecklistOnly, Requirements: []CaptureRequirement{{Key: "front", Section: "outside", Label: "Front", EvidenceKind: "PHOTO", MinimumCount: 1, MaximumCount: 2, Required: true, DescriptionRequired: true, CaptureSourcePolicy: "CAMERA_DEFAULT", Applicability: `asset.kind == "house"`}}, AnalysisType: "REAL_ESTATE", Policy: Policy{GPSRequired: true, GeofenceMeters: 150, AllowGallery: true}}
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
	cases := map[string]func(*TemplateDocument){"no requirements": func(v *TemplateDocument) { v.Requirements = nil }, "unsafe expression": func(v *TemplateDocument) { v.Requirements[0].Applicability = "system.exec{}" }, "unknown segment": func(v *TemplateDocument) { v.SegmentVersionID = "unknown" }, "invalid default comparison mode": func(v *TemplateDocument) { v.DefaultComparisonMode = ComparisonMode("UNKNOWN") }, "requirement limit": func(v *TemplateDocument) { v.Requirements = make([]CaptureRequirement, MaxRequirements+1) }}
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

func TestVideoTemplateContractUT001UT002(t *testing.T) {
	doc := validDocument()
	doc.IsVideo = true
	compiled, err := Compile(encode(t, doc), testRefs{})
	if err != nil || compiled.Document.MediaKind() != "VIDEO" || !compiled.Document.IsVideo {
		t.Fatalf("video modality did not compile: %#v %v", compiled.Document, err)
	}
	if compiled.Document.Requirements[0].EvidenceKind != "PHOTO" {
		t.Fatal("video modality must not rewrite participant requirements into photos")
	}
	if compiled.Digest != "ff325bc78f14cea68c22e16e0c38f886f4c47d744d8ae0a298b72fc3687dca95" {
		t.Fatalf("v1 video canonical digest changed: %s", compiled.Digest)
	}
	withoutFlag := validDocument()
	photo, err := Compile(encode(t, withoutFlag), testRefs{})
	if err != nil || photo.Document.MediaKind() != "PHOTO" || photo.Document.IsVideo {
		t.Fatalf("missing isVideo did not default to PHOTO: %#v %v", photo.Document, err)
	}
	payload := strings.Replace(string(encode(t, withoutFlag)), `"schemaVersion":1`, `"schemaVersion":1,"videoMode":true`, 1)
	if _, err := Compile([]byte(payload), testRefs{}); err == nil {
		t.Fatal("unknown videoMode field weakened strict schema validation")
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
	seeds := CuratedSeeds("REAL_ESTATE", "segment-v1", "segment-v1", "segment-v1")
	if err := ValidateSeeds(seeds, testRefs{}); err != nil {
		t.Fatal(err)
	}
	for _, seed := range seeds {
		if seed.Document.DefaultComparisonMode != ChecklistOnly {
			t.Fatal("workflow defaults should not vary with checklist content")
		}
	}
}
