//go:build integration

package resolvers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/features/reports/publication"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/sensitivecontent"

	"gorm.io/gorm"
)

var pubSnapshotSequence atomic.Int64

// pubSnapshot appends a final report version; the unique prompt digest keeps
// the immutable-snapshot replay rule from collapsing consecutive versions.
func pubSnapshot(t *testing.T, fx api11Fixture, classification, mode string, policyVersion int64, requirements []reportcore.Requirement, evidence []reportcore.Evidence) generate_snapshot.Result {
	t.Helper()
	now := time.Now().UTC()
	created, err := generate_snapshot.Create(context.Background(), fx.h.AdminDB, generate_snapshot.Input{TenantID: fx.seed.TenantID, InspectionID: fx.seed.InspectionID, Mode: "CONSOLIDATED", Classification: classification, TemplateVersionID: "template-v1", ReferenceVersionID: "current-only", PromptDigest: fmt.Sprintf("pub-%d", pubSnapshotSequence.Add(1)), PublicationPolicyVersion: policyVersion, PublicationMode: mode, Requirements: requirements, Evidence: evidence, ActorID: fx.seed.IdentityID, Context: reportcore.Context{Asset: reportcore.AssetContext{ID: fx.seed.AssetID.String(), Name: "Imóvel QA", Address: "QA"}, Participant: reportcore.ParticipantContext{ID: fx.seed.ParticipantID.String(), Name: "QA"}, Template: reportcore.TemplateContext{ID: "template-v1", Name: "QA", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: now.Format(time.RFC3339Nano)}}}, now)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func pubPublishQuery(fx api11Fixture, snapshotID identity.ID, key string) string {
	return api12Query(fx, "publishReport", key, snapshotID, 0)
}

func pubMustSucceed(t *testing.T, response string) {
	t.Helper()
	if strings.Contains(response, `"errors"`) {
		t.Fatalf("unexpected GraphQL error: %s", response)
	}
}

func pubOutbox(t *testing.T, fx api11Fixture, eventType string) []database.OutboxIntent {
	t.Helper()
	var rows []database.OutboxIntent
	if err := fx.h.AdminDB.Where("tenant_id=? AND type=?", fx.seed.TenantID, eventType).Order("created_at").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func pubAudit(t *testing.T, fx api11Fixture, action string) []database.AuditEvent {
	t.Helper()
	var rows []database.AuditEvent
	if err := fx.h.AdminDB.Where("tenant_id=? AND action=?", fx.seed.TenantID, action).Order("occurred_at").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func pubPublicationOf(t *testing.T, fx api11Fixture, snapshotID identity.ID) database.ReportPublication {
	t.Helper()
	var row database.ReportPublication
	if err := fx.h.AdminDB.Where("tenant_id=? AND snapshot_id=?", fx.seed.TenantID, snapshotID).Take(&row).Error; err != nil {
		t.Fatalf("publication of %s: %v", snapshotID, err)
	}
	return row
}

// TestPub_IT208AutomaticFinalizationIsAtomicForEveryClassification covers
// UT-027 and IT-208: AUTOMATIC publishes CRITICAL and ATTENTION (INCONCLUSIVE)
// reports in the snapshot transaction with a deterministic key, audit and event.
func TestPub_IT208AutomaticFinalizationIsAtomicForEveryClassification(t *testing.T) {
	fx := newAPI11Fixture(t)
	retireVVCurrentPublication(t, fx, "pub-it-208-retire")
	for _, classification := range []string{"CRITICAL", "ATTENTION"} {
		created := pubSnapshot(t, fx, classification, "AUTOMATIC", 7, nil, nil)
		row := pubPublicationOf(t, fx, created.SnapshotID)
		if created.PublicationID != row.ID || row.Status != "PUBLISHED" || row.PolicyVersion != 7 || row.ClientMutationID != generate_snapshot.AutomaticMutationID(created.SnapshotID) {
			t.Fatalf("%s automatic publication = %+v result=%+v", classification, row, created)
		}
		var snapshot database.ReportSnapshot
		if err := fx.h.AdminDB.First(&snapshot, "id=?", created.SnapshotID).Error; err != nil || snapshot.PublicationPolicyVersion != 7 {
			t.Fatalf("snapshot policy version = %+v (%v)", snapshot, err)
		}
		assertVVCustomerReport(t, fx, created.SnapshotID.String())
	}
	// The second release superseded the first in the same transaction.
	var published int64
	if err := fx.h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND inspection_id=? AND status='PUBLISHED'", fx.seed.TenantID, fx.seed.InspectionID).Count(&published).Error; err != nil || published != 1 {
		t.Fatalf("active publications = %d (%v), want 1", published, err)
	}
	if events := pubOutbox(t, fx, "report.published.v1"); len(events) != 2 {
		t.Fatalf("report.published.v1 events = %d, want 2", len(events))
	}
	if audits := pubAudit(t, fx, "report.published"); len(audits) != 2 {
		t.Fatalf("published audit records = %d, want 2", len(audits))
	}
	if audits := pubAudit(t, fx, "report.publication_superseded"); len(audits) != 1 {
		t.Fatalf("superseded audit records = %d, want 1", len(audits))
	}
}

// TestPub_IT209ManualFinalizationRecordsPolicyVersionWithoutPublishing covers UT-028 and IT-209.
func TestPub_IT209ManualFinalizationRecordsPolicyVersionWithoutPublishing(t *testing.T) {
	fx := newAPI11Fixture(t)
	retireVVCurrentPublication(t, fx, "pub-it-209-retire")
	created := pubSnapshot(t, fx, "CRITICAL", "MANUAL", 3, nil, nil)
	var snapshot database.ReportSnapshot
	if err := fx.h.AdminDB.First(&snapshot, "id=?", created.SnapshotID).Error; err != nil || snapshot.PublicationPolicyVersion != 3 {
		t.Fatalf("manual snapshot policy version = %+v (%v)", snapshot, err)
	}
	var count int64
	if err := fx.h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND snapshot_id=?", fx.seed.TenantID, created.SnapshotID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("manual finalization published %d times (%v)", count, err)
	}
	assertVVCustomerReport(t, fx, "null")
}

// TestPub_IT210ToIT213PublishLifecycle covers UT-029..031 and IT-210..213:
// one event per release, idempotent retries, non-final rejection and supersession.
func TestPub_IT210ToIT213PublishLifecycle(t *testing.T) {
	fx := newAPI11Fixture(t)
	second := pubSnapshot(t, fx, "NORMAL", "", 0, nil, nil)
	query := pubPublishQuery(fx, second.SnapshotID, "pub-it-210-publish")
	first := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
	pubMustSucceed(t, first)
	if !strings.Contains(first, `"clientMutationId":"pub-it-210-publish"`) {
		t.Fatalf("mutation id not echoed: %s", first)
	}
	replay := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
	pubMustSucceed(t, replay)
	if api12PublicationID(t, first) != api12PublicationID(t, replay) {
		t.Fatalf("replay changed publication: %s vs %s", first, replay)
	}
	// A different key for the same released snapshot keeps the one release.
	other := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, pubPublishQuery(fx, second.SnapshotID, "pub-it-212-other-key"), fx.ctx, "", "")
	pubMustSucceed(t, other)
	if api12PublicationID(t, first) != api12PublicationID(t, other) {
		t.Fatalf("second key created another release: %s", other)
	}
	events := pubOutbox(t, fx, "report.published.v1")
	if len(events) != 1 {
		t.Fatalf("report.published.v1 events = %d, want 1 (IT-212)", len(events))
	}
	var envelope struct {
		Payload map[string]string `json:"payload"`
	}
	if err := json.Unmarshal(events[0].Payload, &envelope); err != nil || envelope.Payload["snapshotId"] != second.SnapshotID.String() || len(envelope.Payload) != 3 {
		t.Fatalf("event payload must carry only identifiers: %s (%v)", events[0].Payload, err)
	}
	// IT-213: RPT1 (fixture) was superseded and both audit records survive.
	previous := pubPublicationOf(t, fx, fx.snapshotID)
	current := pubPublicationOf(t, fx, second.SnapshotID)
	if previous.Status != "SUPERSEDED" || previous.SupersededBy == nil || *previous.SupersededBy != current.ID || current.Status != "PUBLISHED" {
		t.Fatalf("supersession previous=%+v current=%+v", previous, current)
	}
	if len(pubAudit(t, fx, "report.published")) != 1 || len(pubAudit(t, fx, "report.publication_superseded")) != 1 {
		t.Fatal("publish and supersede audit records must both be kept")
	}
	// UT-029/IT-211: a non-final snapshot or a foreign snapshot is rejected.
	service := publication.Service{DB: fx.h.DB}
	if _, err := service.Publish(context.Background(), publication.PublishInput{TenantID: fx.seed.TenantID, InspectionID: fx.seed.InspectionID, SnapshotID: second.SnapshotID, ActorID: fx.seed.IdentityID, ClientMutationID: "pub-it-211-nonfinal", Final: false}); err != nil {
		// The snapshot is already released, so the idempotent path wins; use a new one.
		t.Fatalf("already released snapshot must stay idempotent: %v", err)
	}
	third := pubSnapshot(t, fx, "NORMAL", "", 0, nil, nil)
	if _, err := service.Publish(context.Background(), publication.PublishInput{TenantID: fx.seed.TenantID, InspectionID: fx.seed.InspectionID, SnapshotID: third.SnapshotID, ActorID: fx.seed.IdentityID, ClientMutationID: "pub-it-211-draft", Final: false}); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("non-final publish error = %v, want INVALID_STATE", err)
	}
	if _, err := service.Publish(context.Background(), publication.PublishInput{TenantID: fx.seed.TenantID, InspectionID: fx.seed.InspectionID, SnapshotID: identity.NewID(), ActorID: fx.seed.IdentityID, ClientMutationID: "pub-it-211-missing", Final: true}); !apperror.Is(err, apperror.NotFound) {
		t.Fatalf("missing snapshot error = %v, want NOT_FOUND", err)
	}
	if len(pubOutbox(t, fx, "report.published.v1")) != 1 {
		t.Fatal("rejected publications emitted events")
	}
}

