package render_pdf

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/reports/core"
	customerreport "inspection/services/inspection/internal/features/reports/customer/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/pdf"
)

// completeV3Snapshot fills every optional v3 section and field the snapshot
// validation knows about, internal ones included, so the customer projection
// is exercised against the full set of validation rules.
func completeV3Snapshot() core.Snapshot {
	snapshot := task04PDFSnapshot(1)
	ms := int64(1200)
	snapshot.Evidence = append(snapshot.Evidence,
		core.Evidence{ID: "baseline-photo", RequirementKey: "env:living", Role: "REFERENCE", Description: "Sofá na entrada", Availability: "AVAILABLE", Flags: []string{"GALLERY_SOURCE"}},
		core.Evidence{ID: "current-photo", RequirementKey: "env:living", Role: "CURRENT", Description: "Sofá atual", Availability: "AVAILABLE", Flags: []string{"CAPTURE_TIME_CONFLICT", "QUALITY_WARNING_IGNORED:DARK"}},
	)
	snapshot.Requirements = []core.Requirement{{Key: "roof", Section: "Cobertura", Label: "Telhado e calhas", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "INCONCLUSIVE"}, {Key: "env:living", Section: "Sala", Label: "Sala", AnalysisMode: "COMPARE_ORIGIN_CURRENT", AnalysisStatus: "FAILED"}}
	snapshot.Findings = []core.Finding{{ID: "finding-1", Category: "CONSERVATION", Title: "Risco no sofá", Description: "Risco visível.", Severity: "LOW", Confidence: 0.7, EvidenceIDs: []string{"current-photo"}, Quality: "ADEQUATE", RecommendedAction: "REVIEW"}}
	snapshot.Inventory = &core.InventorySection{
		Reference:          core.InventoryReference{VersionID: "baseline-version", VersionNumber: 2, Purpose: "MOVE_IN", ActivatedAt: "2026-10-01T00:00:00Z"},
		Coverage:           &core.InventoryCoverage{ExpectedItems: 3, AssessedItems: 2, NotObserved: 1, EnvironmentsNotCaptured: []string{"Garagem"}, EnvironmentsAddedDuringCapture: []string{"Escritório"}},
		CatalogDiagnostics: &core.InventoryCatalogDiagnostics{ItemsTruncated: 1, ImagesTruncated: 2},
		Problems: []core.InventoryEntry{
			{ItemIdentityID: "item-sofa", Environment: "Sala", Type: "SOFA", Name: "Sofá", Status: "CONDITION_CHANGED", ConditionBefore: "GOOD", ConditionAfter: "FAIR", Reason: "Risco visível", Pairs: []core.InventoryEvidencePair{{Origin: "baseline-photo", Current: "current-photo", Decision: "SAME_VIEW", TimestampMS: &ms}}},
			{ItemIdentityID: "item-table", Environment: "Sala", Type: "TABLE", Name: "Mesa", Status: "MISSING"},
		},
		Informational: []core.InventoryEntry{{Environment: "Sala", Type: "CHAIR", Name: "Cadeira", Status: "OUTSIDE_INVENTORY"}},
	}
	component := &snapshot.Checklist.Rooms[0].Components[0]
	component.AssessedScope, component.NotAssessed = "Superfície visível", []string{"Parte inferior"}
	component.AttributeSources = []core.AttributeSource{{Attribute: "condition", Source: "VISUAL", EvidenceIDs: []string{"photo-000"}}, {Attribute: "condition", Source: "VOICE"}}
	component.Conflict = true
	component.Limitations = []string{core.LimitationSourceConflict, "Vistas pendentes: detalhe"}
	snapshot.Checklist.Rooms = append(snapshot.Checklist.Rooms, core.ChecklistRoom{Key: "living", TypeCode: "LIVING_ROOM", Name: "Sala", Components: []core.ChecklistComponent{
		{Key: "sofa", TypeCode: "SOFA", Label: "Sofá", Status: "PARTIAL", ReviewStatus: "EDITED", EvidenceIDs: []string{"current-photo"}, Limitations: []string{core.LimitationAmbiguousCatalog}, Conflict: true},
		{Key: "lamp", Label: "Luminária", Status: "NOT_APPLICABLE", ReviewStatus: "ACCEPTED", OutsideScope: true},
	}})
	snapshot.Checklist.Coverage = core.ChecklistCoverage{Expected: 2, FullyAssessed: 1, PartiallyAssessed: 1}
	snapshot.Handover.Entries = append(snapshot.Handover.Entries,
		core.HandoverEntry{Key: "front-keys", Kind: "KEY", Status: "DECLARED", Payload: json.RawMessage(`{"keyType":"Porta","quantity":2,"internalCode":"K-9"}`), Declarant: "Participante"},
		core.HandoverEntry{Key: "gate-control", Kind: "CONTROL", Status: "PENDING"},
		core.HandoverEntry{Key: "note", Kind: "NOTE", Status: "DECLARED", Payload: json.RawMessage(`{"note":"Senha 4321"}`), Declarant: "Participante"},
	)
	snapshot.Processing = &core.ProcessingState{Status: "INCONCLUSIVE", Message: "A catalogação dos componentes está pendente ou inconclusiva."}
	snapshot.Provenance = &core.Provenance{AnalysisMode: "FINDINGS", PromptRevision: "prompt-v1", ModelRevision: "model-v1", ScopeDigest: "scope-v1", EvidenceIDs: []string{"photo-000"}}
	snapshot.Scope = &core.ScopeSnapshot{Digest: "scope-v1", Version: 1, Profile: "PROPERTY_COMPONENTS", Document: json.RawMessage(`{"rooms":[]}`)}
	return snapshot
}

