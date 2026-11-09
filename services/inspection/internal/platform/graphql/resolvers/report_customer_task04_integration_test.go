//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/reports/core"
	generatesnapshot "inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm/clause"
)

type task04ReportPresigner struct{ objectstore.Client }

func (task04ReportPresigner) PresignGet(_ context.Context, bucket, key string, _ time.Duration) (string, error) {
	return "https://signed.example/" + bucket + "/" + key, nil
}

func newTask04ResolverFixture(t *testing.T) (context.Context, *harness.Harness, harness.Task06Fixture, *queryResolver, generatesnapshot.Result) {
	t.Helper()
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewPostgresFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{ComparisonMode: "CURRENT_ONLY"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.Membership{}).Where("tenant_id=? AND id=?", f.TenantID, f.MembershipID).Update("role", auth.CustomerViewer).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Clauses(clause.OnConflict{DoNothing: true}).Create(&database.ProductEntitlement{ID: identity.NewID(), TenantID: f.TenantID, MembershipID: f.MembershipID, Product: auth.DashboardProduct, CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	input := generatesnapshot.Input{
		TenantID: f.TenantID, InspectionID: f.InspectionID, Mode: "HISTORICAL", Classification: "NORMAL",
		TemplateVersionID: f.TemplateVersionID.String(), ReferenceVersionID: generatesnapshot.ReferenceFree,
		PromptDigest: "task04-customer", PublicationMode: "AUTOMATIC", PublicationPolicyVersion: 1,
		Context: core.Context{
			Asset:       core.AssetContext{ID: f.AssetID.String(), Name: "Imóvel QA", ExternalKey: "QA-1", Address: "Rua de teste"},
			Participant: core.ParticipantContext{ID: f.ParticipantID.String(), Name: "Responsável QA"},
			Template:    core.TemplateContext{ID: f.TemplateID.String(), Name: "Modelo QA", Version: 1},
			Inspection:  core.InspectionContext{GeneratedAt: now.Format(time.RFC3339Nano)},
		},
		Checklist: &core.ChecklistSection{
			Coverage: core.ChecklistCoverage{Expected: 1, FullyAssessed: 1},
			Rooms: []core.ChecklistRoom{{Key: "kitchen", TypeCode: "KITCHEN", Name: "Cozinha", Components: []core.ChecklistComponent{{
				Key: "floor", TypeCode: "FLOOR", Label: "Piso", Status: "OBSERVED", ReviewStatus: "REJECTED", Description: "Descrição permitida",
				Condition: "FAIR", EvidenceIDs: []string{"media-public"}, Limitations: []string{"Ângulo parcial"},
			}}}},
		},
		Processing: &core.ProcessingState{Status: "COMPLETED"}, Scope: &core.ScopeSnapshot{Unknown: true},
		Handover: &core.HandoverSection{Entries: []core.HandoverEntry{}}, Provenance: &core.Provenance{PromptRevision: "privada"},
		Evidence: []core.Evidence{{ID: "media-public", RequirementKey: "env:kitchen", Role: "CURRENT", Availability: "MISSING"}},
		Findings: []core.Finding{{Category: "CONSERVATION", Title: "Texto interno confidencial", Description: "Nota interna confidencial", Severity: "LOW", Confidence: 0.99, EvidenceIDs: []string{"media-public"}, Quality: "ADEQUATE", RecommendedAction: "REVIEW"}},
	}
	created, err := generatesnapshot.Create(ctx, h.AdminDB, input, now)
	if err != nil {
		t.Fatal(err)
	}
	meta := requestctx.Metadata{TenantID: f.TenantID, Principal: requestctx.Principal{IdentityID: f.IdentityID, MembershipID: f.MembershipID, TenantID: f.TenantID, Roles: []string{auth.CustomerViewer}, Scopes: []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: f.BusinessUnitID}}, ProductEntitlements: auth.ProductsForRole(auth.CustomerViewer)}}
	ctx = requestctx.WithMetadata(ctx, meta)
	resolver := &queryResolver{Resolver: &Resolver{DB: h.DB, Store: objectstore.Store{Bucket: "reports", Client: task04ReportPresigner{}}, Authorizer: auth.Authorizer{Store: auth.GORMMembershipStore{DB: h.DB}}}}
	return ctx, h, f, resolver, created
}

