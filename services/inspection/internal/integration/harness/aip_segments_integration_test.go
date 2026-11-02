//go:build integration

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	classifyinspection "inspection/services/inspection/internal/features/analysis/classify_inspection"
	processcomparison "inspection/services/inspection/internal/features/analysis/process_comparison"
	analysisprompt "inspection/services/inspection/internal/features/analysis/prompt"
	requestcomparisons "inspection/services/inspection/internal/features/analysis/request_comparisons"
	mediaprocessverified "inspection/services/inspection/internal/features/media/process_verified"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	handleclassified "inspection/services/inspection/internal/features/reports/handle_classified"
	segmentresolve "inspection/services/inspection/internal/features/segments/resolve_definition"
	"inspection/services/inspection/internal/features/templates/catalog"
	templatecore "inspection/services/inspection/internal/features/templates/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/sensitivecontent"

	"gorm.io/gorm"
)

type segmentRefs struct{ segment, analysisType string }

func (r segmentRefs) SegmentExists(value string) bool      { return value == r.segment }
func (r segmentRefs) AnalysisTypeExists(value string) bool { return value == r.analysisType }

// segmentCase describes what distinguishes one curated segment: its template,
// participant roles and suggested environments. Everything else is the shared
// pipeline.
type segmentCase struct {
	segmentKey, templateKey, templateName string
	roles, environments                   []string
}

var curatedSegments = []segmentCase{
	{"property", "property-periodic", "Vistoria periódica do imóvel", []string{"TENANT_PARTICIPANT", "PROPERTY_OWNER"}, []string{"LIVING_ROOM", "KITCHEN", "BEDROOM", "BATHROOM", "LAUNDRY", "BALCONY", "OTHER"}},
	{"construction", "construction-progress", "Acompanhamento de obra", []string{"CONSTRUCTION_RESPONSIBLE", "CONTRACTOR"}, []string{"LIVING_ROOM", "KITCHEN", "BEDROOM", "BATHROOM", "EXTERNAL_AREA", "OTHER"}},
	{"cleaning", "cleaning-quality", "Vistoria de limpeza", []string{"CLEANING_EXECUTOR", "CLEANING_SUPERVISOR"}, []string{"LIVING_ROOM", "KITCHEN", "BEDROOM", "BATHROOM", "TOILET", "SERVICE_AREA", "OTHER"}},
}

// segmentPipelineResult is what one run of the common pipeline leaves behind.
type segmentPipelineResult struct {
	fixture             Task06Fixture
	version             database.TemplateVersion
	snapshot            database.ReportSnapshot
	classifiedEvent     events.RawEnvelope
	comparisonCompleted events.RawEnvelope
	classify            func(context.Context, *gorm.DB, events.RawEnvelope) error
	handleClassified    func(context.Context, *gorm.DB, events.RawEnvelope) error
}