// TestPub_IT504PreviousEligibleSnapshotMayReplaceTheLatest documents the API12
// decision: publication eligibility does not require the newest snapshot, while
// a superseded snapshot is never republished.
func TestPub_IT504PreviousEligibleSnapshotMayReplaceTheLatest(t *testing.T) {
	fx := newAPI11Fixture(t)
	older := pubSnapshot(t, fx, "NORMAL", "", 0, nil, nil)
	newer := pubSnapshot(t, fx, "ATTENTION", "", 0, nil, nil)
	pubMustSucceed(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, pubPublishQuery(fx, newer.SnapshotID, "pub-it-504-newer"), fx.ctx, "", ""))
	pubMustSucceed(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, pubPublishQuery(fx, older.SnapshotID, "pub-it-504-older"), fx.ctx, "", ""))
	if status := pubPublicationOf(t, fx, newer.SnapshotID).Status; status != "SUPERSEDED" {
		t.Fatalf("newer publication status = %s, want SUPERSEDED", status)
	}
	assertVVCustomerReport(t, fx, older.SnapshotID.String())
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, pubPublishQuery(fx, newer.SnapshotID, "pub-it-504-republish"), fx.ctx, "", "")
	assertAPI11Rejected(t, response, "INVALID_STATE")
}

// TestPub_IT214ToIT216InvalidationIsAuditedAndIdempotent covers UT-032 and IT-214..216.
func TestPub_IT214ToIT216InvalidationIsAuditedAndIdempotent(t *testing.T) {
	fx := newAPI11Fixture(t)
	current := api12CurrentPublication(t, fx)
	blank := strings.Replace(api12InvalidateQuery(current.ID, current.Version, "pub-it-215-blank"), `reason:"Correção"`, `reason:"   "`, 1)
	assertAPI11Rejected(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, blank, fx.ctx, "", ""), "INVALID_INPUT")
	assertAPI11Rejected(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12InvalidateQuery(current.ID, current.Version+5, "pub-it-215-stale"), fx.ctx, "", ""), "CONFLICT")
	if status := api12CurrentPublication(t, fx).Status; status != "PUBLISHED" {
		t.Fatalf("rejected invalidations changed the status to %s", status)
	}
	if len(pubOutbox(t, fx, "report.publication_invalidated.v1")) != 0 {
		t.Fatal("rejected invalidation emitted an event")
	}
	query := api12InvalidateQuery(current.ID, current.Version, "pub-it-214-invalidate")
	first := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
	pubMustSucceed(t, first)
	row := api12CurrentPublication(t, fx)
	if row.Status != "INVALIDATED" || row.InvalidatedAt == nil || row.InvalidatedBy == nil || *row.InvalidatedBy != fx.seed.IdentityID || row.Reason != "Correção" || row.Version != current.Version+1 {
		t.Fatalf("invalidated row = %+v", row)
	}
	// IT-216: repeating returns the current state without another event or audit.
	second := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
	pubMustSucceed(t, second)
	if !strings.Contains(second, `"status":"INVALIDATED"`) {
		t.Fatalf("repeated invalidation = %s", second)
	}
	if events := pubOutbox(t, fx, "report.publication_invalidated.v1"); len(events) != 1 {
		t.Fatalf("invalidation events = %d, want 1", len(events))
	} else if strings.Contains(string(events[0].Payload), "Correção") {
		t.Fatalf("invalidation reason leaked into the event: %s", events[0].Payload)
	}
	audits := pubAudit(t, fx, "report.publication_invalidated")
	if len(audits) != 1 || audits[0].Reason != "Correção" || audits[0].ActorID != fx.seed.IdentityID {
		t.Fatalf("invalidation audit = %+v", audits)
	}
	// An invalidated release can never be published again (IT-269).
	assertAPI11Rejected(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, pubPublishQuery(fx, fx.snapshotID, "pub-it-216-republish"), fx.ctx, "", ""), "INVALID_STATE")
}

