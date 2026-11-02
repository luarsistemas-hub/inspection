//go:build integration

package generate_snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	report "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/download_pdf"
	"inspection/services/inspection/internal/features/reports/get_report"
	"inspection/services/inspection/internal/features/reports/render_pdf"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/pdf"

	"gorm.io/gorm"
)

type aipReportEnv struct {
	t   *testing.T
	h   *harness.Harness
	ctx context.Context
	f   harness.Task06Fixture
}

func newAIPReportEnv(t *testing.T) *aipReportEnv {
	t.Helper()
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("integration database URLs are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatalf("open integration harness: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{ComparisonMode: "CURRENT_ONLY"})
	if err != nil {
		t.Fatal(err)
	}
	return &aipReportEnv{t: t, h: h, ctx: ctx, f: f}
}

func (e *aipReportEnv) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

// input is a valid PHOTO report input for the fixture inspection.
func (e *aipReportEnv) input(classification string, reasons []string) Input {
	return Input{
		TenantID: e.f.TenantID, InspectionID: e.f.InspectionID, Mode: "HISTORICAL", Classification: classification,
		TemplateVersionID: e.f.TemplateVersionID.String(), ReferenceVersionID: ReferenceFree, PromptDigest: "aip-prompt-digest", ReasonCodes: reasons,
		Coverage: map[string]string{"evidence": "1 de 1 requisitos"},
		Context:  report.Context{Asset: report.AssetContext{ID: e.f.AssetID.String(), Name: "Imóvel QA", ExternalKey: "QA-01", Address: "Rua de teste, 1"}, Participant: report.ParticipantContext{ID: e.f.ParticipantID.String(), Name: "Responsável QA"}, Template: report.TemplateContext{ID: e.f.TemplateID.String(), Name: "Modelo QA", Version: 1}, Inspection: report.InspectionContext{GeneratedAt: "2026-11-01T12:00:00Z"}},
	}
}

// create stores a snapshot with the worker's privileged connection.
func (e *aipReportEnv) create(input Input) Result {
	e.t.Helper()
	result, err := Create(e.ctx, e.h.AdminDB, input, time.Now())
	e.must(err)
	return result
}

// read runs a query inside a tenant transaction with the runtime (RLS) role.
func (e *aipReportEnv) read(tenantID identity.ID, fn func(*gorm.DB) error) error {
	return e.h.WithinTenant(e.ctx, tenantID, fn)
}

func (e *aipReportEnv) getReport(tenantID identity.ID, in get_report.Input) (database.ReportSnapshot, error) {
	var row database.ReportSnapshot
	err := e.read(tenantID, func(tx *gorm.DB) error {
		query, err := get_report.Setup(get_report.Dependencies{DB: tx})
		if err != nil {
			return err
		}
		row, err = query(e.ctx, in)
		return err
	})
	return row, err
}

