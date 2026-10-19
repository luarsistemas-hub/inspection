//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestVV_IT_265ManualReportRequiresAuthorizedPublish(t *testing.T) {
	fx := newAPI11Fixture(t)
	if err := fx.h.AdminDB.Where("tenant_id=? AND snapshot_id=?", fx.seed.TenantID, fx.snapshotID).Delete(&database.ReportPublication{}).Error; err != nil {
		t.Fatal(err)
	}
	assertVVCustomerReport(t, fx, "null")
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Query(fx, "publishReport", "vv-it-265-publish", fx.snapshotID, 0), fx.ctx, "", "")
	if strings.Contains(response, `"errors"`) {
		t.Fatalf("publishReport: %s", response)
	}
	assertVVCustomerReport(t, fx, fx.snapshotID.String())
}

func TestVV_IT_266AutomaticReportIsCustomerVisible(t *testing.T) {
	fx := newAPI11Fixture(t)
	retireVVCurrentPublication(t, fx, "vv-it-266-retire")
	configureVVPolicy(t, fx, "AUTOMATIC", 0, "vv-it-266-policy")
	id := createVVPublicationSnapshot(t, fx, 1, "AUTOMATIC", nil, nil, nil)
	var publication database.ReportPublication
	if err := fx.h.AdminDB.Where("tenant_id=? AND snapshot_id=?", fx.seed.TenantID, id).Take(&publication).Error; err != nil {
		t.Fatal(err)
	}
	if publication.Status != "PUBLISHED" || publication.PolicyVersion != 1 {
		t.Fatalf("automatic publication = %+v", publication)
	}
	assertVVCustomerReport(t, fx, id.String())
}

func TestVV_IT_267PublishedPartialReportExposesBathroomGap(t *testing.T) {
	fx := newAPI11Fixture(t)
	if err := fx.h.AdminDB.Where("tenant_id=? AND snapshot_id=?", fx.seed.TenantID, fx.snapshotID).Delete(&database.ReportPublication{}).Error; err != nil {
		t.Fatal(err)
	}
	gap := []reportcore.Requirement{{Key: "bathroom", Section: "Banheiro", Label: "Banheiro", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "PENDING", ImpossibilityReason: "Área não capturada"}}
	id := createVVPublicationSnapshot(t, fx, 0, "", gap, nil, nil)
	if result := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Query(fx, "publishReport", "vv-it-267", id, 0), fx.ctx, "", ""); strings.Contains(result, `"errors"`) {
		t.Fatalf("publish: %s", result)
	}
	query := `query { customerReport(inspectionId:"` + fx.seed.InspectionID.String() + `") { snapshotId requirements { key section label analysisStatus impossibilityReason } } }`
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.customer, "", "")
	for _, want := range []string{`"snapshotId":"` + id.String() + `"`, `"key":"bathroom"`, `"section":"Banheiro"`, `"analysisStatus":"PENDING"`, `"impossibilityReason":"Área não capturada"`} {
		if !strings.Contains(response, want) {
			t.Fatalf("customer report missing %s: %s", want, response)
		}
	}
}

func TestVV_IT_268ManualReportPublicationSupersedesPreviousVersion(t *testing.T) {
	fx := newAPI11Fixture(t)
	configureVVPolicy(t, fx, "MANUAL", 0, "vv-it-268-policy")
	previous := api12CurrentPublication(t, fx)
	newID := createVVPublicationSnapshot(t, fx, 1, "MANUAL", nil, nil, nil)
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Query(fx, "publishReport", "vv-it-268-publish", newID, 0), fx.ctx, "", "")
	if strings.Contains(response, `"errors"`) {
		t.Fatalf("publish: %s", response)
	}
	var old, current database.ReportPublication
	if err := fx.h.AdminDB.First(&old, "id=?", previous.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.h.AdminDB.Where("tenant_id=? AND snapshot_id=?", fx.seed.TenantID, newID).Take(&current).Error; err != nil {
		t.Fatal(err)
	}
	if old.Status != "SUPERSEDED" || old.SupersededBy == nil || *old.SupersededBy != current.ID || current.Status != "PUBLISHED" || current.PolicyVersion != 1 {
		t.Fatalf("supersession old=%+v current=%+v", old, current)
	}
	assertVVCustomerReport(t, fx, newID.String())
}

