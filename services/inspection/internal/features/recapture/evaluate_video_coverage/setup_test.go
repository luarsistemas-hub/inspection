package evaluate_video_coverage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	match "inspection/services/inspection/internal/features/analysis/prepare_video_comparisons"
	"inspection/services/inspection/internal/platform/database"
)

type frameReaderStub struct{}

func (frameReaderStub) LoadPreparationFrames(context.Context, identity.ID, identity.ID) ([]match.Frame, error) {
	return nil, nil
}

type matcherStub struct{}

func (matcherStub) Propose(context.Context, []match.Frame, []match.Frame) ([]match.Pair, error) {
	return nil, nil
}
func (matcherStub) Verify(context.Context, []match.Frame, []match.Frame, []match.Pair) ([]match.Pair, error) {
	return nil, nil
}

func TestSetupValidatesRequiredDependencies(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS media").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS capture").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS inspections").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS recapture").Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	valid := Dependencies{DB: db, Frames: frameReaderStub{}, Matcher: matcherStub{}, Verifier: matcherStub{}}
	for name, deps := range map[string]Dependencies{"db": {Frames: valid.Frames, Matcher: valid.Matcher, Verifier: valid.Verifier}, "frames": {DB: db, Matcher: valid.Matcher, Verifier: valid.Verifier}, "matcher": {DB: db, Frames: valid.Frames, Verifier: valid.Verifier}, "verifier": {DB: db, Frames: valid.Frames, Matcher: valid.Matcher}} {
		t.Run(name, func(t *testing.T) {
			if _, err := Setup(deps); err == nil {
				t.Fatal("expected dependency validation error")
			}
		})
	}
	if _, err := Setup(valid); err != nil {
		t.Fatalf("valid setup: %v", err)
	}
}

func TestCoveredComponentsNeedsMatchingVerifiedViewAndIgnoresDuplicates(t *testing.T) {
	c1, c2 := identity.NewID(), identity.NewID()
	o1, o2, curr := identity.NewID(), identity.NewID(), identity.NewID()
	origin := []match.Frame{{ID: o1, Visible: true}, {ID: o2, Visible: true}}
	current := []match.Frame{{ID: curr, Visible: true}}
	selected := []Selection{{ComponentID: c1, OriginFrameIDs: []identity.ID{o1}}, {ComponentID: c2, OriginFrameIDs: []identity.ID{o2}}}
	one := match.Pair{ComponentID: c1, OriginFrameID: o1, CurrentFrameID: curr, Decision: match.SameView}
	other := match.Pair{ComponentID: c2, OriginFrameID: o2, CurrentFrameID: curr, Decision: match.SameView}
	got := CoveredComponents(selected, origin, current, []match.Pair{one, other, one}, []match.Pair{one})
	if len(got) != 1 || got[0] != c1 {
		t.Fatalf("covered=%v; want only %s", got, c1)
	}
	if got := CoveredComponents(selected, origin, current, []match.Pair{one}, nil); len(got) != 0 {
		t.Fatalf("unverified proposal covered components: %v", got)
	}
	misattributed := other
	misattributed.OriginFrameID = o1
	if got := CoveredComponents(selected, origin, current, []match.Pair{misattributed}, []match.Pair{misattributed}); len(got) != 0 {
		t.Fatalf("pair attributed to a different component covered selection: %v", got)
	}
	if got := CoveredComponents(selected, origin, current, nil, nil); len(got) != 0 {
		t.Fatalf("no match covered components: %v", got)
	}
}