// TestPub_IT263ReportsExposePublicationStatus covers VV4/IT-263: report and
// reports expose the release status, so history labels an invalidated RPT1
// instead of replacing its frames when RPT2 exists.
func TestPub_IT263ReportsExposePublicationStatus(t *testing.T) {
	fx := newAPI11Fixture(t)
	published := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, `query { reports(first:25) { nodes { inspectionId version publication { id status invalidatedAt } } } report(inspectionId:"`+fx.seed.InspectionID.String()+`") { version publication { snapshotId status invalidatedAt } } }`, fx.ctx, "", "")
	pubMustSucceed(t, published)
	if !strings.Contains(published, `"publication":{"snapshotId":"`+fx.snapshotID.String()+`","status":"PUBLISHED","invalidatedAt":null}`) {
		t.Fatalf("report.publication = %s", published)
	}
	retireVVCurrentPublication(t, fx, "pub-it-263-invalidate-rpt1")
	rpt2 := pubSnapshot(t, fx, "NORMAL", "", 0, nil, nil)
	pubMustSucceed(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, pubPublishQuery(fx, rpt2.SnapshotID, "pub-it-263-publish-rpt2"), fx.ctx, "", ""))
	history := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, `query { report(inspectionId:"`+fx.seed.InspectionID.String()+`", version:1) { version publication { status invalidatedAt } } }`, fx.ctx, "", "")
	pubMustSucceed(t, history)
	var payload struct {
		Data struct {
			Report struct {
				Version     int `json:"version"`
				Publication struct {
					Status        string  `json:"status"`
					InvalidatedAt *string `json:"invalidatedAt"`
				} `json:"publication"`
			} `json:"report"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(history), &payload); err != nil || payload.Data.Report.Version != 1 || payload.Data.Report.Publication.Status != "INVALIDATED" || payload.Data.Report.Publication.InvalidatedAt == nil {
		t.Fatalf("history of RPT1 = %s (%v)", history, err)
	}
	latest := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, `query { reports(first:25) { nodes { version publication { status } } } }`, fx.ctx, "", "")
	if !strings.Contains(latest, `"version":2`) || !strings.Contains(latest, `"publication":{"status":"PUBLISHED"}`) {
		t.Fatalf("reports list should show RPT2 published: %s", latest)
	}
	// A never released version has no publication.
	unreleased := pubSnapshot(t, fx, "NORMAL", "", 0, nil, nil)
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, `query { report(inspectionId:"`+fx.seed.InspectionID.String()+`", version:3) { publication { id } } }`, fx.ctx, "", "")
	if !strings.Contains(response, `"publication":null`) {
		t.Fatalf("unreleased snapshot %s should have null publication: %s", unreleased.SnapshotID, response)
	}
}

// TestPub_IT190ToIT191CustomerReportVersions covers IT-190/191, UT-037 and the
// historical label: superseded versions keep their safe body, invalidated ones
// expose status metadata only, never-released ones are null.
func TestPub_IT190ToIT191CustomerReportVersions(t *testing.T) {
	fx := newAPI11Fixture(t)
	rpt2 := pubSnapshot(t, fx, "ATTENTION", "", 0, []reportcore.Requirement{{Key: "kitchen", Section: "Cozinha", Label: "Cozinha", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "COMPLETED"}}, nil)
	pubMustSucceed(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, pubPublishQuery(fx, rpt2.SnapshotID, "pub-it-190-publish"), fx.ctx, "", ""))
	rpt3 := pubSnapshot(t, fx, "NORMAL", "", 0, nil, nil) // manual, unpublished
	query := func(version int) string {
		return `query { customerReport(inspectionId:"` + fx.seed.InspectionID.String() + `", version:` + fmt.Sprint(version) + `) { snapshotId version status historical classification advisory context { asset { name } } requirements { key } } }`
	}
	type reportPayload struct {
		Data struct {
			Report *struct {
				SnapshotID     string `json:"snapshotId"`
				Version        int    `json:"version"`
				Status         string `json:"status"`
				Historical     bool   `json:"historical"`
				Classification string `json:"classification"`
				Advisory       string `json:"advisory"`
				Context        struct {
					Asset struct {
						Name string `json:"name"`
					} `json:"asset"`
				} `json:"context"`
				Requirements []struct {
					Key string `json:"key"`
				} `json:"requirements"`
			} `json:"customerReport"`
		} `json:"data"`
	}
	decode := func(response string) reportPayload {
		t.Helper()
		pubMustSucceed(t, response)
		var payload reportPayload
		if err := json.Unmarshal([]byte(response), &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}
	current := decode(executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query(2), fx.customer, "", ""))
	if current.Data.Report == nil || current.Data.Report.Status != "PUBLISHED" || current.Data.Report.Historical || len(current.Data.Report.Requirements) != 1 {
		t.Fatalf("current version = %+v", current.Data.Report)
	}
	historical := decode(executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query(1), fx.customer, "", ""))
	if historical.Data.Report == nil || historical.Data.Report.Status != "SUPERSEDED" || !historical.Data.Report.Historical || historical.Data.Report.SnapshotID != fx.snapshotID.String() || historical.Data.Report.Classification != "ATTENTION" || historical.Data.Report.Context.Asset.Name == "" {
		t.Fatalf("superseded version should keep its safe body: %+v", historical.Data.Report)
	}
	if unpublished := decode(executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query(3), fx.customer, "", "")); unpublished.Data.Report != nil {
		t.Fatalf("unpublished %s must be null: %+v", rpt3.SnapshotID, unpublished.Data.Report)
	}
	// Invalidate the current release: explicit version returns metadata only,
	// the current view and evidence disappear.
	current2 := pubPublicationOf(t, fx, rpt2.SnapshotID)
	pubMustSucceed(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12InvalidateQuery(current2.ID, current2.Version, "pub-it-191-invalidate"), fx.ctx, "", ""))
	metadata := decode(executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query(2), fx.customer, "", ""))
	if metadata.Data.Report == nil || metadata.Data.Report.Status != "INVALIDATED" || !metadata.Data.Report.Historical || metadata.Data.Report.Classification != "" || metadata.Data.Report.Advisory != "" || len(metadata.Data.Report.Requirements) != 0 || metadata.Data.Report.Context.Asset.Name != "" {
		t.Fatalf("invalidated version must expose metadata only: %+v", metadata.Data.Report)
	}
	if after := decode(executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, `query { customerReport(inspectionId:"`+fx.seed.InspectionID.String()+`") { snapshotId status } }`, fx.customer, "", "")); after.Data.Report != nil {
		t.Fatalf("no current report remains after invalidation: %+v", after.Data.Report)
	}
	evidence := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, `query { customerEvidence(inspectionId:"`+fx.seed.InspectionID.String()+`", mode:ADVANCED) { nodes { id url } } }`, fx.customer, "", "")
	pubMustSucceed(t, evidence)
	if !strings.Contains(evidence, `"nodes":[]`) {
		t.Fatalf("invalidated report must expose no evidence: %s", evidence)
	}
	// Out-of-scope customers get no signal at all (IT-194).
	outside := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: fx.seed.TenantID, Roles: []string{auth.CustomerViewer}, Scopes: []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: identity.NewID()}}, ProductEntitlements: auth.ProductsForRole(auth.CustomerViewer)}})
	assertAPI11Rejected(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query(1), outside, "", ""), "NOT_FOUND")
}

type pubEvidenceNode struct {
	ID                string  `json:"id"`
	State             string  `json:"state"`
	LineageID         string  `json:"lineageId"`
	ReplacedBy        *string `json:"replacedBy"`
	MediaAvailability string  `json:"mediaAvailability"`
	URL               *string `json:"url"`
}

func pubCustomerEvidence(t *testing.T, fx api11Fixture, mode string) []pubEvidenceNode {
	t.Helper()
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, `query { customerEvidence(inspectionId:"`+fx.seed.InspectionID.String()+`", mode:`+mode+`, first:100) { nodes { id state lineageId replacedBy mediaAvailability url } } }`, fx.customer, "", "")
	pubMustSucceed(t, response)
	var payload struct {
		Data struct {
			Evidence struct {
				Nodes []pubEvidenceNode `json:"nodes"`
			} `json:"customerEvidence"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Data.Evidence.Nodes
}

// TestPub_IT192CustomerEvidenceSimpleAndAdvancedLineage covers UT-033/034/037
// and IT-192: SIMPLE shows current items only; ADVANCED adds replaced and
// discarded lineage as historical metadata without a media URL. Lineage comes
// only from versions that were released to customers (active or superseded):
// a never-published or an invalidated version never leaks through it (M5).
func TestPub_IT192CustomerEvidenceSimpleAndAdvancedLineage(t *testing.T) {
	fx := newAPI11Fixture(t)
	replacedID, discardedID, currentID := identity.NewID().String(), identity.NewID().String(), identity.NewID().String()
	ghostID, withdrawnID := identity.NewID().String(), identity.NewID().String()
	digest := strings.Repeat("d", 64)
	publish := func(snapshot identity.ID, key string) {
		t.Helper()
		pubMustSucceed(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, pubPublishQuery(fx, snapshot, key), fx.ctx, "", ""))
	}
	rpt2 := pubSnapshot(t, fx, "ATTENTION", "", 0, nil, []reportcore.Evidence{
		{ID: replacedID, RequirementKey: "sala", Role: "CURRENT", Availability: "AVAILABLE", DisplayDigest: digest},
		{ID: discardedID, RequirementKey: "quarto", Role: "CURRENT", Availability: "AVAILABLE"},
	})
	publish(rpt2.SnapshotID, "pub-it-192-publish-rpt2") // supersedes the fixture's RPT1
	// A version that was never released and one that was withdrawn.
	pubSnapshot(t, fx, "ATTENTION", "", 0, nil, []reportcore.Evidence{{ID: ghostID, RequirementKey: "cozinha", Role: "CURRENT", Availability: "AVAILABLE"}})
	withdrawn := pubSnapshot(t, fx, "ATTENTION", "", 0, nil, []reportcore.Evidence{{ID: withdrawnID, RequirementKey: "banheiro", Role: "CURRENT", Availability: "AVAILABLE"}})
	publish(withdrawn.SnapshotID, "pub-it-192-publish-withdrawn")
	released := pubPublicationOf(t, fx, withdrawn.SnapshotID)
	pubMustSucceed(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12InvalidateQuery(released.ID, released.Version, "pub-it-192-withdraw"), fx.ctx, "", ""))
	rpt5 := pubSnapshot(t, fx, "ATTENTION", "", 0, nil, []reportcore.Evidence{{ID: currentID, RequirementKey: "sala", Role: "CURRENT", Availability: "AVAILABLE"}})
	publish(rpt5.SnapshotID, "pub-it-192-publish-current")
	simple := pubCustomerEvidence(t, fx, "SIMPLE")
	if len(simple) != 1 || simple[0].ID != currentID || simple[0].State != "CURRENT" {
		t.Fatalf("SIMPLE evidence = %+v", simple)
	}
	advanced := pubCustomerEvidence(t, fx, "ADVANCED")
	byID := map[string]pubEvidenceNode{}
	for _, node := range advanced {
		byID[node.ID] = node
	}
	// The fixture's first report also carried a frame no later version kept.
	if len(advanced) != 4 || byID[currentID].State != "CURRENT" {
		t.Fatalf("ADVANCED evidence = %+v", advanced)
	}
	for name, hidden := range map[string]string{"never published": ghostID, "invalidated": withdrawnID} {
		if _, leaked := byID[hidden]; leaked {
			t.Fatalf("ADVANCED lineage leaked the %s version: %+v", name, byID[hidden])
		}
	}
	if node := byID[replacedID]; node.State != "REPLACED" || node.ReplacedBy == nil || *node.ReplacedBy != currentID || node.LineageID != currentID || node.URL != nil {
		t.Fatalf("replaced lineage = %+v", node)
	}
	if node := byID[discardedID]; node.State != "DISCARDED" || node.ReplacedBy != nil || node.URL != nil {
		t.Fatalf("discarded lineage = %+v", node)
	}
}

