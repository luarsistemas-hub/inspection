//go:build integration

package resolvers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/features/analysis/classify_inspection"
	"inspection/services/inspection/internal/features/analysis/prompt"
	"inspection/services/inspection/internal/features/reports/core"
	generatesnapshot "inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/features/reports/handle_classified"
	"inspection/services/inspection/internal/features/reports/render_pdf"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

// r9MemoryObjects is a private object store kept in memory for the PDF executor.
type r9MemoryObjects struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (m *r9MemoryObjects) CreateMultipart(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (m *r9MemoryObjects) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "", nil
}
func (m *r9MemoryObjects) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (m *r9MemoryObjects) AbortMultipart(context.Context, string, string, string) error { return nil }
func (m *r9MemoryObjects) Head(context.Context, string, string) (objectstore.Head, error) {
	return objectstore.Head{}, nil
}
func (m *r9MemoryObjects) Get(_ context.Context, _, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.objects[key]
	if !ok {
		return nil, objectstore.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}
func (m *r9MemoryObjects) Put(_ context.Context, _, key string, reader io.Reader, _ int64, _ string) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = data
	return nil
}
func (m *r9MemoryObjects) Delete(_ context.Context, _, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

// TestR9ReissueWithComparativeInventoryPublishesNewVersionEndToEnd covers, on
// PostgreSQL, the reissue of a v3 photo report with a pinned scope and an
// ACTIVE inventory baseline: reissueReport opens the next COMPARE generation;
// the stale classified event of the previous generation is acknowledged as
// superseded (no version, request still QUEUED); the new generation finishes;
// the real classifier and classified handler publish v2 with the new inventory
// result and its confirmed pair, the supplemental handover media resolved through
// the PostgreSQL-only includeSupplementalMedia query; and the PDF executor
// renders the staff and customer PDFs of v2.
func TestR9ReissueWithComparativeInventoryPublishesNewVersionEndToEnd(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewPostgresFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	base, err := h.SeedInventoryBaseline(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	f := base.Task06Fixture
	var env database.InventoryEnvironment
	if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, base.EnvironmentIDs[0]).Take(&env).Error; err != nil {
		t.Fatal(err)
	}
	var sofa database.InventoryItem
	if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, base.ItemIDs[0]).Take(&sofa).Error; err != nil {
		t.Fatal(err)
	}

	// The inspection compares against the ACTIVE baseline with a pinned
	// inventory prompt and a pinned descriptive scope (v3).
	definition := prompt.DefaultDefinitionForTask("INVENTORY_COMPARE")
	payload, digest, err := prompt.CanonicalizeTask("INVENTORY_COMPARE", definition)
	if err != nil {
		t.Fatal(err)
	}
	var comparePrompt database.AnalysisPromptSnapshot
	if err := h.AdminDB.Where("analysis_type='REAL_ESTATE' AND task='INVENTORY_COMPARE' AND canonical_digest=?", digest).Take(&comparePrompt).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		comparePrompt = database.AnalysisPromptSnapshot{ID: identity.NewID(), AnalysisType: "REAL_ESTATE", Task: "INVENTORY_COMPARE", DefinitionJSON: payload, CanonicalDigest: digest, SchemaVersion: 1, CreatedAt: time.Now().UTC()}
		if err := h.AdminDB.Create(&comparePrompt).Error; err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", f.TenantID, f.InspectionID).Update("inventory_prompt_snapshot_id", comparePrompt.ID).Error; err != nil {
		t.Fatal(err)
	}
	// Reference snapshots are immutable: the fixture's snapshot is replaced as
	// inspection creation would have written it (same approach as the
	// process_inventory_job flow test).
	if err := h.AdminDB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT set_config('app.tenant_id', ?, true), set_config('app.retention_purge', 'on', true)", f.TenantID.String()).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Delete(&database.ReferenceSnapshot{}).Error; err != nil {
			return err
		}
		return tx.Create(&database.ReferenceSnapshot{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, ReferenceVersionID: &base.OriginVersionID, ComparisonMode: "FIXED_ORIGIN", Payload: json.RawMessage(`{}`), CreatedAt: time.Now().UTC()}).Error
	}); err != nil {
		t.Fatal(err)
	}
	roomKey := "new:" + f.EnvironmentIdentityID.String()
	requirementKey := "env:" + roomKey
	document, err := json.Marshal(struct {
		Rooms           []catalog.ExpectedRoom   `json:"rooms"`
		HandoverRecords []catalog.HandoverRecord `json:"handoverRecords"`
	}{
		Rooms:           []catalog.ExpectedRoom{{Key: roomKey, TypeCode: "KITCHEN", Name: "Cozinha", Components: []catalog.ExpectedComponent{{Key: "floor", TypeCode: "FLOOR", Label: "Piso", Views: []string{"wide"}}}}},
		HandoverRecords: []catalog.HandoverRecord{{Key: "keys", Kind: "KEY", Required: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var media database.MediaObject
	if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, f.MediaID).Take(&media).Error; err != nil {
		t.Fatal(err)
	}
	copyMedia := func(description string) identity.ID {
		t.Helper()
		row := media
		row.ID = identity.NewID()
		row.ObjectKey = media.ObjectKey + "-r9-" + row.ID.String()
		row.IdempotencyKey = "r9-reissue-media-" + row.ID.String()
		row.Description = description
		if err := h.AdminDB.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		return row.ID
	}
	handoverMedia := copyMedia("Chaves entregues")
	pairMedia := copyMedia("Sofá na vistoria atual")
	t0 := time.Now().UTC().Add(-10 * time.Minute)
	compareJob := func(generation int, status string, createdAt time.Time) database.InventoryJob {
		t.Helper()
		job := database.InventoryJob{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, OriginVersionID: base.OriginVersionID, Kind: "COMPARE", RequirementKey: "env:" + env.IdentityID.String(), EnvironmentIdentityID: &env.IdentityID, Generation: generation, Status: status, NextAttemptAt: createdAt, PromptSnapshotID: &comparePrompt.ID, CreatedAt: createdAt, UpdatedAt: createdAt}
		if err := h.AdminDB.Create(&job).Error; err != nil {
			t.Fatal(err)
		}
		return job
	}
	itemResult := func(jobID identity.ID, status, after string) {
		t.Helper()
		before := sofa.Condition
		row := database.InventoryItemResult{ID: identity.NewID(), TenantID: f.TenantID, JobID: jobID, InspectionID: f.InspectionID, ItemIdentityID: &sofa.IdentityID, Status: status, ConditionBefore: &before, CreatedAt: time.Now().UTC()}
		if after != "" {
			row.ConditionAfter = &after
		}
		if err := h.AdminDB.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		for _, row := range []any{
			&database.InspectionScopeSnapshot{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, TemplateVersionID: f.TemplateVersionID, Profile: "PROPERTY_COMPONENTS", Document: document, Digest: "r9-reissue-scope", Version: 1, CreatedAt: t0},
			&database.ComponentAnswer{ID: identity.NewID(), TenantID: f.TenantID, DraftID: f.DraftID, RequirementKey: requirementKey, ComponentKey: "floor", ViewKey: "wide", State: "OBSERVED", EvidenceIDs: json.RawMessage(`["` + f.MediaID.String() + `"]`), Version: 1, CreatedAt: t0, UpdatedAt: t0},
			&database.HandoverRecord{ID: identity.NewID(), TenantID: f.TenantID, DraftID: f.DraftID, RecordKey: "keys", Kind: "KEY", Payload: json.RawMessage(`{"keyType":"FRONT_DOOR","quantity":2}`), MediaIDs: json.RawMessage(`["` + handoverMedia.String() + `"]`), Declarant: "Participante", Version: 1, CreatedAt: t0, UpdatedAt: t0},
			&database.ComparisonJob{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, PromptSnapshotID: f.PromptSnapshotID, RequirementKey: requirementKey, ModelAlias: "test", InputDigest: "r9-reissue-findings", Status: "COMPLETED", CreatedAt: t0, UpdatedAt: t0},
			&database.PublicationPolicy{ID: identity.NewID(), TenantID: f.TenantID, Mode: "MANUAL", Version: 1, CreatedAt: t0, UpdatedAt: t0},
		} {
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	generation1 := compareJob(1, "SUCCEEDED", t0)
	itemResult(generation1.ID, "MISSING", "")
	// A v3 (strict) comparison only records a proven absence over the item's
	// confirmed pair; without it the issued comparison and inventory would
	// read "not observed".
	if err := h.AdminDB.Create(&database.InventoryEvidencePair{ID: identity.NewID(), TenantID: f.TenantID, JobID: generation1.ID, ItemIdentityID: sofa.IdentityID, OriginKind: "PHOTO", OriginMediaID: base.OriginMediaID, CurrentKind: "PHOTO", CurrentMediaID: pairMedia, Decision: "SAME_VIEW", Confirmed: true, CreatedAt: t0}).Error; err != nil {
		t.Fatal(err)
	}

	classifier, err := classify_inspection.Setup(classify_inspection.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	classify := func(label string) database.ClassificationRun {
		t.Helper()
		envelope := events.RawEnvelope{ID: identity.NewID(), Type: "analysis.comparison_completed.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: f.TenantID, AggregateID: f.InspectionID, CorrelationID: "r9-reissue-" + label, Payload: json.RawMessage(`{"inspectionId":"` + f.InspectionID.String() + `","force":true}`)}
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error { return classifier(ctx, tx, envelope) }); err != nil {
			t.Fatalf("%s classification failed: %v", label, err)
		}
		var run database.ClassificationRun
		if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Order("created_at DESC").Take(&run).Error; err != nil {
			t.Fatal(err)
		}
		return run
	}
	handler, err := handle_classified.Setup(handle_classified.Dependencies{Notifications: task04NoopNotificationService{}, CreateSnapshotInTx: func(ctx context.Context, tx *gorm.DB, input generatesnapshot.Input) (generatesnapshot.Result, error) {
		return generatesnapshot.CreateInTx(ctx, tx, input, time.Now().UTC())
	}})
	if err != nil {
		t.Fatal(err)
	}
	deliverClassified := func(run database.ClassificationRun) error {
		envelope := events.RawEnvelope{ID: identity.NewID(), Type: "inspection.classified.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: f.TenantID, AggregateID: f.InspectionID, CorrelationID: "r9-reissue-classified", Payload: json.RawMessage(`{"inspectionId":"` + f.InspectionID.String() + `","classificationRunId":"` + run.ID.String() + `","classification":"` + run.Classification + `","reasonCodes":[]}`)}
		return h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error { return handler(ctx, tx, envelope) })
	}
	latest := func() (database.ReportSnapshot, core.Snapshot) {
		t.Helper()
		var row database.ReportSnapshot
		if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Order("version_number DESC").Take(&row).Error; err != nil {
			t.Fatal(err)
		}
		var snapshot core.Snapshot
		if err := json.Unmarshal(row.CanonicalJSON, &snapshot); err != nil {
			t.Fatal(err)
		}
		return row, snapshot
	}
	sofaEntry := func(snapshot core.Snapshot) core.InventoryEntry {
		t.Helper()
		if snapshot.Inventory == nil {
			t.Fatal("report has no inventory section")
		}
		for _, entry := range snapshot.Inventory.Problems {
			if entry.Name == "Sofá" {
				return entry
			}
		}
		t.Fatalf("Sofá missing from inventory problems: %+v", snapshot.Inventory.Problems)
		return core.InventoryEntry{}
	}

	// Version 1: the first generation (Sofá missing).
	first := classify("first")
	if err := deliverClassified(first); err != nil {
		t.Fatalf("first report failed: %v", err)
	}
	v1Row, v1 := latest()
	if v1.SchemaVersion != 3 || v1.Checklist == nil || sofaEntry(v1).Status != "MISSING" {
		t.Fatalf("v1 schema=%d checklist=%v sofa=%+v", v1.SchemaVersion, v1.Checklist != nil, sofaEntry(v1))
	}

	// reissueReport opens generation 2 through the production Reanalyze.
	meta := requestctx.Metadata{TenantID: f.TenantID, Principal: requestctx.Principal{IdentityID: f.IdentityID, MembershipID: f.MembershipID, TenantID: f.TenantID, Roles: []string{auth.TenantAdmin}, ProductEntitlements: auth.ProductsForRole(auth.TenantAdmin)}}
	resolver := &mutationResolver{Resolver: &Resolver{DB: h.DB}}
	reissued, err := resolver.reissueReport(requestctx.WithMetadata(ctx, meta), graphql1.ReissueReportInput{InspectionID: f.InspectionID.String(), ExpectedVersion: v1Row.VersionNumber, ClientMutationID: "r9-reissue-inventory"})
	if err != nil || reissued.Report == nil || reissued.Report.Version != v1Row.VersionNumber {
		t.Fatalf("reissueReport=%+v err=%v", reissued, err)
	}
	var generation2 database.InventoryJob
	if err := h.AdminDB.Where("tenant_id=? AND inspection_id=? AND kind='COMPARE' AND generation=2", f.TenantID, f.InspectionID).Take(&generation2).Error; err != nil || generation2.Status != "PENDING" {
		t.Fatalf("generation 2=%+v err=%v", generation2, err)
	}
	requestOutcome := func() string {
		t.Helper()
		var request database.AuditEvent
		if err := h.AdminDB.Where("tenant_id=? AND action='report.reissue_requested' AND target_id=?", f.TenantID, f.InspectionID.String()).Order("occurred_at DESC").Take(&request).Error; err != nil {
			t.Fatal(err)
		}
		return request.Outcome
	}
	versions := func() int64 {
		t.Helper()
		var count int64
		if err := h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		return count
	}

	// Item 3: the classified event of generation 1, redelivered while
	// generation 2 is pending and again after it finished, is superseded: it
	// is acknowledged without a version and leaves the request QUEUED.
	if err := deliverClassified(first); err != nil {
		t.Fatalf("superseded event with pending generation err=%v; want acknowledged", err)
	}
	if err := h.AdminDB.Model(&database.InventoryJob{}).Where("tenant_id=? AND id=?", f.TenantID, generation2.ID).Updates(map[string]any{"status": "SUCCEEDED", "updated_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	itemResult(generation2.ID, "CONDITION_CHANGED", "FAIR")
	// PostgreSQL only accepts confirmed SAME_VIEW pairs; an unconfirmed
	// UNCERTAIN candidate never reaches the report.
	for _, pair := range []database.InventoryEvidencePair{
		{ID: identity.NewID(), TenantID: f.TenantID, JobID: generation2.ID, ItemIdentityID: sofa.IdentityID, OriginKind: "PHOTO", OriginMediaID: base.OriginMediaID, CurrentKind: "PHOTO", CurrentMediaID: pairMedia, Decision: "SAME_VIEW", Confirmed: true, CreatedAt: time.Now().UTC()},
		{ID: identity.NewID(), TenantID: f.TenantID, JobID: generation2.ID, ItemIdentityID: sofa.IdentityID, OriginKind: "PHOTO", OriginMediaID: pairMedia, CurrentKind: "PHOTO", CurrentMediaID: f.MediaID, Decision: "UNCERTAIN", Confirmed: false, CreatedAt: time.Now().UTC()},
	} {
		if err := h.AdminDB.Create(&pair).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := deliverClassified(first); err != nil {
		t.Fatalf("superseded event with terminal generation err=%v; want acknowledged", err)
	}
	if count := versions(); count != 1 {
		t.Fatalf("superseded classification published %d versions; want only v1", count)
	}
	if outcome := requestOutcome(); outcome != "QUEUED" {
		t.Fatalf("superseded event closed the reissue request as %s", outcome)
	}

	// The classification of generation 2 publishes v2 with its inventory.
	second := classify("second")
	if second.ID == first.ID || !second.CreatedAt.After(generation2.CreatedAt) {
		t.Fatalf("second classification=%+v; want a new run after generation 2", second)
	}
	if err := deliverClassified(second); err != nil {
		t.Fatalf("reissued report failed: %v", err)
	}
	v2Row, v2 := latest()
	if v2Row.VersionNumber != v1Row.VersionNumber+1 {
		t.Fatalf("reissue versions v1=%d v2=%d", v1Row.VersionNumber, v2Row.VersionNumber)
	}
	entry := sofaEntry(v2)
	if entry.Status != "CONDITION_CHANGED" || entry.ConditionAfter != "FAIR" || len(entry.Pairs) != 1 || entry.Pairs[0].Decision != "SAME_VIEW" || entry.Pairs[0].Current != pairMedia.String() {
		t.Fatalf("v2 inventory entry=%+v; want generation 2 result and pair", entry)
	}
	if v2.Inventory.Coverage == nil || v2.Inventory.Coverage.ExpectedItems != 1 || v2.Inventory.Coverage.AssessedItems != 1 {
		t.Fatalf("v2 inventory coverage=%+v", v2.Inventory.Coverage)
	}
	var handoverEvidence *core.Evidence
	for i := range v2.Evidence {
		if v2.Evidence[i].ID == handoverMedia.String() {
			handoverEvidence = &v2.Evidence[i]
		}
	}
	if handoverEvidence == nil || handoverEvidence.Role != "CURRENT" || handoverEvidence.Description != "Chaves entregues" {
		t.Fatalf("handover media was not resolved by includeSupplementalMedia: %+v", v2.Evidence)
	}
	declared := false
	for _, handover := range v2.Handover.Entries {
		declared = declared || (handover.Key == "keys" && handover.Status == "DECLARED")
	}
	if !declared || len(v2.Checklist.Rooms) != 1 || v2.Checklist.Rooms[0].Components[0].Status == "NOT_OBSERVED" {
		t.Fatalf("v2 descriptive sections handover=%+v checklist=%+v", v2.Handover, v2.Checklist)
	}
	if outcome := requestOutcome(); outcome != "COMPLETED" {
		t.Fatalf("reissue request outcome=%s; want COMPLETED", outcome)
	}
	var published int64
	if err := h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND snapshot_id=? AND status='PUBLISHED'", f.TenantID, v2Row.ID).Count(&published).Error; err != nil || published != 1 {
		t.Fatalf("v2 publications=%d err=%v; want one active publication", published, err)
	}
	var unchanged database.ReportSnapshot
	if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, v1Row.ID).Take(&unchanged).Error; err != nil || !bytes.Equal(unchanged.CanonicalJSON, v1Row.CanonicalJSON) {
		t.Fatalf("v1 changed after the reissue (err=%v)", err)
	}

	// The PDF jobs of v2 are queued as on report.snapshot_created and the real
	// executor renders both audiences. Queued work of other tenants sharing
	// this disposable database is pushed out of this run.
	if err := h.AdminDB.Model(&database.ReportPDFJob{}).Where("status='QUEUED' AND tenant_id<>?", f.TenantID).Update("next_attempt_at", time.Now().Add(24*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.ReportPDFJob{}).Where("status='PROCESSING' AND tenant_id<>?", f.TenantID).Update("lease_expires_at", time.Now().Add(24*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	queue, err := render_pdf.Setup(render_pdf.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	created := events.RawEnvelope{ID: identity.NewID(), Type: "report.snapshot_created.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: f.TenantID, AggregateID: f.InspectionID, CorrelationID: "r9-reissue-pdf", Payload: json.RawMessage(`{"inspectionId":"` + f.InspectionID.String() + `","snapshotId":"` + v2Row.ID.String() + `","version":` + fmt.Sprint(v2Row.VersionNumber) + `}`)}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error { return queue(ctx, tx, created) }); err != nil {
		t.Fatalf("queue v2 PDF jobs: %v", err)
	}
	objects := &r9MemoryObjects{objects: map[string][]byte{}}
	executor, err := render_pdf.SetupExecutor(h.AdminDB, objectstore.Store{Bucket: "private", Client: objects}, render_pdf.DefaultLimits(), time.Now, "")
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		worked, err := executor.RunDue(ctx)
		if err != nil {
			t.Fatalf("PDF executor: %v", err)
		}
		if !worked {
			break
		}
	}
	var jobs []database.ReportPDFJob
	if err := h.AdminDB.Where("tenant_id=? AND snapshot_id=?", f.TenantID, v2Row.ID).Find(&jobs).Error; err != nil || len(jobs) != 2 {
		t.Fatalf("v2 PDF jobs=%+v err=%v", jobs, err)
	}
	for _, job := range jobs {
		if job.Status != "READY" || job.ArtifactID == nil {
			t.Fatalf("v2 %s PDF job=%+v; want READY with an artifact", job.Audience, job)
		}
		var artifact database.ReportArtifact
		if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, *job.ArtifactID).Take(&artifact).Error; err != nil {
			t.Fatal(err)
		}
		objects.mu.Lock()
		data := objects.objects[artifact.ObjectKey]
		objects.mu.Unlock()
		if !bytes.HasPrefix(data, []byte("%PDF")) || render_pdf.PageCount(data) == 0 {
			t.Fatalf("v2 %s artifact %s is not a rendered PDF (%d bytes)", job.Audience, artifact.ObjectKey, len(data))
		}
		if job.Audience == "PDF_CUSTOMER" && strings.Contains(string(data), base.OriginVersionID.String()) {
			t.Fatal("customer PDF exposed the internal baseline version ID")
		}
	}
}