func (e *aipReportEnv) snapshots() int64 {
	var total int64
	e.must(e.h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", e.f.TenantID, e.f.InspectionID).Count(&total).Error)
	return total
}

func aipRequirement(key, label, status string, noChange *bool) report.Requirement {
	return report.Requirement{Key: key, Section: "Sala", Label: label, Instructions: "Registrar o ambiente.", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: status, NoRelevantChange: noChange}
}

// TestAIP_IT_261InvalidReportIdentityOrVersionIsRejectedWithoutLeakingAnotherReport covers IT-261.
// The unsupported-format half is asserted at the transport (IT-426).
func TestAIP_IT_261InvalidReportIdentityOrVersionIsRejectedWithoutLeakingAnotherReport(t *testing.T) {
	e := newAIPReportEnv(t)
	other, err := e.h.SeedTask06Fixture(e.ctx, harness.Task06FixtureOptions{ComparisonMode: "CURRENT_ONLY"})
	e.must(err)
	mine := e.create(e.input("NORMAL", nil))
	// A different tenant's report exists and must never be returned.
	foreign := Input{TenantID: other.TenantID, InspectionID: other.InspectionID, Mode: "HISTORICAL", Classification: "CRITICAL", TemplateVersionID: other.TemplateVersionID.String(), ReferenceVersionID: ReferenceFree, PromptDigest: "other", ReasonCodes: []string{"CRITICAL_FINDING"}, Context: e.input("", nil).Context}
	foreignResult, err := Create(e.ctx, e.h.AdminDB, foreign, time.Now())
	e.must(err)

	got, err := e.getReport(e.f.TenantID, get_report.Input{TenantID: e.f.TenantID, InspectionID: e.f.InspectionID})
	if err != nil || got.ID != mine.SnapshotID {
		t.Fatalf("own report = %+v (%v)", got.ID, err)
	}
	one := 1
	if got, err := e.getReport(e.f.TenantID, get_report.Input{TenantID: e.f.TenantID, InspectionID: e.f.InspectionID, Version: &one}); err != nil || got.ID != mine.SnapshotID {
		t.Fatalf("explicit version = %+v (%v)", got.ID, err)
	}
	missing := func(name string, tenant identity.ID, in get_report.Input) {
		row, err := e.getReport(tenant, in)
		if !errors.Is(err, gorm.ErrRecordNotFound) || row.ID != (identity.ID{}) || len(row.CanonicalJSON) != 0 {
			t.Fatalf("%s returned %v / %v, want a bare not-found without report data", name, row.ID, err)
		}
	}
	zero, negative, far := 0, -1, 99
	missing("version zero", e.f.TenantID, get_report.Input{TenantID: e.f.TenantID, InspectionID: e.f.InspectionID, Version: &zero})
	missing("negative version", e.f.TenantID, get_report.Input{TenantID: e.f.TenantID, InspectionID: e.f.InspectionID, Version: &negative})
	missing("version that does not exist", e.f.TenantID, get_report.Input{TenantID: e.f.TenantID, InspectionID: e.f.InspectionID, Version: &far})
	missing("unknown inspection", e.f.TenantID, get_report.Input{TenantID: e.f.TenantID, InspectionID: identity.NewID()})
	missing("zero identity", e.f.TenantID, get_report.Input{})
	missing("another tenant's inspection by id", e.f.TenantID, get_report.Input{TenantID: e.f.TenantID, InspectionID: other.InspectionID})
	missing("another tenant asking for this inspection", other.TenantID, get_report.Input{TenantID: other.TenantID, InspectionID: e.f.InspectionID})
	// The slice filters by tenant itself, so a privileged connection is also safe.
	query, err := get_report.Setup(get_report.Dependencies{DB: e.h.AdminDB})
	e.must(err)
	if row, err := query(e.ctx, get_report.Input{TenantID: other.TenantID, InspectionID: e.f.InspectionID}); !errors.Is(err, gorm.ErrRecordNotFound) || row.ID != (identity.ID{}) {
		t.Fatalf("a privileged connection leaked %v across tenants (%v)", row.ID, err)
	}
	if foreignResult.SnapshotID == mine.SnapshotID {
		t.Fatal("fixtures share a snapshot")
	}
	if _, err := get_report.Setup(get_report.Dependencies{}); err == nil {
		t.Fatal("get_report accepted a missing database")
	}
}

// TestAIP_IT_262ReportWithoutFindingsStillExplainsCoverageAndClassification covers IT-262.
func TestAIP_IT_262ReportWithoutFindingsStillExplainsCoverageAndClassification(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		e := newAIPReportEnv(t)
		input := e.input("NORMAL", nil)
		input.Requirements = []report.Requirement{aipRequirement("sala", "Sala principal", "COMPLETED", nil)}
		input.Evidence = []report.Evidence{{ID: e.f.MediaID.String(), RequirementKey: "sala", Role: "CURRENT", Description: "Vista geral"}}
		e.create(input)
		row, err := e.getReport(e.f.TenantID, get_report.Input{TenantID: e.f.TenantID, InspectionID: e.f.InspectionID})
		e.must(err)
		var stored report.Snapshot
		e.must(json.Unmarshal(row.CanonicalJSON, &stored))
		var html string
		e.must(json.Unmarshal(row.HTML, &html))
		if stored.Classification != "NORMAL" || len(stored.Findings) != 0 || len(stored.Requirements) != 1 || len(stored.Evidence) != 1 || stored.Coverage["evidence"] == "" {
			t.Fatalf("stored report does not describe its coverage: %+v", stored)
		}
		for _, want := range []string{"Sala principal", "Concluída", "Nenhuma constatação foi registrada; isso não confirma a ausência de problemas.", "Sem alertas identificados", "não atribui culpa"} {
			if !strings.Contains(html, want) && !strings.Contains(strings.ToLower(html), strings.ToLower(want)) {
				t.Fatalf("the report HTML does not say %q", want)
			}
		}
	})
	t.Run("attention explained by reason codes", func(t *testing.T) {
		e := newAIPReportEnv(t)
		reasons := []string{"INCONCLUSIVE", "EVIDENCE_FLAG", "MISSING_EVIDENCE", "SKIPPED_STAGE"}
		input := e.input("ATTENTION", reasons)
		input.Requirements = []report.Requirement{aipRequirement("sala", "Sala principal", "INCONCLUSIVE", nil)}
		e.create(input)
		row, err := e.getReport(e.f.TenantID, get_report.Input{TenantID: e.f.TenantID, InspectionID: e.f.InspectionID})
		e.must(err)
		var html string
		e.must(json.Unmarshal(row.HTML, &html))
		for _, want := range []string{"Análise inconclusiva", "Evidência com alerta", "Evidência ausente", "Etapa pulada", "Inconclusiva"} {
			if !strings.Contains(html, want) {
				t.Fatalf("an ATTENTION report without findings does not explain %q", want)
			}
		}
		if strings.Contains(html, "Situação não reconhecida") {
			t.Fatal("the report shows an unrecognized situation instead of the reason")
		}
	})
}

