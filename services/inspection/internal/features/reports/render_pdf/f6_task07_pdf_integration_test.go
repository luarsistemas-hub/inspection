//go:build integration

package render_pdf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/pdf"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

func TestVV_IT_294RPT1PDFKeepsPinnedVideoEvidenceAndBathroomGap(t *testing.T) {
	snapshot := validPDFSnapshot()
	snapshot.Advisory = "Relatório de evidência fixada."
	snapshot.ScopeStatements = []string{"Banheiro não observado; conclusão limitada à cozinha."}
	snapshot.Requirements = []reportcore.Requirement{{Key: "video:kitchen", Section: "Vídeo", Label: "Cozinha · Pia", AnalysisMode: "COMPARE_ORIGIN_CURRENT", AnalysisStatus: "COMPLETED"}, {Key: "video:bathroom", Section: "Vídeo", Label: "Banheiro · Lacuna", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "INCONCLUSIVE", Coverage: "Não observado"}}
	pts, ms := int64(70_123_456), int64(70_123)
	snapshot.Evidence = []reportcore.Evidence{{ID: "current-frame-001", RequirementKey: "video:kitchen", Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: "current-frame-001", SourceMediaID: "source-current-001", PreparationID: "prep-current-001", PTSMicros: &pts, TimestampMS: &ms, FrameDigest: strings.Repeat("a", 64), Availability: "AVAILABLE"}, {ID: "origin-frame-001", RequirementKey: "video:kitchen", Role: "REFERENCE", EvidenceKind: "VIDEO_FRAME", FrameID: "origin-frame-001", SourceMediaID: "source-origin-001", PreparationID: "prep-origin-001", PTSMicros: &pts, TimestampMS: &ms, FrameDigest: strings.Repeat("b", 64), Availability: "AVAILABLE"}}
	assets := pdf.AssetBundle{"evidence-current-frame-001.jpg": jpegBytes(t, 160, 90), "evidence-origin-frame-001.jpg": jpegBytes(t, 161, 90)}
	data := renderVVTask07(t, snapshot, assets)
	if PageCount(data) < 1 || extractVVTask07Images(t, data) != 2 {
		t.Fatal("RPT1 PDF did not retain its two pinned still images")
	}
	html, err := reportcore.HTMLForPDF(snapshot, map[string]bool{"current-frame-001": true, "origin-frame-001": true}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Cozinha · Pia", "Banheiro · Lacuna", "Não observado", "origin-frame-001", "current-frame-001"} {
		if !strings.Contains(string(html), want) {
			t.Errorf("RPT1 renderer input missing %q", want)
		}
	}
	for _, evidence := range snapshot.Evidence {
		_, caption, _, captionErr := evidenceComponents(evidence, assets, "Cozinha · Pia", "snapshot-rpt1", "")
		if captionErr != nil || !strings.Contains(caption, evidence.FrameID) || !strings.Contains(caption, evidence.SourceMediaID) || !strings.Contains(caption, "01:10") || !strings.Contains(caption, label(evidence.Role)) {
			t.Errorf("RPT1 video caption lacks exact role/source/PTS: %q (%v)", caption, captionErr)
		}
	}
}

