package render_pdf

// IT-019 (bytes): an existing media-only comparison report keeps its issued
// JSON, HTML and PDF presentation. The files under
// testdata/it019_head_media_only were written by HEAD's code (commit fba7de44,
// from a `git archive` copy, before report v3): the canonical JSON and HTML of
// versions 1 and 2 and, per PDF audience, the page content-stream digests and
// decoded text. The current code must read and render them unchanged.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/pdf"
)

func TestIT019HeadEraMediaOnlyReportBytesStayReadable(t *testing.T) {
	dir := filepath.Join("testdata", "it019_head_media_only")
	for _, version := range []int{1, 2} {
		name := fmt.Sprintf("v%d", version)
		issued, err := os.ReadFile(filepath.Join(dir, name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var snapshot reportcore.Snapshot
		if err := json.Unmarshal(issued, &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.SchemaVersion != 1 || snapshot.Comparison != nil || snapshot.Inventory != nil {
			t.Fatalf("%s is not the HEAD-era media-only shape", name)
		}
		canonical, _, err := reportcore.CanonicalJSON(snapshot)
		if err != nil || !bytes.Equal(canonical, issued) {
			t.Fatalf("%s canonical JSON changed (err=%v)", name, err)
		}
		html, err := reportcore.HTML(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		wantHTML, err := os.ReadFile(filepath.Join(dir, name+".html"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(html, wantHTML) {
			t.Fatalf("%s HTML differs from the HEAD-era bytes:\n got: %s\nwant: %s", name, html, wantHTML)
		}
		for _, audience := range []string{"PDF", "PDF_CUSTOMER"} {
			projected, internal, err := projectForAudience(snapshot, audience)
			if err != nil {
				t.Fatal(err)
			}
			data, err := (MarotoRenderer{DashboardOrigin: "https://dashboard.example"}).Render(context.Background(), PDFDocument{SnapshotID: "it019-snapshot", Version: version, Snapshot: projected, Internal: internal}, pdf.AssetBundle{})
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(dir, name+"_"+strings.ToLower(audience)+".golden"))
			if err != nil {
				t.Fatal(err)
			}
			if got := legacyPDFDump(t, data); got != string(want) {
				t.Fatalf("%s %s differs from the HEAD-era PDF:\n got: %s\nwant: %s", name, audience, got, want)
			}
		}
	}
}