// TestPub_IT193AndIT228SensitiveMediaNeverGetsURL covers UT-035/038/039,
// IT-193 and IT-228: a snapshot flag or a later BLOCKED screening blocks the
// visual at signing time, and a withdrawn release signs nothing.
func TestPub_IT193AndIT228SensitiveMediaNeverGetsURL(t *testing.T) {
	fx := newAPI11Fixture(t)
	now := time.Now().UTC()
	derivative := database.MediaDerivative{ID: identity.NewID(), TenantID: fx.seed.TenantID, MediaID: fx.seed.MediaID, ObjectKey: "pub-it/" + fx.seed.MediaID.String() + ".jpg", Kind: "DISPLAY", SHA256: strings.Repeat("e", 64), ContentType: "image/jpeg", Width: 1, Height: 1, SizeBytes: 1, CreatedAt: now}
	if err := fx.h.AdminDB.Create(&derivative).Error; err != nil {
		t.Fatal(err)
	}
	flagged := identity.NewID().String()
	created := pubSnapshot(t, fx, "ATTENTION", "", 0, nil, []reportcore.Evidence{
		{ID: fx.seed.MediaID.String(), RequirementKey: "sala", Role: "CURRENT", Availability: "AVAILABLE"},
		{ID: flagged, RequirementKey: "quarto", Role: "CURRENT", Availability: "AVAILABLE", Flags: []string{"SENSITIVE"}},
	})
	retireVVCurrentPublication(t, fx, "pub-it-193-retire")
	pubMustSucceed(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, pubPublishQuery(fx, created.SnapshotID, "pub-it-193-publish"), fx.ctx, "", ""))
	// M3: without any terminal screening decision the photo is never signed.
	for _, node := range pubCustomerEvidence(t, fx, "SIMPLE") {
		if node.ID == fx.seed.MediaID.String() && (node.MediaAvailability != "MISSING" || node.URL != nil) {
			t.Fatalf("photo without a screening decision must not be signed: %+v", node)
		}
	}
	// The decision is recorded as the screening service records it: the embedded
	// detector runs on a real image and its digest and regions are stored with
	// the CLEARED run. The media row itself is left alone because the fixture's
	// evidence is already submitted and immutable.
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewNRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	detected, err := sensitivecontent.NewEmbeddedDetector().Detect(context.Background(), pngBytes.Bytes())
	if err != nil || len(detected.Regions) != 0 {
		t.Fatalf("detector on a blank image = %+v (%v), want no regions", detected, err)
	}
	regions, _ := json.Marshal(detected.Regions)
	if err := fx.h.AdminDB.Create(&database.ScreeningRun{ID: identity.NewID(), TenantID: fx.seed.TenantID, MediaID: fx.seed.MediaID, Status: "CLEARED", ModelDigest: detected.ModelDigest, Regions: regions, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	nodes := map[string]pubEvidenceNode{}
	for _, node := range pubCustomerEvidence(t, fx, "SIMPLE") {
		nodes[node.ID] = node
	}
	if node := nodes[fx.seed.MediaID.String()]; node.MediaAvailability != "AVAILABLE" || node.URL == nil || !strings.Contains(*node.URL, derivative.ObjectKey) {
		t.Fatalf("visible photo must receive a signed URL: %+v", node)
	}
	if node := nodes[flagged]; node.MediaAvailability != "SENSITIVE_BLOCKED" || node.URL != nil {
		t.Fatalf("flagged evidence must be blocked metadata: %+v", node)
	}
	// IT-228: the screening result changes after the report was published.
	if err := fx.h.AdminDB.Create(&database.ScreeningRun{ID: identity.NewID(), TenantID: fx.seed.TenantID, MediaID: fx.seed.MediaID, Status: "BLOCKED", ModelDigest: strings.Repeat("a", 64), Regions: json.RawMessage(`[]`), CreatedAt: now.Add(time.Second)}).Error; err != nil {
		t.Fatal(err)
	}
	for _, node := range pubCustomerEvidence(t, fx, "SIMPLE") {
		if node.ID == fx.seed.MediaID.String() && (node.MediaAvailability != "SENSITIVE_BLOCKED" || node.URL != nil) {
			t.Fatalf("media blocked after publication must not be signed: %+v", node)
		}
	}
	// A false-positive override clears the block again.
	if err := fx.h.AdminDB.Create(&database.ScreeningRun{ID: identity.NewID(), TenantID: fx.seed.TenantID, MediaID: fx.seed.MediaID, Status: "OVERRIDDEN", ModelDigest: strings.Repeat("a", 64), Regions: json.RawMessage(`[]`), CreatedAt: now.Add(2 * time.Second)}).Error; err != nil {
		t.Fatal(err)
	}
	for _, node := range pubCustomerEvidence(t, fx, "SIMPLE") {
		if node.ID == fx.seed.MediaID.String() && node.URL == nil {
			t.Fatalf("overridden screening should be visible again: %+v", node)
		}
	}
	// Revoking the release stops the next URL from being issued.
	pubMustSucceed(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12InvalidateQuery(pubPublicationOf(t, fx, created.SnapshotID).ID, 1, "pub-it-228-invalidate"), fx.ctx, "", ""))
	if nodes := pubCustomerEvidence(t, fx, "SIMPLE"); len(nodes) != 0 {
		t.Fatalf("withdrawn release exposed evidence: %+v", nodes)
	}
}

