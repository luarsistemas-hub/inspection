package core

import (
	"strings"
	"testing"
)

func validSnapshot() Snapshot {
	return Snapshot{SchemaVersion: 1, ReportID: "report-1", InspectionID: "inspection-1", TemplateVersionID: "template-v1", ReferenceVersionID: "reference-v1", ProfileVersionID: "profile-v1", Mode: "HISTORICAL", Classification: "ATTENTION", Advisory: "Internal advisory triage; not a finding of fault."}
}

func TestSnapshotRejectsMutableURLs(t *testing.T) {
	snapshot := validSnapshot()
	snapshot.Coverage = map[string]string{"evidence": "https://object-store/private"}
	if _, _, err := CanonicalJSON(snapshot); err == nil {
		t.Fatal("mutable URL accepted in immutable snapshot")
	}
}

func TestCanonicalJSONIsStable(t *testing.T) {
	first, firstDigest, err := CanonicalJSON(validSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	second, secondDigest, err := CanonicalJSON(validSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || firstDigest != secondDigest {
		t.Fatal("canonical snapshot changed")
	}
}

func TestHTMLIsInternalAndEscaped(t *testing.T) {
	snapshot := validSnapshot()
	snapshot.Advisory = "<script>unsafe</script>"
	html, err := HTML(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(html), "<script>") {
		t.Fatal("unescaped report text")
	}
	if !strings.Contains(string(html), "Internal advisory inspection report") {
		t.Fatal("missing internal advisory marker")
	}
}