// publishSegmentTemplate publishes and activates the curated seed of one
// segment through the production template service and points the fixture's
// inspection and capture draft at it.
func publishSegmentTemplate(ctx context.Context, t *testing.T, h *Harness, fixture Task06Fixture, segment segmentCase) database.TemplateVersion {
	t.Helper()
	// The shared fixture stores its segment version as ACTIVE, while the
	// production publisher resolves PUBLISHED versions; align it with the
	// real lifecycle before publishing templates against it.
	if err := h.AdminDB.Model(&database.SegmentDefinitionVersion{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.SegmentVersionID).Update("status", "PUBLISHED").Error; err != nil {
		t.Fatalf("publish the fixture segment version: %v", err)
	}
	bus := mediator.New()
	if err := segmentresolve.Setup(segmentresolve.Dependencies{DB: h.DB, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	admin := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: fixture.TenantID, CorrelationID: "aip-segments", Principal: requestctx.Principal{IdentityID: fixture.IdentityID, TenantID: fixture.TenantID, Roles: []string{auth.TenantAdmin}, ProductEntitlements: []string{auth.AdminProduct}}})
	segmentID := fixture.SegmentVersionID.String()
	seeds := catalog.CuratedSeeds(analysisprompt.RealEstate, segmentID, segmentID, segmentID)
	if err := catalog.ValidateSeeds(seeds, segmentRefs{segment: segmentID, analysisType: analysisprompt.RealEstate}); err != nil {
		t.Fatalf("curated seeds do not compile: %v", err)
	}
	var seed *catalog.Seed
	for index := range seeds {
		if seeds[index].TemplateKey == segment.templateKey {
			seed = &seeds[index]
		}
	}
	if seed == nil {
		t.Fatalf("no curated seed %q", segment.templateKey)
	}
	payload, err := json.Marshal(seed.Document)
	if err != nil {
		t.Fatal(err)
	}
	service := templatecore.Service{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}
	view, err := service.Publish(admin, fixture.TenantID, seed.TemplateKey, segment.templateName, "aip-seed:"+seed.TemplateKey, payload)
	if err != nil {
		t.Fatalf("publish %s seed: %v", seed.TemplateKey, err)
	}
	view, err = service.Activate(admin, fixture.TenantID, view.Version.ID, view.Template.Version)
	if err != nil {
		t.Fatalf("activate %s seed: %v", seed.TemplateKey, err)
	}
	if err := h.AdminDB.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Updates(map[string]any{"template_id": view.Template.ID, "template_version_id": view.Version.ID}).Error; err != nil {
		t.Fatalf("point the inspection at the %s template: %v", seed.TemplateKey, err)
	}
	// The draft snapshot is immutable by trigger once created. The shared
	// fixture creates its draft before this segment template exists, so the
	// owner role aligns that one column with the inspection (triggers off for
	// this single statement only); production creates both from one version.
	if err := h.AdminDB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL session_replication_role = replica").Error; err != nil {
			return err
		}
		return tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).Update("template_version_id", view.Version.ID).Error
	}); err != nil {
		t.Fatalf("point the capture draft at the %s template: %v", seed.TemplateKey, err)
	}
	return view.Version
}