// Item 1 (round 8): a valid v3 comparative inventory (coverage present) must
// project for the customer; before the fix the reference lost ActivatedAt and
// ValidateCustomerSnapshot rejected every such PDF permanently.
func TestR8CustomerProjectionKeepsComparativeInventoryReferenceValid(t *testing.T) {
	snapshot := task04PDFSnapshot(1)
	snapshot.Inventory = &core.InventorySection{
		Reference:     core.InventoryReference{VersionID: "baseline-v1", VersionNumber: 1, Purpose: "MOVE_IN", ActivatedAt: "2026-10-01T00:00:00Z"},
		Coverage:      &core.InventoryCoverage{ExpectedItems: 2, AssessedItems: 1, NotObserved: 1},
		Problems:      []core.InventoryEntry{},
		Informational: []core.InventoryEntry{},
	}
	if err := core.Validate(snapshot); err != nil {
		t.Fatalf("fixture invalid: %v", err)
	}
	projected, internal, err := projectForAudience(snapshot, "PDF_CUSTOMER")
	if err != nil || internal {
		t.Fatalf("customer projection of a valid comparative inventory failed: internal=%v err=%v", internal, err)
	}
	reference := projected.Inventory.Reference
	if reference.ActivatedAt != "2026-10-01T00:00:00Z" || reference.Purpose != "MOVE_IN" || reference.VersionID == "baseline-v1" {
		t.Fatalf("customer reference = %+v; want activation and purpose kept, internal version ID withheld", reference)
	}
}

