//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/testfailure"
)

var api12Operations = []string{"configurePublicationPolicy", "publishReport", "invalidateReportPublication", "publicationPolicy"}

func TestVV_IT_497ManualPublicationLifecycleKeepsExistingShapes(t *testing.T) {
	fx := newAPI11Fixture(t)
	configured := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Query(fx, "configurePublicationPolicy", "vv-it-497-policy", identity.ID{}, 0), fx.ctx, "", "")
	if strings.Contains(configured, `"errors"`) || !strings.Contains(configured, `"mode":"MANUAL"`) || !strings.Contains(configured, `"version":1`) {
		t.Fatalf("configure MANUAL = %s", configured)
	}
	rpt := createAPI12Snapshot(t, fx)
	published := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Query(fx, "publishReport", "vv-it-497-publish", rpt, 0), fx.ctx, "", "")
	publicationID := api12PublicationID(t, published)
	if !strings.Contains(published, `"status":"PUBLISHED"`) || !strings.Contains(published, `"snapshotId":"`+rpt.String()+`"`) {
		t.Fatalf("publishReport = %s", published)
	}
	invalidated := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12InvalidateQuery(publicationID, 1, "vv-it-497-invalidate"), fx.ctx, "", "")
	if strings.Contains(invalidated, `"errors"`) || !strings.Contains(invalidated, `"status":"INVALIDATED"`) || !strings.Contains(invalidated, `"version":2`) {
		t.Fatalf("invalidateReportPublication = %s", invalidated)
	}
	policy := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Query(fx, "publicationPolicy", "", identity.ID{}, 0), fx.ctx, "", "")
	if strings.Contains(policy, `"errors"`) || !strings.Contains(policy, `"mode":"MANUAL"`) || !strings.Contains(policy, `"version":1`) {
		t.Fatalf("publicationPolicy = %s", policy)
	}
}

func TestVV_IT_498PublicationOperationsRequireAuthentication(t *testing.T) {
	fx := newAPI11Fixture(t)
	for _, operation := range api12Operations {
		t.Run(operation, func(t *testing.T) {
			assertAPI11Rejected(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Target(t, fx, operation, "vv-it-498-"+operation), context.Background(), "", ""), "UNAUTHENTICATED")
		})
	}
	assertAPI12Unchanged(t, fx)
}

func TestVV_IT_499PublicationOperationsRequireCapability(t *testing.T) {
	fx := newAPI11Fixture(t)
	roles := map[string]string{"configurePublicationPolicy": auth.Manager, "publishReport": auth.Viewer, "invalidateReportPublication": auth.Viewer, "publicationPolicy": auth.OrganizationAdmin}
	for _, operation := range api12Operations {
		t.Run(operation, func(t *testing.T) {
			actor := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: fx.seed.TenantID, Roles: []string{roles[operation]}}, CorrelationID: "vv-it-499"})
			assertAPI11Rejected(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Target(t, fx, operation, "vv-it-499-"+operation), actor, "", ""), "FORBIDDEN")
		})
	}
	assertAPI12Unchanged(t, fx)
}

