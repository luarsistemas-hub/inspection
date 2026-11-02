//go:build integration

package resolvers

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/publication"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
)

type vvHistoryReport struct {
	Data struct {
		Report *struct {
			ID          string `json:"id"`
			Version     int    `json:"version"`
			JSONDigest  string `json:"jsonDigest"`
			Publication *struct {
				SnapshotID    string  `json:"snapshotId"`
				Status        string  `json:"status"`
				InvalidatedAt *string `json:"invalidatedAt"`
			} `json:"publication"`
			Evidence struct {
				Nodes []struct {
					ID      string `json:"id"`
					FrameID string `json:"frameId"`
				} `json:"nodes"`
			} `json:"evidence"`
		} `json:"report"`
	} `json:"data"`
}

func vvReportVersion(t *testing.T, fx api11Fixture, version int) vvHistoryReport {
	t.Helper()
	query := `query { report(inspectionId:"` + fx.seed.InspectionID.String() + `", version:` + strconv.Itoa(version) + `) { id version jsonDigest publication { snapshotId status invalidatedAt } evidence(first:50) { nodes { id frameId } } } }`
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
	var out vvHistoryReport
	if err := json.Unmarshal([]byte(response), &out); err != nil || strings.Contains(response, `"errors"`) || out.Data.Report == nil {
		t.Fatalf("report version %d = %s (%v)", version, response, err)
	}
	return out
}

// IT-263: invalidating RPT1 after RPT2 exists labels RPT1 as invalidated in the
// authorized history; its snapshot, digest and frames are untouched and are not
// replaced by RPT2's.
func TestVV_IT_263InvalidatedRPT1HistoryKeepsItsFramesAfterRPT2Exists(t *testing.T) {
	fx := newAPI11Fixture(t)
	before := vvReportVersion(t, fx, 1)
	if before.Data.Report.Publication == nil || before.Data.Report.Publication.Status != "PUBLISHED" || len(before.Data.Report.Evidence.Nodes) != 1 || before.Data.Report.Evidence.Nodes[0].FrameID != fx.frameID.String() {
		t.Fatalf("RPT1 before invalidation = %+v", before.Data.Report)
	}
	rpt2 := createVVPublicationSnapshot(t, fx, 0, "", nil, nil, nil)
	publicationRow := api12CurrentPublication(t, fx)
	if response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12InvalidateQuery(publicationRow.ID, publicationRow.Version, "vv-it-263-invalidate"), fx.ctx, "", ""); strings.Contains(response, `"errors"`) || !strings.Contains(response, `"status":"INVALIDATED"`) {
		t.Fatalf("invalidate RPT1: %s", response)
	}

	after := vvReportVersion(t, fx, 1)
	history := after.Data.Report
	if history.Publication == nil || history.Publication.Status != "INVALIDATED" || history.Publication.InvalidatedAt == nil || history.Publication.SnapshotID != fx.snapshotID.String() {
		t.Fatalf("RPT1 history is not labelled invalidated: %+v", history.Publication)
	}
	if history.JSONDigest != before.Data.Report.JSONDigest || history.ID != before.Data.Report.ID {
		t.Fatalf("invalidation changed the immutable RPT1 snapshot: before=%+v after=%+v", before.Data.Report, history)
	}
	if len(history.Evidence.Nodes) != 1 || history.Evidence.Nodes[0].FrameID != fx.frameID.String() {
		t.Fatalf("RPT1 frames were replaced after invalidation: %+v", history.Evidence.Nodes)
	}

	second := vvReportVersion(t, fx, 2).Data.Report
	if second.ID != rpt2.String() || second.Version != 2 || second.Publication != nil || len(second.Evidence.Nodes) != 0 {
		t.Fatalf("RPT2 must stay a separate, unpublished snapshot without RPT1 frames: %+v", second)
	}
	assertVVCustomerReport(t, fx, "null")
	var snapshots int64
	if err := fx.h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, fx.seed.InspectionID).Count(&snapshots).Error; err != nil || snapshots != 2 {
		t.Fatalf("snapshots after invalidation = %d (%v), want RPT1 and RPT2 kept", snapshots, err)
	}
}