func TestVV_IT_269InvalidatedReportCannotBePublishedAgain(t *testing.T) {
	fx := newAPI11Fixture(t)
	pub := api12CurrentPublication(t, fx)
	invalidated := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12InvalidateQuery(pub.ID, pub.Version, "vv-it-269-invalidate"), fx.ctx, "", "")
	if strings.Contains(invalidated, `"errors"`) {
		t.Fatalf("invalidate: %s", invalidated)
	}
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Query(fx, "publishReport", "vv-it-269-republish", fx.snapshotID, 0), fx.ctx, "", "")
	if !strings.Contains(response, `"code":"INVALID_STATE"`) || !strings.Contains(response, `"errors"`) {
		t.Fatalf("republishing invalidated snapshot should return INVALID_STATE: %s", response)
	}
	var n int64
	if err := fx.h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND snapshot_id=?", fx.seed.TenantID, fx.snapshotID).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("invalidated snapshot got %d release records", n)
	}
}

func TestVV_IT_270MissingPolicyDefaultsToManual(t *testing.T) {
	fx := newAPI11Fixture(t)
	var n int64
	if err := fx.h.AdminDB.Model(&database.PublicationPolicy{}).Where("tenant_id=?", fx.seed.TenantID).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("fixture unexpectedly has policy override (%d)", n)
	}
	if err := fx.h.AdminDB.Where("tenant_id=? AND snapshot_id=?", fx.seed.TenantID, fx.snapshotID).Delete(&database.ReportPublication{}).Error; err != nil {
		t.Fatal(err)
	}
	assertVVCustomerReport(t, fx, "null")
	id := createVVPublicationSnapshot(t, fx, 0, "", nil, nil, nil)
	var snap database.ReportSnapshot
	if err := fx.h.AdminDB.First(&snap, "id=?", id).Error; err != nil {
		t.Fatal(err)
	}
	if snap.PublicationPolicyVersion != 0 {
		t.Fatalf("default policy version=%d, want 0", snap.PublicationPolicyVersion)
	}
	assertVVCustomerReport(t, fx, "null")
}

func TestVV_IT_271CustomerEvidencePaginationReachesAll200Findings(t *testing.T) {
	fx := newAPI11Fixture(t)
	if err := fx.h.AdminDB.Where("tenant_id=? AND snapshot_id=?", fx.seed.TenantID, fx.snapshotID).Delete(&database.ReportPublication{}).Error; err != nil {
		t.Fatal(err)
	}
	evidence := make([]reportcore.Evidence, 200)
	findings := make([]reportcore.Finding, 200)
	for i := range evidence {
		evidence[i] = reportcore.Evidence{ID: identity.NewID().String(), RequirementKey: fmt.Sprintf("finding-%03d", i), Role: "CURRENT", Description: fmt.Sprintf("Achado %03d", i), Availability: "MISSING"}
		findings[i] = reportcore.Finding{ID: identity.NewID().String(), Category: "CONSERVATION", Title: fmt.Sprintf("Achado %03d", i), Description: "Observação", Severity: "LOW", Confidence: 0.9, Quality: "HIGH", RecommendedAction: "Revisar", EvidenceIDs: []string{evidence[i].ID}}
	}
	id := createVVPublicationSnapshot(t, fx, 0, "", nil, evidence, findings)
	internal := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, `query { report(inspectionId:"`+fx.seed.InspectionID.String()+`") { findings { id } } }`, fx.ctx, "", "")
	var internalPayload struct {
		Data struct {
			Report struct {
				Findings []struct {
					ID string `json:"id"`
				} `json:"findings"`
			} `json:"report"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(internal), &internalPayload); err != nil || len(internalPayload.Data.Report.Findings) != 200 {
		t.Fatalf("snapshot has %d findings, want 200: %s (%v)", len(internalPayload.Data.Report.Findings), internal, err)
	}
	if response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Query(fx, "publishReport", "vv-it-271-publish", id, 0), fx.ctx, "", ""); strings.Contains(response, `"errors"`) {
		t.Fatalf("publish: %s", response)
	}
	query := `query { customerEvidence(inspectionId:"` + fx.seed.InspectionID.String() + `",first:100) { nodes { id description } pageInfo { endCursor hasNextPage } } }`
	seen := map[string]bool{}
	var cursor string
	for page := 0; page < 2; page++ {
		q := query
		if cursor != "" {
			q = strings.Replace(query, `) { nodes`, `,after:"`+cursor+`") { nodes`, 1)
		}
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, q, fx.customer, "", "")
		var payload struct {
			Data struct {
				Evidence struct {
					Nodes []struct {
						ID string `json:"id"`
					} `json:"nodes"`
					PageInfo struct {
						EndCursor string `json:"endCursor"`
						HasNext   bool   `json:"hasNextPage"`
					} `json:"pageInfo"`
				} `json:"customerEvidence"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(response), &payload); err != nil {
			t.Fatal(err)
		}
		for _, node := range payload.Data.Evidence.Nodes {
			seen[node.ID] = true
		}
		cursor = payload.Data.Evidence.PageInfo.EndCursor
		if payload.Data.Evidence.PageInfo.HasNext != (page == 0) {
			t.Fatalf("page %d pageInfo=%+v", page, payload.Data.Evidence.PageInfo)
		}
	}
	if len(seen) != 200 {
		t.Fatalf("customer pagination reached %d unique findings, want 200", len(seen))
	}
}