func TestVV_IT_500ForeignTenantPublicationResourcesAreNotFound(t *testing.T) {
	fx := newAPI11Fixture(t)
	foreign := newAPI11FixtureWithHarness(t, fx.h)
	var foreignPublication database.ReportPublication
	if err := fx.h.AdminDB.Where("tenant_id=? AND snapshot_id=?", foreign.seed.TenantID, foreign.snapshotID).Take(&foreignPublication).Error; err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"publishReport":               `mutation { publishReport(input:{inspectionId:"` + foreign.seed.InspectionID.String() + `",snapshotId:"` + foreign.snapshotID.String() + `",clientMutationId:"vv-it-500-publish"}) { publication { id } userErrors { code } } }`,
		"invalidateReportPublication": api12InvalidateQuery(foreignPublication.ID, 1, "vv-it-500-invalidate"),
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
			assertAPI11Rejected(t, response, "NOT_FOUND")
			if strings.Contains(response, foreign.seed.TenantID.String()) || strings.Contains(response, foreign.snapshotID.String()) {
				t.Fatalf("foreign publication metadata leaked: %s", response)
			}
		})
	}
	var after database.ReportPublication
	if err := fx.h.AdminDB.Where("id=?", foreignPublication.ID).Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.Status != "PUBLISHED" || after.Version != foreignPublication.Version {
		t.Fatalf("foreign publication changed: %+v", after)
	}
	t.Run("policy is actor scoped", func(t *testing.T) {
		if response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Query(fx, "configurePublicationPolicy", "vv-it-500-policy", identity.ID{}, 0), fx.ctx, "", ""); strings.Contains(response, `"errors"`) {
			t.Fatalf("configure own policy failed: %s", response)
		}
		var foreignPolicies int64
		if err := fx.h.AdminDB.Model(&database.PublicationPolicy{}).Where("tenant_id=?", foreign.seed.TenantID).Count(&foreignPolicies).Error; err != nil {
			t.Fatal(err)
		}
		if foreignPolicies != 0 {
			t.Fatalf("configuring tenant A wrote %d tenant B policies", foreignPolicies)
		}
		for _, query := range []string{
			`query { publicationPolicy(tenantId:"` + foreign.seed.TenantID.String() + `") { mode } }`,
			`mutation { configurePublicationPolicy(input:{mode:"MANUAL",expectedVersion:0,tenantId:"` + foreign.seed.TenantID.String() + `",clientMutationId:"x"}) { policy { mode } } }`,
		} {
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
			if !strings.Contains(response, `"errors"`) || !strings.Contains(response, "tenantId") {
				t.Fatalf("injected tenant selector was not rejected by schema validation: %s", response)
			}
		}
	})
}

func TestVV_IT_501PublicationAdapterUnavailable(t *testing.T) {
	assertAPI12Failure(t, "dependency", "DEPENDENCY_UNAVAILABLE")
}

func TestVV_IT_502PublicationInternalErrorHidesSecret(t *testing.T) {
	assertAPI12Failure(t, "internal", "INTERNAL")
}

func TestVV_IT_505StalePublicationVersionsAreConflict(t *testing.T) {
	fx := newAPI11Fixture(t)
	if response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Query(fx, "configurePublicationPolicy", "vv-it-505-first", identity.ID{}, 0), fx.ctx, "", ""); strings.Contains(response, `"errors"`) {
		t.Fatalf("initial policy: %s", response)
	}
	assertAPI11Rejected(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Query(fx, "configurePublicationPolicy", "vv-it-505-stale", identity.ID{}, 0), fx.ctx, "", ""), "CONFLICT")
	publication := api12CurrentPublication(t, fx)
	assertAPI11Rejected(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12InvalidateQuery(publication.ID, publication.Version+5, "vv-it-505-invalidate"), fx.ctx, "", ""), "CONFLICT")
	var policy database.PublicationPolicy
	if err := fx.h.AdminDB.Where("tenant_id=?", fx.seed.TenantID).Take(&policy).Error; err != nil {
		t.Fatal(err)
	}
	if after := api12CurrentPublication(t, fx); policy.Version != 1 || after.Status != "PUBLISHED" || after.Version != publication.Version {
		t.Fatalf("stale writes changed state: policy=%d publication=%+v", policy.Version, after)
	}
}

func assertAPI12Failure(t *testing.T, failure, code string) {
	t.Helper()
	for _, operation := range api12Operations {
		t.Run(operation, func(t *testing.T) {
			fx := newAPI11Fixture(t)
			if err := testfailure.Install(fx.h.DB); err != nil {
				t.Fatal(err)
			}
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Target(t, fx, operation, "vv-api12-"+failure+"-"+operation), fx.ctx, operation, failure)
			assertAPI11Rejected(t, response, code)
			if strings.Contains(response, testfailure.Secret) {
				t.Fatalf("adapter error leaked secret: %s", response)
			}
			assertAPI12Unchanged(t, fx)
		})
	}
}

// api12Target returns a valid request for operation against the fixture.
func api12Target(t *testing.T, fx api11Fixture, operation, clientID string) string {
	t.Helper()
	switch operation {
	case "publishReport":
		return api12Query(fx, operation, clientID, fx.snapshotID, 0)
	case "invalidateReportPublication":
		publication := api12CurrentPublication(t, fx)
		return api12InvalidateQuery(publication.ID, publication.Version, clientID)
	default:
		return api12Query(fx, operation, clientID, identity.ID{}, 0)
	}
}

