package resolvers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestVV_IT_257TwoHundredFindingsAndLimitationsRemainReachable(t *testing.T) {
	db := task06ResolverDB(t)
	for _, statement := range []string{
		`CREATE TABLE media.video_frames (id blob primary key, tenant_id blob, source_media_id blob, preparation_id blob, pts_us integer, sha256 text, object_key text)`,
		`CREATE TABLE reports.report_pdf_jobs (id blob primary key, tenant_id blob, snapshot_id blob, audience text, status text)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, inspectionID := identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO inspections.inspections (id,tenant_id,business_unit_id,asset_id) VALUES (?,?,?,?)`, inspectionID, tenantID, identity.NewID(), identity.NewID()).Error; err != nil {
		t.Fatal(err)
	}
	const total = 200
	findings := make([]reportcore.Finding, 0, total)
	evidence := make([]reportcore.Evidence, 0, total)
	requirements := make([]reportcore.Requirement, 0, total)
	for i := 0; i < total; i++ {
		evidenceID := fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
		frameID, sourceID, preparationID := mustTask06ID(t, evidenceID), identity.NewID(), identity.NewID()
		pts, ms, digest := int64(i)*1_000_000, int64(i)*1_000, strings.Repeat("a", 64)
		evidence = append(evidence, reportcore.Evidence{ID: evidenceID, RequirementKey: fmt.Sprintf("video:finding-%03d", i), Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: evidenceID, SourceMediaID: sourceID.String(), PreparationID: preparationID.String(), PTSMicros: &pts, TimestampMS: &ms, FrameDigest: digest, DisplayDigest: digest, SourceAvailability: "AVAILABLE"})
		if err := db.Exec(`INSERT INTO media.video_frames (id,tenant_id,source_media_id,preparation_id,pts_us,sha256,object_key) VALUES (?,?,?,?,?,?,?)`, frameID, tenantID, sourceID, preparationID, pts, digest, "frame.jpg").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`INSERT INTO media.video_sources (id,tenant_id,media_id,status,screening_status) VALUES (?,?,?,'RETAINED','CLEAR')`, identity.NewID(), tenantID, sourceID).Error; err != nil {
			t.Fatal(err)
		}
		findings = append(findings, reportcore.Finding{ID: fmt.Sprintf("finding-%03d", i), Category: "CONSERVATION", Title: fmt.Sprintf("Constatação %03d", i), Description: "Desgaste observado.", Severity: "LOW", Confidence: 0.8, EvidenceIDs: []string{evidenceID}, Quality: "ADEQUATE", RecommendedAction: "MONITOR"})
		requirements = append(requirements, reportcore.Requirement{Key: fmt.Sprintf("video:gap-%03d", i), Section: "Vídeo", Label: fmt.Sprintf("Ambiente %03d — Componente", i), Coverage: generate_snapshot.VideoUnobservedCoverage, AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "INCONCLUSIVE"})
	}
	if _, err := generate_snapshot.Create(context.Background(), db, generate_snapshot.Input{TenantID: tenantID, InspectionID: inspectionID, Mode: "CONSOLIDATED", Classification: "ATTENTION", TemplateVersionID: "template-v1", ReferenceVersionID: "current-only", PromptDigest: "prompt-v1", Requirements: requirements, Findings: findings, Evidence: evidence, Context: reportcore.Context{Asset: reportcore.AssetContext{ID: identity.NewID().String(), Name: "QA", Address: "QA"}, Participant: reportcore.ParticipantContext{ID: identity.NewID().String(), Name: "QA"}, Template: reportcore.TemplateContext{ID: "template-v1", Name: "QA", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano)}}}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	server := task06GraphQLServer(db)
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{auth.TenantAdmin}}})
	findingIDs, gapKeys, evidenceIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	after, pages := "", 0
	for {
		page := `evidence(first:100) { nodes { id } pageInfo { endCursor hasNextPage } }`
		if after != "" {
			page = `evidence(first:100,after:"` + after + `") { nodes { id } pageInfo { endCursor hasNextPage } }`
		}
		response := executeTask06GraphQL(t, server, `query { report(inspectionId:"`+inspectionID.String()+`") { findings { id } requirements { key analysisStatus coverage } `+page+` } }`, ctx)
		var decoded struct {
			Data struct {
				Report struct {
					Findings []struct {
						ID string `json:"id"`
					} `json:"findings"`
					Requirements []struct {
						Key, AnalysisStatus, Coverage string
					} `json:"requirements"`
					Evidence struct {
						Nodes []struct {
							ID string `json:"id"`
						} `json:"nodes"`
						PageInfo struct {
							EndCursor string `json:"endCursor"`
							HasNext   bool   `json:"hasNextPage"`
						} `json:"pageInfo"`
					} `json:"evidence"`
				} `json:"report"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(response), &decoded); err != nil || strings.Contains(response, `"errors"`) {
			t.Fatalf("report page failed: %.400s (%v)", response, err)
		}
		for _, finding := range decoded.Data.Report.Findings {
			findingIDs[finding.ID] = true
		}
		for _, requirement := range decoded.Data.Report.Requirements {
			if requirement.AnalysisStatus == "INCONCLUSIVE" && requirement.Coverage == generate_snapshot.VideoUnobservedCoverage {
				gapKeys[requirement.Key] = true
			}
		}
		for _, node := range decoded.Data.Report.Evidence.Nodes {
			evidenceIDs[node.ID] = true
		}
		pages++
		if !decoded.Data.Report.Evidence.PageInfo.HasNext {
			break
		}
		if pages > 3 {
			t.Fatal("evidence pagination did not terminate")
		}
		after = decoded.Data.Report.Evidence.PageInfo.EndCursor
	}
	if len(findingIDs) != total || len(gapKeys) != total || len(evidenceIDs) != total || pages != 2 {
		t.Fatalf("reachable findings=%d limitations=%d evidence=%d pages=%d, want %d each over 2 pages", len(findingIDs), len(gapKeys), len(evidenceIDs), pages, total)
	}
}

func mustTask06ID(t *testing.T, value string) identity.ID {
	t.Helper()
	id, err := identity.ParseID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