func TestVV_IT_295CurrentOnlyRPT2PDFHasNoOriginOrHistoricalClaim(t *testing.T) {
	snapshot := validPDFSnapshot()
	snapshot.Mode = "CONSOLIDATED"
	snapshot.ReferenceVersionID = "current-only"
	snapshot.ReasonCodes = nil
	snapshot.Requirements[0].AnalysisMode = "CURRENT_ONLY"
	snapshot.Evidence = []reportcore.Evidence{{ID: "current-frame-rpt2", RequirementKey: "roof", Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: "current-frame-rpt2", SourceMediaID: "current-source-rpt2", PreparationID: "current-prep-rpt2", TimestampMS: ptrTask07Int64(70_000), FrameDigest: strings.Repeat("a", 64), Availability: "AVAILABLE"}}
	data := renderVVTask07(t, snapshot, pdf.AssetBundle{"evidence-current-frame-rpt2.jpg": jpegBytes(t, 80, 60)})
	html, err := reportcore.HTMLForPDF(snapshot, map[string]bool{"current-frame-rpt2": true}, false)
	if err != nil || PageCount(data) < 1 {
		t.Fatalf("current-only PDF render err=%v pages=%d", err, PageCount(data))
	}
	_, caption, _, err := evidenceComponents(snapshot.Evidence[0], nil, "", "snapshot-rpt2", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Referência", "Comparação com referência", "Mudança observada", "Histórico"} {
		if strings.Contains(string(html), forbidden) || strings.Contains(caption, forbidden) {
			t.Errorf("current-only PDF contains historical/origin claim %q", forbidden)
		}
	}
	if !strings.Contains(caption, "Vistoria atual") || !strings.Contains(caption, "current-source-rpt2") || strings.Contains(caption, "Referência") {
		t.Fatal("current-only PDF omitted the current evidence")
	}
}

func TestVV_IT_296VideoPDFLinkUsesAuthenticatedEvidenceRoute(t *testing.T) {
	snapshot := validPDFSnapshot()
	snapshot.Evidence = []reportcore.Evidence{{ID: "frame-link-001", RequirementKey: "roof", Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: "frame-link-001", SourceMediaID: "source-link-001", PreparationID: "prep-link-001", TimestampMS: ptrTask07Int64(70_000), FrameDigest: strings.Repeat("a", 64), Availability: "AVAILABLE"}}
	const origin, snapshotID = "https://dashboard.example", "snapshot-link-001"
	data, err := (MarotoRenderer{DashboardOrigin: origin}).Render(context.Background(), PDFDocument{SnapshotID: snapshotID, Version: 1, Snapshot: snapshot, Internal: true}, pdf.AssetBundle{"evidence-frame-link-001.jpg": jpegBytes(t, 80, 60)})
	if err != nil {
		t.Fatal(err)
	}
	want := origin + "/media/video-evidence/frame-link-001?snapshotId=" + snapshotID
	if !bytes.Contains(data, []byte(want)) || strings.Contains(strings.ToLower(string(data)), "signature=") || strings.Contains(strings.ToLower(string(data)), "token=") {
		t.Fatalf("PDF link missing authenticated product route or contains signed credentials: %q", want)
	}
}

func TestVV_IT_299AllInconclusivePDFListsEveryScopeLimitation(t *testing.T) {
	snapshot := validPDFSnapshot()
	snapshot.ScopeStatements = []string{"Banheiro não observado.", "Cozinha sem evidência suficiente.", "Nenhuma conclusão comparativa é sustentada."}
	snapshot.Requirements = []reportcore.Requirement{{Key: "bath", Section: "Vídeo", Label: "Banheiro", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "INCONCLUSIVE", Coverage: "Não observado"}, {Key: "kitchen", Section: "Vídeo", Label: "Cozinha", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "INCONCLUSIVE", Coverage: "Evidência insuficiente"}}
	snapshot.Classification, snapshot.Advisory = "ATTENTION", "Análise inconclusiva; revisar evidências disponíveis."
	data := renderVVTask07(t, snapshot, nil)
	html, err := reportcore.HTMLForPDF(snapshot, nil, true)
	if err != nil || PageCount(data) < 1 {
		t.Fatalf("all-inconclusive PDF render err=%v pages=%d", err, PageCount(data))
	}
	for _, want := range snapshot.ScopeStatements {
		if !strings.Contains(string(html), want) {
			t.Errorf("PDF omitted scope limitation %q", want)
		}
	}
	if strings.Contains(strings.ToLower(string(html)), "imóvel satisfatório") || strings.Contains(strings.ToLower(string(html)), "condição satisfatória") {
		t.Fatal("all-inconclusive PDF included a satisfactory-condition summary")
	}
	if lines := scopeStatementLines(snapshot.ScopeStatements); len(lines) != len(snapshot.ScopeStatements) {
		t.Fatalf("PDF scope statements=%d, want all %d limitations", len(lines), len(snapshot.ScopeStatements))
	}
	for index, statement := range snapshot.ScopeStatements {
		if lines := scopeStatementLines(snapshot.ScopeStatements); lines[index] != "• "+statement {
			t.Errorf("scope limitation %d was not emitted verbatim: %q", index, lines[index])
		}
	}
}

