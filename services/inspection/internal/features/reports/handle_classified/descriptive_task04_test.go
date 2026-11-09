package handle_classified

import (
	"strings"
	"testing"

	"inspection/libs/identity"
	inventoryexecutor "inspection/services/inspection/internal/features/analysis/inventory/core"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	customerreport "inspection/services/inspection/internal/features/reports/customer/core"
	"inspection/services/inspection/internal/platform/database"
)

func TestCatalogJobSucceededIsTerminalSuccess(t *testing.T) {
	comparison := []database.ComparisonJob{{Status: "COMPLETED"}}
	catalog := []database.InventoryJob{{Status: "SUCCEEDED"}}
	if got := aggregateReportProcessing(comparison, catalog); got != "COMPLETED" {
		t.Fatalf("successful catalog processing status=%q, want COMPLETED", got)
	}
	checklist := &reportcore.ChecklistSection{Rooms: []reportcore.ChecklistRoom{{Components: []reportcore.ChecklistComponent{{Description: "Piso registrado", Condition: "GOOD"}}}}}
	processing := &reportcore.ProcessingState{Status: "COMPLETED"}
	applyCatalogAvailability(checklist, processing, false, false)
	if checklist.Rooms[0].Components[0].Description != "Piso registrado" || checklist.Rooms[0].Components[0].Condition != "GOOD" {
		t.Fatal("successful catalog output was incorrectly hidden")
	}
}

func TestCatalogProcessingStateIncludesCatalogAndComparisonOutcomes(t *testing.T) {
	comparisonSucceeded := []database.ComparisonJob{{Status: "COMPLETED"}}
	if got := aggregateReportProcessing(comparisonSucceeded, []database.InventoryJob{{Kind: "CATALOG", Status: "FAILED"}}); got != "FAILED" {
		t.Fatalf("failed catalog status=%q, want FAILED", got)
	}
	if got := aggregateReportProcessing([]database.ComparisonJob{{Status: "FAILED"}}, []database.InventoryJob{{Kind: "CATALOG", Status: "SUCCEEDED"}}); got != "FAILED" {
		t.Fatalf("failed comparison status=%q, want FAILED", got)
	}
	if got := aggregateReportProcessing(comparisonSucceeded, []database.InventoryJob{{Kind: "CATALOG", Status: "RUNNING"}}); got != "INCONCLUSIVE" {
		t.Fatalf("pending catalog status=%q, want INCONCLUSIVE", got)
	}
}

func TestLatestCatalogGenerationControlsReportProcessing(t *testing.T) {
	comparison := []database.ComparisonJob{{Status: "COMPLETED"}}
	catalogJobs := []database.InventoryJob{{Kind: "CATALOG", RequirementKey: "room:kitchen", Generation: 1, Status: "FAILED"}, {Kind: "CATALOG", RequirementKey: "room:kitchen", Generation: 2, Status: "COMPLETED"}}
	latest := inventoryexecutor.LatestGeneration(catalogJobs)
	if got := aggregateReportProcessing(comparison, latest); got != "COMPLETED" {
		t.Fatalf("latest catalog generation status=%q, want COMPLETED (jobs=%+v)", got, latest)
	}
}

func TestObservedComponentWithUnavailableEvidenceIsLimited(t *testing.T) {
	state, limitation := assessEvidenceState("OBSERVED", []string{"media-1"}, []reportcore.Evidence{{ID: "media-1", Availability: "UNAVAILABLE"}})
	if state != "PARTIAL" || limitation == "" {
		t.Fatalf("unavailable observed evidence state=%q limitation=%q", state, limitation)
	}
	state, limitation = assessEvidenceState("OBSERVED", nil, nil)
	if state != "NOT_OBSERVED" || limitation == "" {
		t.Fatalf("evidence-free observed state=%q limitation=%q", state, limitation)
	}
}

func TestCatalogComponentMatchIsScopedToRoomAndCustomerProjection(t *testing.T) {
	component := "floor"
	firstEnvironment, secondEnvironment := identity.NewID(), identity.NewID()
	firstRoom, secondRoom := identity.NewID(), identity.NewID()
	items := []database.InventoryItem{
		{ID: identity.NewID(), EnvironmentID: firstEnvironment, ComponentKey: &component, Description: "Piso claro", Condition: "GOOD"},
		{ID: identity.NewID(), EnvironmentID: secondEnvironment, ComponentKey: &component, Description: "Piso escuro", Condition: "FAIR"},
	}
	environments := map[identity.ID]database.InventoryEnvironment{
		firstEnvironment:  {ID: firstEnvironment, IdentityID: firstRoom},
		secondEnvironment: {ID: secondEnvironment, IdentityID: secondRoom},
	}
	first := matchCatalogItemsForRoom(items, environments, "new:"+firstRoom.String(), component)
	second := matchCatalogItemsForRoom(items, environments, "new:"+secondRoom.String(), component)
	if len(first) != 1 || first[0].Description != "Piso claro" || len(second) != 1 || second[0].Description != "Piso escuro" {
		t.Fatalf("room-scoped catalog matches first=%+v second=%+v", first, second)
	}
	snapshot := reportcore.Snapshot{SchemaVersion: 3, Checklist: &reportcore.ChecklistSection{Rooms: []reportcore.ChecklistRoom{
		{Name: "Cozinha", Components: []reportcore.ChecklistComponent{{Label: "Piso", Status: "OBSERVED", Description: first[0].Description, Condition: first[0].Condition}}},
		{Name: "Banheiro", Components: []reportcore.ChecklistComponent{{Label: "Piso", Status: "OBSERVED", Description: second[0].Description, Condition: second[0].Condition}}},
	}}}
	view := customerreport.ProjectChecklist(snapshot)
	if view == nil || view.Rooms[0].Components[0].Description != "Piso claro" || view.Rooms[1].Components[0].Description != "Piso escuro" {
		t.Fatalf("customer projection inherited another room's item: %+v", view)
	}
}

