//go:build integration

package resolvers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
)

func pubManager(fx api11Fixture, scopes ...requestctx.Scope) context.Context {
	return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: fx.seed.TenantID, Roles: []string{auth.Manager}, Scopes: scopes, ProductEntitlements: auth.ProductsForRole(auth.Manager)}, CorrelationID: "pubfix-manager"})
}

// TestPubFix_ManagerScopeGuardsPublishAndInvalidate covers M1: a MANAGER acts
// on publications only within a business unit, asset or project scope; outside
// it both mutations answer NOT_FOUND, indistinguishable from a missing target,
// and change nothing.
func TestPubFix_ManagerScopeGuardsPublishAndInvalidate(t *testing.T) {
	fx := newAPI11Fixture(t)
	outside := map[string]context.Context{
		"no scope":               pubManager(fx),
		"other business unit":    pubManager(fx, requestctx.Scope{Kind: "BUSINESS_UNIT", ID: identity.NewID()}),
		"other asset":            pubManager(fx, requestctx.Scope{Kind: "ASSET", ID: identity.NewID()}),
		"other project":          pubManager(fx, requestctx.Scope{Kind: "PROJECT", ID: identity.NewID()}),
		"other inspection grant": pubManager(fx, requestctx.Scope{Kind: "INSPECTION", ID: identity.NewID()}),
	}
	candidate := pubSnapshot(t, fx, "NORMAL", "", 0, nil, nil)
	current := api12CurrentPublication(t, fx)
	missingPublish := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, strings.Replace(pubPublishQuery(fx, candidate.SnapshotID, "pubfix-m1-missing"), fx.seed.InspectionID.String(), identity.NewID().String(), 1), fx.ctx, "", "")
	assertAPI11Rejected(t, missingPublish, "NOT_FOUND")
	missingInvalidate := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12InvalidateQuery(identity.NewID(), 1, "pubfix-m1-missing-inv"), fx.ctx, "", "")
	assertAPI11Rejected(t, missingInvalidate, "NOT_FOUND")
	for name, actor := range outside {
		t.Run(name, func(t *testing.T) {
			publish := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, pubPublishQuery(fx, candidate.SnapshotID, "pubfix-m1-publish-"+name), actor, "", "")
			assertAPI11Rejected(t, publish, "NOT_FOUND")
			if !strings.Contains(publish, "Vistoria não encontrada.") || strings.Contains(publish, candidate.SnapshotID.String()) {
				t.Fatalf("out-of-scope publish must look like a missing inspection: %s", publish)
			}
			invalidate := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12InvalidateQuery(current.ID, current.Version, "pubfix-m1-invalidate-"+name), actor, "", "")
			assertAPI11Rejected(t, invalidate, "NOT_FOUND")
			if !strings.Contains(invalidate, "Publicação não encontrada.") || !strings.Contains(missingInvalidate, "Publicação não encontrada.") {
				t.Fatalf("out-of-scope invalidate must look like a missing publication: %s", invalidate)
			}
		})
	}
	var rows int64
	if err := fx.h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND snapshot_id=?", fx.seed.TenantID, candidate.SnapshotID).Count(&rows).Error; err != nil || rows != 0 {
		t.Fatalf("out-of-scope managers published %d times (%v)", rows, err)
	}
	if after := api12CurrentPublication(t, fx); after.Status != "PUBLISHED" || after.Version != current.Version {
		t.Fatalf("out-of-scope managers changed the publication: %+v", after)
	}
	// Inside any of the three scopes the same manager succeeds.
	inside := []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: fx.seed.BusinessUnitID}, {Kind: "ASSET", ID: fx.seed.AssetID}, {Kind: "PROJECT", ID: fx.seed.ProjectID}}
	for _, scope := range inside {
		t.Run("inside "+scope.Kind, func(t *testing.T) {
			actor := pubManager(fx, scope)
			next := pubSnapshot(t, fx, "NORMAL", "", 0, nil, nil)
			pubMustSucceed(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, pubPublishQuery(fx, next.SnapshotID, "pubfix-m1-in-publish-"+scope.Kind), actor, "", ""))
			row := pubPublicationOf(t, fx, next.SnapshotID)
			pubMustSucceed(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12InvalidateQuery(row.ID, row.Version, "pubfix-m1-in-invalidate-"+scope.Kind), actor, "", ""))
		})
	}
}

