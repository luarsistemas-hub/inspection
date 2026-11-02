package render_pdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	analysis "inspection/services/inspection/internal/features/analysis/core"
	"inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/pdf"

	"inspection/libs/identity"
)

// TestAIP_UT_015MarotoRendererComposesCanonicalSnapshotAssetsWithinTimeoutAndReturnsBytes
// covers UT-015 of autonomous-inspection-platform, adapted to the current
// renderer: PDFs are composed in process by Maroto from the canonical snapshot
// plus request-authorized embedded assets, under the worker timeout, and the
// caller receives the PDF bytes (the earlier Gotenberg HTTP contract was
// removed).
func TestAIP_UT_015MarotoRendererComposesCanonicalSnapshotAssetsWithinTimeoutAndReturnsBytes(t *testing.T) {
	snapshot := validPDFSnapshot()
	snapshot.Advisory = "Esta triagem interna não atribui culpa. <script>alert(1)</script> & \"aspas\""
	snapshot.Evidence = []core.Evidence{{ID: "current-photo", RequirementKey: "roof", Role: "CURRENT", Description: "Vistoria atual"}}
	document := PDFDocument{SnapshotID: "snapshot-1", Version: 3, Snapshot: snapshot, Internal: true}

	without, err := (MarotoRenderer{}).Render(context.Background(), document, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	with, err := (MarotoRenderer{}).Render(ctx, document, pdf.AssetBundle{"evidence-current-photo.jpg": jpegBytes(t, 640, 360)})
	if err != nil {
		t.Fatalf("render under a deadline failed: %v", err)
	}
	for name, data := range map[string][]byte{"without assets": without, "with assets": with} {
		if !bytes.HasPrefix(data, []byte("%PDF-")) || PageCount(data) < 1 {
			t.Fatalf("%s output is not a PDF with pages (len=%d)", name, len(data))
		}
	}
	if len(with) <= len(without) {
		t.Fatalf("embedded evidence image was not written to the PDF: %d <= %d", len(with), len(without))
	}
	// Markup in canonical text is data, never interpreted: it cannot add pages
	// or abort the rendering, and the same input renders to a PDF each time.
	if again, err := (MarotoRenderer{}).Render(context.Background(), document, nil); err != nil || PageCount(again) != PageCount(without) {
		t.Fatalf("repeated render changed: pages %d vs %d err=%v", PageCount(again), PageCount(without), err)
	}
	// The worker always bounds rendering with a positive timeout shorter than
	// its lease.
	limits := DefaultLimits()
	if limits.Timeout <= 0 || limits.LeaseDuration <= limits.Timeout {
		t.Fatalf("default limits do not bound rendering: %+v", limits)
	}
}

// TestAIP_UT_016MarotoRendererRejectsExternalAssetsPartialOutputAndTimeout
// covers UT-016, adapted to Maroto: external/mutable asset URLs never enter the
// document, partial or oversized output is refused and an expired deadline
// aborts the render with a retryable error.
func TestAIP_UT_016MarotoRendererRejectsExternalAssetsPartialOutputAndTimeout(t *testing.T) {
	base := func() PDFDocument {
		snapshot := validPDFSnapshot()
		snapshot.Evidence = []core.Evidence{{ID: "current-photo", RequirementKey: "roof", Role: "CURRENT"}}
		return PDFDocument{SnapshotID: "snapshot-1", Version: 1, Snapshot: snapshot, Internal: true}
	}
	t.Run("external asset URL in the canonical snapshot", func(t *testing.T) {
		document := base()
		document.Snapshot.Coverage = map[string]string{"evidence": "https://cdn.example.test/photo.jpg"}
		if _, err := (MarotoRenderer{}).Render(context.Background(), document, nil); err == nil {
			t.Fatal("snapshot with an external URL was rendered")
		}
		document = base()
		document.Snapshot.Evidence[0].Flags = []string{"http://169.254.169.254/latest/meta-data"}
		if _, err := (MarotoRenderer{}).Render(context.Background(), document, nil); err == nil {
			t.Fatal("evidence flag with an external URL was rendered")
		}
	})
	t.Run("asset bundle carrying a URL instead of image bytes", func(t *testing.T) {
		for _, payload := range []string{"https://cdn.example.test/photo.jpg", "data:image/png;base64,AAAA", "file:///etc/passwd"} {
			_, err := (MarotoRenderer{}).Render(context.Background(), base(), pdf.AssetBundle{"evidence-current-photo.jpg": []byte(payload)})
			if err == nil || !strings.Contains(err.Error(), "invalid evidence image") {
				t.Fatalf("payload %q was not rejected as an invalid image: %v", payload, err)
			}
		}
	})
	t.Run("asset limits and oversized or partial output", func(t *testing.T) {
		image := jpegBytes(t, 320, 240)
		if _, err := (MarotoRenderer{MaxImageBytes: int64(len(image) - 1)}).Render(context.Background(), base(), pdf.AssetBundle{"evidence-current-photo.jpg": image}); err == nil {
			t.Fatal("image bytes above the limit were rendered")
		}
		if _, err := (MarotoRenderer{MaxPDFBytes: 64}).Render(context.Background(), base(), nil); err == nil {
			t.Fatal("PDF above the byte limit was returned")
		}
		long := base()
		long.Snapshot.Advisory = strings.Repeat("Texto extenso de vistoria. ", 4000)
		if _, err := (MarotoRenderer{MaxPages: 1}).Render(context.Background(), long, nil); err == nil {
			t.Fatal("PDF above the page limit was returned")
		}
		if _, err := (MarotoRenderer{MaxEvidence: 0}).Render(context.Background(), PDFDocument{}, nil); err == nil {
			t.Fatal("incomplete document was rendered")
		}
	})
	t.Run("expired deadline aborts the render", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
		defer cancel()
		<-ctx.Done()
		data, err := (MarotoRenderer{}).Render(ctx, base(), nil)
		if !errors.Is(err, context.DeadlineExceeded) || data != nil {
			t.Fatalf("expired deadline returned %d bytes and %v", len(data), err)
		}
	})
	t.Run("stored image that no longer matches its digest is a permanent failure", func(t *testing.T) {
		// Partial (truncated) object reads never reach the renderer: the
		// executor verifies the digest of the bytes it read.
		env := newTestEnv(t)
		data := jpegBytes(t, 40, 30)
		mediaID := identity.NewID()
		key, hash := objectstore.DerivativeKey(env.tenantID, mediaID, "DISPLAY", data)
		env.client.objects[key] = data[:len(data)/2]
		if err := env.db.Create(&database.MediaDerivative{ID: identity.NewID(), TenantID: env.tenantID, MediaID: mediaID, ObjectKey: key, Kind: "DISPLAY", SHA256: hash, ContentType: "image/jpeg", Width: 40, Height: 30, SizeBytes: int64(len(data))}).Error; err != nil {
			t.Fatal(err)
		}
		snapshot := validPDFSnapshot()
		snapshot.Evidence = []core.Evidence{{ID: mediaID.String(), RequirementKey: "roof", Role: "CURRENT", DisplayDigest: hash}}
		if _, err := env.executor.loadDisplayAssets(context.Background(), env.tenantID, snapshot); !errors.Is(err, errPermanentPDF) {
			t.Fatalf("truncated stored image err = %v", err)
		}
	})
	t.Run("evidence reference that is an external URL is not an asset", func(t *testing.T) {
		env := newTestEnv(t)
		snapshot := validPDFSnapshot()
		snapshot.Evidence = []core.Evidence{{ID: "https://cdn.example.test/photo.jpg", RequirementKey: "roof", Role: "CURRENT"}}
		if _, err := env.executor.loadDisplayAssets(context.Background(), env.tenantID, snapshot); !errors.Is(err, errPermanentPDF) {
			t.Fatalf("URL evidence reference err = %v", err)
		}
	})
}

