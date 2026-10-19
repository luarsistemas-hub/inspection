package resolvers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestReportEvidenceGraphQLProjectsSnapshotVideoLineageAndPaginates(t *testing.T) {
	db := task06ResolverDB(t)
	for _, statement := range []string{
		`CREATE TABLE media.video_frames (id blob primary key, tenant_id blob, source_media_id blob, preparation_id blob, pts_us integer, sha256 text, object_key text)`,
		`CREATE TABLE reports.report_pdf_jobs (id blob primary key, tenant_id blob, snapshot_id blob, audience text, status text)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, inspectionID, planID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO inspections.inspections (id,tenant_id,business_unit_id,asset_id) VALUES (?,?,?,?)`, inspectionID, tenantID, identity.NewID(), identity.NewID()).Error; err != nil {
		t.Fatal(err)
	}
	firstFrame, err := identity.ParseID("00000000-0000-4000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	secondFrame, err := identity.ParseID("00000000-0000-4000-8000-000000000002")
	if err != nil {
		t.Fatal(err)
	}
	frames := []identity.ID{firstFrame, secondFrame}
	evidence := make([]reportcore.Evidence, 0, len(frames))
	for i, frameID := range frames {
		sourceID, prepID := identity.NewID(), identity.NewID()
		pts, ms := int64(10_000_000+i*1_000_000), int64(10_000+i*1_000)
		digest := strings.Repeat("a", 64)
		evidence = append(evidence, reportcore.Evidence{ID: frameID.String(), RequirementKey: "video:kitchen", Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: frameID.String(), SourceMediaID: sourceID.String(), PreparationID: prepID.String(), PTSMicros: &pts, TimestampMS: &ms, FrameDigest: digest, DisplayDigest: digest, SourceAvailability: "AVAILABLE"})
		if err := db.Exec(`INSERT INTO media.video_frames (id,tenant_id,source_media_id,preparation_id,pts_us,sha256,object_key) VALUES (?,?,?,?,?,?,?)`, frameID, tenantID, sourceID, prepID, pts, digest, "frame.jpg").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`INSERT INTO media.video_sources (id,tenant_id,media_id,status,screening_status) VALUES (?,?,?,'RETAINED','CLEAR')`, identity.NewID(), tenantID, sourceID).Error; err != nil {
			t.Fatal(err)
		}
	}
	seedTask06PlanFrames(t, db, tenantID, planID, frames...)
	created, err := generate_snapshot.Create(context.Background(), db, generate_snapshot.Input{TenantID: tenantID, InspectionID: inspectionID, Mode: "CONSOLIDATED", Classification: "ATTENTION", TemplateVersionID: "template-v1", ReferenceVersionID: "reference-v1", PromptDigest: "prompt-v1", VideoPlanID: &planID, Evidence: evidence, Context: reportcore.Context{Asset: reportcore.AssetContext{ID: identity.NewID().String(), Name: "QA", Address: "QA"}, Participant: reportcore.ParticipantContext{ID: identity.NewID().String(), Name: "QA"}, Template: reportcore.TemplateContext{ID: "template-v1", Name: "QA", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano)}}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	server := task06GraphQLServer(db)
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.TenantAdmin}}})
	response := executeTask06GraphQL(t, server, `query { report(inspectionId:"`+inspectionID.String()+`") { evidence(first:1) { nodes { id evidenceKind frameId timestampMs ptsMicros preparationId planId sourceMediaId sourceAvailability } pageInfo { endCursor hasNextPage } } } }`, ctx)
	var decoded struct {
		Data struct {
			Report struct {
				Evidence struct {
					Nodes    []map[string]any `json:"nodes"`
					PageInfo struct {
						EndCursor string `json:"endCursor"`
						HasNext   bool   `json:"hasNextPage"`
					} `json:"pageInfo"`
				} `json:"evidence"`
			} `json:"report"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &decoded); err != nil {
		t.Fatalf("decode GraphQL response %s: %v", response, err)
	}
	if strings.Contains(response, `"errors"`) || len(decoded.Data.Report.Evidence.Nodes) != 1 || !decoded.Data.Report.Evidence.PageInfo.HasNext {
		t.Fatalf("unexpected first page: %s", response)
	}
	for _, field := range []string{"evidenceKind", "frameId", "timestampMs", "ptsMicros", "preparationId", "planId", "sourceMediaId", "sourceAvailability"} {
		if _, ok := decoded.Data.Report.Evidence.Nodes[0][field]; !ok {
			t.Fatalf("missing %s in response: %s", field, response)
		}
	}
	if decoded.Data.Report.Evidence.Nodes[0]["planId"] != planID.String() {
		t.Fatalf("plan provenance was not snapshot-pinned: %s", response)
	}
	secondPage := executeTask06GraphQL(t, server, `query { report(inspectionId:"`+inspectionID.String()+`") { evidence(first:1,after:"`+decoded.Data.Report.Evidence.PageInfo.EndCursor+`") { nodes { id frameId timestampMs ptsMicros sourceMediaId } } } }`, ctx)
	var secondDecoded struct {
		Data struct {
			Report struct {
				Evidence struct {
					Nodes []map[string]any `json:"nodes"`
				} `json:"evidence"`
			} `json:"report"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(secondPage), &secondDecoded); err != nil || strings.Contains(secondPage, `"errors"`) || len(secondDecoded.Data.Report.Evidence.Nodes) != 1 {
		t.Fatalf("unexpected second evidence page: %s err=%v", secondPage, err)
	}
	firstEvidence, secondEvidence := decoded.Data.Report.Evidence.Nodes[0], secondDecoded.Data.Report.Evidence.Nodes[0]
	if firstEvidence["timestampMs"] != float64(10_000) || firstEvidence["ptsMicros"] != "10000000" || secondEvidence["timestampMs"] != float64(11_000) || secondEvidence["ptsMicros"] != "11000000" {
		t.Fatalf("F11/F12 timestamps were not independently projected at 10s and 11s: first=%v second=%v", firstEvidence, secondEvidence)
	}
	if firstEvidence["id"] == secondEvidence["id"] || firstEvidence["frameId"] == secondEvidence["frameId"] || firstEvidence["sourceMediaId"] == secondEvidence["sourceMediaId"] {
		t.Fatalf("evidence, frame, and source identities must be independent: first=%v second=%v", firstEvidence, secondEvidence)
	}
	bad := executeTask06GraphQL(t, server, `query { report(inspectionId:"`+inspectionID.String()+`") { evidence(first:101) { nodes { id } } } }`, ctx)
	if !strings.Contains(bad, "INVALID_INPUT") {
		t.Fatalf("oversized page accepted: %s", bad)
	}
	_ = created
}