// TestAIP_IT_264ReportAndPDFAreDeniedAcrossTenantsAndOnlyMintShortPrivateURLs covers IT-264.
// External and out-of-scope users are denied at the transport (IT-424, IT-426).
func TestAIP_IT_264ReportAndPDFAreDeniedAcrossTenantsAndOnlyMintShortPrivateURLs(t *testing.T) {
	e := newAIPReportEnv(t)
	other, err := e.h.SeedTask06Fixture(e.ctx, harness.Task06FixtureOptions{ComparisonMode: "CURRENT_ONLY"})
	e.must(err)
	result := e.create(e.input("NORMAL", nil))
	body := []byte("%PDF-1.4 aip-264")
	key := "tenant/" + e.f.TenantID.String() + "/reports/" + result.SnapshotID.String() + ".pdf"
	e.must(e.h.Store.Client.Put(e.ctx, e.h.Store.Bucket, key, bytes.NewReader(body), int64(len(body)), "application/pdf"))
	artifact := database.ReportArtifact{ID: identity.NewID(), TenantID: e.f.TenantID, SnapshotID: result.SnapshotID, Kind: "PDF", ObjectKey: key, SHA256: "aip-sha", Status: "READY", CreatedAt: time.Now().UTC()}
	e.must(e.h.AdminDB.Create(&artifact).Error)

	download := func(tenantID identity.ID, conn *gorm.DB, in download_pdf.Input) (download_pdf.Result, error) {
		query, err := download_pdf.Setup(download_pdf.Dependencies{DB: conn, Store: e.h.Store})
		if err != nil {
			t.Fatal(err)
		}
		return query(e.ctx, in)
	}
	var got download_pdf.Result
	e.must(e.read(e.f.TenantID, func(tx *gorm.DB) (err error) {
		got, err = download(e.f.TenantID, tx, download_pdf.Input{TenantID: e.f.TenantID, SnapshotID: result.SnapshotID})
		return err
	}))
	if got.Artifact.ObjectKey != "" || got.Artifact.ID != artifact.ID || got.Artifact.SHA256 != "aip-sha" || got.URL == "" {
		t.Fatalf("download result exposes or lacks data: %+v", got)
	}
	parsed, err := url.Parse(got.URL)
	if err != nil || parsed.Query().Get("X-Amz-Expires") != "300" || !strings.Contains(parsed.Path, e.h.Store.Bucket) {
		t.Fatalf("URL is not a five minute private presigned URL: %s (%v)", got.URL, err)
	}
	response, err := http.Get(got.URL)
	e.must(err)
	fetched, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Equal(fetched, body) {
		t.Fatalf("presigned URL returned %d %q", response.StatusCode, fetched)
	}
	// Another tenant: not found through its own RLS scope and through the slice filter.
	for name, run := range map[string]func() (download_pdf.Result, error){
		"runtime role of another tenant": func() (download_pdf.Result, error) {
			var result download_pdf.Result
			err := e.read(other.TenantID, func(tx *gorm.DB) (err error) {
				result, err = download(other.TenantID, tx, download_pdf.Input{TenantID: other.TenantID, SnapshotID: got.Artifact.SnapshotID})
				return err
			})
			return result, err
		},
		"privileged connection naming another tenant": func() (download_pdf.Result, error) {
			return download(other.TenantID, e.h.AdminDB, download_pdf.Input{TenantID: other.TenantID, SnapshotID: got.Artifact.SnapshotID})
		},
		"unknown snapshot": func() (download_pdf.Result, error) {
			return download(e.f.TenantID, e.h.AdminDB, download_pdf.Input{TenantID: e.f.TenantID, SnapshotID: identity.NewID()})
		},
	} {
		result, err := run()
		if !errors.Is(err, gorm.ErrRecordNotFound) || result.URL != "" || result.Artifact.ID != (identity.ID{}) {
			t.Fatalf("%s got %+v / %v, want not found", name, result, err)
		}
	}
	if row, err := e.getReport(other.TenantID, get_report.Input{TenantID: other.TenantID, InspectionID: e.f.InspectionID}); !errors.Is(err, gorm.ErrRecordNotFound) || row.ID != (identity.ID{}) {
		t.Fatalf("another tenant read the report: %v %v", row.ID, err)
	}
	if _, err := download_pdf.Setup(download_pdf.Dependencies{DB: e.h.AdminDB}); err == nil {
		t.Fatal("download_pdf accepted a missing private object store")
	}
}