func TestVV_IT_300TwoHundredVideoFindingsPaginateWithReadableCaptions(t *testing.T) {
	snapshot := task07LargeVideoSnapshot(200, true)
	assets := make(pdf.AssetBundle, 200)
	for index, evidence := range snapshot.Evidence {
		assets["evidence-"+evidence.ID+".jpg"] = jpegBytes(t, 32+index, 24)
	}
	data := renderVVTask07(t, snapshot, assets)
	if pages := PageCount(data); pages < 20 || pages > maxPDFPages {
		t.Fatalf("200-finding PDF page count=%d, expected a paginated document within limits", pages)
	}
	for index := 0; index < 200; index++ {
		evidence := snapshot.Evidence[index]
		_, caption, _, err := evidenceComponents(evidence, assets, snapshot.Requirements[index].Label, "snapshot-large", "")
		if err != nil || len(assets["evidence-"+evidence.ID+".jpg"]) == 0 || !strings.Contains(caption, evidence.FrameID) || !strings.Contains(caption, evidence.SourceMediaID) || !strings.Contains(analysisSummary(snapshot.Requirements[index]), fmt.Sprintf("Limitação %03d", index)) {
			t.Errorf("paginated image/caption/limitation %d failed: %v", index, err)
		}
	}
}

func TestVV_IT_307TwoHundredVideoCaptionsAndLimitationsSurvivePagination(t *testing.T) {
	snapshot := task07LargeVideoSnapshot(200, true)
	assets := make(pdf.AssetBundle, 200)
	for index, evidence := range snapshot.Evidence {
		assets["evidence-"+evidence.ID+".jpg"] = jpegBytes(t, 32+index, 24)
	}
	data := renderVVTask07(t, snapshot, assets)
	if pages := PageCount(data); pages < 20 || pages > maxPDFPages || extractVVTask07Images(t, data) != 200 {
		t.Fatalf("200-item PDF pages=%d embedded images=%d", pages, extractVVTask07Images(t, data))
	}
	for index := 0; index < 200; index++ {
		evidence := snapshot.Evidence[index]
		_, caption, _, err := evidenceComponents(evidence, assets, snapshot.Requirements[index].Label, "snapshot-large", "")
		if err != nil || !strings.Contains(caption, evidence.FrameID) || !strings.Contains(caption, evidence.SourceMediaID) || !strings.Contains(analysisSummary(snapshot.Requirements[index]), fmt.Sprintf("Limitação %03d", index)) {
			t.Fatalf("pagination omitted caption or limitation %03d: %v", index, err)
		}
	}
}

func TestVV_IT_298MissingFrameDigestFailsWithoutSubstituteImage(t *testing.T) {
	env := newTestEnv(t)
	snapshot := validPDFSnapshot()
	frameID := identity.NewID().String()
	snapshot.Evidence = []reportcore.Evidence{{ID: frameID, RequirementKey: "roof", Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: frameID, SourceMediaID: identity.NewID().String(), PreparationID: identity.NewID().String(), TimestampMS: ptrTask07Int64(1000), Availability: "AVAILABLE"}}
	storeVVTask07Snapshot(t, env, snapshot)
	job := env.addJob(t, "PDF", "QUEUED", 0)
	if worked, err := env.executor.RunDue(context.Background()); err != nil || !worked {
		t.Fatalf("RunDue worked=%v err=%v", worked, err)
	}
	if got := env.job(t, job.ID); got.Status != "FAILED" || got.LastError != "FRAME_INTEGRITY_FAILED" || got.ArtifactID != nil {
		t.Fatalf("missing frame digest result=%+v", got)
	}
	if env.client.count() != 0 {
		t.Fatal("failed export left a substitute or partial PDF object")
	}
}