// IT-504 (and the eligible half of IT-269): an invalidated snapshot is
// ineligible and returns INVALID_STATE without a new release, while a completed
// partial snapshot whose only blemishes are findings and scope gaps stays
// eligible.
func TestVV_IT_504InvalidatedIsIneligibleButPartialFindingsStayEligible(t *testing.T) {
	fx := newAPI11Fixture(t)
	current := api12CurrentPublication(t, fx)
	if response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12InvalidateQuery(current.ID, current.Version, "vv-it-504-invalidate"), fx.ctx, "", ""); strings.Contains(response, `"errors"`) {
		t.Fatalf("invalidate: %s", response)
	}
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Query(fx, "publishReport", "vv-it-504-republish", fx.snapshotID, 0), fx.ctx, "", "")
	if !strings.Contains(response, `"code":"INVALID_STATE"`) {
		t.Fatalf("publishing an invalidated snapshot = %s, want INVALID_STATE", response)
	}
	var releases int64
	if err := fx.h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND snapshot_id=?", fx.seed.TenantID, fx.snapshotID).Count(&releases).Error; err != nil || releases != 1 {
		t.Fatalf("rejected publication changed the release records: %d (%v)", releases, err)
	}

	evidence := []reportcore.Evidence{{ID: identity.NewID().String(), RequirementKey: "kitchen", Role: "CURRENT", Description: "Cozinha", Availability: "MISSING"}}
	findings := []reportcore.Finding{{ID: identity.NewID().String(), Category: "CONSERVATION", Title: "Desgaste", Description: "Observação", Severity: "LOW", Confidence: 0.9, Quality: "HIGH", RecommendedAction: "Revisar", EvidenceIDs: []string{evidence[0].ID}}}
	gap := []reportcore.Requirement{{Key: "bathroom", Section: "Banheiro", Label: "Banheiro", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "PENDING", ImpossibilityReason: "Área não capturada"}}
	partial := createVVPublicationSnapshot(t, fx, 0, "", gap, evidence, findings)
	published := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Query(fx, "publishReport", "vv-it-504-partial", partial, 0), fx.ctx, "", "")
	if strings.Contains(published, `"errors"`) || !strings.Contains(published, `"status":"PUBLISHED"`) || !strings.Contains(published, `"snapshotId":"`+partial.String()+`"`) {
		t.Fatalf("a completed partial snapshot with findings must stay eligible: %s", published)
	}
}

// IT-503 and IT-276: a snapshot that is not final cannot be released
// (INVALID_STATE, Portuguese message, nothing written), and the resolver always
// asks for a final release, so a completed partial-scope snapshot published
// through GraphQL is not treated as unfinished.
func TestVV_IT_503_276UnfinishedSnapshotCannotBePublishedButPartialScopeIsFinal(t *testing.T) {
	fx := newAPI11Fixture(t)
	gap := []reportcore.Requirement{{Key: "bathroom", Section: "Banheiro", Label: "Banheiro", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "PENDING", ImpossibilityReason: "Área não capturada"}}
	snapshot := createVVPublicationSnapshot(t, fx, 0, "", gap, nil, nil)
	service := publication.Service{DB: fx.h.DB}
	_, err := service.Publish(fx.ctx, publication.PublishInput{TenantID: fx.seed.TenantID, InspectionID: fx.seed.InspectionID, SnapshotID: snapshot, ActorID: fx.seed.IdentityID, ClientMutationID: "vv-it-503-unfinished", Final: false})
	var appErr *apperror.Error
	if err == nil || !errors.As(err, &appErr) || appErr.Code != apperror.InvalidState || appErr.Safe != "Somente laudos finais podem ser publicados." {
		t.Fatalf("unfinished snapshot publication = %v, want INVALID_STATE with the Portuguese message", err)
	}
	var releases int64
	if err := fx.h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND snapshot_id=?", fx.seed.TenantID, snapshot).Count(&releases).Error; err != nil || releases != 0 {
		t.Fatalf("rejected unfinished publication wrote %d release rows (%v)", releases, err)
	}
	published := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12Query(fx, "publishReport", "vv-it-276-partial", snapshot, 0), fx.ctx, "", "")
	if strings.Contains(published, `"errors"`) || !strings.Contains(published, `"status":"PUBLISHED"`) {
		t.Fatalf("resolver must publish a completed partial-scope snapshot as final: %s", published)
	}
}
