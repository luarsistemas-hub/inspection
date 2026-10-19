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
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/features/reports/publication"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/testfailure"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

var api11Operations = []string{"report", "reports", "customerReport", "customerEvidence"}

type api11Fixture struct {
	fixedOriginGraphQLFixture
	customer             context.Context
	snapshotID, planID   identity.ID
	frameID, sourceMedia identity.ID
}

func TestVV_IT_488PublishedVideoReportExposesFrameProvenance(t *testing.T) {
	fx := newAPI11Fixture(t)
	internal := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api11Query(fx, "report", fx.seed.InspectionID, ""), fx.ctx, "", "")
	customer := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api11Query(fx, "customerEvidence", fx.seed.InspectionID, ""), fx.customer, "", "")
	for name, response := range map[string]string{"report": internal, "customerEvidence": customer} {
		for _, want := range []string{`"evidenceKind":"VIDEO_FRAME"`, `"frameId":"` + fx.frameID.String() + `"`, `"planId":"` + fx.planID.String() + `"`, `"sourceMediaId":"` + fx.sourceMedia.String() + `"`, `"timestampMs":7000`, `"sourceAvailability":"AVAILABLE"`} {
			if strings.Contains(response, `"errors"`) || !strings.Contains(response, want) {
				t.Fatalf("%s missing %s: %s", name, want, response)
			}
		}
	}
	if response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api11Query(fx, "customerReport", fx.seed.InspectionID, ""), fx.customer, "", ""); strings.Contains(response, `"errors"`) || !strings.Contains(response, `"snapshotId":"`+fx.snapshotID.String()+`"`) {
		t.Fatalf("customerReport did not return published RPT1: %s", response)
	}
	if response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api11Query(fx, "reports", identity.ID{}, ""), fx.ctx, "", ""); strings.Contains(response, `"errors"`) || !strings.Contains(response, fx.seed.InspectionID.String()) {
		t.Fatalf("reports did not list RPT1: %s", response)
	}
}

func TestVV_IT_489ReportOperationsRequireAuthentication(t *testing.T) {
	fx := newAPI11Fixture(t)
	for _, operation := range api11Operations {
		t.Run(operation, func(t *testing.T) {
			assertAPI11Rejected(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api11Query(fx, operation, fx.seed.InspectionID, ""), context.Background(), "", ""), "UNAUTHENTICATED")
		})
	}
}

func TestVV_IT_490ReportOperationsRequireReadCapability(t *testing.T) {
	fx := newAPI11Fixture(t)
	for _, operation := range api11Operations {
		t.Run(operation, func(t *testing.T) {
			// Internal reads exclude customer viewers; customer reads exclude
			// administrative roles outside the reader set.
			role := auth.CustomerViewer
			if strings.HasPrefix(operation, "customer") {
				role = auth.OrganizationAdmin
			}
			actor := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: fx.seed.TenantID, Roles: []string{role}, Scopes: []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: fx.seed.BusinessUnitID}}}, CorrelationID: "vv-it-490"})
			assertAPI11Rejected(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api11Query(fx, operation, fx.seed.InspectionID, ""), actor, "", ""), "FORBIDDEN")
		})
	}
}

func TestVV_IT_491ForeignTenantReportsAreNotFound(t *testing.T) {
	fx := newAPI11Fixture(t)
	foreign := newAPI11FixtureWithHarness(t, fx.h)
	for _, operation := range []string{"report", "customerReport", "customerEvidence"} {
		t.Run(operation, func(t *testing.T) {
			actor := fx.ctx
			if strings.HasPrefix(operation, "customer") {
				actor = fx.customer
			}
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api11Query(fx, operation, foreign.seed.InspectionID, ""), actor, "", "")
			assertAPI11Rejected(t, response, "NOT_FOUND")
			for _, leaked := range []string{foreign.seed.TenantID.String(), foreign.snapshotID.String(), foreign.frameID.String()} {
				if strings.Contains(response, leaked) {
					t.Fatalf("foreign report metadata leaked: %s", response)
				}
			}
		})
	}
	t.Run("reports is actor scoped", func(t *testing.T) {
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api11Query(fx, "reports", identity.ID{}, ""), fx.ctx, "", "")
		if strings.Contains(response, foreign.seed.InspectionID.String()) || !strings.Contains(response, fx.seed.InspectionID.String()) {
			t.Fatalf("reports leaked another tenant or lost the actor tenant: %s", response)
		}
		injected := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, `query { reports(tenantId:"`+foreign.seed.TenantID.String()+`") { nodes { inspectionId } } }`, fx.ctx, "", "")
		if !strings.Contains(injected, `"errors"`) || !strings.Contains(injected, "tenantId") || strings.Contains(injected, `"nodes"`) {
			t.Fatalf("injected tenant selector was not rejected by schema validation: %s", injected)
		}
	})
}