// TestAIP_IT_265TwoGenerationAttemptsRetainOneLogicalReportVersion covers IT-265.
func TestAIP_IT_265TwoGenerationAttemptsRetainOneLogicalReportVersion(t *testing.T) {
	e := newAIPReportEnv(t)
	input := e.input("ATTENTION", []string{"OBSERVED_CHANGE"})
	// A large report widens the window between reading the latest version and
	// inserting the next one, which is where concurrent attempts used to collide.
	for i := 0; i < 60; i++ {
		key := fmt.Sprintf("room-%03d", i)
		input.Requirements = append(input.Requirements, aipRequirement(key, fmt.Sprintf("Ambiente %03d", i), "COMPLETED", nil))
		input.Evidence = append(input.Evidence, report.Evidence{ID: identity.NewID().String(), RequirementKey: key, Role: "CURRENT", Description: "Vistoria atual"})
	}
	const attempts = 8
	// Warm one connection per attempt so they really run side by side.
	sqlDB, err := e.h.AdminDB.DB()
	e.must(err)
	sqlDB.SetMaxIdleConns(attempts * 2)
	var warm sync.WaitGroup
	for i := 0; i < attempts; i++ {
		warm.Add(1)
		go func() {
			defer warm.Done()
			_ = e.h.AdminDB.Exec("SELECT pg_sleep(0.2)").Error
		}()
	}
	warm.Wait()
	var wg sync.WaitGroup
	results := make(chan Result, attempts)
	errs := make(chan error, attempts)
	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := Create(e.ctx, e.h.AdminDB, input, time.Now())
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("a generation attempt failed instead of being absorbed: %v", err)
	}
	var first Result
	for result := range results {
		if first.SnapshotID == (identity.ID{}) {
			first = result
		}
		if result.SnapshotID != first.SnapshotID || result.Version != 1 || result.JSONDigest != first.JSONDigest {
			t.Fatalf("attempts returned different logical reports: %+v vs %+v", result, first)
		}
	}
	if rows := e.snapshots(); rows != 1 {
		t.Fatalf("%d snapshot rows were retained for one logical report, want 1", rows)
	}
}