// TestPubFix_PublishAndInvalidateRaceNeverRewritesAnInvalidation covers M2: a
// release racing the invalidation of its predecessor leaves a consistent
// ledger. Either the predecessor is INVALIDATED with its audit columns intact,
// or it is SUPERSEDED and the invalidation was refused; it is never SUPERSEDED
// carrying the cleared remains of an invalidation.
func TestPubFix_PublishAndInvalidateRaceNeverRewritesAnInvalidation(t *testing.T) {
	fx := newAPI11Fixture(t)
	current := api12CurrentPublication(t, fx)
	invalidatedFirst, supersededFirst := 0, 0
	for i := 0; i < 20; i++ {
		next := pubSnapshot(t, fx, "NORMAL", "", 0, nil, nil)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var invalidateResponse, publishResponse string
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			invalidateResponse = executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12InvalidateQuery(current.ID, current.Version, "pubfix-m2-inv-"+identity.NewID().String()), fx.ctx, "", "")
		}()
		go func() {
			defer wg.Done()
			<-start
			publishResponse = executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, pubPublishQuery(fx, next.SnapshotID, "pubfix-m2-pub-"+identity.NewID().String()), fx.ctx, "", "")
		}()
		close(start)
		wg.Wait()
		pubMustSucceed(t, publishResponse)
		var previous database.ReportPublication
		if err := fx.h.AdminDB.Where("id=?", current.ID).Take(&previous).Error; err != nil {
			t.Fatal(err)
		}
		switch previous.Status {
		case "INVALIDATED":
			invalidatedFirst++
			if strings.Contains(invalidateResponse, `"errors"`) || previous.InvalidatedAt == nil || previous.InvalidatedBy == nil || previous.Reason != "Correção" || previous.SupersededBy != nil {
				t.Fatalf("iteration %d: invalidated publication lost its audit: %+v / %s", i, previous, invalidateResponse)
			}
		case "SUPERSEDED":
			supersededFirst++
			if !strings.Contains(invalidateResponse, `"errors"`) || previous.InvalidatedAt != nil || previous.InvalidatedBy != nil || previous.Reason != "" || previous.SupersededBy == nil {
				t.Fatalf("iteration %d: superseded publication is inconsistent: %+v / %s", i, previous, invalidateResponse)
			}
		default:
			t.Fatalf("iteration %d: predecessor status %s", i, previous.Status)
		}
		var active []database.ReportPublication
		if err := fx.h.AdminDB.Where("tenant_id=? AND inspection_id=? AND status='PUBLISHED'", fx.seed.TenantID, fx.seed.InspectionID).Find(&active).Error; err != nil || len(active) != 1 || active[0].SnapshotID != next.SnapshotID {
			t.Fatalf("iteration %d: active publications = %+v (%v), want only the new release", i, active, err)
		}
		current = active[0]
	}
	t.Logf("race outcomes: invalidation first %d, release first %d", invalidatedFirst, supersededFirst)
}

// TestPubFix_ReusedMutationKeyForAnotherSnapshotIsConflict covers the L item:
// the key of a published snapshot cannot silently stand for a different one.
func TestPubFix_ReusedMutationKeyForAnotherSnapshotIsConflict(t *testing.T) {
	fx := newAPI11Fixture(t)
	first := pubSnapshot(t, fx, "NORMAL", "", 0, nil, nil)
	second := pubSnapshot(t, fx, "NORMAL", "", 0, nil, nil)
	pubMustSucceed(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, pubPublishQuery(fx, first.SnapshotID, "pubfix-reused-key"), fx.ctx, "", ""))
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, pubPublishQuery(fx, second.SnapshotID, "pubfix-reused-key"), fx.ctx, "", "")
	assertAPI11Rejected(t, response, "CONFLICT")
	var rows int64
	if err := fx.h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND snapshot_id=?", fx.seed.TenantID, second.SnapshotID).Count(&rows).Error; err != nil || rows != 0 {
		t.Fatalf("conflicting key published the second snapshot (%d rows, %v)", rows, err)
	}
	// The same key for the same snapshot is still an idempotent replay.
	pubMustSucceed(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, pubPublishQuery(fx, first.SnapshotID, "pubfix-reused-key"), fx.ctx, "", ""))
}