// runSegmentPipeline runs template -> inspection -> media triage -> analysis
// (stubbed provider) -> classification -> report snapshot for one segment,
// using the same production handlers for every segment.
func runSegmentPipeline(ctx context.Context, t *testing.T, h *Harness, segment segmentCase) segmentPipelineResult {
	t.Helper()
	fixture, err := h.SeedTask06Fixture(ctx, Task06FixtureOptions{ComparisonMode: "CURRENT_ONLY"})
	if err != nil {
		t.Fatal(err)
	}
	result := segmentPipelineResult{fixture: fixture, version: publishSegmentTemplate(ctx, t, h, fixture, segment)}

	// Rewind the submitted fixture to the point where the real journey runs
	// media triage: an OPEN draft and a decodable VERIFIED original.
	photoBytes := photoFlowPNG(t)
	var original database.MediaObject
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.TenantID, fixture.MediaID).Take(&original).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, original.ObjectKey, bytes.NewReader(photoBytes), int64(len(photoBytes)), "image/png"); err != nil {
		t.Fatalf("upload decodable original: %v", err)
	}
	mediaWhere := func() *gorm.DB {
		return h.AdminDB.Model(&database.MediaObject{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.MediaID)
	}
	if err := h.AdminDB.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).Update("status", "OPEN").Error; err != nil {
		t.Fatal(err)
	}
	for _, step := range []map[string]any{
		{"status": "RECEIVED"},
		{"sha256": digest(photoBytes), "size_bytes": int64(len(photoBytes))},
		{"status": "VERIFIED"},
	} {
		if err := mediaWhere().Updates(step).Error; err != nil {
			t.Fatalf("rewind media %v: %v", step, err)
		}
	}
	verified, err := mediaprocessverified.Setup(mediaprocessverified.Dependencies{Store: objectstore.Store{Client: h.Store.Client, Bucket: h.Store.Bucket}, Detector: sensitivecontent.NewDisabledDetector()})
	if err != nil {
		t.Fatal(err)
	}
	verifiedPayload, _ := json.Marshal(map[string]any{"mediaId": fixture.MediaID})
	verifiedEnvelope := events.RawEnvelope{ID: identity.NewID(), Type: "media.verified.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: fixture.TenantID, AggregateID: fixture.MediaID, CorrelationID: "aip-segment-verified-" + fixture.MediaID.String(), Payload: verifiedPayload}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error { return verified(ctx, tx, verifiedEnvelope) }); err != nil {
		t.Fatalf("process verified media: %v", err)
	}
	if err := h.AdminDB.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).Update("status", "SUBMITTED").Error; err != nil {
		t.Fatal(err)
	}

	schedule, err := requestcomparisons.Setup(requestcomparisons.Dependencies{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	requestPayload, _ := json.Marshal(map[string]any{"inspectionId": fixture.InspectionID, "draftId": fixture.DraftID, "kind": "INSPECTION"})
	submitted := rawPhotoSubmission(fixture.TenantID, fixture.InspectionID, requestPayload)
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error { return schedule(ctx, tx, submitted) }); err != nil {
		t.Fatalf("schedule comparison: %v", err)
	}
	// Replaying the submission must not create a second job.
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error { return schedule(ctx, tx, submitted) }); err != nil {
		t.Fatalf("replayed submission: %v", err)
	}
	requested, err := loadOutboxEnvelope(ctx, h, fixture.TenantID, "analysis.comparison_requested.v1")
	if err != nil {
		t.Fatal(err)
	}
	process, err := processcomparison.Setup(processcomparison.Dependencies{Gateway: photoWorkerStoppedGateway{}, BuildRequest: func(context.Context, *gorm.DB, database.ComparisonJob, processcomparison.Payload) (llm.StructuredRequest, error) {
		return llm.StructuredRequest{ModelAlias: "inspection-vision", PromptDigest: "segment-fixture", SystemPrompt: "Classify only visible evidence.", UserPrompt: "Inspect the current photo.", JSONSchema: json.RawMessage(`{"type":"object"}`), MinimumConfidenceBPS: 8500, Mode: "CURRENT_ONLY", Images: []llm.NormalizedImage{{EvidenceID: "SEGMENT-1", Source: "CURRENT", Digest: "fixture-digest", DataURL: "data:image/png;base64,AAAA"}}}, nil
	}, Now: time.Now, Mode: "mock"})
	if err != nil {
		t.Fatal(err)
	}
	var requestBody struct {
		JobID identity.ID `json:"jobId"`
	}
	if err := json.Unmarshal(requested.Payload, &requestBody); err != nil || requestBody.JobID == (identity.ID{}) {
		t.Fatalf("decode comparison request: %v", err)
	}
	processPayload, _ := json.Marshal(processcomparison.Payload{JobID: requestBody.JobID})
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error { return process(ctx, tx, processPayload, fixture.TenantID) }); err != nil {
		t.Fatalf("process comparison: %v", err)
	}
	result.comparisonCompleted, err = loadOutboxEnvelope(ctx, h, fixture.TenantID, "analysis.comparison_completed.v1")
	if err != nil {
		t.Fatal(err)
	}
	if result.classify, err = classifyinspection.Setup(classifyinspection.Dependencies{Now: time.Now}); err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error { return result.classify(ctx, tx, result.comparisonCompleted) }); err != nil {
		t.Fatalf("classify: %v", err)
	}
	result.classifiedEvent, err = loadOutboxEnvelope(ctx, h, fixture.TenantID, "inspection.classified.v1")
	if err != nil {
		t.Fatal(err)
	}
	createSnapshot, err := generate_snapshot.Setup(generate_snapshot.Dependencies{DB: h.DB, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	if result.handleClassified, err = handleclassified.Setup(handleclassified.Dependencies{Notifications: photoFlowNotifications{}, CreateSnapshot: createSnapshot, Now: time.Now}); err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error { return result.handleClassified(ctx, tx, result.classifiedEvent) }); err != nil {
		t.Fatalf("handle classified event: %v", err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND inspection_id=?", fixture.TenantID, fixture.InspectionID).Order("version_number DESC").Take(&result.snapshot).Error
	}); err != nil {
		t.Fatalf("load report snapshot: %v", err)
	}
	return result
}