// TestAIP_IT_267RepeatedGenerationAndDownloadKeepContentAndIdentityStable covers IT-267.
func TestAIP_IT_267RepeatedGenerationAndDownloadKeepContentAndIdentityStable(t *testing.T) {
	e := newAIPReportEnv(t)
	input := e.input("NORMAL", nil)
	first := e.create(input)
	row1, err := e.getReport(e.f.TenantID, get_report.Input{TenantID: e.f.TenantID, InspectionID: e.f.InspectionID})
	e.must(err)
	for i := 0; i < 3; i++ {
		again := e.create(input)
		if again.SnapshotID != first.SnapshotID || again.Version != 1 || again.JSONDigest != first.JSONDigest || again.HTMLDigest != first.HTMLDigest {
			t.Fatalf("regeneration %d changed the report: %+v vs %+v", i, again, first)
		}
	}
	row2, err := e.getReport(e.f.TenantID, get_report.Input{TenantID: e.f.TenantID, InspectionID: e.f.InspectionID})
	e.must(err)
	if row1.ID != row2.ID || !bytes.Equal(row1.CanonicalJSON, row2.CanonicalJSON) || !bytes.Equal(row1.HTML, row2.HTML) || row1.JSONDigest != row2.JSONDigest || e.snapshots() != 1 {
		t.Fatal("content or identity changed across retries")
	}
	// A real change appends a new version; the retried one stays untouched.
	changed := e.input("ATTENTION", []string{"OBSERVED_CHANGE"})
	next := e.create(changed)
	if next.SnapshotID == first.SnapshotID || next.Version != 2 {
		t.Fatalf("a changed report must append version 2: %+v", next)
	}
	oldAgain, err := e.getReport(e.f.TenantID, get_report.Input{TenantID: e.f.TenantID, InspectionID: e.f.InspectionID, Version: ptr(1)})
	e.must(err)
	if oldAgain.ID != first.SnapshotID || !bytes.Equal(oldAgain.CanonicalJSON, row1.CanonicalJSON) {
		t.Fatal("version 1 changed after version 2 was appended")
	}
	// Download retries return the same artifact and object.
	key := "tenant/" + e.f.TenantID.String() + "/reports/" + first.SnapshotID.String() + ".pdf"
	e.must(e.h.Store.Client.Put(e.ctx, e.h.Store.Bucket, key, strings.NewReader("%PDF-1.4"), 8, "application/pdf"))
	e.must(e.h.AdminDB.Create(&database.ReportArtifact{ID: identity.NewID(), TenantID: e.f.TenantID, SnapshotID: first.SnapshotID, Kind: "PDF", ObjectKey: key, SHA256: "aip-sha", Status: "READY", CreatedAt: time.Now().UTC()}).Error)
	query, err := download_pdf.Setup(download_pdf.Dependencies{DB: e.h.AdminDB, Store: e.h.Store})
	e.must(err)
	var artifactID identity.ID
	for i := 0; i < 3; i++ {
		result, err := query(e.ctx, download_pdf.Input{TenantID: e.f.TenantID, SnapshotID: first.SnapshotID})
		e.must(err)
		if i == 0 {
			artifactID = result.Artifact.ID
		}
		if result.Artifact.ID != artifactID || result.Artifact.SHA256 != "aip-sha" || result.Artifact.SnapshotID != first.SnapshotID || result.URL == "" {
			t.Fatalf("download %d returned a different artifact: %+v", i, result.Artifact)
		}
	}
}

func ptr(value int) *int { return &value }

