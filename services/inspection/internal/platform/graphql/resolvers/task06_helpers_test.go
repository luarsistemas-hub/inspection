package resolvers

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"testing"
	"time"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestUT058SourceExpiryOverlaysAvailabilityWithoutChangingSnapshot(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"ATTACH DATABASE ':memory:' AS media",
		"CREATE TABLE media.video_frames (id text primary key, tenant_id text, source_media_id text, preparation_id text, sha256 text, object_key text)",
		"CREATE TABLE media.video_sources (id text primary key, tenant_id text, media_id text, status text, original_deleted_at datetime, source_expiry_at datetime)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, snapshotID, frameID, sourceID, preparationID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := db.Exec("INSERT INTO media.video_frames (id, tenant_id, source_media_id, preparation_id, sha256, object_key) VALUES (?, ?, ?, ?, ?, ?)", frameID, tenantID, sourceID, preparationID, digest, "frame.jpg").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO media.video_sources (id, tenant_id, media_id, status) VALUES (?, ?, ?, 'RETAINED')", identity.NewID(), tenantID, sourceID).Error; err != nil {
		t.Fatal(err)
	}
	snapshot := reportcore.Snapshot{Evidence: []reportcore.Evidence{{ID: frameID.String(), FrameID: frameID.String(), SourceMediaID: sourceID.String(), PreparationID: preparationID.String(), FrameDigest: digest, DisplayDigest: digest, EvidenceKind: "VIDEO_FRAME", SourceAvailability: "AVAILABLE"}}}
	canonical, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	row := database.ReportSnapshot{ID: snapshotID, TenantID: tenantID, CanonicalJSON: canonical, JSONDigest: digest}
	view := mapReport(row)
	original := make(map[string]any)
	for key, value := range view.CanonicalJSON {
		original[key] = value
	}
	if err := hydrateReportMedia(context.Background(), db, objectstore.Store{}, tenantID, view); err != nil {
		t.Fatal(err)
	}
	if view.Evidence.Nodes[0].SourceAvailability == nil || *view.Evidence.Nodes[0].SourceAvailability != "AVAILABLE" {
		t.Fatalf("before expiry: %+v", view.Evidence.Nodes[0])
	}
	if err := db.Exec("UPDATE media.video_sources SET status='EXPIRED' WHERE tenant_id=? AND media_id=?", tenantID, sourceID).Error; err != nil {
		t.Fatal(err)
	}
	view = mapReport(row)
	if err := hydrateReportMedia(context.Background(), db, objectstore.Store{}, tenantID, view); err != nil {
		t.Fatal(err)
	}
	evidence := view.Evidence.Nodes[0]
	if evidence.Availability != "AVAILABLE" || evidence.SourceAvailability == nil || *evidence.SourceAvailability != "EXPIRED" || evidence.URL == nil || *evidence.URL != "/media/video-evidence/"+frameID.String()+"?snapshotId="+snapshotID.String() {
		t.Fatalf("expired source did not retain its frame: %+v", evidence)
	}
	if !reflect.DeepEqual(view.CanonicalJSON, original) || string(row.CanonicalJSON) != string(canonical) || row.JSONDigest != digest {
		t.Fatal("availability overlay changed the canonical snapshot")
	}
}

func TestMapReportPresentsExistingVideoLabelsInPortuguese(t *testing.T) {
	snapshot := reportcore.Snapshot{
		VideoPlanID:  "plan-1",
		Requirements: []reportcore.Requirement{{Key: "video:fireplace", Section: "Vídeo", Label: "living_room — fireplace", Instructions: "Para recaptura: filme living_room — fireplace, mostrando a vista front_view."}},
		Evidence:     []reportcore.Evidence{{ID: "frame-1", RequirementKey: "video:fireplace", EvidenceKind: "VIDEO_FRAME"}},
	}
	canonical, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	view := mapReport(database.ReportSnapshot{ID: identity.NewID(), InspectionID: identity.NewID(), CanonicalJSON: canonical})
	if got := view.Requirements[0].Label; got != "Sala de estar — Lareira" {
		t.Fatalf("report label = %q", got)
	}
	if got := *view.Requirements[0].Instructions; got != "Para recaptura: filme Sala de estar — Lareira, mostrando a vista frontal." {
		t.Fatalf("report instructions = %q", got)
	}
	if got := *view.Evidence.Nodes[0].Component; got != "Sala de estar — Lareira" {
		t.Fatalf("evidence component = %q", got)
	}
	if got := view.CanonicalJSON["requirements"].([]any)[0].(map[string]any)["label"]; got != "living_room — fireplace" {
		t.Fatalf("canonical label = %q", got)
	}
}

