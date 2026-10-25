//go:build integration

package resolvers

import (
	"context"
	"strings"
	"testing"
	"time"

	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/platform/database"
)

func TestIT053CustomerSeesV2InventoryOnlyAfterPublication(t *testing.T) {
	fx := newAPI11Fixture(t)
	if err := fx.h.AdminDB.Where("tenant_id=? AND snapshot_id=?", fx.seed.TenantID, fx.snapshotID).Delete(&database.ReportPublication{}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	evidence := []reportcore.Evidence{{ID: "origin-photo", RequirementKey: "env:sala", Role: "REFERENCE", EvidenceKind: "PHOTO"}, {ID: "current-photo", RequirementKey: "env:sala", Role: "CURRENT", EvidenceKind: "PHOTO"}}
	inventory := &reportcore.InventorySection{
		Reference:     reportcore.InventoryReference{VersionID: "inventory-version", VersionNumber: 3, Purpose: "MOVE_IN", ActivatedAt: now.Format(time.RFC3339)},
		Coverage:      &reportcore.InventoryCoverage{ExpectedItems: 2, AssessedItems: 2, EnvironmentsNotCaptured: []string{}, EnvironmentsAddedDuringCapture: []string{}},
		Problems:      []reportcore.InventoryEntry{{ItemIdentityID: "private-item", Environment: "Sala", Type: "SOFA", Name: "Sofá", Status: "MISSING", Pairs: []reportcore.InventoryEvidencePair{{Origin: "origin-photo", Current: "current-photo", Decision: "SAME_VIEW"}}}},
		Informational: []reportcore.InventoryEntry{},
	}
	created, err := generate_snapshot.Create(context.Background(), fx.h.AdminDB, generate_snapshot.Input{TenantID: fx.seed.TenantID, InspectionID: fx.seed.InspectionID, Mode: "CONSOLIDATED", Classification: "CRITICAL", TemplateVersionID: "template-v2", ReferenceVersionID: "inventory-version", PromptDigest: "it053", Evidence: evidence, Inventory: inventory, Context: reportcore.Context{Asset: reportcore.AssetContext{ID: fx.seed.AssetID.String(), Name: "QA", Address: "QA"}, Participant: reportcore.ParticipantContext{ID: fx.seed.ParticipantID.String(), Name: "QA"}, Template: reportcore.TemplateContext{ID: "template-v2", Name: "QA", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: now.Format(time.RFC3339Nano)}}}, now)
	if err != nil {
		t.Fatal(err)
	}
	query := `query { customerReport(inspectionId:"` + fx.seed.InspectionID.String() + `") { snapshotId inventory { referenceVersionNumber purpose coverage { expectedItems assessedItems } problems { environment name status pairs { originEvidenceId currentEvidenceId } } } } }`
	unpublished := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.customer, "", "")
	if strings.Contains(unpublished, `"errors"`) || !strings.Contains(unpublished, `"customerReport":null`) {
		t.Fatalf("unpublished v2 report must not be visible: %s", unpublished)
	}
	if response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Query(fx, "publishReport", "it053-publish", created.SnapshotID, 0), fx.ctx, "", ""); strings.Contains(response, `"errors"`) || strings.Contains(response, `"code"`) {
		t.Fatalf("publishReport: %s", response)
	}
	published := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.customer, "", "")
	for _, want := range []string{`"snapshotId":"` + created.SnapshotID.String() + `"`, `"referenceVersionNumber":3`, `"purpose":"MOVE_IN"`, `"expectedItems":2`, `"name":"Sofá"`, `"status":"MISSING"`, `"originEvidenceId":"origin-photo"`} {
		if !strings.Contains(published, want) {
			t.Fatalf("published customer report missing %s: %s", want, published)
		}
	}
	for _, forbidden := range []string{"private-item", "inventory-version", "SAME_VIEW"} {
		if strings.Contains(published, forbidden) {
			t.Fatalf("customer report leaked %q: %s", forbidden, published)
		}
	}
}