func TestVV_IT_272ViewerCannotPublish(t *testing.T) {
	fx := newAPI11Fixture(t)
	viewer := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: fx.seed.TenantID, Roles: []string{auth.Viewer}}, CorrelationID: "vv-it-272"})
	assertAPI11Rejected(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Query(fx, "publishReport", "vv-it-272", fx.snapshotID, 0), viewer, "", ""), "FORBIDDEN")
	if p := api12CurrentPublication(t, fx); p.Status != "PUBLISHED" || p.Version != 1 {
		t.Fatalf("viewer mutated publication: %+v", p)
	}
}

func TestVV_IT_273PolicyRaceAuditsSnapshotPolicyVersion(t *testing.T) {
	fx := newAPI11Fixture(t)
	id := createVVPublicationSnapshot(t, fx, 0, "", nil, nil, nil)
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan string, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		r := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Query(fx, "configurePublicationPolicy", "vv-it-273-policy", identity.ID{}, 0), fx.ctx, "", "")
		if strings.Contains(r, `"errors"`) {
			errs <- r
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		r := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Query(fx, "publishReport", "vv-it-273-publish", id, 0), fx.ctx, "", "")
		if strings.Contains(r, `"errors"`) {
			errs <- r
		}
	}()
	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("racing operation failed: %s", e)
	}
	var snap database.ReportSnapshot
	if err := fx.h.AdminDB.First(&snap, "id=?", id).Error; err != nil {
		t.Fatal(err)
	}
	var pub database.ReportPublication
	if err := fx.h.AdminDB.Where("tenant_id=? AND snapshot_id=?", fx.seed.TenantID, id).Take(&pub).Error; err != nil {
		t.Fatal(err)
	}
	var policy database.PublicationPolicy
	if err := fx.h.AdminDB.Where("tenant_id=?", fx.seed.TenantID).Take(&policy).Error; err != nil {
		t.Fatal(err)
	}
	if pub.PolicyVersion != snap.PublicationPolicyVersion || pub.PolicyVersion != 0 || policy.Mode != "MANUAL" || policy.Version != 1 {
		t.Fatalf("incoherent committed policy snapshot=%d publication=%d policy=%+v", snap.PublicationPolicyVersion, pub.PolicyVersion, policy)
	}
}

func TestVV_IT_274LostPublishResponseCanBeReconciled(t *testing.T) {
	fx := newAPI11Fixture(t)
	if err := fx.h.AdminDB.Where("tenant_id=? AND snapshot_id=?", fx.seed.TenantID, fx.snapshotID).Delete(&database.ReportPublication{}).Error; err != nil {
		t.Fatal(err)
	}
	_ = executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Query(fx, "publishReport", "vv-it-274-lost", fx.snapshotID, 0), fx.ctx, "", "") // simulate dropped response
	assertVVCustomerReport(t, fx, fx.snapshotID.String())
}

func TestVV_IT_275ReplayPublishKeepsOneReleaseRecord(t *testing.T) {
	fx := newAPI11Fixture(t)
	if err := fx.h.AdminDB.Where("tenant_id=? AND snapshot_id=?", fx.seed.TenantID, fx.snapshotID).Delete(&database.ReportPublication{}).Error; err != nil {
		t.Fatal(err)
	}
	q := api12Query(fx, "publishReport", "vv-it-275-k19", fx.snapshotID, 0)
	first := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, q, fx.ctx, "", "")
	second := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, q, fx.ctx, "", "")
	if strings.Contains(first, `"errors"`) || strings.Contains(second, `"errors"`) || api12PublicationID(t, first) != api12PublicationID(t, second) {
		t.Fatalf("replay changed release: first=%s second=%s", first, second)
	}
	var n int64
	if err := fx.h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND snapshot_id=? AND client_mutation_id=?", fx.seed.TenantID, fx.snapshotID, "vv-it-275-k19").Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("release count=%d", n)
	}
}