func TestVV_IT_303RendererFailureAfterPageTwoDoesNotPublishPartialPDF(t *testing.T) {
	env := newTestEnv(t)
	snapshot := validPDFSnapshot()
	snapshot.Advisory = strings.Repeat("Relato longo que ultrapassa duas páginas. ", 500)
	storeVVTask07Snapshot(t, env, snapshot)
	env.executor.renderer.MaxPages = 2
	job := env.addJob(t, "PDF", "QUEUED", 0)
	if worked, err := env.executor.RunDue(context.Background()); err != nil || !worked {
		t.Fatalf("RunDue worked=%v err=%v", worked, err)
	}
	got := env.job(t, job.ID)
	if got.Status != "FAILED" || got.ArtifactID != nil || got.PendingObjectKey != "" {
		t.Fatalf("failed renderer exposed completed or partial artifact: %+v", got)
	}
	var artifacts int64
	if err := env.db.Model(&database.ReportArtifact{}).Where("snapshot_id=? AND kind='PDF'", env.snapshot.ID).Count(&artifacts).Error; err != nil || artifacts != 0 {
		t.Fatalf("published artifact count=%d err=%v", artifacts, err)
	}
	if env.client.count() != 0 {
		t.Fatal("renderer failure left a partial object in storage")
	}
}

func TestVV_IT_297ExpiredSourceStillExportsRetainedFrame(t *testing.T) {
	env := newTestEnv(t)
	snapshot, frame, data := seedVVTask07VideoFrame(t, env, "EXPIRED")
	storeVVTask07Snapshot(t, env, snapshot)
	job := env.addJob(t, "PDF", "QUEUED", 0)
	if worked, err := env.executor.RunDue(context.Background()); err != nil || !worked {
		t.Fatalf("RunDue worked=%v err=%v", worked, err)
	}
	got := env.job(t, job.ID)
	if got.Status != "READY" || got.ArtifactID == nil {
		t.Fatalf("expired source prevented PDF from retained frame: %+v", got)
	}
	var artifact database.ReportArtifact
	if err := env.db.First(&artifact, "id=?", *got.ArtifactID).Error; err != nil {
		t.Fatal(err)
	}
	pdfBytes := env.client.objects[artifact.ObjectKey]
	if !bytes.HasPrefix(pdfBytes, []byte("%PDF-")) || len(data) == 0 {
		t.Fatal("PDF was not rendered from the retained frame")
	}
	rendered := snapshot
	rendered.Evidence = append([]reportcore.Evidence(nil), snapshot.Evidence...)
	if err := overlaySourceAvailability(context.Background(), env.db, env.tenantID, &rendered, env.clock.UTC()); err != nil {
		t.Fatal(err)
	}
	_, caption, _, err := evidenceComponents(rendered.Evidence[0], pdf.AssetBundle{"evidence-" + frame.ID.String() + ".jpg": data}, "Telhado e calhas", "snapshot-rpt1", "")
	if err != nil || !strings.Contains(caption, "Vídeo original indisponível; frame preservado") {
		t.Fatalf("unavailable-source notice missing from PDF caption %q err=%v", caption, err)
	}
}