func TestHydrateReportMediaSignsAvailableDisplayDerivative(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS media").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE media.derivatives (id blob primary key, tenant_id blob not null, media_id blob not null, object_key text not null, kind text not null, sha256 text not null, content_type text not null default 'image/jpeg', width integer, height integer, size_bytes integer, profile text, created_at datetime)").Error; err != nil {
		t.Fatal(err)
	}

	tenantID, mediaID := identity.NewID(), identity.NewID()
	if err := db.Create(&database.MediaDerivative{ID: identity.NewID(), TenantID: tenantID, MediaID: mediaID, ObjectKey: "tenant/display.jpg", Kind: "DISPLAY", SHA256: "digest"}).Error; err != nil {
		t.Fatal(err)
	}
	report := &graphql1.Report{Evidence: &graphql1.ReportEvidenceConnection{Nodes: []*graphql1.ReportEvidence{{ID: mediaID.String(), Availability: "AVAILABLE"}}}}
	store := objectstore.Store{Bucket: "private", Client: reportMediaClient{}}

	if err := hydrateReportMedia(context.Background(), db, store, tenantID, report); err != nil {
		t.Fatal(err)
	}
	if report.Evidence.Nodes[0].Availability != "AVAILABLE" || report.Evidence.Nodes[0].URL == nil || *report.Evidence.Nodes[0].URL != "https://signed.invalid/display.jpg" {
		t.Fatalf("media was not hydrated: %+v", report.Evidence.Nodes[0])
	}
}

func TestEvidenceCursorIsTenantAndSnapshotScoped(t *testing.T) {
	tenantID, snapshotID, evidenceID := identity.NewID(), identity.NewID(), identity.NewID()
	cursor := encodeEvidenceCursor(tenantID, snapshotID, evidenceID.String())
	if got, err := decodeEvidenceCursor(cursor, tenantID, snapshotID); err != nil || got != evidenceID.String() {
		t.Fatalf("decode cursor = %q, %v", got, err)
	}
	if _, err := decodeEvidenceCursor(cursor, identity.NewID(), snapshotID); err == nil {
		t.Fatal("accepted cursor from another tenant")
	}
	if _, err := decodeEvidenceCursor(cursor, tenantID, identity.NewID()); err == nil {
		t.Fatal("accepted cursor from another snapshot")
	}
}

func TestEvidencePageValidatesLimitAndCursor(t *testing.T) {
	tooMany := 101
	if _, err := pageLimit(&tooMany); err == nil {
		t.Fatal("accepted page size above contract maximum")
	}
	items := []*graphql1.ReportEvidence{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	page, more, err := sliceEvidencePage(items, 2, "")
	if err != nil || !more || len(page) != 2 || page[1].ID != "b" {
		t.Fatalf("page = %#v, %v, %v", page, more, err)
	}
	page, more, err = sliceEvidencePage(items, 2, "b")
	if err != nil || more || len(page) != 1 || page[0].ID != "c" {
		t.Fatalf("after page = %#v, %v, %v", page, more, err)
	}
	if _, _, err := sliceEvidencePage(items, 2, "missing"); err == nil {
		t.Fatal("accepted invalid cursor")
	}
}

type reportMediaClient struct{}

func (reportMediaClient) CreateMultipart(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (reportMediaClient) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "", nil
}
func (reportMediaClient) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (reportMediaClient) AbortMultipart(context.Context, string, string, string) error { return nil }
func (reportMediaClient) Head(context.Context, string, string) (objectstore.Head, error) {
	return objectstore.Head{}, nil
}
func (reportMediaClient) Get(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(nilReader{}), nil
}
func (reportMediaClient) Put(context.Context, string, string, io.Reader, int64, string) error {
	return nil
}
func (reportMediaClient) PresignGet(_ context.Context, _ string, key string, _ time.Duration) (string, error) {
	return "https://signed.invalid/" + key[len("tenant/"):], nil
}

type nilReader struct{}

func (nilReader) Read([]byte) (int, error) { return 0, io.EOF }
func (nilReader) Close() error             { return nil }