// TestAIP_IT_270ManyEvidencePairsStayCompleteInStorageHTMLAndPDF covers IT-270.
func TestAIP_IT_270ManyEvidencePairsStayCompleteInStorageHTMLAndPDF(t *testing.T) {
	e := newAIPReportEnv(t)
	const pairs = 80
	input := e.input("ATTENTION", []string{"OBSERVED_CHANGE"})
	assets := pdf.AssetBundle{}
	noChange := false
	for i := 0; i < pairs; i++ {
		key := fmt.Sprintf("room-%03d", i)
		originID, currentID := identity.NewID().String(), identity.NewID().String()
		requirement := aipRequirement(key, fmt.Sprintf("Ambiente %03d", i), "COMPLETED", &noChange)
		requirement.AnalysisMode = "COMPARE_ORIGIN_CURRENT"
		input.Requirements = append(input.Requirements, requirement)
		input.Evidence = append(input.Evidence,
			report.Evidence{ID: originID, RequirementKey: key, Role: "REFERENCE", Description: "Referência"},
			report.Evidence{ID: currentID, RequirementKey: key, Role: "CURRENT", Description: "Vistoria atual"})
		input.Findings = append(input.Findings, report.Finding{Category: "CONSERVATION", Title: fmt.Sprintf("Mudança %03d", i), Description: "Alteração visual observada no ambiente.", Severity: "LOW", Confidence: .9, EvidenceIDs: []string{originID, currentID}, Quality: "ADEQUATE", RecommendedAction: "REVIEW"})
		if i%10 == 0 {
			assets["evidence-"+originID+".jpg"], assets["evidence-"+currentID+".jpg"] = aipJPEG(t), aipJPEG(t)
		}
	}
	result := e.create(input)
	row, err := e.getReport(e.f.TenantID, get_report.Input{TenantID: e.f.TenantID, InspectionID: e.f.InspectionID})
	e.must(err)
	if row.ID != result.SnapshotID {
		t.Fatal("stored report is not the generated one")
	}
	var stored report.Snapshot
	e.must(json.Unmarshal(row.CanonicalJSON, &stored))
	if len(stored.Evidence) != pairs*2 || len(stored.Requirements) != pairs || len(stored.Findings) != pairs {
		t.Fatalf("stored %d evidence, %d requirements and %d findings; want %d/%d/%d", len(stored.Evidence), len(stored.Requirements), len(stored.Findings), pairs*2, pairs, pairs)
	}
	var html string
	e.must(json.Unmarshal(row.HTML, &html))
	if got := strings.Count(html, `data-evidence-id="`); got != pairs*2 {
		t.Fatalf("HTML renders %d of %d evidence items", got, pairs*2)
	}
	for i := 0; i < pairs; i++ {
		if !strings.Contains(html, fmt.Sprintf("Mudança %03d", i)) || !strings.Contains(html, fmt.Sprintf("Ambiente %03d", i)) {
			t.Fatalf("pair %d is missing from the HTML", i)
		}
	}
	rendered, err := (render_pdf.MarotoRenderer{}).Render(e.ctx, render_pdf.PDFDocument{SnapshotID: result.SnapshotID.String(), Version: result.Version, Snapshot: stored, Internal: true}, assets)
	if err != nil {
		t.Fatalf("PDF with %d evidence pairs failed: %v", pairs, err)
	}
	if pages := render_pdf.PageCount(rendered); pages < 10 || pages > 200 {
		t.Fatalf("PDF has %d pages for %d pairs, want a paginated document", pages, pairs)
	}
}

func aipJPEG(t *testing.T) []byte {
	t.Helper()
	return harnessJPEG(t)
}

// TestAIP_UT_037PublishedReportCannotBeMutatedByTheDatabase covers the
// published-mutation half of UT-037 on PostgreSQL.
func TestAIP_UT_037PublishedReportCannotBeMutatedByTheDatabase(t *testing.T) {
	e := newAIPReportEnv(t)
	result := e.create(e.input("NORMAL", nil))
	before, err := e.getReport(e.f.TenantID, get_report.Input{TenantID: e.f.TenantID, InspectionID: e.f.InspectionID})
	e.must(err)
	statements := []string{
		"UPDATE reports.report_snapshots SET classification='CRITICAL' WHERE id=?",
		"UPDATE reports.report_snapshots SET canonical_json='{}'::jsonb WHERE id=?",
		"UPDATE reports.report_snapshots SET json_digest='tampered' WHERE id=?",
		"UPDATE reports.report_snapshots SET html='\"\"'::jsonb WHERE id=?",
		"UPDATE reports.report_snapshots SET version_number=version_number+1 WHERE id=?",
		"DELETE FROM reports.report_snapshots WHERE id=?",
	}
	for _, statement := range statements {
		if err := e.read(e.f.TenantID, func(tx *gorm.DB) error { return tx.Exec(statement, result.SnapshotID).Error }); err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Fatalf("the runtime role mutated a published report with %q: %v", statement, err)
		}
		if err := e.h.AdminDB.Exec(statement, result.SnapshotID).Error; err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Fatalf("the privileged role mutated a published report with %q: %v", statement, err)
		}
	}
	after, err := e.getReport(e.f.TenantID, get_report.Input{TenantID: e.f.TenantID, InspectionID: e.f.InspectionID})
	e.must(err)
	if !bytes.Equal(before.CanonicalJSON, after.CanonicalJSON) || before.JSONDigest != after.JSONDigest || before.Classification != after.Classification || before.VersionNumber != after.VersionNumber {
		t.Fatal("the stored report changed")
	}
	// Only the retention purge may remove a report, and only inside its marked transaction.
	purged := e.h.AdminDB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT set_config('app.retention_purge','on',true)").Error; err != nil {
			return err
		}
		return tx.Exec("DELETE FROM reports.report_snapshots WHERE id=?", result.SnapshotID).Error
	})
	if purged != nil || e.snapshots() != 0 {
		t.Fatalf("the retention purge could not remove the report: %v", purged)
	}
}