func TestVV_IT_492ReportOperationsAdapterUnavailable(t *testing.T) {
	assertAPI11Failure(t, "dependency", "DEPENDENCY_UNAVAILABLE")
}

func TestVV_IT_493ReportOperationsInternalErrorHidesSecret(t *testing.T) {
	assertAPI11Failure(t, "internal", "INTERNAL")
}

func TestVV_IT_494PageAboveOneHundredIsInvalidInput(t *testing.T) {
	fx := newAPI11Fixture(t)
	cases := map[string]struct {
		query string
		actor context.Context
	}{
		"report evidence":  {`query { report(inspectionId:"` + fx.seed.InspectionID.String() + `") { evidence(first:101) { nodes { id } } } }`, fx.ctx},
		"reports":          {`query { reports(first:101) { nodes { inspectionId } } }`, fx.ctx},
		"customerEvidence": {`query { customerEvidence(inspectionId:"` + fx.seed.InspectionID.String() + `",first:101) { nodes { id } } }`, fx.customer},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, tc.query, tc.actor, "", "")
			if !strings.Contains(response, "INVALID_INPUT") || strings.Contains(response, `"nodes":[{`) {
				t.Fatalf("first=101 accepted: %s", response)
			}
		})
	}
}

func TestVV_IT_495CursorFromAnotherResourceIsInvalidInput(t *testing.T) {
	fx := newAPI11Fixture(t)
	other := newAPI11FixtureWithHarness(t, fx.h)
	otherCursor := encodeEvidenceCursor(fx.seed.TenantID, other.snapshotID, other.frameID.String())
	cases := map[string]struct {
		query string
		actor context.Context
	}{
		"report evidence":  {`query { report(inspectionId:"` + fx.seed.InspectionID.String() + `") { evidence(first:1,after:"` + otherCursor + `") { nodes { id } } } }`, fx.ctx},
		"customerEvidence": {`query { customerEvidence(inspectionId:"` + fx.seed.InspectionID.String() + `",first:1,after:"` + otherCursor + `") { nodes { id } } }`, fx.customer},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, tc.query, tc.actor, "", "")
			if !strings.Contains(response, "INVALID_INPUT") || strings.Contains(response, `"nodes":[{`) {
				t.Fatalf("foreign cursor accepted: %s", response)
			}
		})
	}
}

func TestVV_IT_496UnfinishedReportIsInvalidState(t *testing.T) {
	fx := newAPI11Fixture(t)
	pending, err := fx.h.SeedTask06Fixture(context.Background(), harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	actor := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: pending.TenantID, Principal: requestctx.Principal{IdentityID: pending.IdentityID, TenantID: pending.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "vv-it-496"})
	assertAPI11Rejected(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api11Query(fx, "report", pending.InspectionID, ""), actor, "", ""), "INVALID_STATE")
	// An explicit version that was never produced stays unknown.
	assertAPI11Rejected(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, `query { report(inspectionId:"`+fx.seed.InspectionID.String()+`",version:9) { id } }`, fx.ctx, "", ""), "NOT_FOUND")
}