type pubTimelineNode struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	OccurredAt string `json:"occurredAt"`
	Historical bool   `json:"historical"`
}

func pubTimeline(t *testing.T, fx api11Fixture, ctx context.Context, arguments string) ([]pubTimelineNode, string) {
	t.Helper()
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, `query { customerTimeline(`+arguments+`) { nodes { id kind status occurredAt historical } pageInfo { endCursor hasNextPage } } }`, ctx, "", "")
	if strings.Contains(response, `"errors"`) {
		return nil, response
	}
	var payload struct {
		Data struct {
			Timeline struct {
				Nodes []pubTimelineNode `json:"nodes"`
			} `json:"customerTimeline"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Data.Timeline.Nodes, response
}

// TestPub_IT189CustomerTimelineOrdersByOccurrenceAndHidesSiblings covers IT-189.
func TestPub_IT189CustomerTimelineOrdersByOccurrenceAndHidesSiblings(t *testing.T) {
	fx := newAPI11Fixture(t)
	second := pubSnapshot(t, fx, "NORMAL", "", 0, nil, nil)
	pubMustSucceed(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, pubPublishQuery(fx, second.SnapshotID, "pub-it-189-publish"), fx.ctx, "", ""))
	// Late processing: the event that occurred first is recorded last.
	base := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Microsecond)
	first := pubPublicationOf(t, fx, fx.snapshotID)
	if err := fx.h.AdminDB.Model(&database.ReportPublication{}).Where("id=?", first.ID).Updates(map[string]any{"published_at": base, "updated_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND snapshot_id=?", fx.seed.TenantID, second.SnapshotID).Update("published_at", base.Add(time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	nodes, response := pubTimeline(t, fx, fx.customer, `assetId:"`+fx.seed.AssetID.String()+`"`)
	if len(nodes) != 2 || nodes[0].Status != "PUBLISHED" || nodes[0].Historical || nodes[1].Status != "SUPERSEDED" || !nodes[1].Historical || nodes[0].Kind != "REPORT_PUBLISHED" {
		t.Fatalf("timeline must order by occurredAt desc: %s", response)
	}
	if strings.Contains(response, "canonical") || strings.Contains(response, "classification") {
		t.Fatalf("timeline leaked report content: %s", response)
	}
	// Project and unfiltered reads agree; paging is stable.
	if byProject, _ := pubTimeline(t, fx, fx.customer, `projectId:"`+fx.seed.ProjectID.String()+`"`); len(byProject) != 2 {
		t.Fatalf("project timeline = %+v", byProject)
	}
	pageOne, _ := pubTimeline(t, fx, fx.customer, `first:1`)
	if len(pageOne) != 1 || pageOne[0].ID != nodes[0].ID {
		t.Fatalf("first page = %+v", pageOne)
	}
	var cursor struct {
		Data struct {
			Timeline struct {
				PageInfo struct {
					EndCursor string `json:"endCursor"`
					HasNext   bool   `json:"hasNextPage"`
				} `json:"pageInfo"`
			} `json:"customerTimeline"`
		} `json:"data"`
	}
	_, raw := pubTimeline(t, fx, fx.customer, `first:1`)
	if err := json.Unmarshal([]byte(raw), &cursor); err != nil || !cursor.Data.Timeline.PageInfo.HasNext || cursor.Data.Timeline.PageInfo.EndCursor == "" {
		t.Fatalf("page info = %s (%v)", raw, err)
	}
	pageTwo, _ := pubTimeline(t, fx, fx.customer, `first:1, after:"`+cursor.Data.Timeline.PageInfo.EndCursor+`"`)
	if len(pageTwo) != 1 || pageTwo[0].ID != nodes[1].ID {
		t.Fatalf("second page = %+v", pageTwo)
	}
	if _, response := pubTimeline(t, fx, fx.customer, `after:"not-a-cursor"`); !strings.Contains(response, `"code":"INVALID_INPUT"`) {
		t.Fatalf("malformed cursor should be INVALID_INPUT: %s", response)
	}
	// An unshared sibling asset is NOT_FOUND, and an unrelated tenant asset too.
	var sibling database.Asset
	if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.AssetID).Take(&sibling).Error; err != nil {
		t.Fatal(err)
	}
	var unit database.BusinessUnit
	if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.BusinessUnitID).Take(&unit).Error; err != nil {
		t.Fatal(err)
	}
	unit.ID, unit.Name, unit.Code = identity.NewID(), "Outra unidade", "OUTRA-"+identity.NewID().String()[:8]
	if err := fx.h.AdminDB.Create(&unit).Error; err != nil {
		t.Fatalf("create sibling business unit: %v", err)
	}
	otherUnit := unit.ID
	sibling.ID, sibling.BusinessUnitID, sibling.Name, sibling.ExternalKey = identity.NewID(), otherUnit, "Irmão", "SIBLING-"+identity.NewID().String()
	sibling.IdempotencyKey = "pub-it-189-" + sibling.ID.String()
	if err := fx.h.AdminDB.Create(&sibling).Error; err != nil {
		t.Fatalf("create sibling asset: %v", err)
	}
	if _, response := pubTimeline(t, fx, fx.customer, `assetId:"`+sibling.ID.String()+`"`); !strings.Contains(response, `"code":"NOT_FOUND"`) {
		t.Fatalf("unshared sibling asset should be NOT_FOUND: %s", response)
	}
	if _, response := pubTimeline(t, fx, fx.customer, `assetId:"`+identity.NewID().String()+`"`); !strings.Contains(response, `"code":"NOT_FOUND"`) {
		t.Fatalf("unknown asset should be NOT_FOUND: %s", response)
	}
}

// TestPub_IT229PublicationTablesAreIsolatedByRLS covers IT-229 for the policy
// and ledger tables: another tenant's rows are invisible and unwritable.
func TestPub_IT229PublicationTablesAreIsolatedByRLS(t *testing.T) {
	a := newAPI11Fixture(t)
	b := newAPI11FixtureWithHarness(t, a.h)
	configureVVPolicy(t, b, "AUTOMATIC", 0, "pub-it-229-policy-b")
	count := func(tenant identity.ID, model any, where string, args ...any) int64 {
		t.Helper()
		var n int64
		if err := a.h.WithinTenant(context.Background(), tenant, func(tx *gorm.DB) error {
			return tx.Model(model).Where(where, args...).Count(&n).Error
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(a.seed.TenantID, &database.ReportPublication{}, "tenant_id=?", b.seed.TenantID); n != 0 {
		t.Fatalf("tenant A read %d publications of tenant B", n)
	}
	if n := count(a.seed.TenantID, &database.PublicationPolicy{}, "tenant_id=?", b.seed.TenantID); n != 0 {
		t.Fatalf("tenant A read %d policies of tenant B", n)
	}
	if n := count(b.seed.TenantID, &database.ReportPublication{}, "tenant_id=?", b.seed.TenantID); n != 1 {
		t.Fatalf("tenant B cannot read its own publication (%d)", n)
	}
	now := time.Now().UTC()
	err := a.h.WithinTenant(context.Background(), a.seed.TenantID, func(tx *gorm.DB) error {
		return tx.Create(&database.ReportPublication{ID: identity.NewID(), TenantID: b.seed.TenantID, SnapshotID: identity.NewID(), InspectionID: b.seed.InspectionID, ClientMutationID: "pub-it-229-foreign", Status: "PUBLISHED", ActorID: a.seed.IdentityID, Version: 1, PublishedAt: &now, CreatedAt: now, UpdatedAt: now}).Error
	})
	if err == nil {
		t.Fatal("tenant A inserted a publication for tenant B")
	}
	err = a.h.WithinTenant(context.Background(), a.seed.TenantID, func(tx *gorm.DB) error {
		return tx.Create(&database.PublicationPolicy{ID: identity.NewID(), TenantID: b.seed.TenantID, Mode: "AUTOMATIC", Version: 9}).Error
	})
	if err == nil {
		t.Fatal("tenant A inserted a policy for tenant B")
	}
	var unscoped int64
	if err := a.h.DB.Model(&database.ReportPublication{}).Count(&unscoped).Error; err != nil || unscoped != 0 {
		t.Fatalf("runtime role without tenant context read %d publications (%v)", unscoped, err)
	}
}

// TestPub_ConcurrentPublicationsKeepOneActiveRelease races two releases of the
// same inspection: both succeed, exactly one stays PUBLISHED and the other is
// superseded instead of failing on the single-active-release index.
func TestPub_ConcurrentPublicationsKeepOneActiveRelease(t *testing.T) {
	fx := newAPI11Fixture(t)
	a := pubSnapshot(t, fx, "NORMAL", "", 0, nil, nil)
	b := pubSnapshot(t, fx, "ATTENTION", "", 0, nil, nil)
	start := make(chan struct{})
	type outcome struct{ name, response string }
	results := make(chan outcome, 3)
	run := func(name string, snapshot identity.ID, key string) {
		<-start
		results <- outcome{name, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, pubPublishQuery(fx, snapshot, key), fx.ctx, "", "")}
	}
	go run("a", a.SnapshotID, "pub-race-a")
	go run("b", b.SnapshotID, "pub-race-b")
	go run("b-retry", b.SnapshotID, "pub-race-b-retry")
	close(start)
	for i := 0; i < 3; i++ {
		result := <-results
		if strings.HasPrefix(result.name, "b") && strings.Contains(result.response, `"code":"INVALID_STATE"`) {
			// The later of the two requests for b lost the race to the release of a, which already
			// superseded b: a superseded snapshot is never republished.
			continue
		}
		pubMustSucceed(t, result.response)
	}
	var active int64
	if err := fx.h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND inspection_id=? AND status='PUBLISHED'", fx.seed.TenantID, fx.seed.InspectionID).Count(&active).Error; err != nil || active != 1 {
		t.Fatalf("active releases = %d (%v), want 1", active, err)
	}
	var total int64
	fx.h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND snapshot_id IN ?", fx.seed.TenantID, []identity.ID{a.SnapshotID, b.SnapshotID}).Count(&total)
	if total != 2 {
		t.Fatalf("release rows for the two snapshots = %d, want 2", total)
	}
}

// TestPub_ConcurrentInvalidationsEmitOneEvent races the same invalidation twice.
func TestPub_ConcurrentInvalidationsEmitOneEvent(t *testing.T) {
	fx := newAPI11Fixture(t)
	current := api12CurrentPublication(t, fx)
	start := make(chan struct{})
	results := make(chan string, 2)
	for _, key := range []string{"pub-race-inv-1", "pub-race-inv-2"} {
		go func(key string) {
			<-start
			results <- executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12InvalidateQuery(current.ID, current.Version, key), fx.ctx, "", "")
		}(key)
	}
	close(start)
	for i := 0; i < 2; i++ {
		response := <-results
		pubMustSucceed(t, response)
		if !strings.Contains(response, `"status":"INVALIDATED"`) {
			t.Fatalf("racing invalidation = %s", response)
		}
	}
	if events := pubOutbox(t, fx, "report.publication_invalidated.v1"); len(events) != 1 {
		t.Fatalf("invalidation events = %d, want 1", len(events))
	}
	if audits := pubAudit(t, fx, "report.publication_invalidated"); len(audits) != 1 {
		t.Fatalf("invalidation audits = %d, want 1", len(audits))
	}
}