// aipClassifierReasonCodes collects every reason code the deterministic
// classifier can emit, by exercising each condition once. The same derivation
// lives in reports/core tests so neither vocabulary depends on a fixed list.
func aipClassifierReasonCodes() []string {
	all := []analysis.ComparisonFacts{{}, {Terminal: true, Inconclusive: true, TechnicalFailure: true, Missing: true, Skipped: true, Flagged: true, Uncorrected: true, Findings: []analysis.Finding{{Severity: "LOW"}, {Severity: "CRITICAL"}}}}
	decisions := []analysis.Decision{analysis.Classify(nil), analysis.Classify(all)}
	decisions = append(decisions, analysis.ClassifyInventory(analysis.Decision{Classification: analysis.ClassificationNormal}, analysis.InventoryFacts{Missing: 1, Worsened: 1, NotObserved: 1, EnvironmentsNotCaptured: 1}))
	seen := map[string]bool{}
	var codes []string
	for _, decision := range decisions {
		for _, code := range decision.ReasonCodes {
			if !seen[code] {
				seen[code] = true
				codes = append(codes, code)
			}
		}
	}
	return codes
}

// TestAIP_IT_262PDFExplainsEveryClassifierReasonCodeInPortuguese keeps the PDF
// vocabulary in step with the classifier: every code the classifier emits is
// explained by the shared core.ReasonLabel (the PDF prints that text, never a
// raw code) and a PDF carrying all of them renders.
func TestAIP_IT_262PDFExplainsEveryClassifierReasonCodeInPortuguese(t *testing.T) {
	codes := aipClassifierReasonCodes()
	if len(codes) != 15 {
		t.Fatalf("classifier emits %d reason codes, want the 15 of the report vocabulary: %v", len(codes), codes)
	}
	for _, code := range codes {
		if got := core.ReasonLabel(code); got == "Situação não reconhecida" || got == code || got == strings.ReplaceAll(strings.ToLower(code), "_", " ") {
			t.Errorf("reason %s is not explained in Portuguese (%q)", code, got)
		}
	}
	snapshot := validPDFSnapshot()
	snapshot.ReasonCodes = codes
	data, err := (MarotoRenderer{}).Render(context.Background(), PDFDocument{SnapshotID: "snapshot-1", Version: 1, Snapshot: snapshot, Internal: true}, nil)
	if err != nil || PageCount(data) < 1 {
		t.Fatalf("report with explained reasons did not render: %v", err)
	}
}