func assertAPI11Failure(t *testing.T, failure, code string) {
	t.Helper()
	for _, operation := range api11Operations {
		t.Run(operation, func(t *testing.T) {
			fx := newAPI11Fixture(t)
			if err := testfailure.Install(fx.h.DB); err != nil {
				t.Fatal(err)
			}
			actor := fx.ctx
			if strings.HasPrefix(operation, "customer") {
				actor = fx.customer
			}
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api11Query(fx, operation, fx.seed.InspectionID, ""), actor, operation, failure)
			assertAPI11Rejected(t, response, code)
			if strings.Contains(response, testfailure.Secret) {
				t.Fatalf("adapter error leaked secret: %s", response)
			}
		})
	}
}

func api11Query(fx api11Fixture, operation string, inspectionID identity.ID, after string) string {
	evidence := `nodes { id evidenceKind frameId planId sourceMediaId timestampMs sourceAvailability } pageInfo { endCursor hasNextPage }`
	switch operation {
	case "report":
		return `query { report(inspectionId:"` + inspectionID.String() + `") { id version evidence(first:25) { ` + evidence + ` } } }`
	case "reports":
		return `query { reports(first:25) { nodes { inspectionId } pageInfo { hasNextPage } } }`
	case "customerReport":
		return `query { customerReport(inspectionId:"` + inspectionID.String() + `") { snapshotId status } }`
	case "customerEvidence":
		return `query { customerEvidence(inspectionId:"` + inspectionID.String() + `",first:25) { ` + evidence + ` } }`
	}
	panic("unknown API11 operation " + operation)
}

func newAPI11Fixture(t *testing.T) api11Fixture {
	t.Helper()
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	h, err := harness.NewFromEnv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return newAPI11FixtureWithHarness(t, h)
}

func newAPI11FixtureWithHarness(t *testing.T, h *harness.Harness) api11Fixture {
	t.Helper()
	seed, err := h.SeedTask06Fixture(context.Background(), harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	report := seedAPI11Report(t, h, seed)
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: h.DB, Bus: mediator.New(), Store: h.Store, PublicationService: publication.Service{DB: h.DB}}}))
	server.SetErrorPresenter(graph.PresentError)
	admin := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: seed.TenantID, Principal: requestctx.Principal{IdentityID: seed.IdentityID, TenantID: seed.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "vv-api11"})
	customer := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: seed.TenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: seed.TenantID, Roles: []string{auth.CustomerViewer}, Scopes: []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: seed.BusinessUnitID}}}, CorrelationID: "vv-api11-customer"})
	return api11Fixture{fixedOriginGraphQLFixture: fixedOriginGraphQLFixture{ctx: admin, h: h, seed: seed, server: server}, customer: customer, snapshotID: report.snapshotID, planID: report.planID, frameID: report.frameID, sourceMedia: report.sourceMedia}
}

type api11Report struct {
	snapshotID, planID, frameID, sourceMedia identity.ID
}

