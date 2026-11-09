//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	generatesnapshot "inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

// Item 8 (review round 2): the customer GraphQL comparison applies the same
// screening triage as the customer PDF (screening.ApplyDecisions over the
// snapshot's own blocks): a baseline photo whose latest screening decision is
// BLOCKED is labeled unavailable even though the issued snapshot holds it as
// available; a cleared photo stays available.
func TestTask05R2CustomerComparisonAppliesTheCustomerPDFScreening(t *testing.T) {
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
	room := "env:" + base.EnvironmentIdentityIDs[0].String()
	now := time.Now().UTC()
	evidence := []reportcore.Evidence{
		{ID: f.MediaID.String(), RequirementKey: room, Role: "CURRENT", EvidenceKind: "PHOTO", Description: "Sofá na vistoria atual", Availability: "AVAILABLE"},
		{ID: base.OriginMediaID.String(), RequirementKey: room, Role: "REFERENCE", EvidenceKind: "PHOTO", Description: "Sofá na referência", Availability: "AVAILABLE"},
	}
	items := []reportcore.ComparisonItem{{ItemIdentityID: identity.NewID().String(), Room: "Sala", TypeCode: "SOFA", Name: "Sofá", Outcome: reportcore.ComparisonUnchanged, BaselineCondition: "GOOD", CurrentCondition: "GOOD", Pairs: []reportcore.ComparisonPair{{BaselineEvidenceID: base.OriginMediaID.String(), CurrentEvidenceID: f.MediaID.String()}}}}
	comparison := &reportcore.ComparisonSection{Baseline: reportcore.ComparisonBaseline{VersionID: base.OriginVersionID.String(), VersionNumber: 1, Purpose: "MOVE_IN", ActivatedAt: now.Add(-time.Hour).Format(time.RFC3339)}, Status: "COMPLETED", Items: items, Counts: reportcore.CountComparison(items), OutsideBaseline: []reportcore.ComparisonObservation{}}
	input := generatesnapshot.Input{TenantID: f.TenantID, InspectionID: f.InspectionID, Mode: "HISTORICAL", Classification: "NORMAL", TemplateVersionID: f.TemplateVersionID.String(), ReferenceVersionID: base.OriginVersionID.String(), PromptDigest: "prompt", Context: reportcore.Context{Asset: reportcore.AssetContext{ID: "asset", Name: "Imóvel", Address: "Rua"}, Participant: reportcore.ParticipantContext{ID: "participant", Name: "Participante"}, Template: reportcore.TemplateContext{ID: "template", Name: "Modelo", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: now.Format(time.RFC3339Nano)}}, Evidence: evidence, Checklist: &reportcore.ChecklistSection{Rooms: []reportcore.ChecklistRoom{}, Coverage: reportcore.ChecklistCoverage{UnknownScope: true}}, Processing: &reportcore.ProcessingState{Status: "COMPLETED"}, Scope: &reportcore.ScopeSnapshot{Unknown: true}, Handover: &reportcore.HandoverSection{Entries: []reportcore.HandoverEntry{}}, Provenance: &reportcore.Provenance{PromptRevision: "prompt"}, Comparison: comparison, PublicationMode: "AUTOMATIC", ActorID: f.IdentityID}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		if _, err := generatesnapshot.CreateInTx(ctx, tx, input, now); err != nil {
			return err
		}
		for media, status := range map[identity.ID]string{base.OriginMediaID: "BLOCKED", f.MediaID: "CLEARED"} {
			if err := tx.Create(&database.ScreeningRun{ID: identity.NewID(), TenantID: f.TenantID, MediaID: media, Status: status, ModelDigest: "screening-model", Regions: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	meta := requestctx.Metadata{TenantID: f.TenantID, Principal: requestctx.Principal{IdentityID: f.IdentityID, MembershipID: f.MembershipID, TenantID: f.TenantID, Roles: []string{auth.TenantAdmin}, ProductEntitlements: auth.ProductsForRole(auth.TenantAdmin)}}
	query := &queryResolver{Resolver: &Resolver{DB: h.DB}}
	report, err := query.CustomerReport(requestctx.WithMetadata(ctx, meta), f.InspectionID.String(), nil, nil)
	if err != nil || report == nil || report.Comparison == nil || len(report.Comparison.Items) != 1 || len(report.Comparison.Items[0].Pairs) != 1 {
		t.Fatalf("customer report = %+v, %v", report, err)
	}
	pair := report.Comparison.Items[0].Pairs[0]
	if pair.BaselineAvailability != "UNAVAILABLE" || pair.CurrentAvailability != "AVAILABLE" {
		t.Fatalf("customer pair = %+v; want the screened baseline photo unavailable as in the customer PDF", pair)
	}
	if report.Comparison.Items[0].Outcome != "UNCHANGED" || report.Comparison.Counts.Unchanged != 1 {
		t.Fatalf("screening changed the issued conclusion: %+v", report.Comparison.Items[0])
	}
}