// TestIT014IT015CustomerReportAndDownloadUsePinnedAllowlistedSnapshot proves
// the customer projection removes internal fields and the download resolves
// the artifact for that exact immutable version.
func TestIT014IT015CustomerReportAndDownloadUsePinnedAllowlistedSnapshot(t *testing.T) {
	ctx, h, f, resolver, created := newTask04ResolverFixture(t)
	report, err := resolver.customerReport(ctx, f.InspectionID.String(), &created.Version)
	if err != nil || report == nil || report.SnapshotID != created.SnapshotID.String() || report.Checklist == nil || report.Checklist.Rooms[0].Components[0].Description == nil || *report.Checklist.Rooms[0].Components[0].Description != "Descrição permitida" || len(report.Checklist.Rooms[0].Components[0].Limitations) != 1 || report.Classification != "NORMAL" || !report.Limited || report.ClassificationLabel != "Resultado com limitações" {
		t.Fatalf("customer projection=%+v err=%v", report, err)
	}
	if got := report.Checklist.Rooms[0].Components[0]; got.Status != "Observado" || got.Condition == nil || *got.Condition != "Regular" || len(got.EvidenceIds) != 1 || got.EvidenceIds[0] != "E001" {
		t.Fatalf("customer report vocabulary/evidence reference=%+v", got)
	}
	var snapshotRow database.ReportSnapshot
	if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, created.SnapshotID).Take(&snapshotRow).Error; err != nil {
		t.Fatal(err)
	}
	internalView := mapReport(snapshotRow)
	if internalView.ClassificationLabel != report.ClassificationLabel || internalView.Limited != report.Limited || !strings.Contains(internalView.HTML, report.ClassificationLabel) {
		t.Fatalf("internal GraphQL/HTML classification differs from customer screen: report=%+v customer=%+v", internalView, report)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"backstage confidencial", "privada", "Texto interno confidencial", "Nota interna confidencial", "Confidence", "confidence"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("customer projection leaked %q: %s", private, encoded)
		}
	}
	artifact := database.ReportArtifact{ID: identity.NewID(), TenantID: f.TenantID, SnapshotID: created.SnapshotID, Kind: "PDF_CUSTOMER", ObjectKey: "tenant/report-v1.pdf", SHA256: strings.Repeat("a", 64), Status: "READY", CreatedAt: time.Now().UTC()}
	if err := h.AdminDB.Create(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	kind := "PDF_CUSTOMER"
	download, err := resolver.ReportDownload(ctx, created.SnapshotID.String(), &kind)
	if err != nil || download == nil || download.SnapshotID != created.SnapshotID.String() || download.Status != "READY" || download.Sha256 == nil || *download.Sha256 != artifact.SHA256 || !strings.Contains(download.URL, artifact.ObjectKey) {
		t.Fatalf("customer download=%+v err=%v", download, err)
	}
}

// TestIT042RevokedMembershipCannotReadCustomerReportOrDownload proves that
// current membership revocation is rechecked at both customer boundaries.
func TestIT042RevokedMembershipCannotReadCustomerReportOrDownload(t *testing.T) {
	ctx, h, f, resolver, created := newTask04ResolverFixture(t)
	if err := h.AdminDB.Model(&database.Membership{}).Where("tenant_id=? AND id=?", f.TenantID, f.MembershipID).Update("status", "REVOKED").Error; err != nil {
		t.Fatal(err)
	}
	if report, err := resolver.customerReport(ctx, f.InspectionID.String(), &created.Version); err == nil || report != nil {
		t.Fatalf("revoked membership received report=%+v err=%v", report, err)
	}
	if download, err := resolver.ReportDownload(ctx, created.SnapshotID.String(), nil); err == nil || download != nil {
		t.Fatalf("revoked membership received media=%+v err=%v", download, err)
	}
}
