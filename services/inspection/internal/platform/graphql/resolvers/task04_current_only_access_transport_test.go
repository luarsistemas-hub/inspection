package resolvers

import (
	"context"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestVV_IT_200CustomerCannotReadV2ConditionResults(t *testing.T) {
	db := task06ResolverDB(t)
	tenantID, inspectionID, planID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO inspections.inspections (id,tenant_id,business_unit_id,asset_id) VALUES (?,?,?,?)`, inspectionID, tenantID, identity.NewID(), identity.NewID()).Error; err != nil {
		t.Fatal(err)
	}
	ctx := reportcore.Context{Asset: reportcore.AssetContext{ID: identity.NewID().String(), Name: "Imóvel V2", Address: "Rua QA"}, Participant: reportcore.ParticipantContext{ID: identity.NewID().String(), Name: "QA"}, Template: reportcore.TemplateContext{ID: "v2", Name: "Modelo", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	if _, err := generate_snapshot.Create(context.Background(), db, generate_snapshot.Input{TenantID: tenantID, InspectionID: inspectionID, Mode: "CONSOLIDATED", Classification: "ATTENTION", TemplateVersionID: "v2", ReferenceVersionID: "current-only", PromptDigest: "prompt", VideoPlanID: &planID, Findings: []reportcore.Finding{{Category: "CONSERVATION", Title: "Condição privada V2", Description: "Marcador confidencial de condição observado.", Severity: "LOW", Quality: "ADEQUATE", RecommendedAction: "Verificar", Confidence: .9, EvidenceIDs: []string{identity.NewID().String()}}}, Context: ctx}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	server := task06GraphQLServer(db)
	foreignTenantID := identity.NewID()
	foreignAdmin := identity.NewID()
	requestContext := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: foreignTenantID, Principal: requestctx.Principal{IdentityID: foreignAdmin, TenantID: foreignTenantID, Roles: []string{auth.TenantAdmin}}})
	for _, query := range []string{
		`query { report(inspectionId:"` + inspectionID.String() + `") { id findings { title description } } }`,
		`query { customerReport(inspectionId:"` + inspectionID.String() + `") { snapshotId advisory } }`,
	} {
		response := executeTask06GraphQL(t, server, query, requestContext)
		if !strings.Contains(response, "NOT_FOUND") || strings.Contains(response, "Condição privada V2") || strings.Contains(response, "Marcador confidencial") {
			t.Fatalf("unauthorized V2 query exposed a report or returned another error: %s", response)
		}
	}
	// A customer of the same tenant scoped to another business unit must not
	// reach V2 through the published customer views either.
	sameTenantCustomer := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.CustomerViewer}, Scopes: []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: identity.NewID()}}}})
	for _, query := range []string{
		`query { customerReport(inspectionId:"` + inspectionID.String() + `") { snapshotId advisory } }`,
		`query { customerEvidence(inspectionId:"` + inspectionID.String() + `") { nodes { requirementKey } } }`,
	} {
		response := executeTask06GraphQL(t, server, query, sameTenantCustomer)
		if !strings.Contains(response, "NOT_FOUND") || strings.Contains(response, "Condição privada V2") || strings.Contains(response, "Marcador confidencial") {
			t.Fatalf("out-of-scope customer reached V2 results: %s", response)
		}
	}
}