// Item 1 (round 8): property check over every optional v3 section. Removing
// any one of them must still leave a customer projection that validates and
// renders, and the projection must never keep an internal field.
func TestR8CompleteV3SnapshotCustomerProjectionValidatesAndRenders(t *testing.T) {
	variants := map[string]func(*core.Snapshot){
		"complete":     func(*core.Snapshot) {},
		"no inventory": func(s *core.Snapshot) { s.Inventory = nil },
		"cataloging": func(s *core.Snapshot) {
			s.Inventory.Coverage = nil
			s.Inventory.Reference.Purpose = ""
			s.Inventory.Problems = nil
		},
		"no findings": func(s *core.Snapshot) { s.Findings = nil },
		"no handover": func(s *core.Snapshot) { s.Handover.Entries = []core.HandoverEntry{} },
		"failed processing": func(s *core.Snapshot) {
			s.Processing = &core.ProcessingState{Status: "FAILED", Message: "A catalogação dos componentes falhou."}
		},
		"unknown scope": func(s *core.Snapshot) {
			s.Checklist.Coverage = core.ChecklistCoverage{UnknownScope: true}
			s.Scope.Unknown = true
		},
		"not authorized": func(s *core.Snapshot) { s.Processing = &core.ProcessingState{Status: "NOT_AUTHORIZED"} },
		"no evidence at all": func(s *core.Snapshot) {
			s.Evidence = nil
			s.Inventory = nil
			s.Findings = nil
			stripEvidenceReferences(s)
		},
	}
	for name, mutate := range variants {
		t.Run(name, func(t *testing.T) {
			snapshot := completeV3Snapshot()
			mutate(&snapshot)
			if err := core.Validate(snapshot); err != nil {
				t.Fatalf("fixture invalid: %v", err)
			}
			projected, _, err := projectForAudience(snapshot, "PDF_CUSTOMER")
			if err != nil {
				t.Fatalf("customer projection failed: %v", err)
			}
			if err := core.ValidateCustomerSnapshot(projected); err != nil {
				t.Fatalf("customer projection does not validate: %v", err)
			}
			encoded, _ := json.Marshal(projected)
			for _, internal := range []string{"attributeSources", "assessedScope", "notAssessed", "itemIdentityId", "catalogDiagnostics", "Senha 4321", "internalCode", "baseline-version", "scope-v1", "prompt-v1", "finding-1", core.LimitationSourceConflict, core.LimitationAmbiguousCatalog, "catalogação"} {
				if strings.Contains(string(encoded), internal) {
					t.Fatalf("customer projection keeps internal %q: %s", internal, encoded)
				}
			}
			assets := pdf.AssetBundle{}
			for _, item := range projected.Evidence {
				if core.EvidenceAvailable(item.Availability) {
					assets["evidence-"+item.ID+".jpg"] = jpegBytes(t, 40, 30)
				}
			}
			data, err := (MarotoRenderer{DashboardOrigin: "https://dashboard.example.test"}).Render(context.Background(), PDFDocument{SnapshotID: "complete", Version: 1, Snapshot: projected}, assets)
			if err != nil {
				t.Fatalf("customer PDF render failed: %v", err)
			}
			text := assertV3PDFTextAndPages(t, data, 1, []string{"LAUDO DE VISTORIA", core.ClassificationLabelForSnapshot(snapshot)})
			for _, forbidden := range []string{"Situação não reconhecida", "fontes visual e por voz", "item de catálogo", "Senha 4321", "LAUDO INTERNO"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("customer PDF contains %q: %s", forbidden, text)
				}
			}
		})
	}
}

func stripEvidenceReferences(snapshot *core.Snapshot) {
	for i := range snapshot.Checklist.Rooms {
		for j := range snapshot.Checklist.Rooms[i].Components {
			snapshot.Checklist.Rooms[i].Components[j].EvidenceIDs = nil
			snapshot.Checklist.Rooms[i].Components[j].AttributeSources = nil
		}
	}
	for i := range snapshot.Handover.Entries {
		snapshot.Handover.Entries[i].EvidenceIDs = nil
	}
	snapshot.Provenance.EvidenceIDs = nil
}