func TestVV_IT_302SourceExpiryDuringAssetLoadUsesRenderTimeNotice(t *testing.T) {
	env := newTestEnv(t)
	snapshot, frame, _ := seedVVTask07VideoFrame(t, env, "RETAINED")
	storeVVTask07Snapshot(t, env, snapshot)
	env.client.getHook = func(key string) {
		if key == frame.ObjectKey {
			if err := env.db.Exec("UPDATE media.video_sources SET status='EXPIRED' WHERE media_id=?", frame.SourceMediaID).Error; err != nil {
				t.Errorf("expire source during frame read: %v", err)
			}
			env.client.getHook = nil
		}
	}
	job := env.addJob(t, "PDF", "QUEUED", 0)
	if worked, err := env.executor.RunDue(context.Background()); err != nil || !worked {
		t.Fatalf("RunDue worked=%v err=%v", worked, err)
	}
	if got := env.job(t, job.ID); got.Status != "READY" || got.ArtifactID == nil {
		t.Fatalf("source expiry during render failed PDF export: %+v", got)
	}
	var canonical reportcore.Snapshot
	if err := json.Unmarshal(env.snapshot.CanonicalJSON, &canonical); err != nil {
		t.Fatal(err)
	}
	if canonical.Evidence[0].SourceAvailability != "AVAILABLE" {
		t.Fatal("source expiry changed the immutable report snapshot")
	}
	rendered := snapshot
	if err := overlaySourceAvailability(context.Background(), env.db, env.tenantID, &rendered, env.clock.UTC()); err != nil {
		t.Fatal(err)
	}
	if rendered.Evidence[0].SourceAvailability != "EXPIRED" {
		t.Fatalf("expired source overlay=%s", rendered.Evidence[0].SourceAvailability)
	}
	_, caption, _, err := evidenceComponents(rendered.Evidence[0], nil, "Telhado e calhas", "snapshot-rpt1", "")
	if err != nil || !strings.Contains(caption, "Vídeo original indisponível; frame preservado") {
		t.Fatalf("render-time source notice missing: %q err=%v", caption, err)
	}
}

func TestVV_IT_304SourceExpiryOnlyChangesRenderAvailabilityNotice(t *testing.T) {
	env := newTestEnv(t)
	snapshot, frame, _ := seedVVTask07VideoFrame(t, env, "RETAINED")
	storeVVTask07Snapshot(t, env, snapshot)
	job := env.addJob(t, "PDF", "QUEUED", 0)
	if worked, err := env.executor.RunDue(context.Background()); err != nil || !worked {
		t.Fatalf("first RunDue worked=%v err=%v", worked, err)
	}
	firstJob := env.job(t, job.ID)
	if firstJob.Status != "READY" || firstJob.ArtifactID == nil {
		t.Fatalf("first export was not ready: %+v", firstJob)
	}
	var firstArtifact database.ReportArtifact
	if err := env.db.First(&firstArtifact, "id=?", *firstJob.ArtifactID).Error; err != nil {
		t.Fatal(err)
	}
	firstPDF := append([]byte(nil), env.client.objects[firstArtifact.ObjectKey]...)
	before := snapshot
	before.Evidence = append([]reportcore.Evidence(nil), snapshot.Evidence...)
	if err := overlaySourceAvailability(context.Background(), env.db, env.tenantID, &before, env.clock.UTC()); err != nil {
		t.Fatal(err)
	}
	if before.Evidence[0].SourceAvailability != "AVAILABLE" {
		t.Fatalf("source before expiry overlay=%s", before.Evidence[0].SourceAvailability)
	}
	if err := env.db.Exec("UPDATE media.video_sources SET status='EXPIRED' WHERE media_id=?", frame.SourceMediaID).Error; err != nil {
		t.Fatal(err)
	}
	after := snapshot
	after.Evidence = append([]reportcore.Evidence(nil), snapshot.Evidence...)
	if err := overlaySourceAvailability(context.Background(), env.db, env.tenantID, &after, env.clock.UTC()); err != nil {
		t.Fatal(err)
	}
	if after.Evidence[0].SourceAvailability != "EXPIRED" {
		t.Fatalf("source after expiry overlay=%s", after.Evidence[0].SourceAvailability)
	}
	if !bytes.Equal(env.snapshot.CanonicalJSON, mustTask07Canonical(t, snapshot)) {
		t.Fatal("expiry modified the persisted canonical report snapshot")
	}
	beforeEvidence, afterEvidence := before.Evidence[0], after.Evidence[0]
	beforeEvidence.SourceAvailability, afterEvidence.SourceAvailability = "", ""
	if !reflect.DeepEqual(beforeEvidence, afterEvidence) {
		t.Fatalf("source expiry changed pinned evidence: before=%+v after=%+v", beforeEvidence, afterEvidence)
	}
	assets := pdf.AssetBundle{"evidence-" + frame.ID.String() + ".jpg": env.client.objects[frame.ObjectKey]}
	_, beforeCaption, _, err := evidenceComponents(before.Evidence[0], assets, "Telhado e calhas", "snapshot-rpt1", "")
	if err != nil {
		t.Fatal(err)
	}
	_, afterCaption, _, err := evidenceComponents(after.Evidence[0], assets, "Telhado e calhas", "snapshot-rpt1", "")
	if err != nil || strings.ReplaceAll(afterCaption, " · Vídeo original indisponível; frame preservado", "") != beforeCaption {
		t.Fatalf("expiry changed finding/caption content beyond availability notice: before=%q after=%q err=%v", beforeCaption, afterCaption, err)
	}
	if err := env.client.Delete(context.Background(), "private", firstArtifact.ObjectKey); err != nil {
		t.Fatal(err)
	}
	if err := env.db.Delete(&firstArtifact).Error; err != nil {
		t.Fatal(err)
	}
	if err := env.db.Table("reports.report_pdf_jobs").Where("id=?", job.ID).Updates(map[string]any{"status": "QUEUED", "artifact_id": nil, "next_attempt_at": *env.clock}).Error; err != nil {
		t.Fatal(err)
	}
	if worked, err := env.executor.RunDue(context.Background()); err != nil || !worked {
		t.Fatalf("second RunDue worked=%v err=%v", worked, err)
	}
	secondJob := env.job(t, job.ID)
	if secondJob.Status != "READY" || secondJob.ArtifactID == nil {
		t.Fatalf("second export was not ready: %+v", secondJob)
	}
	var secondArtifact database.ReportArtifact
	if err := env.db.First(&secondArtifact, "id=?", *secondJob.ArtifactID).Error; err != nil {
		t.Fatal(err)
	}
	secondPDF := env.client.objects[secondArtifact.ObjectKey]
	if PageCount(firstPDF) != PageCount(secondPDF) || bytes.Equal(firstPDF, secondPDF) {
		t.Fatal("source availability overlay was not reflected in an otherwise stable PDF")
	}
}