// seedAPI11Report stores a published RPT1 whose VIDEO_FRAME evidence is a
// member of a sealed plan of the seed inspection.
func seedAPI11Report(t *testing.T, h *harness.Harness, seed harness.Task06Fixture) api11Report {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	digest := strings.Repeat("c", 64)
	report := api11Report{planID: identity.NewID(), frameID: identity.NewID(), sourceMedia: identity.NewID()}
	preparationID, submissionID, itemID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", seed.TenantID, seed.InspectionID).Update("media_kind", "VIDEO").Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: report.sourceMedia, TenantID: seed.TenantID, ResponsibilityID: seed.ResponsibilityID, ObjectKey: "vv-api11/" + report.sourceMedia.String() + ".mp4", ContentType: "video/mp4", SHA256: digest, SizeBytes: 10, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "vv-api11-" + report.sourceMedia.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: seed.TenantID, DraftID: seed.DraftID, ResponsibilityID: seed.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: json.RawMessage(`{}`), PayloadDigest: digest, SubmittedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: seed.TenantID, MediaID: report.sourceMedia, SubmissionID: &submissionID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &now, CreatedAt: now}).Error; err != nil {
			return err
		}
		days, expiry := 5, now.Add(5*24*time.Hour)
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: seed.TenantID, MediaID: report.sourceMedia, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 30_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &now, RetentionDays: &days, SourceExpiryAt: &expiry, Status: "RETAINED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: report.frameID, TenantID: seed.TenantID, SourceMediaID: report.sourceMedia, PreparationID: preparationID, PTSMicros: 7_000_000, SHA256: digest, ObjectKey: "vv-api11/frame/" + report.frameID.String(), Width: 2, Height: 2, Role: "CURRENT", SizeBytes: 10, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlan{ID: report.planID, TenantID: seed.TenantID, InspectionID: seed.InspectionID, SubmissionID: submissionID, CurrentPreparationID: preparationID, Generation: 1, PlanDigest: digest, Status: "SEALED", SealedAt: &now, NextAttemptAt: now, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlanItem{ID: itemID, TenantID: seed.TenantID, PlanID: report.planID, ComponentID: identity.NewID(), RequirementKey: "video:kitchen", Outcome: "CURRENT_ONLY", InputDigest: digest, OriginFrameIDs: json.RawMessage(`[]`), CurrentFrameIDs: json.RawMessage(`["` + report.frameID.String() + `"]`), CoreGroups: json.RawMessage(`[]`), CurrentGroups: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoPlanItemFrame{ID: identity.NewID(), TenantID: seed.TenantID, PlanItemID: itemID, FrameID: report.frameID, Role: "CURRENT", CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	pts, ms := int64(7_000_000), int64(7_000)
	created, err := generate_snapshot.Create(ctx, h.AdminDB, generate_snapshot.Input{TenantID: seed.TenantID, InspectionID: seed.InspectionID, VideoPlanID: &report.planID, Mode: "CONSOLIDATED", Classification: "ATTENTION", TemplateVersionID: "template-v1", ReferenceVersionID: "current-only", PromptDigest: "prompt-v1", Requirements: []reportcore.Requirement{{Key: "video:kitchen", Section: "Vídeo", Label: "Cozinha — Pia", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "COMPLETED"}}, Evidence: []reportcore.Evidence{{ID: report.frameID.String(), RequirementKey: "video:kitchen", Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: report.frameID.String(), SourceMediaID: report.sourceMedia.String(), PreparationID: preparationID.String(), PTSMicros: &pts, TimestampMS: &ms, FrameDigest: digest, DisplayDigest: digest, Availability: "AVAILABLE", SourceAvailability: "RETAINED"}}, Context: reportcore.Context{Asset: reportcore.AssetContext{ID: seed.AssetID.String(), Name: "QA", Address: "QA"}, Participant: reportcore.ParticipantContext{ID: seed.ParticipantID.String(), Name: "QA"}, Template: reportcore.TemplateContext{ID: "template-v1", Name: "QA", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: now.Format(time.RFC3339Nano)}}}, now)
	if err != nil {
		t.Fatal(err)
	}
	report.snapshotID = created.SnapshotID
	if err := h.AdminDB.Create(&database.ReportPublication{ID: identity.NewID(), TenantID: seed.TenantID, SnapshotID: created.SnapshotID, InspectionID: seed.InspectionID, PolicyVersion: 1, Status: "PUBLISHED", ActorID: seed.IdentityID, Version: 1, PublishedAt: &now, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	return report
}

// assertAPI11Rejected accepts a null nullable root field (report,
// customerReport) or null data, with the expected error code.
func assertAPI11Rejected(t *testing.T, response, code string) {
	t.Helper()
	var envelope struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []struct {
			Extensions map[string]any `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(response), &envelope); err != nil {
		t.Fatalf("decode GraphQL response: %v; response=%s", err, response)
	}
	for _, value := range envelope.Data {
		if string(value) != "null" {
			t.Fatalf("rejected read returned data: %s", response)
		}
	}
	for _, item := range envelope.Errors {
		if item.Extensions["code"] == code {
			return
		}
	}
	t.Fatalf("expected GraphQL error %s, got %s", code, response)
}
