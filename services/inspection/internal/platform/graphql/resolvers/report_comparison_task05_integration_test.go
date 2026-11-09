//go:build integration

package resolvers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/features/analysis/prompt"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	generatesnapshot "inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/features/reports/render_pdf"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/database/migrations"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

// IT-019: an existing media-only comparison report (versions 1 and 2) stays
// readable after the approved-baseline creation gate and the v3 migrations:
// both versions keep their issued JSON and HTML, the staff and customer reads
// return them without a comparison section, and their PDFs still render.
func TestIT019LegacyMediaOnlyComparisonReportStaysReadableAfterV3Rollout(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewPostgresFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	var applied int
	if err := h.AdminDB.Raw("SELECT coalesce(max(version),0) FROM platform.schema_migrations").Scan(&applied).Error; err != nil {
		t.Fatal(err)
	}
	if applied != migrations.LatestVersion() || applied < 130 {
		t.Fatalf("database at migration %d; want the latest (%d)", applied, migrations.LatestVersion())
	}
	f, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	// A legacy media-only origin (no inventory) pinned by a comparison
	// inspection created before the gate.
	legacy, issuedAt := identity.NewID(), time.Now().UTC().Add(-48*time.Hour)
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		origin := database.Origin{ID: identity.NewID(), TenantID: f.TenantID, AssetID: f.AssetID, ActiveVersionID: &legacy, Version: 1, CreatedAt: issuedAt, UpdatedAt: issuedAt}
		if err := tx.Create(&origin).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.OriginVersion{ID: legacy, TenantID: f.TenantID, OriginID: origin.ID, VersionNumber: 1, Source: "CAPTURE", Status: "ACTIVE", IdempotencyKey: "it019-" + legacy.String(), CreatedAt: issuedAt, ActivatedAt: &issuedAt}).Error; err != nil {
			return err
		}
		return tx.Create(&database.OriginEvidence{ID: identity.NewID(), TenantID: f.TenantID, OriginVersionID: legacy, MediaID: f.MediaID, Category: "GERAL", Description: "Sala na entrada", AttentionItems: json.RawMessage(`[]`), CreatedAt: issuedAt}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT set_config('app.tenant_id', ?, true), set_config('app.retention_purge', 'on', true)", f.TenantID.String()).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Delete(&database.ReferenceSnapshot{}).Error; err != nil {
			return err
		}
		payload := json.RawMessage(`{"items":[{"mediaId":"` + f.MediaID.String() + `","category":"GERAL","description":"Sala na entrada"}]}`)
		return tx.Create(&database.ReferenceSnapshot{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, ReferenceVersionID: &legacy, ComparisonMode: "FIXED_ORIGIN", Payload: payload, CreatedAt: issuedAt}).Error
	}); err != nil {
		t.Fatal(err)
	}
	// The report rows are the bytes HEAD's code (before report v3) persisted
	// for this input: canonical JSON and HTML written by the pre-v3
	// generate_snapshot path (render_pdf/testdata/it019_head_media_only, whose
	// PDF presentation is checked byte for byte by the render_pdf IT-019
	// test), with this fixture's IDs in place of the generator's.
	firstID, secondID, now := identity.NewID(), identity.NewID(), time.Now().UTC()
	ids := strings.NewReplacer(
		"10000000-0000-4000-8000-000000000001", f.InspectionID.String(),
		"20000000-0000-4000-8000-000000000002", f.MediaID.String(),
		"30000000-0000-4000-8000-000000000003", f.AssetID.String(),
		"40000000-0000-4000-8000-000000000004", f.ParticipantID.String(),
		"50000000-0000-4000-8000-000000000005", f.TemplateID.String(),
		"60000000-0000-4000-8000-000000000006", f.TemplateVersionID.String(),
		"70000000-0000-4000-8000-000000000007", legacy.String(),
		"80000000-0000-4000-8000-000000000008", firstID.String(),
	)
	golden := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "features", "reports", "render_pdf", "testdata", "it019_head_media_only", name))
		if err != nil {
			t.Fatal(err)
		}
		return []byte(ids.Replace(string(data)))
	}
	rows := map[int]database.ReportSnapshot{}
	for version, row := range map[int]database.ReportSnapshot{1: {ID: firstID, Classification: "NORMAL"}, 2: {ID: secondID, Classification: "ATTENTION"}} {
		canonical, html := golden(fmt.Sprintf("v%d.json", version)), golden(fmt.Sprintf("v%d.html", version))
		jsonSum, htmlSum := sha256.Sum256(canonical), sha256.Sum256(html)
		htmlJSON, err := json.Marshal(string(html))
		if err != nil {
			t.Fatal(err)
		}
		row.TenantID, row.InspectionID, row.Mode, row.SchemaVersion, row.VersionNumber, row.PDFRenderVersion = f.TenantID, f.InspectionID, "HISTORICAL", 1, version, 1
		row.JSONDigest, row.HTMLDigest, row.CanonicalJSON, row.HTML, row.CreatedAt = hex.EncodeToString(jsonSum[:]), hex.EncodeToString(htmlSum[:]), canonical, htmlJSON, now.Add(time.Duration(version)*time.Second)
		rows[version] = row
	}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		for _, version := range []int{1, 2} {
			row := rows[version]
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		// Version 2 was released by the AUTOMATIC policy when it was issued.
		published := now.Add(2 * time.Second)
		return tx.Create(&database.ReportPublication{ID: identity.NewID(), TenantID: f.TenantID, SnapshotID: secondID, InspectionID: f.InspectionID, PolicyVersion: 1, ClientMutationID: "auto:" + secondID.String(), Status: "PUBLISHED", ActorID: f.IdentityID, PublishedAt: &published, CreatedAt: published, UpdatedAt: published}).Error
	}); err != nil {
		t.Fatal(err)
	}
	second := struct {
		SnapshotID identity.ID
		Version    int
	}{secondID, 2}

	meta := requestctx.Metadata{TenantID: f.TenantID, Principal: requestctx.Principal{IdentityID: f.IdentityID, MembershipID: f.MembershipID, TenantID: f.TenantID, Roles: []string{auth.TenantAdmin}, ProductEntitlements: auth.ProductsForRole(auth.TenantAdmin)}}
	authorized := requestctx.WithMetadata(ctx, meta)
	query := &queryResolver{Resolver: &Resolver{DB: h.DB}}
	for version, row := range rows {
		v := version
		report, err := query.Report(authorized, f.InspectionID.String(), &v)
		if err != nil || report == nil {
			t.Fatalf("report v%d unreadable: %v", version, err)
		}
		var issued reportcore.Snapshot
		if err := json.Unmarshal(row.CanonicalJSON, &issued); err != nil {
			t.Fatal(err)
		}
		_, digest, err := reportcore.CanonicalJSON(issued)
		if err != nil || digest != row.JSONDigest {
			t.Fatalf("v%d canonical JSON no longer validates to its digest: %v", version, err)
		}
		html, err := reportcore.HTML(issued)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(html)
		var storedHTML string
		_ = json.Unmarshal(row.HTML, &storedHTML)
		if hex.EncodeToString(sum[:]) != row.HTMLDigest || string(html) != storedHTML {
			t.Fatalf("v%d HTML renders differently after the rollout", version)
		}
		if report.Version != version || report.Comparison != nil || report.Inventory != nil || report.Checklist != nil || report.HTML != storedHTML || report.JSONDigest != row.JSONDigest {
			t.Fatalf("report v%d = %+v; want the historical media-only shape", version, report)
		}
		if _, ok := report.CanonicalJSON["comparison"]; ok {
			t.Fatalf("report v%d gained a comparison section", version)
		}
	}
	outcome := graphql1.ComparisonOutcomeUnchanged
	customer, err := query.CustomerReport(authorized, f.InspectionID.String(), nil, &outcome)
	if err != nil || customer == nil || customer.Version != 2 || customer.Comparison != nil || customer.Inventory != nil || customer.Status != "PUBLISHED" {
		t.Fatalf("customer report = %+v, %v; want the published legacy v2 without comparison", customer, err)
	}
	missing := 99
	if report, err := query.CustomerReport(authorized, f.InspectionID.String(), &missing, nil); err != nil || report != nil {
		t.Fatalf("unknown version = %+v, %v; want no data", report, err)
	}

	// Both PDF audiences of the published legacy version still render.
	if err := h.AdminDB.Model(&database.ReportPDFJob{}).Where("status IN ('QUEUED','PROCESSING') AND tenant_id<>?", f.TenantID).Updates(map[string]any{"next_attempt_at": time.Now().Add(24 * time.Hour), "lease_expires_at": time.Now().Add(24 * time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	queue, err := render_pdf.Setup(render_pdf.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	created := events.RawEnvelope{ID: identity.NewID(), Type: "report.snapshot_created.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: f.TenantID, AggregateID: f.InspectionID, CorrelationID: "it019-pdf", Payload: json.RawMessage(`{"inspectionId":"` + f.InspectionID.String() + `","snapshotId":"` + second.SnapshotID.String() + `","version":` + fmt.Sprint(second.Version) + `}`)}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error { return queue(ctx, tx, created) }); err != nil {
		t.Fatal(err)
	}
	objects := &r9MemoryObjects{objects: map[string][]byte{}}
	executor, err := render_pdf.SetupExecutor(h.AdminDB, objectstore.Store{Bucket: "private", Client: objects}, render_pdf.DefaultLimits(), time.Now, "")
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		worked, err := executor.RunDue(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	var jobs []database.ReportPDFJob
	if err := h.AdminDB.Where("tenant_id=? AND snapshot_id=?", f.TenantID, second.SnapshotID).Find(&jobs).Error; err != nil || len(jobs) != 2 {
		t.Fatalf("legacy PDF jobs = %+v, %v", jobs, err)
	}
	for _, job := range jobs {
		if job.Status != "READY" || job.ArtifactID == nil {
			t.Fatalf("legacy %s PDF = %+v; want READY", job.Audience, job)
		}
		var artifact database.ReportArtifact
		if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, *job.ArtifactID).Take(&artifact).Error; err != nil {
			t.Fatal(err)
		}
		objects.mu.Lock()
		data := objects.objects[artifact.ObjectKey]
		objects.mu.Unlock()
		if !bytes.HasPrefix(data, []byte("%PDF")) || strings.Contains(string(data), "Comparação com a referência") {
			t.Fatalf("legacy %s PDF is not the historical document", job.Audience)
		}
	}
}

// The reviseComparisonPairing mutation, through the real resolver, slice and
// PostgreSQL RLS: staff get a cross-room pair refused with a Portuguese
// userError and nothing written, then a same-room correction opens the next
// comparison generation with the staff pair and queues the report reissue.
func TestTask05ReviseComparisonPairingMutationEndToEnd(t *testing.T) {
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
	definition := prompt.DefaultDefinitionForTask("INVENTORY_COMPARE")
	payload, digest, err := prompt.CanonicalizeTask("INVENTORY_COMPARE", definition)
	if err != nil {
		t.Fatal(err)
	}
	var comparePrompt database.AnalysisPromptSnapshot
	if err := h.AdminDB.Where("analysis_type='REAL_ESTATE' AND task='INVENTORY_COMPARE' AND canonical_digest=?", digest).Take(&comparePrompt).Error; err != nil {
		comparePrompt = database.AnalysisPromptSnapshot{ID: identity.NewID(), AnalysisType: "REAL_ESTATE", Task: "INVENTORY_COMPARE", DefinitionJSON: payload, CanonicalDigest: digest, SchemaVersion: 1, CreatedAt: time.Now().UTC()}
		if err := h.AdminDB.Create(&comparePrompt).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := h.AdminDB.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", f.TenantID, f.InspectionID).Update("inventory_prompt_snapshot_id", comparePrompt.ID).Error; err != nil {
		t.Fatal(err)
	}
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
	var media database.MediaObject
	if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, f.MediaID).Take(&media).Error; err != nil {
		t.Fatal(err)
	}
	copyMedia := func() identity.ID {
		row := media
		row.ID = identity.NewID()
		row.ObjectKey = media.ObjectKey + "-t05-" + row.ID.String()
		row.IdempotencyKey = "t05-revise-" + row.ID.String()
		row.Status = "READY"
		if err := h.AdminDB.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		return row.ID
	}
	current, elsewhere := copyMedia(), copyMedia()
	roomKey := "env:" + base.EnvironmentIdentityIDs[0].String()
	document, _ := json.Marshal(map[string]any{"rooms": []any{map[string]any{"key": base.EnvironmentIdentityIDs[0].String(), "typeCode": "LIVING_ROOM", "name": "Sala", "components": []any{map[string]any{"key": "geral", "typeCode": "OTHER", "label": "Visão geral", "views": []string{"wide"}}}}}, "handoverRecords": []any{}})
	now := time.Now().UTC()
	job := database.InventoryJob{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, OriginVersionID: base.OriginVersionID, Kind: "COMPARE", RequirementKey: roomKey, EnvironmentIdentityID: &base.EnvironmentIdentityIDs[0], Generation: 1, Status: "SUCCEEDED", NextAttemptAt: now, PromptSnapshotID: &comparePrompt.ID, CreatedAt: now, UpdatedAt: now}
	// Another room of the inspection, already compared: a correction in the
	// living room must not re-analyse it (review round 2, item 7).
	otherRoom := identity.NewID()
	otherJob := database.InventoryJob{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, OriginVersionID: base.OriginVersionID, Kind: "COMPARE", RequirementKey: "env:" + otherRoom.String(), EnvironmentIdentityID: &otherRoom, Generation: 1, Status: "SUCCEEDED", NextAttemptAt: now, PromptSnapshotID: &comparePrompt.ID, CreatedAt: now, UpdatedAt: now}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		ids, _ := json.Marshal([]identity.ID{current})
		for _, row := range []any{
			&database.InspectionScopeSnapshot{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, TemplateVersionID: f.TemplateVersionID, Profile: "PROPERTY_COMPONENTS", Document: document, Digest: "t05-revise-scope", Version: 1, CreatedAt: now},
			&database.RequirementAnswer{ID: identity.NewID(), TenantID: f.TenantID, DraftID: f.DraftID, RequirementKey: roomKey, MediaIDs: ids, Flags: json.RawMessage(`[]`), Version: 1, CreatedAt: now, UpdatedAt: now},
			&job,
			&otherJob,
		} {
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		input := generatesnapshot.Input{TenantID: f.TenantID, InspectionID: f.InspectionID, Mode: "HISTORICAL", Classification: "NORMAL", TemplateVersionID: f.TemplateVersionID.String(), ReferenceVersionID: base.OriginVersionID.String(), PromptDigest: "prompt", Context: reportcore.Context{Asset: reportcore.AssetContext{ID: "asset", Name: "Imóvel", Address: "Rua"}, Participant: reportcore.ParticipantContext{ID: "participant", Name: "Participante"}, Template: reportcore.TemplateContext{ID: "template", Name: "Modelo", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: now.Format(time.RFC3339Nano)}}, Checklist: &reportcore.ChecklistSection{Rooms: []reportcore.ChecklistRoom{}, Coverage: reportcore.ChecklistCoverage{UnknownScope: true}}, Processing: &reportcore.ProcessingState{Status: "COMPLETED"}, Scope: &reportcore.ScopeSnapshot{Unknown: true}, Handover: &reportcore.HandoverSection{Entries: []reportcore.HandoverEntry{}}, Provenance: &reportcore.Provenance{PromptRevision: "prompt"}, PublicationMode: "MANUAL"}
		_, err := generatesnapshot.CreateInTx(ctx, tx, input, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var sofa database.InventoryItem
	if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, base.ItemIDs[0]).Take(&sofa).Error; err != nil {
		t.Fatal(err)
	}
	meta := requestctx.Metadata{TenantID: f.TenantID, Principal: requestctx.Principal{IdentityID: f.IdentityID, MembershipID: f.MembershipID, TenantID: f.TenantID, Roles: []string{auth.TenantAdmin}, ProductEntitlements: auth.ProductsForRole(auth.TenantAdmin)}}
	authorized := requestctx.WithMetadata(ctx, meta)
	resolver := &mutationResolver{Resolver: &Resolver{DB: h.DB}}
	request := func(currentID identity.ID, key string) *graphql1.ReviseComparisonPairingPayload {
		t.Helper()
		payload, err := resolver.reviseComparisonPairing(authorized, graphql1.ReviseComparisonPairingInput{InspectionID: f.InspectionID.String(), ItemIdentityID: sofa.IdentityID.String(), Pairs: []*graphql1.ComparisonPairInput{{BaselineEvidenceID: base.OriginMediaID.String(), CurrentEvidenceID: currentID.String()}}, ExpectedVersion: 1, ClientMutationID: key})
		if err != nil {
			t.Fatalf("reviseComparisonPairing transport error: %v", err)
		}
		return payload
	}
	rejected := request(elsewhere, "t05-revise-cross-room")
	if len(rejected.UserErrors) != 1 || rejected.UserErrors[0].Field == nil || *rejected.UserErrors[0].Field != "pairs" || !strings.Contains(rejected.UserErrors[0].Message, "mesmo ambiente") || rejected.Report != nil {
		t.Fatalf("cross-room pair = %+v; want a Portuguese userError on pairs", rejected)
	}
	var generations int64
	h.AdminDB.Model(&database.InventoryJob{}).Where("tenant_id=? AND inspection_id=? AND generation>1", f.TenantID, f.InspectionID).Count(&generations)
	if generations != 0 {
		t.Fatal("a refused revision opened a new generation")
	}
	accepted := request(current, "t05-revise-same-room")
	if len(accepted.UserErrors) != 0 || accepted.Report == nil || accepted.Report.Version != 1 {
		t.Fatalf("same-room correction = %+v", accepted)
	}
	var next database.InventoryJob
	if err := h.AdminDB.Where("tenant_id=? AND inspection_id=? AND requirement_key=? AND generation=2", f.TenantID, f.InspectionID, roomKey).Take(&next).Error; err != nil || next.Status != "PENDING" {
		t.Fatalf("next generation = %+v, %v", next, err)
	}
	var reanalysed []database.InventoryJob
	if err := h.AdminDB.Where("tenant_id=? AND inspection_id=? AND generation>1", f.TenantID, f.InspectionID).Find(&reanalysed).Error; err != nil || len(reanalysed) != 1 || reanalysed[0].ID != next.ID {
		t.Fatalf("new generations = %+v, %v; want only the corrected room", reanalysed, err)
	}
	var staff []database.InventoryEvidencePair
	if err := h.AdminDB.Where("tenant_id=? AND job_id=? AND source='STAFF'", f.TenantID, next.ID).Find(&staff).Error; err != nil || len(staff) != 1 || !staff[0].Confirmed || staff[0].CurrentMediaID != current || staff[0].OriginMediaID != base.OriginMediaID {
		t.Fatalf("staff pairs = %+v, %v", staff, err)
	}
	var queued int64
	h.AdminDB.Model(&database.AuditEvent{}).Where("tenant_id=? AND action='report.reissue_requested' AND outcome='QUEUED' AND target_id=?", f.TenantID, f.InspectionID.String()).Count(&queued)
	if queued != 1 {
		t.Fatalf("queued reissues = %d; want 1", queued)
	}
}