func mustTask07Canonical(t *testing.T, snapshot reportcore.Snapshot) []byte {
	t.Helper()
	canonical, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func seedVVTask07VideoFrame(t *testing.T, env *testEnv, sourceStatus string) (reportcore.Snapshot, database.VideoFrame, []byte) {
	t.Helper()
	if err := env.db.Exec("CREATE TABLE media.video_sources (id blob primary key, tenant_id blob, media_id blob, status text, original_deleted_at datetime, source_expiry_at datetime)").Error; err != nil {
		t.Fatal(err)
	}
	frameID, sourceID, preparationID := identity.NewID(), identity.NewID(), identity.NewID()
	data := jpegBytes(t, 90, 60)
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	key := "frames/" + frameID.String() + ".jpg"
	env.client.objects[key] = data
	frame := database.VideoFrame{ID: frameID, TenantID: env.tenantID, SourceMediaID: sourceID, PreparationID: preparationID, SHA256: digest, ObjectKey: key, Width: 90, Height: 60, SizeBytes: int64(len(data))}
	if err := env.db.Exec("INSERT INTO media.video_frames (id,tenant_id,source_media_id,preparation_id,sha256,object_key,width,height,size_bytes) VALUES (?,?,?,?,?,?,?,?,?)", frame.ID, frame.TenantID, frame.SourceMediaID, frame.PreparationID, frame.SHA256, frame.ObjectKey, frame.Width, frame.Height, frame.SizeBytes).Error; err != nil {
		t.Fatal(err)
	}
	if err := env.db.Exec("INSERT INTO media.video_sources (id,tenant_id,media_id,status) VALUES (?,?,?,?)", identity.NewID(), env.tenantID, sourceID, sourceStatus).Error; err != nil {
		t.Fatal(err)
	}
	var source database.VideoSource
	if err := env.db.Where("tenant_id=? AND media_id=?", env.tenantID, sourceID).Take(&source).Error; err != nil {
		t.Fatalf("read seeded source status %s: %v", sourceStatus, err)
	}
	snapshot := validPDFSnapshot()
	ms, pts := int64(70_000), int64(70_000_000)
	snapshot.Evidence = []reportcore.Evidence{{ID: frameID.String(), RequirementKey: "roof", Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: frameID.String(), SourceMediaID: sourceID.String(), PreparationID: preparationID.String(), PTSMicros: &pts, TimestampMS: &ms, FrameDigest: digest, DisplayDigest: digest, Availability: "AVAILABLE", SourceAvailability: "AVAILABLE"}}
	return snapshot, frame, data
}

func storeVVTask07Snapshot(t *testing.T, env *testEnv, snapshot reportcore.Snapshot) {
	t.Helper()
	canonical, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(canonical)
	digest := hex.EncodeToString(sum[:])
	if err := env.db.Model(&env.snapshot).Updates(map[string]any{"canonical_json": json.RawMessage(canonical), "json_digest": digest}).Error; err != nil {
		t.Fatal(err)
	}
	env.snapshot.CanonicalJSON, env.snapshot.JSONDigest = json.RawMessage(canonical), digest
}

func task07LargeVideoSnapshot(count int, limitations bool) reportcore.Snapshot {
	snapshot := validPDFSnapshot()
	snapshot.Requirements = make([]reportcore.Requirement, 0, count)
	snapshot.Findings = make([]reportcore.Finding, 0, count)
	snapshot.Evidence = make([]reportcore.Evidence, 0, count)
	for index := 0; index < count; index++ {
		key := fmt.Sprintf("video:finding-%03d", index)
		evidenceID := identity.NewID().String()
		frameID, sourceID := fmt.Sprintf("frame-%03d", index), fmt.Sprintf("source-%03d", index)
		ms, pts := int64(index*1000), int64(index*1_000_000)
		snapshot.Requirements = append(snapshot.Requirements, reportcore.Requirement{Key: key, Section: "Vídeo", Label: fmt.Sprintf("Componente %03d", index), AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "INCONCLUSIVE", Coverage: func() string {
			if limitations {
				return fmt.Sprintf("Limitação %03d", index)
			}
			return ""
		}()})
		snapshot.Findings = append(snapshot.Findings, reportcore.Finding{ID: fmt.Sprintf("Finding %03d", index), Category: "CONSERVATION", Title: fmt.Sprintf("Finding %03d", index), Description: "Desgaste observado.", Severity: "LOW", Confidence: .8, EvidenceIDs: []string{evidenceID}, Quality: "ADEQUATE", RecommendedAction: "MONITOR"})
		snapshot.Evidence = append(snapshot.Evidence, reportcore.Evidence{ID: evidenceID, RequirementKey: key, Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: evidenceID, SourceMediaID: sourceID, PreparationID: "prep-" + frameID, Description: frameID, PTSMicros: &pts, TimestampMS: &ms, FrameDigest: strings.Repeat("a", 64), Availability: "AVAILABLE"})
	}
	return snapshot
}

func renderVVTask07(t *testing.T, snapshot reportcore.Snapshot, assets pdf.AssetBundle) []byte {
	t.Helper()
	data, err := (MarotoRenderer{}).Render(context.Background(), PDFDocument{SnapshotID: "snapshot-vv-task07", Version: 1, Snapshot: snapshot, Internal: true}, assets)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func extractVVTask07Images(t *testing.T, data []byte) int {
	t.Helper()
	model.ConfigPath = "disable"
	count := 0
	err := api.ExtractImages(bytes.NewReader(data), nil, func(_ model.Image, _ bool, _ int) error { count++; return nil }, model.NewDefaultConfiguration())
	if err != nil {
		t.Fatalf("extract PDF images: %v", err)
	}
	return count
}

func ptrTask07Int64(value int64) *int64 { return &value }
