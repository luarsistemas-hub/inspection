package handle_classified

import (
	"context"
	"strings"
	"testing"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPromotedVideoOriginFramesAppearForCapturedEnvironmentWithoutItems(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:promoted-report-"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"ATTACH DATABASE ':memory:' AS origins",
		"ATTACH DATABASE ':memory:' AS analysis",
		"ATTACH DATABASE ':memory:' AS media",
		"CREATE TABLE origins.inventory_environments (id TEXT, tenant_id TEXT, origin_version_id TEXT, identity_id TEXT, requirement_key TEXT)",
		"CREATE TABLE origins.inspection_origin_promotions (id TEXT, tenant_id TEXT, inspection_id TEXT, origin_version_id TEXT, status TEXT, source_kind TEXT, video_plan_id TEXT)",
		"CREATE TABLE media.video_evidence_references (id TEXT, tenant_id TEXT, preparation_id TEXT, owner_type TEXT, owner_id TEXT)",
		"CREATE TABLE analysis.comparison_jobs (id TEXT, tenant_id TEXT, inspection_id TEXT, video_plan_id TEXT, requirement_key TEXT, video_core_group INTEGER)",
		"CREATE TABLE analysis.video_plan_items (id TEXT, tenant_id TEXT, plan_id TEXT, carried_result_job_id TEXT, requirement_key TEXT)",
		"CREATE TABLE analysis.video_plan_item_frames (id TEXT, tenant_id TEXT, plan_item_id TEXT, frame_id TEXT, role TEXT, core_group INTEGER, pair_order INTEGER)",
		"CREATE TABLE media.video_frames (id TEXT, tenant_id TEXT, source_media_id TEXT, preparation_id TEXT, pts_us INTEGER, sha256 TEXT)",
		"CREATE TABLE media.video_sources (id TEXT, tenant_id TEXT, media_id TEXT, status TEXT)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenant, version, sourceInspection, plan := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	preparation, media, item, currentFrame, oldOriginFrame := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	key := "env:" + identity.NewID().String()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO origins.inspection_origin_promotions (id,tenant_id,inspection_id,origin_version_id,status,source_kind,video_plan_id) VALUES (?,?,?,?,?,?,?)", []any{identity.NewID(), tenant, sourceInspection, version, "COMPLETED", "VIDEO", plan}},
		{"INSERT INTO media.video_evidence_references (id,tenant_id,preparation_id,owner_type,owner_id) VALUES (?,?,?,?,?)", []any{identity.NewID(), tenant, preparation, "ORIGIN_VERSION", version}},
		{"INSERT INTO analysis.video_plan_items (id,tenant_id,plan_id,requirement_key) VALUES (?,?,?,?)", []any{item, tenant, plan, key}},
		{"INSERT INTO media.video_sources (id,tenant_id,media_id,status) VALUES (?,?,?,?)", []any{identity.NewID(), tenant, media, "EXPIRED"}},
		{"INSERT INTO media.video_frames (id,tenant_id,source_media_id,preparation_id,pts_us,sha256) VALUES (?,?,?,?,?,?)", []any{currentFrame, tenant, media, preparation, 1200000, strings.Repeat("a", 64)}},
		{"INSERT INTO media.video_frames (id,tenant_id,source_media_id,preparation_id,pts_us,sha256) VALUES (?,?,?,?,?,?)", []any{oldOriginFrame, tenant, media, preparation, 900000, strings.Repeat("b", 64)}},
		{"INSERT INTO analysis.video_plan_item_frames (id,tenant_id,plan_item_id,frame_id,role,core_group,pair_order) VALUES (?,?,?,?,?,?,?)", []any{identity.NewID(), tenant, item, currentFrame, "CURRENT", 0, 0}},
		{"INSERT INTO analysis.video_plan_item_frames (id,tenant_id,plan_item_id,frame_id,role,core_group,pair_order) VALUES (?,?,?,?,?,?,?)", []any{identity.NewID(), tenant, item, oldOriginFrame, "ORIGIN", 0, 0}},
	} {
		if err := db.Exec(statement.sql, statement.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	requirements := []reportcore.Requirement{{Key: key}, {Key: "env:another"}}
	evidence, err := promotedOriginReportEvidence(context.Background(), db, tenant, &version, requirements, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 1 || evidence[0].ID != currentFrame.String() || evidence[0].RequirementKey != key || evidence[0].Role != "REFERENCE" || evidence[0].SourceAvailability != "EXPIRED" {
		t.Fatalf("promoted origin evidence=%+v", evidence)
	}
	if evidence[0].EvidenceKind != "VIDEO_FRAME" || evidence[0].FrameID != currentFrame.String() || evidence[0].FrameDigest != strings.Repeat("a", 64) || evidence[0].TimestampMS == nil || *evidence[0].TimestampMS != 1200 {
		t.Fatalf("promoted video frame metadata=%+v", evidence[0])
	}
	duplicate, err := promotedOriginReportEvidence(context.Background(), db, tenant, &version, requirements, evidence)
	if err != nil || len(duplicate) != 0 {
		t.Fatalf("duplicate promoted origin evidence=%+v, err=%v", duplicate, err)
	}
	// A room added during the promoted capture is addressed by its inventory identity later.
	addedItem, addedFrame, roomIdentity := identity.NewID(), identity.NewID(), identity.NewID()
	addedKey := "env:new:" + roomIdentity.String()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO analysis.video_plan_items (id,tenant_id,plan_id,requirement_key) VALUES (?,?,?,?)", []any{addedItem, tenant, plan, addedKey}},
		{"INSERT INTO media.video_frames (id,tenant_id,source_media_id,preparation_id,pts_us,sha256) VALUES (?,?,?,?,?,?)", []any{addedFrame, tenant, media, preparation, 1500000, strings.Repeat("c", 64)}},
		{"INSERT INTO analysis.video_plan_item_frames (id,tenant_id,plan_item_id,frame_id,role,core_group,pair_order) VALUES (?,?,?,?,?,?,?)", []any{identity.NewID(), tenant, addedItem, addedFrame, "CURRENT", 0, 0}},
		{"INSERT INTO origins.inventory_environments (id,tenant_id,origin_version_id,identity_id,requirement_key) VALUES (?,?,?,?,?)", []any{identity.NewID(), tenant, version, roomIdentity, addedKey}},
	} {
		if err := db.Exec(statement.sql, statement.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	laterKey := "env:" + roomIdentity.String()
	added, err := promotedOriginReportEvidence(context.Background(), db, tenant, &version, append(requirements, reportcore.Requirement{Key: laterKey}), evidence)
	if err != nil || len(added) != 1 || added[0].ID != addedFrame.String() || added[0].RequirementKey != laterKey {
		t.Fatalf("added-room promoted origin evidence=%+v, err=%v", added, err)
	}
}