// TestPubFix_NewDatabaseEnforcesPublicationUniqueness covers M6: in a database
// created by AutoMigrate before migration 21, migration 110 supplies the two
// unique keys the ledger relies on.
func TestPubFix_NewDatabaseEnforcesPublicationUniqueness(t *testing.T) {
	fx := newAPI11Fixture(t)
	var names []string
	if err := fx.h.AdminDB.Raw("SELECT indexname FROM pg_indexes WHERE schemaname='reports' AND tablename='report_publications'").Scan(&names).Error; err != nil {
		t.Fatal(err)
	}
	have := strings.Join(names, ",")
	for _, want := range []string{"uq_report_publication_snapshot", "uq_report_publication_mutation", "idx_report_publication_active"} {
		if !strings.Contains(have, want) {
			t.Fatalf("index %s missing from reports.report_publications: %s", want, have)
		}
	}
	next := pubSnapshot(t, fx, "NORMAL", "", 0, nil, nil)
	pubMustSucceed(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, pubPublishQuery(fx, next.SnapshotID, "pubfix-unique-key"), fx.ctx, "", ""))
	released := pubPublicationOf(t, fx, next.SnapshotID)
	now := time.Now().UTC()
	duplicate := func(mutate func(*database.ReportPublication)) error {
		row := database.ReportPublication{ID: identity.NewID(), TenantID: released.TenantID, SnapshotID: released.SnapshotID, InspectionID: released.InspectionID, PolicyVersion: 1, ClientMutationID: "pubfix-unique-other", Status: "SUPERSEDED", ActorID: released.ActorID, Version: 1, PublishedAt: &now, CreatedAt: now, UpdatedAt: now}
		mutate(&row)
		return fx.h.AdminDB.Create(&row).Error
	}
	if err := duplicate(func(*database.ReportPublication) {}); err == nil {
		t.Fatal("a second publication of the same snapshot was accepted")
	}
	if err := duplicate(func(r *database.ReportPublication) {
		r.SnapshotID, r.ClientMutationID = identity.NewID(), released.ClientMutationID
	}); err == nil {
		t.Fatal("a reused client mutation key was accepted")
	}
}