// TestAIP_IT_263EvidenceOrTextBeyondOnePagePaginatesWithoutDroppingContent
// covers IT-263: long texts and many evidence items grow the document by
// pages, every evidence image is embedded once and the canonical HTML keeps
// every finding.
func TestAIP_IT_263EvidenceOrTextBeyondOnePagePaginatesWithoutDroppingContent(t *testing.T) {
	const evidenceItems, findingCount = 36, 60
	snapshot := validPDFSnapshot()
	snapshot.Advisory = strings.Repeat("Descrição extensa com acentuação: vistoria, conservação e segurança. ", 700)
	assets := pdf.AssetBundle{}
	var titles []string
	for i := 0; i < evidenceItems; i++ {
		id := fmt.Sprintf("photo-%03d", i)
		snapshot.Evidence = append(snapshot.Evidence, core.Evidence{ID: id, RequirementKey: "roof", Role: "CURRENT", Description: "Vistoria atual " + id})
		assets["evidence-"+id+".jpg"] = jpegBytes(t, 120+i, 90)
	}
	for i := 0; i < findingCount; i++ {
		title := fmt.Sprintf("Constatação %03d", i)
		titles = append(titles, title)
		snapshot.Findings = append(snapshot.Findings, core.Finding{Category: "CONSERVATION", Title: title, Description: strings.Repeat("Alteração visual observada no telhado. ", 40), Severity: "LOW", Confidence: .9, EvidenceIDs: []string{"photo-000"}, Quality: "ADEQUATE", RecommendedAction: "REVIEW"})
	}
	small := validPDFSnapshot()
	smallPDF, err := (MarotoRenderer{}).Render(context.Background(), PDFDocument{SnapshotID: "snapshot-1", Version: 1, Snapshot: small, Internal: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	large, err := (MarotoRenderer{}).Render(context.Background(), PDFDocument{SnapshotID: "snapshot-1", Version: 1, Snapshot: snapshot, Internal: true}, assets)
	if err != nil {
		t.Fatal(err)
	}
	if pages := PageCount(large); pages < 10 || pages <= PageCount(smallPDF) {
		t.Fatalf("large report has %d pages (small report %d); it must paginate", pages, PageCount(smallPDF))
	}
	if images := bytes.Count(large, []byte("/Subtype /Image")); images < evidenceItems {
		t.Fatalf("only %d of %d evidence images were embedded", images, evidenceItems)
	}
	html, err := core.HTML(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range titles {
		if !strings.Contains(string(html), title) {
			t.Fatalf("finding %q was dropped from the report", title)
		}
	}
	// Past the configured limits the render is refused instead of silently truncating.
	if _, err := (MarotoRenderer{MaxEvidence: evidenceItems - 1}).Render(context.Background(), PDFDocument{SnapshotID: "snapshot-1", Version: 1, Snapshot: snapshot, Internal: true}, assets); err == nil {
		t.Fatal("a report beyond the evidence limit was rendered with dropped evidence")
	}
}

// TestAIP_IT_266PDFFailureStaysVisibleAndBoundedWithoutChangingTheAnalysis
// covers IT-266: a failing PDF job stays queued and visible while it retries
// within the configured bound, then either becomes a PDF or a recorded
// terminal failure, and the immutable snapshot (analysis and classification)
// never changes.
func TestAIP_IT_266PDFFailureStaysVisibleAndBoundedWithoutChangingTheAnalysis(t *testing.T) {
	t.Run("recovers within the retry bound", func(t *testing.T) {
		env := newTestEnv(t)
		before := env.snapshot
		env.client.putErr = errors.New("storage unavailable")
		job := env.addJob(t, "PDF", "QUEUED", 0)
		for attempt := 0; attempt < 2; attempt++ {
			if _, err := env.executor.RunDue(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := env.job(t, job.ID)
			if got.Status != "QUEUED" || got.Attempts != attempt+1 || got.LastError == "" || got.ArtifactID != nil {
				t.Fatalf("attempt %d: job = %+v, want a visible queued retry", attempt+1, got)
			}
			*env.clock = got.NextAttemptAt
		}
		env.client.putErr = nil
		if _, err := env.executor.RunDue(context.Background()); err != nil {
			t.Fatal(err)
		}
		got := env.job(t, job.ID)
		if got.Status != "READY" || got.ArtifactID == nil || got.Attempts != 3 {
			t.Fatalf("job after recovery = %+v", got)
		}
		assertSnapshotUnchanged(t, env, before)
	})
	t.Run("ends as a recorded failure", func(t *testing.T) {
		env := newTestEnv(t)
		before := env.snapshot
		env.client.putErr = errors.New("storage unavailable")
		job := env.addJob(t, "PDF", "QUEUED", 0)
		for attempt := 0; attempt < env.executor.limits.MaxAttempts; attempt++ {
			if _, err := env.executor.RunDue(context.Background()); err != nil {
				t.Fatal(err)
			}
			*env.clock = env.job(t, job.ID).NextAttemptAt
		}
		got := env.job(t, job.ID)
		if got.Status != "FAILED" || got.Attempts != env.executor.limits.MaxAttempts || got.ArtifactID != nil {
			t.Fatalf("terminal job = %+v", got)
		}
		if worked, err := env.executor.RunDue(context.Background()); err != nil || worked {
			t.Fatalf("a failed job must not be retried forever: worked=%v err=%v", worked, err)
		}
		if statuses := env.outboxStatuses(t); len(statuses) != 1 || statuses[0] != "report.ready.v1:PDF:FAILED" {
			t.Fatalf("failure was not announced once: %v", statuses)
		}
		assertSnapshotUnchanged(t, env, before)
	})
}

func assertSnapshotUnchanged(t *testing.T, env *testEnv, before database.ReportSnapshot) {
	t.Helper()
	var after database.ReportSnapshot
	if err := env.db.First(&after, "id=?", before.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.Classification != before.Classification || after.JSONDigest != before.JSONDigest || after.HTMLDigest != before.HTMLDigest || !bytes.Equal(after.CanonicalJSON, before.CanonicalJSON) || after.VersionNumber != before.VersionNumber {
		t.Fatalf("a PDF failure changed the immutable snapshot: %+v -> %+v", before, after)
	}
}
