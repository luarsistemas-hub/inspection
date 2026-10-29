package generate_snapshot

import (
	"context"
	"strings"
	"testing"

	"inspection/libs/identity"
	report "inspection/services/inspection/internal/features/reports/core"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestVerifyPlanFrameEvidenceAcceptsOnlyRetainedPromotedOriginFrame(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:promoted-frame-"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"ATTACH DATABASE ':memory:' AS analysis",
		"ATTACH DATABASE ':memory:' AS origins",
		"ATTACH DATABASE ':memory:' AS media",
		"CREATE TABLE analysis.video_plan_items (id TEXT, tenant_id TEXT, plan_id TEXT, requirement_key TEXT, carried_result_job_id TEXT)",
		"CREATE TABLE analysis.comparison_jobs (id TEXT, tenant_id TEXT, video_plan_item_id TEXT)",
		"CREATE TABLE analysis.video_plan_item_frames (id TEXT, tenant_id TEXT, plan_item_id TEXT, frame_id TEXT, role TEXT)",
		"CREATE TABLE origins.inventory_environments (id TEXT, tenant_id TEXT, origin_version_id TEXT, identity_id TEXT, requirement_key TEXT)",
		"CREATE TABLE origins.inspection_origin_promotions (id TEXT, tenant_id TEXT, video_plan_id TEXT, origin_version_id TEXT, status TEXT, source_kind TEXT)",
		"CREATE TABLE media.video_frames (id TEXT, tenant_id TEXT, preparation_id TEXT, sha256 TEXT)",
		"CREATE TABLE media.video_evidence_references (id TEXT, tenant_id TEXT, preparation_id TEXT, owner_type TEXT, owner_id TEXT)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenant, currentPlan, sourcePlan, version := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	planItem, frame, preparation := identity.NewID(), identity.NewID(), identity.NewID()
	key, digest := "env:"+identity.NewID().String(), strings.Repeat("a", 64)
	for _, row := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO analysis.video_plan_items (id,tenant_id,plan_id,requirement_key) VALUES (?,?,?,?)", []any{planItem, tenant, sourcePlan, key}},
		{"INSERT INTO analysis.video_plan_item_frames (id,tenant_id,plan_item_id,frame_id,role) VALUES (?,?,?,?,?)", []any{identity.NewID(), tenant, planItem, frame, "CURRENT"}},
		{"INSERT INTO origins.inspection_origin_promotions (id,tenant_id,video_plan_id,origin_version_id,status,source_kind) VALUES (?,?,?,?,?,?)", []any{identity.NewID(), tenant, sourcePlan, version, "COMPLETED", "VIDEO"}},
		{"INSERT INTO media.video_frames (id,tenant_id,preparation_id,sha256) VALUES (?,?,?,?)", []any{frame, tenant, preparation, digest}},
		{"INSERT INTO media.video_evidence_references (id,tenant_id,preparation_id,owner_type,owner_id) VALUES (?,?,?,?,?)", []any{identity.NewID(), tenant, preparation, "ORIGIN_VERSION", version}},
	} {
		if err := db.Exec(row.sql, row.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	evidence := report.Evidence{ID: frame.String(), EvidenceKind: "VIDEO_FRAME", Role: "REFERENCE", RequirementKey: key, FrameID: frame.String(), PreparationID: preparation.String(), FrameDigest: digest}
	verify := func(item report.Evidence, versionID string) error {
		return verifyPlanFrameEvidence(context.Background(), db, tenant, currentPlan, versionID, []report.Evidence{item})
	}
	if err := verify(evidence, version.String()); err != nil {
		t.Fatalf("retained origin frame rejected: %v", err)
	}
	wrongDigest := evidence
	wrongDigest.FrameDigest = strings.Repeat("b", 64)
	for name, item := range map[string]report.Evidence{"digest": wrongDigest, "role": func() report.Evidence { copy := evidence; copy.Role = "CURRENT"; return copy }(), "requirement": func() report.Evidence { copy := evidence; copy.RequirementKey = "env:wrong"; return copy }()} {
		if err := verify(item, version.String()); err != ErrFrameIntegrity {
			t.Errorf("%s: got %v, want frame integrity error", name, err)
		}
	}
	// A room added during the promoted capture is addressed by its inventory identity later.
	addedPlanItem, addedFrame, roomIdentity := identity.NewID(), identity.NewID(), identity.NewID()
	addedKey := "env:new:" + roomIdentity.String()
	for _, row := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO analysis.video_plan_items (id,tenant_id,plan_id,requirement_key) VALUES (?,?,?,?)", []any{addedPlanItem, tenant, sourcePlan, addedKey}},
		{"INSERT INTO analysis.video_plan_item_frames (id,tenant_id,plan_item_id,frame_id,role) VALUES (?,?,?,?,?)", []any{identity.NewID(), tenant, addedPlanItem, addedFrame, "CURRENT"}},
		{"INSERT INTO media.video_frames (id,tenant_id,preparation_id,sha256) VALUES (?,?,?,?)", []any{addedFrame, tenant, preparation, digest}},
		{"INSERT INTO origins.inventory_environments (id,tenant_id,origin_version_id,identity_id,requirement_key) VALUES (?,?,?,?,?)", []any{identity.NewID(), tenant, version, roomIdentity, addedKey}},
	} {
		if err := db.Exec(row.sql, row.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	added := evidence
	added.ID, added.FrameID, added.RequirementKey = addedFrame.String(), addedFrame.String(), "env:"+roomIdentity.String()
	if err := verify(added, version.String()); err != nil {
		t.Fatalf("added-room origin frame rejected: %v", err)
	}
	if err := verify(evidence, identity.NewID().String()); err != ErrFrameIntegrity {
		t.Errorf("wrong pinned origin: got %v", err)
	}
	if err := db.Exec("DELETE FROM media.video_evidence_references").Error; err != nil {
		t.Fatal(err)
	}
	if err := verify(evidence, version.String()); err != ErrFrameIntegrity {
		t.Errorf("unretained frame: got %v", err)
	}
}