// TestPubFix_TimelineKeepsOneEntryPerEvent covers the timeline item: a release
// and its withdrawal are two entries with their own identity and time; the
// release entry is not rewritten by the invalidation and paging stays stable.
func TestPubFix_TimelineKeepsOneEntryPerEvent(t *testing.T) {
	fx := newAPI11Fixture(t)
	before := api12CurrentPublication(t, fx)
	asset := `assetId:"` + fx.seed.AssetID.String() + `"`
	nodes, response := pubTimeline(t, fx, fx.customer, asset)
	if len(nodes) != 1 || nodes[0].Kind != "REPORT_PUBLISHED" || nodes[0].Historical {
		t.Fatalf("timeline before invalidation = %s", response)
	}
	publishedID, publishedAt := nodes[0].ID, nodes[0].OccurredAt
	pubMustSucceed(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api12InvalidateQuery(before.ID, before.Version, "pubfix-timeline-invalidate"), fx.ctx, "", ""))
	nodes, response = pubTimeline(t, fx, fx.customer, asset)
	if len(nodes) != 2 || nodes[0].Kind != "REPORT_INVALIDATED" || nodes[0].Status != "INVALIDATED" || !nodes[0].Historical || nodes[1].Kind != "REPORT_PUBLISHED" || !nodes[1].Historical {
		t.Fatalf("timeline after invalidation must list withdrawal then release: %s", response)
	}
	if nodes[0].ID == nodes[1].ID || nodes[1].ID != publishedID || nodes[1].OccurredAt != publishedAt {
		t.Fatalf("release entry was rewritten by the invalidation: before (%s, %s) after %+v", publishedID, publishedAt, nodes)
	}
	var row database.ReportPublication
	if err := fx.h.AdminDB.Where("id=?", before.ID).Take(&row).Error; err != nil || row.InvalidatedAt == nil {
		t.Fatalf("invalidated row = %+v (%v)", row, err)
	}
	withdrawnAt, err := time.Parse(time.RFC3339Nano, nodes[0].OccurredAt)
	if err != nil || !withdrawnAt.Equal(row.InvalidatedAt.UTC()) {
		t.Fatalf("withdrawal occurredAt = %s (%v), want %s", nodes[0].OccurredAt, err, row.InvalidatedAt.UTC())
	}
	// Paging one entry at a time visits both exactly once, in a stable order.
	var seen []string
	cursor := ""
	for page := 0; page < 3; page++ {
		arguments := `first:1`
		if cursor != "" {
			arguments += `, after:"` + cursor + `"`
		}
		pageNodes, raw := pubTimeline(t, fx, fx.customer, arguments)
		if len(pageNodes) == 0 {
			break
		}
		seen = append(seen, pageNodes[0].Kind)
		var info struct {
			Data struct {
				Timeline struct {
					PageInfo struct {
						EndCursor string `json:"endCursor"`
						HasNext   bool   `json:"hasNextPage"`
					} `json:"pageInfo"`
				} `json:"customerTimeline"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(raw), &info); err != nil {
			t.Fatal(err)
		}
		cursor = info.Data.Timeline.PageInfo.EndCursor
		if !info.Data.Timeline.PageInfo.HasNext {
			break
		}
	}
	if strings.Join(seen, ",") != "REPORT_INVALIDATED,REPORT_PUBLISHED" {
		t.Fatalf("paged timeline = %v", seen)
	}
}

// TestPubFix_BlockedVideoEvidenceExposesNoFrameID covers M4 in the GraphQL
// view: a frame blocked by SENSITIVE_DETECTION (or any block) is metadata only,
// with no URL and nothing that addresses the frame or its provenance: neither
// its identifiers, nor its position in the video, nor an id or cursor that
// embeds the frame id.
func TestPubFix_BlockedVideoEvidenceExposesNoFrameID(t *testing.T) {
	fx := newAPI11Fixture(t)
	var frame database.VideoFrame
	if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.frameID).Take(&frame).Error; err != nil {
		t.Fatal(err)
	}
	digest := frame.SHA256
	pts, ms := frame.PTSMicros, frame.PTSMicros/1000
	released := pubSnapshot(t, fx, "ATTENTION", "", 0, []reportcore.Requirement{{Key: "video:kitchen", Section: "Vídeo", Label: "Cozinha", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "COMPLETED"}}, []reportcore.Evidence{{PTSMicros: &pts, TimestampMS: &ms, SourceAvailability: "RETAINED", ID: fx.frameID.String(), RequirementKey: "video:kitchen", Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: fx.frameID.String(), SourceMediaID: fx.sourceMedia.String(), PreparationID: frame.PreparationID.String(), FrameDigest: digest, DisplayDigest: digest, Availability: "AVAILABLE", Flags: []string{"SENSITIVE_DETECTION"}}})
	pubMustSucceed(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, pubPublishQuery(fx, released.SnapshotID, "pubfix-m4-publish"), fx.ctx, "", ""))
	query := func(arguments string) string {
		return executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, `query { customerEvidence(inspectionId:"`+fx.seed.InspectionID.String()+`", first:25`+arguments+`) { nodes { id lineageId replacedBy evidenceKind frameId preparationId sourceMediaId ptsMicros timestampMs mediaAvailability url } pageInfo { endCursor hasNextPage } } }`, fx.customer, "", "")
	}
	response := query("")
	pubMustSucceed(t, response)
	var payload struct {
		Data struct {
			Evidence struct {
				Nodes []struct {
					ID                string  `json:"id"`
					LineageID         string  `json:"lineageId"`
					FrameID           *string `json:"frameId"`
					PreparationID     *string `json:"preparationId"`
					SourceMediaID     *string `json:"sourceMediaId"`
					PtsMicros         *string `json:"ptsMicros"`
					TimestampMs       *int    `json:"timestampMs"`
					MediaAvailability string  `json:"mediaAvailability"`
					URL               *string `json:"url"`
				} `json:"nodes"`
				PageInfo struct {
					EndCursor string `json:"endCursor"`
				} `json:"pageInfo"`
			} `json:"customerEvidence"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil || len(payload.Data.Evidence.Nodes) != 1 {
		t.Fatalf("customerEvidence = %s (%v)", response, err)
	}
	node := payload.Data.Evidence.Nodes[0]
	if node.MediaAvailability != "SENSITIVE_BLOCKED" || node.URL != nil || node.FrameID != nil || node.PreparationID != nil || node.SourceMediaID != nil || node.PtsMicros != nil || node.TimestampMs != nil {
		t.Fatalf("blocked frame must be metadata only: %+v", node)
	}
	if node.ID == fx.frameID.String() || node.LineageID == fx.frameID.String() || node.ID == "" {
		t.Fatalf("blocked frame kept an addressable id: %+v", node)
	}
	for name, secret := range map[string]string{"frame": fx.frameID.String(), "source": fx.sourceMedia.String(), "preparation": frame.PreparationID.String()} {
		if strings.Contains(response, secret) {
			t.Fatalf("the response still carries the %s id: %s", name, response)
		}
	}
	cursor, err := base64.RawURLEncoding.DecodeString(payload.Data.Evidence.PageInfo.EndCursor)
	if err != nil || strings.Contains(string(cursor), fx.frameID.String()) {
		t.Fatalf("the page cursor embeds the frame id: %q (%v)", cursor, err)
	}
	// The opaque id still works as a cursor: paging after it is an empty page, not an error.
	after := query(`, after:"` + payload.Data.Evidence.PageInfo.EndCursor + `"`)
	pubMustSucceed(t, after)
	if !strings.Contains(after, `"nodes":[]`) {
		t.Fatalf("paging after the blocked frame = %s", after)
	}
}