// Every curated segment passes through the same template -> inspection ->
// analysis -> classification -> report pipeline on PostgreSQL, keeps only its
// own context in the report, and survives replays with one result
// (IT-321..IT-340 pipeline contract of the construction and cleaning seeds,
// shared with the property seed).
func TestAIP_IT321To340EverySegmentRunsTheCommonPipelineOnPostgres(t *testing.T) {
	results := map[string]segmentPipelineResult{}
	for _, segment := range curatedSegments {
		segment := segment
		t.Run(segment.segmentKey, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			h, err := NewFromEnv(ctx)
			if err != nil {
				t.Skipf("integration dependencies unavailable: %v", err)
			}
			t.Cleanup(func() { _ = h.Close() })
			run := runSegmentPipeline(ctx, t, h, segment)
			results[segment.segmentKey] = run
			fixture := run.fixture

			// Template: the published curated seed keeps its segment-specific
			// roles and environments and the shared reference-free mode.
			var document catalog.TemplateDocument
			if err := json.Unmarshal(run.version.DefinitionJSON, &document); err != nil {
				t.Fatal(err)
			}
			if document.SchemaVersion != catalog.EnvironmentSchemaVersion || document.CaptureLayout != catalog.EnvironmentsLayout || document.DefaultComparisonMode != catalog.ChecklistOnly || document.AnalysisType != analysisprompt.RealEstate {
				t.Fatalf("seed contract: %+v", document)
			}
			if strings.Join(document.ParticipantRoles, ",") != strings.Join(segment.roles, ",") || strings.Join(document.SuggestedEnvironmentTypes, ",") != strings.Join(segment.environments, ",") {
				t.Fatalf("segment context lost: roles=%v environments=%v", document.ParticipantRoles, document.SuggestedEnvironmentTypes)
			}

			// Analysis and classification: one completed job, one run each.
			if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
				var jobs []database.ComparisonJob
				if err := tx.Where("tenant_id=? AND inspection_id=?", fixture.TenantID, fixture.InspectionID).Find(&jobs).Error; err != nil {
					return err
				}
				if len(jobs) != 1 || jobs[0].Status != "COMPLETED" {
					t.Errorf("comparison jobs = %+v, want one COMPLETED", jobs)
				}
				var analysisRuns, classifications, snapshots int64
				if err := tx.Model(&database.AnalysisRun{}).Where("tenant_id=? AND job_id IN (SELECT id FROM analysis.comparison_jobs WHERE tenant_id=? AND inspection_id=?)", fixture.TenantID, fixture.TenantID, fixture.InspectionID).Count(&analysisRuns).Error; err != nil {
					return err
				}
				if err := tx.Model(&database.ClassificationRun{}).Where("tenant_id=? AND inspection_id=?", fixture.TenantID, fixture.InspectionID).Count(&classifications).Error; err != nil {
					return err
				}
				if err := tx.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", fixture.TenantID, fixture.InspectionID).Count(&snapshots).Error; err != nil {
					return err
				}
				if analysisRuns != 1 || classifications != 1 || snapshots != 1 {
					t.Errorf("results after the replayed submission: analysis=%d classification=%d report=%d, want 1 each", analysisRuns, classifications, snapshots)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}

			// Report: immutable internal snapshot carrying this segment's context only.
			var snapshot reportcore.Snapshot
			if err := json.Unmarshal(run.snapshot.CanonicalJSON, &snapshot); err != nil {
				t.Fatal(err)
			}
			if snapshot.Classification == "" || snapshot.Context.Template.Name != segment.templateName || snapshot.Context.Template.Version != 1 {
				t.Fatalf("report context template = %+v classification=%q, want %q v1", snapshot.Context.Template, snapshot.Classification, segment.templateName)
			}
			if snapshot.Context.Inspection.ProjectID != fixture.ProjectID.String() || snapshot.Context.Inspection.StageLabel == "" {
				t.Fatalf("report lost the project/stage timeline context: %+v", snapshot.Context.Inspection)
			}
			for _, other := range curatedSegments {
				if other.segmentKey != segment.segmentKey && strings.Contains(string(run.snapshot.CanonicalJSON)+string(run.snapshot.HTML), other.templateName) {
					t.Fatalf("report of %s names the %s template", segment.segmentKey, other.segmentKey)
				}
			}
			if len(snapshot.Evidence) != 1 || snapshot.Evidence[0].ID != fixture.MediaID.String() {
				t.Fatalf("report evidence = %+v, want the captured media %s", snapshot.Evidence, fixture.MediaID)
			}

			// Replaying the terminal events keeps one stage result and one report version.
			for attempt := 0; attempt < 2; attempt++ {
				if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error { return run.classify(ctx, tx, run.comparisonCompleted) }); err != nil {
					t.Fatalf("replayed classification: %v", err)
				}
				if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error { return run.handleClassified(ctx, tx, run.classifiedEvent) }); err != nil {
					t.Fatalf("replayed classified event: %v", err)
				}
			}
			var classifications, snapshots int64
			if err := h.AdminDB.Model(&database.ClassificationRun{}).Where("tenant_id=? AND inspection_id=?", fixture.TenantID, fixture.InspectionID).Count(&classifications).Error; err != nil {
				t.Fatal(err)
			}
			if err := h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", fixture.TenantID, fixture.InspectionID).Count(&snapshots).Error; err != nil {
				t.Fatal(err)
			}
			if classifications != 1 || snapshots != 1 {
				var versions []database.ReportSnapshot
				_ = h.AdminDB.Where("tenant_id=? AND inspection_id=?", fixture.TenantID, fixture.InspectionID).Order("version_number ASC").Find(&versions).Error
				var differences []string
				for i := 1; i < len(versions); i++ {
					var left, right any
					_ = json.Unmarshal(versions[i-1].CanonicalJSON, &left)
					_ = json.Unmarshal(versions[i].CanonicalJSON, &right)
					differences = append(differences, diffJSON(left, right, "$")...)
				}
				t.Fatalf("replayed terminal events produced %d classification runs and %d report versions, want 1 each; differing fields: %v", classifications, snapshots, differences)
			}
		})
	}
	// The same pipeline produced the same classification for every segment:
	// only the segment context differs.
	var reference string
	for _, segment := range curatedSegments {
		run, ok := results[segment.segmentKey]
		if !ok {
			continue
		}
		if reference == "" {
			reference = run.snapshot.Classification
		} else if run.snapshot.Classification != reference {
			t.Fatalf("%s classified as %s while another segment got %s through the same pipeline", segment.segmentKey, run.snapshot.Classification, reference)
		}
	}
}

// diffJSON lists the paths whose values differ between two decoded documents.
func diffJSON(left, right any, path string) []string {
	switch l := left.(type) {
	case map[string]any:
		r, ok := right.(map[string]any)
		if !ok {
			return []string{path}
		}
		var out []string
		for key, value := range l {
			out = append(out, diffJSON(value, r[key], path+"."+key)...)
		}
		for key := range r {
			if _, ok := l[key]; !ok {
				out = append(out, path+"."+key)
			}
		}
		return out
	case []any:
		r, ok := right.([]any)
		if !ok || len(r) != len(l) {
			return []string{path}
		}
		var out []string
		for index := range l {
			out = append(out, diffJSON(l[index], r[index], fmt.Sprintf("%s[%d]", path, index))...)
		}
		return out
	default:
		if fmt.Sprint(left) != fmt.Sprint(right) {
			return []string{path}
		}
		return nil
	}
}