func api12Query(fx api11Fixture, operation, clientID string, snapshotID identity.ID, expectedVersion int) string {
	publication := `publication { id snapshotId status version } userErrors { code field } clientMutationId`
	switch operation {
	case "configurePublicationPolicy":
		return `mutation { configurePublicationPolicy(input:{mode:"MANUAL",expectedVersion:` + strconv.Itoa(expectedVersion) + `,clientMutationId:"` + clientID + `"}) { policy { mode version } userErrors { code field } clientMutationId } }`
	case "publishReport":
		return `mutation { publishReport(input:{inspectionId:"` + fx.seed.InspectionID.String() + `",snapshotId:"` + snapshotID.String() + `",clientMutationId:"` + clientID + `"}) { ` + publication + ` } }`
	case "publicationPolicy":
		return `query { publicationPolicy { mode version } }`
	}
	panic("unknown API12 operation " + operation)
}

func api12InvalidateQuery(publicationID identity.ID, expectedVersion int64, clientID string) string {
	return `mutation { invalidateReportPublication(input:{publicationId:"` + publicationID.String() + `",reason:"Correção",expectedVersion:` + strconv.FormatInt(expectedVersion, 10) + `,clientMutationId:"` + clientID + `"}) { publication { id snapshotId status version } userErrors { code field } clientMutationId } }`
}

func api12CurrentPublication(t *testing.T, fx api11Fixture) database.ReportPublication {
	t.Helper()
	var publication database.ReportPublication
	if err := fx.h.AdminDB.Where("tenant_id=? AND snapshot_id=?", fx.seed.TenantID, fx.snapshotID).Take(&publication).Error; err != nil {
		t.Fatal(err)
	}
	return publication
}

func api12PublicationID(t *testing.T, response string) identity.ID {
	t.Helper()
	var payload struct {
		Data struct {
			Publish struct {
				Publication *struct {
					ID string `json:"id"`
				} `json:"publication"`
			} `json:"publishReport"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil || payload.Data.Publish.Publication == nil {
		t.Fatalf("publishReport returned no publication: %s (%v)", response, err)
	}
	id, err := identity.ParseID(payload.Data.Publish.Publication.ID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// createAPI12Snapshot stores an unpublished RPT2 of the fixture inspection.
func createAPI12Snapshot(t *testing.T, fx api11Fixture) identity.ID {
	t.Helper()
	now := time.Now().UTC()
	created, err := generate_snapshot.Create(context.Background(), fx.h.AdminDB, generate_snapshot.Input{TenantID: fx.seed.TenantID, InspectionID: fx.seed.InspectionID, Mode: "CONSOLIDATED", Classification: "NORMAL", TemplateVersionID: "template-v1", ReferenceVersionID: "current-only", PromptDigest: "prompt-v2", Requirements: []reportcore.Requirement{{Key: "video:kitchen", Section: "Vídeo", Label: "Cozinha — Pia", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "COMPLETED"}}, Context: reportcore.Context{Asset: reportcore.AssetContext{ID: fx.seed.AssetID.String(), Name: "QA", Address: "QA"}, Participant: reportcore.ParticipantContext{ID: fx.seed.ParticipantID.String(), Name: "QA"}, Template: reportcore.TemplateContext{ID: "template-v1", Name: "QA", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: now.Format(time.RFC3339Nano)}}}, now)
	if err != nil {
		t.Fatal(err)
	}
	return created.SnapshotID
}

func assertAPI12Unchanged(t *testing.T, fx api11Fixture) {
	t.Helper()
	var policies, publications int64
	if err := fx.h.AdminDB.Model(&database.PublicationPolicy{}).Where("tenant_id=?", fx.seed.TenantID).Count(&policies).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=?", fx.seed.TenantID).Count(&publications).Error; err != nil {
		t.Fatal(err)
	}
	if current := api12CurrentPublication(t, fx); policies != 0 || publications != 1 || current.Status != "PUBLISHED" || current.Version != 1 {
		t.Fatalf("rejected publication operation mutated state: policies=%d publications=%d current=%+v", policies, publications, current)
	}
}