func TestDuplicateCatalogItemsForOneRoomAreAmbiguous(t *testing.T) {
	component := "floor"
	environmentID, roomID := identity.NewID(), identity.NewID()
	items := []database.InventoryItem{
		{ID: identity.NewID(), EnvironmentID: environmentID, ComponentKey: &component, Description: "Piso claro"},
		{ID: identity.NewID(), EnvironmentID: environmentID, ComponentKey: &component, Description: "Piso escuro"},
	}
	environments := map[identity.ID]database.InventoryEnvironment{environmentID: {ID: environmentID, IdentityID: roomID}}
	matches := matchCatalogItemsForRoom(items, environments, "new:"+roomID.String(), component)
	if len(matches) != 2 {
		t.Fatalf("duplicate room/component matches=%d, want conflict candidates retained", len(matches))
	}
}

func TestUT015ExpectedViewMissingKeepsComponentPartiallyAssessed(t *testing.T) {
	expected := []string{"wide", "detail"}
	answers := []database.ComponentAnswer{{ViewKey: "wide", State: "OBSERVED"}}
	state, missing := assessExpectedViews("OBSERVED", expected, answers)
	if state != "PARTIAL" || len(missing) != 1 || missing[0] != "detail" {
		t.Fatalf("one of two expected views produced state=%s missing=%v", state, missing)
	}
}

func TestUT015ExpectedViewsUsePortugueseLabels(t *testing.T) {
	if got := expectedViewLabel("wide"); got != "visão geral" {
		t.Fatalf("wide view label=%q", got)
	}
	if got := expectedViewLabel("detail"); got != "detalhe" {
		t.Fatalf("detail view label=%q", got)
	}
	if got := expectedViewLabel("WIDE"); got != "visão geral" {
		t.Fatalf("case-insensitive wide label=%q", got)
	}
}

func TestIT041NotAuthorizedProjectionDoesNotClaimNormality(t *testing.T) {
	snapshot := reportcore.Snapshot{SchemaVersion: 3, ReportID: "RPT-IT041", InspectionID: "inspection-1", TemplateVersionID: "template-v1", ReferenceVersionID: "reference-v1", PromptDigest: "digest", Mode: "HISTORICAL", Classification: "NORMAL", Advisory: "Resultado sujeito às evidências disponíveis.", Context: reportcore.Context{Asset: reportcore.AssetContext{ID: "asset", Name: "Imóvel", Address: "Rua Exemplo"}, Participant: reportcore.ParticipantContext{ID: "participant", Name: "Participante"}, Template: reportcore.TemplateContext{ID: "template", Name: "Modelo", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: "2026-11-04T00:00:00Z"}}, Processing: &reportcore.ProcessingState{Status: "NOT_AUTHORIZED"}, Checklist: &reportcore.ChecklistSection{Rooms: []reportcore.ChecklistRoom{}, Coverage: reportcore.ChecklistCoverage{UnknownScope: true}}, Scope: &reportcore.ScopeSnapshot{Digest: "scope", Version: 1, Profile: "PROPERTY_COMPONENTS"}, Handover: &reportcore.HandoverSection{}, Provenance: &reportcore.Provenance{PromptRevision: "prompt-v1", ScopeDigest: "scope"}}
	customer := customerreport.ProjectChecklist(snapshot)
	if customer == nil || customer.Processing != "Não autorizada" || len(customer.Rooms) != 0 || customer.Coverage.UnknownScope != true {
		t.Fatalf("NOT_AUTHORIZED customer projection=%+v", customer)
	}
	if !customer.Limited || customer.ClassificationLabel != "Resultado com limitações" {
		t.Fatalf("NOT_AUTHORIZED projection label=%q limited=%v", customer.ClassificationLabel, customer.Limited)
	}
	html, err := reportcore.HTML(snapshot)
	if err != nil || !strings.Contains(string(html), "Resultado com limitações") || !strings.Contains(string(html), "Não autorizada") || strings.Contains(string(html), "Sem alertas identificados") {
		t.Fatalf("NOT_AUTHORIZED HTML makes a positive claim: err=%v html=%s", err, html)
	}
}

func TestCatalogFailureRemovesPositiveClaimsAndPreservesFailedAnalysis(t *testing.T) {
	checklist := &reportcore.ChecklistSection{Rooms: []reportcore.ChecklistRoom{{Components: []reportcore.ChecklistComponent{{Description: "Piso em excelente estado", Condition: "GOOD", AssessedScope: "Toda a superfície"}}}}}
	processing := &reportcore.ProcessingState{Status: "FAILED", Message: "A análise comparativa falhou."}
	applyCatalogAvailability(checklist, processing, false, true)
	component := checklist.Rooms[0].Components[0]
	if component.Description != "" || component.Condition != "" || component.AssessedScope != "" || processing.Status != "FAILED" {
		t.Fatalf("catalog or comparison failure retained a positive claim/status: component=%+v processing=%+v", component, processing)
	}
	if len(component.Limitations) != 1 || component.Limitations[0] != "Catalogação pendente ou inconclusiva; a condição não foi determinada." {
		t.Fatalf("catalog limitation=%v", component.Limitations)
	}
}
