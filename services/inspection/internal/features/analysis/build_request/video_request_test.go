package build_request

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"testing"
	"time"

	"inspection/libs/identity"
	preparevideocomparisons "inspection/services/inspection/internal/features/analysis/prepare_video_comparisons"
	processcomparison "inspection/services/inspection/internal/features/analysis/process_comparison"
	analysisprompt "inspection/services/inspection/internal/features/analysis/prompt"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type videoRequestObjects map[string][]byte

func (videoRequestObjects) CreateMultipart(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (videoRequestObjects) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "", nil
}
func (videoRequestObjects) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (videoRequestObjects) AbortMultipart(context.Context, string, string, string) error { return nil }
func (videoRequestObjects) Head(context.Context, string, string) (objectstore.Head, error) {
	return objectstore.Head{}, nil
}
func (o videoRequestObjects) Get(_ context.Context, _, key string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(o[key])), nil
}
func (videoRequestObjects) Put(context.Context, string, string, io.Reader, int64, string) error {
	return nil
}

func TestVideoPlanRequestPinsDistinctFrameEvidenceToOneComponentPairUT046(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, schema := range []string{"analysis", "inspections", "media"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		"CREATE TABLE analysis.video_plans (id TEXT PRIMARY KEY, tenant_id TEXT, inspection_id TEXT, reference_version_id TEXT, status TEXT)",
		"CREATE TABLE analysis.video_plan_items (id TEXT PRIMARY KEY, tenant_id TEXT, plan_id TEXT, component_id TEXT, requirement_key TEXT, outcome TEXT, core_groups BLOB)",
		"CREATE TABLE inspections.inspections (id TEXT PRIMARY KEY, tenant_id TEXT, analysis_prompt_snapshot_id TEXT)",
		"CREATE TABLE analysis.prompt_snapshots (id TEXT PRIMARY KEY, analysis_type TEXT, definition_json BLOB, canonical_digest TEXT)",
		"CREATE TABLE inspections.reference_snapshots (id TEXT PRIMARY KEY, tenant_id TEXT, inspection_id TEXT, reference_version_id TEXT)",
		"CREATE TABLE media.video_frames (id TEXT PRIMARY KEY, tenant_id TEXT, sha256 TEXT, object_key TEXT)",
		"CREATE TABLE media.video_sources (id TEXT PRIMARY KEY, tenant_id TEXT, media_id TEXT, screening_status TEXT)",
		"CREATE TABLE media.video_scope_items (id TEXT PRIMARY KEY, tenant_id TEXT, preparation_id TEXT, frame_id TEXT, environment_candidate TEXT, component_label TEXT, view_label TEXT, visibility TEXT, identity_status TEXT, landmarks BLOB, created_at DATETIME)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, inspectionID, planID, itemID, componentID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	originVersionID, promptID, originFrameID, currentFrameID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	definition, promptDigest, err := analysisprompt.Canonicalize(analysisprompt.DefaultDefinition())
	if err != nil {
		t.Fatal(err)
	}
	originBytes, currentBytes := []byte("frame-F01"), []byte("frame-F11")
	originHash, currentHash := sha256.Sum256(originBytes), sha256.Sum256(currentBytes)
	objects := videoRequestObjects{"frames/F01.jpg": originBytes, "frames/F11.jpg": currentBytes}
	pairs, err := json.Marshal([][]preparevideocomparisons.Pair{{{ComponentID: componentID, OriginFrameID: originFrameID, CurrentFrameID: currentFrameID, Decision: preparevideocomparisons.SameView, Landmarks: []string{"sink wall"}}}})
	if err != nil {
		t.Fatal(err)
	}
	insert := func(statement string, args ...any) {
		t.Helper()
		if err := db.Exec(statement, args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	insert("INSERT INTO analysis.video_plans (id,tenant_id,inspection_id,reference_version_id,status) VALUES (?,?,?,?,?)", planID, tenantID, inspectionID, originVersionID, "SEALED")
	insert("INSERT INTO analysis.video_plan_items (id,tenant_id,plan_id,component_id,requirement_key,outcome,core_groups) VALUES (?,?,?,?,?,?,?)", itemID, tenantID, planID, componentID, "video:"+componentID.String(), preparevideocomparisons.Matched, pairs)
	insert("INSERT INTO inspections.inspections (id,tenant_id,analysis_prompt_snapshot_id) VALUES (?,?,?)", inspectionID, tenantID, promptID)
	insert("INSERT INTO analysis.prompt_snapshots (id,analysis_type,definition_json,canonical_digest) VALUES (?,?,?,?)", promptID, analysisprompt.RealEstate, definition, promptDigest)
	insert("INSERT INTO inspections.reference_snapshots (id,tenant_id,inspection_id,reference_version_id) VALUES (?,?,?,?)", identity.NewID(), tenantID, inspectionID, originVersionID)
	insert("INSERT INTO media.video_frames (id,tenant_id,sha256,object_key) VALUES (?,?,?,?)", originFrameID, tenantID, hex.EncodeToString(originHash[:]), "frames/F01.jpg")
	insert("INSERT INTO media.video_frames (id,tenant_id,sha256,object_key) VALUES (?,?,?,?)", currentFrameID, tenantID, hex.EncodeToString(currentHash[:]), "frames/F11.jpg")
	request, err := build(objectstore.Store{Bucket: "test", Client: objects})(context.Background(), db, database.ComparisonJob{TenantID: tenantID, InspectionID: inspectionID, VideoPlanID: &planID, VideoPlanItemID: &itemID}, processcomparison.Payload{})
	if err != nil {
		t.Fatal(err)
	}
	wantPairID := "component:" + componentID.String()
	if request.Mode != "COMPARE_ORIGIN_CURRENT" || len(request.Images) != 2 || request.Images[0].EvidenceID != originFrameID.String() || request.Images[1].EvidenceID != currentFrameID.String() || request.Images[0].EvidenceID == request.Images[1].EvidenceID || request.Images[0].PairID != wantPairID || request.Images[1].PairID != wantPairID || request.Images[0].Source != "ORIGIN" || request.Images[1].Source != "CURRENT" {
		t.Fatalf("video request lost exact frame pair: %+v", request.Images)
	}
}