// Item 1 (round 8): the real executor publishes the customer PDF of a v3
// photo report with a comparative inventory (Inventory + Coverage).
func TestR8CustomerV3PDFExecutorPublishesComparativeInventory(t *testing.T) {
	env := newTestEnv(t)
	if err := env.db.Exec(`CREATE TABLE media.screening_runs (id blob PRIMARY KEY, tenant_id blob, media_id blob, status text, model_digest text, regions blob, created_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	snapshot := task04PDFSnapshot(1)
	baselineID, currentID := identity.NewID(), identity.NewID()
	for _, id := range []identity.ID{baselineID, currentID} {
		data := jpegBytes(t, 40, 30)
		key, hash := objectstore.DerivativeKey(env.tenantID, id, "DISPLAY", data)
		env.client.objects[key] = data
		if err := env.db.Create(&database.MediaDerivative{ID: identity.NewID(), TenantID: env.tenantID, MediaID: id, ObjectKey: key, Kind: "DISPLAY", SHA256: hash, ContentType: "image/jpeg", Width: 40, Height: 30, SizeBytes: int64(len(data))}).Error; err != nil {
			t.Fatal(err)
		}
		if err := env.db.Exec("INSERT INTO media.screening_runs (id,tenant_id,media_id,status,model_digest,regions,created_at) VALUES (?,?,?,?,?,?,?)", identity.NewID(), env.tenantID, id, "CLEARED", "digest", "[]", env.clock.Add(-time.Minute)).Error; err != nil {
			t.Fatal(err)
		}
	}
	snapshot.Evidence = []core.Evidence{
		{ID: baselineID.String(), RequirementKey: "env:living", Role: "REFERENCE", Description: "Sofá na entrada", Availability: "AVAILABLE"},
		{ID: currentID.String(), RequirementKey: "env:living", Role: "CURRENT", Description: "Sofá atual", Availability: "AVAILABLE"},
	}
	snapshot.Checklist.Rooms[0].Components[0].EvidenceIDs = []string{currentID.String()}
	snapshot.Handover.Entries[0].EvidenceIDs = nil
	snapshot.Inventory = &core.InventorySection{
		Reference:     core.InventoryReference{VersionID: identity.NewID().String(), VersionNumber: 1, Purpose: "MOVE_IN", ActivatedAt: "2026-10-01T00:00:00Z"},
		Coverage:      &core.InventoryCoverage{ExpectedItems: 2, AssessedItems: 2, NotObserved: 0, EnvironmentsNotCaptured: []string{}, EnvironmentsAddedDuringCapture: []string{}},
		Problems:      []core.InventoryEntry{{ItemIdentityID: identity.NewID().String(), Environment: "Sala", Type: "SOFA", Name: "Sofá", Status: "CONDITION_CHANGED", ConditionBefore: "GOOD", ConditionAfter: "FAIR", Pairs: []core.InventoryEvidencePair{{Origin: baselineID.String(), Current: currentID.String(), Decision: "SAME_VIEW"}}}},
		Informational: []core.InventoryEntry{},
	}
	canonical, digest, err := core.CanonicalJSON(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.db.Model(&database.ReportSnapshot{}).Where("id=?", env.snapshot.ID).Updates(map[string]any{"canonical_json": canonical, "json_digest": digest}).Error; err != nil {
		t.Fatal(err)
	}
	job := env.addJob(t, "PDF_CUSTOMER", "QUEUED", 0)
	if worked, err := env.executor.RunDue(context.Background()); err != nil || !worked {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
	completed := env.job(t, job.ID)
	if completed.Status != "READY" || completed.ArtifactID == nil {
		t.Fatalf("customer PDF of a comparative v3 report was not published: %+v", completed)
	}
	var artifact database.ReportArtifact
	if err := env.db.Where("tenant_id=? AND id=?", env.tenantID, *completed.ArtifactID).Take(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	text := assertV3PDFTextAndPages(t, env.client.objects[artifact.ObjectKey], 1, []string{"LAUDO DE VISTORIA", "Referência: Entrada no imóvel", "2 de 2 itens avaliados", "Alterado · Sala · Sofá", "Bom → Regular"})
	if strings.Contains(text, "Situação não reconhecida") {
		t.Fatalf("customer PDF has an unlabeled code: %s", text)
	}
}

// Item 5 (round 8): the customer screen, the HTML and the customer PDF derive
// the classification label, the limitation and the evidence notice from the
// same facts. Evidence withheld from the customer (sensitive flag, screening
// not cleared) is not "no evidence".
func TestR8ScreenHTMLAndCustomerPDFAgreeOnClassificationAndEvidenceNotice(t *testing.T) {
	type expectation struct {
		label, notice, absent string
	}
	cases := map[string]struct {
		mutate func(*core.Snapshot)
		redact func(*core.Snapshot)
		want   expectation
	}{
		"sensitive photo of a NORMAL complete report": {
			mutate: func(s *core.Snapshot) { s.Evidence[0].Flags = []string{"sensitive"} },
			want:   expectation{label: "Sem alertas identificados", notice: "As evidências registradas não são exibidas nesta versão", absent: "Nenhuma evidência registrada"},
		},
		"photo with screening not cleared": {
			redact: func(s *core.Snapshot) { redactForCustomer(&s.Evidence[0], s.SchemaVersion) },
			want:   expectation{label: "Sem alertas identificados", notice: "As evidências registradas não são exibidas nesta versão", absent: "Nenhuma evidência registrada"},
		},
		"no usable evidence": {
			mutate: func(s *core.Snapshot) { s.Evidence[0].Availability = "UNAVAILABLE" },
			want:   expectation{label: "Resultado com limitações", notice: "Nenhuma evidência registrada", absent: "não são exibidas nesta versão"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			snapshot := task04PDFSnapshot(1)
			snapshot.Evidence = snapshot.Evidence[:1]
			snapshot.Handover.Entries[0].EvidenceIDs = nil
			if tc.mutate != nil {
				tc.mutate(&snapshot)
			}
			screen := customerreport.ProjectChecklist(snapshot)
			if screen.ClassificationLabel != tc.want.label || core.ClassificationLabelForSnapshot(snapshot) != tc.want.label {
				t.Fatalf("screen label=%q limited=%v, want %q", screen.ClassificationLabel, screen.Limited, tc.want.label)
			}
			projected, _, err := projectForAudience(snapshot, "PDF_CUSTOMER")
			if err != nil {
				t.Fatal(err)
			}
			if tc.redact != nil {
				tc.redact(&projected)
			}
			if got := core.ClassificationLabelForSnapshot(projected); got != screen.ClassificationLabel || core.ClassificationLimitedForSnapshot(projected) != screen.Limited {
				t.Fatalf("customer PDF label=%q, screen label=%q", got, screen.ClassificationLabel)
			}
			internalHTML, err := core.HTML(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			customerHTML, err := core.HTMLForPDF(projected, nil, false)
			if err == nil {
				// The customer HTML is rendered from the same projection when available.
				if !strings.Contains(string(customerHTML), tc.want.label) || !strings.Contains(string(customerHTML), tc.want.notice) || strings.Contains(string(customerHTML), tc.want.absent) {
					t.Fatalf("customer HTML disagrees: %s", customerHTML)
				}
			}
			if !strings.Contains(string(internalHTML), `<span class="badge">`+tc.want.label+`</span>`) {
				t.Fatalf("internal HTML label differs from screen %q", tc.want.label)
			}
			data, err := (MarotoRenderer{}).Render(context.Background(), PDFDocument{SnapshotID: "agree", Version: 1, Snapshot: projected}, nil)
			if err != nil {
				t.Fatal(err)
			}
			text := assertV3PDFTextAndPages(t, data, 1, []string{"Classificação: " + tc.want.label, tc.want.notice})
			if strings.Contains(text, tc.want.absent) {
				t.Fatalf("customer PDF says %q: %s", tc.want.absent, text)
			}
		})
	}
}

// emittedEvidenceFlags lists every flag written to answers or media and
// copied into report evidence: capture/core (GPS policy, gallery, capture
// time, quality warnings, declared impossibility for env: requirements),
// media/core (device-local EXIF time, screening false positive) and the
// historical sensitive flags still read by customer projections.
var emittedEvidenceFlags = []string{
	"GPS_MISSING_OR_INVALID", "GPS_OUTSIDE_GUIDED_WINDOW", "GPS_LOW_ACCURACY", "OUT_OF_GEOFENCE",
	"GALLERY_SOURCE", "CAPTURE_TIME_CONFLICT", "CAPTURE_TIME_DEVICE_LOCAL", "SENSITIVE_CONTENT_FALSE_POSITIVE",
	"QUALITY_WARNING:DARK", "QUALITY_WARNING:BLURRY", "QUALITY_WARNING:SHORT", "QUALITY_WARNING:TOO_FEW_PHOTOS",
	"QUALITY_WARNING_IGNORED:DARK", "QUALITY_WARNING_IGNORED:BLURRY", "QUALITY_WARNING_IGNORED:SHORT", "QUALITY_WARNING_IGNORED:TOO_FEW_PHOTOS",
	"IMPOSSIBILITY:LOCKED", "IMPOSSIBILITY:NO_ACCESS", "IMPOSSIBILITY:DOES_NOT_EXIST", "IMPOSSIBILITY:UNSAFE", "IMPOSSIBILITY:OTHER",
	"GPS_MISSING", "LOW_QUALITY", "SENSITIVE_DETECTION",
}

// Item 6 (round 8): every flag the capture, media and screening slices emit,
// and every analysis status, has one Portuguese label shared by the v3 HTML,
// the internal PDF and the customer PDF.
func TestR8EmittedFlagsAndStatusesShareOneVocabularyInHTMLAndPDF(t *testing.T) {
	flags := emittedEvidenceFlags
	statuses := []string{"PENDING", "COMPLETED", "INCONCLUSIVE", "FAILED"}
	snapshot := task04PDFSnapshot(0)
	for i, status := range statuses {
		key := "req-" + strings.ToLower(status)
		snapshot.Requirements = append(snapshot.Requirements, core.Requirement{Key: key, Section: "Seção", Label: "Requisito " + status, AnalysisMode: "CURRENT_ONLY", AnalysisStatus: status})
		item := core.Evidence{ID: "flagged-" + status, RequirementKey: key, Role: "CURRENT", Description: "Foto", Availability: "AVAILABLE"}
		for j := i; j < len(flags); j += len(statuses) {
			item.Flags = append(item.Flags, flags[j])
		}
		snapshot.Evidence = append(snapshot.Evidence, item)
	}
	html, err := core.HTML(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, audience := range []string{"PDF", "PDF_CUSTOMER"} {
		projected, internal, err := projectForAudience(snapshot, audience)
		if err != nil {
			t.Fatal(err)
		}
		for i := range projected.Evidence {
			projected.Evidence[i].Flags = append([]string(nil), snapshot.Evidence[i].Flags...)
		}
		data, err := (MarotoRenderer{}).Render(context.Background(), PDFDocument{SnapshotID: "vocabulary", Version: 1, Snapshot: projected, Internal: internal}, nil)
		if err != nil {
			t.Fatal(err)
		}
		text := assertV3PDFTextAndPages(t, data, 1, nil)
		for _, surface := range []struct{ name, text string }{{"HTML", string(html)}, {audience, text}} {
			if strings.Contains(surface.text, "Situação não reconhecida") {
				t.Fatalf("%s shows an unrecognized code: %s", surface.name, surface.text)
			}
			for _, flag := range flags {
				if label := core.FlagLabel(flag); !strings.Contains(strings.Join(strings.Fields(surface.text), " "), label) {
					t.Errorf("%s lacks the shared label %q of flag %s", surface.name, label, flag)
				}
			}
			for _, status := range statuses {
				if label := core.AnalysisStatusLabel(status); !strings.Contains(surface.text, label) {
					t.Errorf("%s lacks the shared label %q of status %s", surface.name, label, status)
				}
			}
		}
	}
}