func TestPreparedNonSupplementIsIgnored(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS media").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS capture").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS inspections").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS recapture").Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.Exec("CREATE TABLE media.video_preparation_jobs (id text, tenant_id text, media_id text, status text, manifest_digest text)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE media.media_objects (id text, tenant_id text, responsibility_id text, requirement_key text, media_kind text)").Error; err != nil {
		t.Fatal(err)
	}
	tenant, mediaID, preparationID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec("INSERT INTO media.video_preparation_jobs (id,tenant_id,media_id,status,manifest_digest) VALUES (?,?,?,?,?)", preparationID, tenant, mediaID, "READY", "digest").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO media.media_objects (id,tenant_id,responsibility_id,requirement_key,media_kind) VALUES (?,?,?,?,?)", mediaID, tenant, identity.NewID(), "video:main", "VIDEO").Error; err != nil {
		t.Fatal(err)
	}
	evaluator, err := Setup(Dependencies{DB: db, Frames: frameReaderStub{}, Matcher: matcherStub{}, Verifier: matcherStub{}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.VideoPreparedPayload{MediaID: mediaID, PreparationID: preparationID, ManifestDigest: "digest"})
	event := events.RawEnvelope{Type: "media.video_prepared.v1", TenantID: tenant, Payload: payload}
	if err := evaluator.HandlePrepared(context.Background(), db, event); err != ErrNotApplicable {
		t.Fatalf("error=%v; want ErrNotApplicable", err)
	}
}

func TestDuplicatePreparedSupplementEnqueuesOneCoverageJob(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"media", "capture", "inspections", "recapture"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	ddl := []string{
		"CREATE TABLE media.video_preparation_jobs (id text, tenant_id text, media_id text, status text, manifest_digest text)",
		"CREATE TABLE media.media_objects (id text, tenant_id text, responsibility_id text, requirement_key text, media_kind text)",
		"CREATE TABLE inspections.responsibilities (id text, tenant_id text, inspection_id text, participant_id text, status text)",
		"CREATE TABLE capture.capture_drafts (id text, tenant_id text, responsibility_id text, kind text, media_kind text)",
		"CREATE TABLE recapture.requests (id text, tenant_id text, inspection_id text, responsibility_id text, status text, deadline_at datetime, idempotency_key text)",
		"CREATE TABLE recapture.video_coverage_jobs (id text, tenant_id text, preparation_id text, request_id text, status text, attempts integer, next_attempt_at datetime, lease_token text, lease_until datetime, error_code text, covered_ids blob, created_at datetime, UNIQUE(tenant_id,preparation_id))",
	}
	for _, sql := range ddl {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenant, inspectionID, responsibilityID := identity.NewID(), identity.NewID(), identity.NewID()
	mediaID, preparationID, requestID := identity.NewID(), identity.NewID(), identity.NewID()
	for _, row := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO media.video_preparation_jobs VALUES (?,?,?,?,?)", []any{preparationID, tenant, mediaID, "READY", "digest"}},
		{"INSERT INTO media.media_objects VALUES (?,?,?,?,?)", []any{mediaID, tenant, responsibilityID, "video:supplement", "VIDEO"}},
		{"INSERT INTO inspections.responsibilities VALUES (?,?,?,?,?)", []any{responsibilityID, tenant, inspectionID, identity.NewID(), "ACTIVE"}},
		{"INSERT INTO capture.capture_drafts VALUES (?,?,?,?,?)", []any{identity.NewID(), tenant, responsibilityID, "RECAPTURE", "VIDEO"}},
		{"INSERT INTO recapture.requests VALUES (?,?,?,?,?,?,?)", []any{requestID, tenant, inspectionID, responsibilityID, "RECEIVED", time.Now().Add(time.Hour), "request"}},
	} {
		if err := db.Exec(row.sql, row.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	evaluator, err := Setup(Dependencies{DB: db, Frames: frameReaderStub{}, Matcher: matcherStub{}, Verifier: matcherStub{}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.VideoPreparedPayload{MediaID: mediaID, PreparationID: preparationID, ManifestDigest: "digest"})
	event := events.RawEnvelope{Type: "media.video_prepared.v1", TenantID: tenant, Payload: payload}
	if err := evaluator.HandlePrepared(context.Background(), db, event); err != nil {
		t.Fatal(err)
	}
	if err := evaluator.HandlePrepared(context.Background(), db, event); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&database.VideoCoverageJob{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("coverage job count=%d err=%v; want one", count, err)
	}
}