func TestVV_IT_277InvalidatedReportIsNoLongerCurrentForCustomer(t *testing.T) {
	fx := newAPI11Fixture(t)
	pub := api12CurrentPublication(t, fx)
	r := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12InvalidateQuery(pub.ID, pub.Version, "vv-it-277-invalidate"), fx.ctx, "", "")
	if strings.Contains(r, `"errors"`) {
		t.Fatalf("invalidate: %s", r)
	}
	assertVVCustomerReport(t, fx, "null")
}

func TestVV_IT_278OnlyAutomaticTenantIsAutomaticallyVisible(t *testing.T) {
	a := newAPI11Fixture(t)
	if err := a.h.AdminDB.Where("tenant_id=? AND snapshot_id=?", a.seed.TenantID, a.snapshotID).Delete(&database.ReportPublication{}).Error; err != nil {
		t.Fatal(err)
	}
	assertVVCustomerReport(t, a, "null")
	b := newAPI11FixtureWithHarness(t, a.h)
	retireVVCurrentPublication(t, b, "vv-it-278-retire-b")
	configureVVPolicy(t, b, "AUTOMATIC", 0, "vv-it-278-policy")
	id := createVVPublicationSnapshot(t, b, 1, "AUTOMATIC", nil, nil, nil)
	assertVVCustomerReport(t, b, id.String())
	assertVVCustomerReport(t, a, "null")
}

func retireVVCurrentPublication(t *testing.T, fx api11Fixture, key string) {
	t.Helper()
	publication := api12CurrentPublication(t, fx)
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12InvalidateQuery(publication.ID, publication.Version, key), fx.ctx, "", "")
	if strings.Contains(response, `"errors"`) {
		t.Fatalf("invalidate prior publication: %s", response)
	}
}

func configureVVPolicy(t *testing.T, fx api11Fixture, mode string, version int, key string) {
	t.Helper()
	q := strings.Replace(api12Query(fx, "configurePublicationPolicy", key, identity.ID{}, version), `mode:"MANUAL"`, `mode:"`+mode+`"`, 1)
	r := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, q, fx.ctx, "", "")
	if strings.Contains(r, `"errors"`) {
		t.Fatalf("configure %s policy: %s", mode, r)
	}
}

func createVVPublicationSnapshot(t *testing.T, fx api11Fixture, policyVersion int64, publicationMode string, requirements []reportcore.Requirement, evidence []reportcore.Evidence, findings []reportcore.Finding) identity.ID {
	t.Helper()
	now := time.Now().UTC()
	created, err := generate_snapshot.Create(context.Background(), fx.h.AdminDB, generate_snapshot.Input{TenantID: fx.seed.TenantID, InspectionID: fx.seed.InspectionID, Mode: "CONSOLIDATED", Classification: "ATTENTION", TemplateVersionID: "template-v1", ReferenceVersionID: "current-only", PromptDigest: "vv-publication-v1", PublicationPolicyVersion: policyVersion, PublicationMode: publicationMode, Requirements: requirements, Evidence: evidence, Findings: findings, Context: reportcore.Context{Asset: reportcore.AssetContext{ID: fx.seed.AssetID.String(), Name: "QA", Address: "QA"}, Participant: reportcore.ParticipantContext{ID: fx.seed.ParticipantID.String(), Name: "QA"}, Template: reportcore.TemplateContext{ID: "template-v1", Name: "QA", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: now.Format(time.RFC3339Nano)}}}, now)
	if err != nil {
		t.Fatal(err)
	}
	return created.SnapshotID
}

func assertVVCustomerReport(t *testing.T, fx api11Fixture, want string) {
	t.Helper()
	r := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, `query { customerReport(inspectionId:"`+fx.seed.InspectionID.String()+`") { snapshotId status } }`, fx.customer, "", "")
	if strings.Contains(r, `"errors"`) {
		t.Fatalf("customerReport: %s", r)
	}
	if want == "null" && !strings.Contains(r, `"customerReport":null`) {
		t.Fatalf("customer report should be inaccessible: %s", r)
	}
	if want != "null" && !strings.Contains(r, `"snapshotId":"`+want+`"`) {
		t.Fatalf("customer report should show %s: %s", want, r)
	}
}
