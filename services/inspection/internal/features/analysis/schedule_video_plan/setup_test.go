package schedule_video_plan

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	buildplan "inspection/services/inspection/internal/features/analysis/prepare_video_comparisons"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type framesStub struct{}

func (framesStub) LoadPreparationFrames(context.Context, identity.ID, identity.ID) ([]buildplan.Frame, error) {
	return nil, nil
}

type matcherStub struct{}

func (matcherStub) Propose(context.Context, []buildplan.Frame, []buildplan.Frame) ([]buildplan.Pair, error) {
	return nil, nil
}

type failingMatcher struct{}

func (failingMatcher) Propose(context.Context, []buildplan.Frame, []buildplan.Frame) ([]buildplan.Pair, error) {
	return nil, errors.New("temporary matcher outage")
}
func (failingMatcher) Verify(context.Context, []buildplan.Frame, []buildplan.Frame, []buildplan.Pair) ([]buildplan.Pair, error) {
	return nil, nil
}

type oneFrameReader struct{ frames []buildplan.Frame }

func (r oneFrameReader) LoadPreparationFrames(_ context.Context, _, preparationID identity.ID) ([]buildplan.Frame, error) {
	frames := make([]buildplan.Frame, 0, len(r.frames))
	for _, frame := range r.frames {
		if frame.PreparationID == preparationID {
			frames = append(frames, frame)
		}
	}
	return frames, nil
}

type leaseStealingWriter struct{ db *gorm.DB }

func (w leaseStealingWriter) SealPlan(_ context.Context, plan buildplan.Plan) error {
	if err := w.db.Model(&database.VideoPlan{}).Where("tenant_id=? AND id=?", plan.TenantID, plan.ID).Update("lease_token", nil).Error; err != nil {
		return err
	}
	return buildplan.ErrInvalidPlan
}
func (matcherStub) Verify(context.Context, []buildplan.Frame, []buildplan.Frame, []buildplan.Pair) ([]buildplan.Pair, error) {
	return nil, nil
}

func TestSetupValidatesDependencies(t *testing.T) {
	db := schedulePlanTestDB(t)
	valid := Dependencies{DB: db, Frames: framesStub{}, Matcher: matcherStub{}, Verifier: matcherStub{}}
	for name, deps := range map[string]Dependencies{
		"db":       {Frames: framesStub{}, Matcher: matcherStub{}, Verifier: matcherStub{}},
		"frames":   {DB: db, Matcher: matcherStub{}, Verifier: matcherStub{}},
		"matcher":  {DB: db, Frames: framesStub{}, Verifier: matcherStub{}},
		"verifier": {DB: db, Frames: framesStub{}, Matcher: matcherStub{}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Setup(deps); err == nil {
				t.Fatal("expected missing dependency to fail")
			}
		})
	}
	if _, err := Setup(valid); err != nil {
		t.Fatalf("valid setup: %v", err)
	}
}

func TestPreparedDigestMismatchMarksBuildingPlanFailed(t *testing.T) {
	db := schedulePlanTestDB(t)
	tenant, submissionID, preparationID := identity.NewID(), identity.NewID(), identity.NewID()
	createPlanRows(t, db, tenant, submissionID, preparationID, "BUILDING", "authoritative")
	scheduler, err := Setup(Dependencies{DB: db, Frames: framesStub{}, Matcher: matcherStub{}, Verifier: matcherStub{}})
	if err != nil {
		t.Fatal(err)
	}
	event := eventsEnvelope("media.video_prepared.v1", json.RawMessage(`{"mediaId":"`+lookupPreparationMedia(t, db, preparationID).String()+`","preparationId":"`+preparationID.String()+`","manifestDigest":"different"}`), tenant)
	err = scheduler.Handle(context.Background(), db, event)
	if !errors.Is(err, messaging.ErrPermanent) {
		t.Fatalf("expected permanent digest mismatch, got %v", err)
	}
	var plan database.VideoPlan
	if err := db.First(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if plan.Status != "FAILED" || plan.PlanDigest != "" {
		t.Fatalf("plan status=%s digest=%q; want FAILED and unsealed", plan.Status, plan.PlanDigest)
	}
}

func TestPreparedBeforeReadyIsDeferredWithoutPlan(t *testing.T) {
	db := schedulePlanTestDB(t)
	tenant, preparationID, mediaID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec("INSERT INTO media.video_preparation_jobs (id,tenant_id,media_id,status,manifest_digest,pending_object_keys,next_attempt_at,created_at) VALUES (?,?,?,?,?,?,?,?)", preparationID, tenant, mediaID, "QUEUED", "pending", []byte("[]"), time.Now(), time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	scheduler, err := Setup(Dependencies{DB: db, Frames: framesStub{}, Matcher: matcherStub{}, Verifier: matcherStub{}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.VideoPreparedPayload{MediaID: mediaID, PreparationID: preparationID, ManifestDigest: "pending"})
	err = scheduler.Handle(context.Background(), db, eventsEnvelope("media.video_prepared.v1", payload, tenant))
	if !errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, messaging.ErrPermanent) {
		t.Fatalf("expected retryable defer, got %v", err)
	}
	var count int64
	if err := db.Model(&database.VideoPlan{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("deferred preparation created %d plans", count)
	}
}

func TestUnknownSubmissionIsTerminal(t *testing.T) {
	db := schedulePlanTestDB(t)
	scheduler, err := Setup(Dependencies{DB: db, Frames: framesStub{}, Matcher: matcherStub{}, Verifier: matcherStub{}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(videoSubmitted{DraftID: identity.NewID(), SubmissionID: identity.NewID(), ResponsibilityID: identity.NewID(), MediaID: identity.NewID(), PreparationStatus: "READY"})
	err = scheduler.Handle(context.Background(), db, eventsEnvelope("capture.video_submitted.v1", payload, identity.NewID()))
	if !errors.Is(err, messaging.ErrPermanent) || !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("unknown submission should be terminal: %v", err)
	}
}

func TestDuplicateSubmissionPreservesPlanGeneration(t *testing.T) {
	db := schedulePlanTestDB(t)
	tenant, submissionID, preparationID := identity.NewID(), identity.NewID(), identity.NewID()
	seedReadySubmission(t, db, tenant, submissionID, preparationID)
	scheduler, err := Setup(Dependencies{DB: db, Frames: framesStub{}, Matcher: matcherStub{}, Verifier: matcherStub{}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(videoSubmitted{DraftID: identity.NewID(), SubmissionID: submissionID, ResponsibilityID: lookupResponsibility(t, db), MediaID: lookupMedia(t, db), PreparationStatus: "READY"})
	event := eventsEnvelope("capture.video_submitted.v1", payload, tenant)
	if err := scheduler.Handle(context.Background(), db, event); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Handle(context.Background(), db, event); err != nil {
		t.Fatal(err)
	}
	var plans []database.VideoPlan
	if err := db.Find(&plans).Error; err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].Generation != 1 {
		t.Fatalf("duplicate changed plan generation: %+v", plans)
	}
}

func TestFinishFailureSchedulesBackoffAndTerminalProvider(t *testing.T) {
	delays := []time.Duration{10 * time.Second, time.Minute, 5 * time.Minute}
	for attempt, delay := range delays {
		db := schedulePlanTestDB(t)
		now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
		tenant, planID, submissionID, prepID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
		plan := database.VideoPlan{ID: planID, TenantID: tenant, SubmissionID: submissionID, CurrentPreparationID: prepID, Generation: 1, Status: "BUILDING", Attempts: attempt + 1, NextAttemptAt: now, LeaseToken: idPtr(identity.NewID())}
		if err := insertVideoPlan(db, plan); err != nil {
			t.Fatal(err)
		}
		scheduler, err := Setup(Dependencies{DB: db, Frames: framesStub{}, Matcher: matcherStub{}, Verifier: matcherStub{}, Now: func() time.Time { return now }, Within: sqliteWithin(db)})
		if err != nil {
			t.Fatal(err)
		}
		if err := scheduler.finishFailure(context.Background(), plan, *plan.LeaseToken, errors.New("temporary")); err != nil {
			t.Fatal(err)
		}
		var got database.VideoPlan
		if err := db.First(&got, "id = ?", planID).Error; err != nil {
			t.Fatal(err)
		}
		if got.Status != "BUILDING" || !got.NextAttemptAt.Equal(now.Add(delay)) {
			t.Fatalf("attempt %d backoff state=%s next=%s", attempt+1, got.Status, got.NextAttemptAt)
		}
	}
	db := schedulePlanTestDB(t)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	plan := database.VideoPlan{ID: identity.NewID(), TenantID: identity.NewID(), SubmissionID: identity.NewID(), CurrentPreparationID: identity.NewID(), Generation: 1, Status: "BUILDING", Attempts: 4, NextAttemptAt: now, LeaseToken: idPtr(identity.NewID())}
	if err := insertVideoPlan(db, plan); err != nil {
		t.Fatal(err)
	}
	scheduler, _ := Setup(Dependencies{DB: db, Frames: framesStub{}, Matcher: matcherStub{}, Verifier: matcherStub{}, Now: func() time.Time { return now }, Within: sqliteWithin(db)})
	if err := scheduler.finishFailure(context.Background(), plan, *plan.LeaseToken, errors.New("temporary")); err != nil {
		t.Fatal(err)
	}
	var exhausted database.VideoPlan
	if err := db.First(&exhausted, "id = ?", plan.ID).Error; err != nil {
		t.Fatal(err)
	}
	if exhausted.Status != "FAILED" {
		t.Fatalf("fourth transient failure status=%s; want FAILED", exhausted.Status)
	}

	db = schedulePlanTestDB(t)
	now = time.Now().UTC()
	plan = database.VideoPlan{ID: identity.NewID(), TenantID: identity.NewID(), SubmissionID: identity.NewID(), CurrentPreparationID: identity.NewID(), Generation: 1, Status: "BUILDING", Attempts: 1, NextAttemptAt: now, LeaseToken: idPtr(identity.NewID())}
	if err := insertVideoPlan(db, plan); err != nil {
		t.Fatal(err)
	}
	scheduler, _ = Setup(Dependencies{DB: db, Frames: framesStub{}, Matcher: matcherStub{}, Verifier: matcherStub{}, Now: func() time.Time { return now }, Within: sqliteWithin(db)})
	if err := scheduler.finishFailure(context.Background(), plan, *plan.LeaseToken, buildplan.ErrProviderTerminal); err != nil {
		t.Fatal(err)
	}
	var got database.VideoPlan
	if err := db.First(&got, "id = ?", plan.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != "FAILED" || got.ErrorCode != "VIDEO_MATCH_CONFIGURATION" {
		t.Fatalf("terminal provider state=%s code=%s", got.Status, got.ErrorCode)
	}
}

func TestRunDueDoesNotSealWhenLeaseIsLost(t *testing.T) {
	db := schedulePlanTestDB(t)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	tenant, planID, submissionID, prepID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	inspectionID, assetID, scopeID, frameID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO analysis.video_plans (id,tenant_id,inspection_id,submission_id,current_preparation_id,generation,status,plan_digest,attempts,next_attempt_at,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)", []any{planID, tenant, inspectionID, submissionID, prepID, 1, "BUILDING", "", 0, now, now}},
		{"INSERT INTO inspections.inspections (id,tenant_id,asset_id,media_kind) VALUES (?,?,?,?)", []any{inspectionID, tenant, assetID, "VIDEO"}},
		{"INSERT INTO capture.submission_versions (id,tenant_id,responsibility_id,payload,payload_digest) VALUES (?,?,?,?,?)", []any{submissionID, tenant, identity.NewID(), []byte(`{"mediaKind":"VIDEO"}`), "d"}},
		{"INSERT INTO media.video_preparation_jobs (id,tenant_id,media_id,submission_id,status,generation,manifest_digest,pending_object_keys,next_attempt_at,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)", []any{prepID, tenant, identity.NewID(), submissionID, "READY", 1, "d", []byte("[]"), now, now}},
		{"INSERT INTO media.video_scope_items (id,tenant_id,preparation_id,created_at) VALUES (?,?,?,?)", []any{scopeID, tenant, prepID, now}},
	} {
		if err := db.Exec(stmt.query, stmt.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	frames := oneFrameReader{frames: []buildplan.Frame{{ID: frameID, TenantID: tenant, AssetID: assetID, PreparationID: prepID, Generation: 1, Digest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Component: scopeID.String(), Visible: true}}}
	scheduler, err := Setup(Dependencies{DB: db, ClaimDB: db, Frames: frames, Matcher: matcherStub{}, Verifier: matcherStub{}, Plans: leaseStealingWriter{db: db}, Now: func() time.Time { return now }, Within: sqliteWithin(db)})
	if err != nil {
		t.Fatal(err)
	}
	worked, err := scheduler.RunDue(context.Background())
	if !worked || !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("RunDue = %t, %v; want lease lost", worked, err)
	}
	var got database.VideoPlan
	if err := db.First(&got, "id = ?", planID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != "BUILDING" || got.PlanDigest != "" {
		t.Fatalf("lost lease plan was sealed: status=%s digest=%q", got.Status, got.PlanDigest)
	}
}

func TestRunDueRetriesTransientMatcherWithFirstBackoff(t *testing.T) {
	db := schedulePlanTestDB(t)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	tenant, planID, submissionID := identity.NewID(), identity.NewID(), identity.NewID()
	currentPrep, originPrep, referenceID := identity.NewID(), identity.NewID(), identity.NewID()
	inspectionID, assetID, scopeID := identity.NewID(), identity.NewID(), identity.NewID()
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO analysis.video_plans (id,tenant_id,inspection_id,submission_id,reference_version_id,origin_preparation_id,current_preparation_id,generation,status,plan_digest,attempts,next_attempt_at,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)", []any{planID, tenant, inspectionID, submissionID, referenceID, originPrep, currentPrep, 1, "BUILDING", "", 0, now, now}},
		{"INSERT INTO inspections.inspections (id,tenant_id,asset_id,media_kind) VALUES (?,?,?,?)", []any{inspectionID, tenant, assetID, "VIDEO"}},
		{"INSERT INTO capture.submission_versions (id,tenant_id,responsibility_id,payload,payload_digest) VALUES (?,?,?,?,?)", []any{submissionID, tenant, identity.NewID(), []byte(`{"mediaKind":"VIDEO"}`), "d"}},
		{"INSERT INTO media.video_preparation_jobs (id,tenant_id,media_id,submission_id,status,generation,manifest_digest,pending_object_keys,next_attempt_at,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)", []any{currentPrep, tenant, identity.NewID(), submissionID, "READY", 1, "d", []byte("[]"), now, now}},
		{"INSERT INTO media.video_preparation_jobs (id,tenant_id,media_id,status,generation,manifest_digest,pending_object_keys,next_attempt_at,created_at) VALUES (?,?,?,?,?,?,?,?,?)", []any{originPrep, tenant, identity.NewID(), "READY", 1, "d", []byte("[]"), now, now}},
		{"INSERT INTO media.video_scope_items (id,tenant_id,preparation_id,created_at) VALUES (?,?,?,?)", []any{scopeID, tenant, originPrep, now}},
	} {
		if err := db.Exec(stmt.query, stmt.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	frames := oneFrameReader{frames: []buildplan.Frame{
		{ID: identity.NewID(), TenantID: tenant, AssetID: assetID, PreparationID: currentPrep, Generation: 1, Digest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Component: scopeID.String(), Visible: true},
		{ID: identity.NewID(), TenantID: tenant, AssetID: assetID, PreparationID: originPrep, Generation: 1, Digest: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", Component: scopeID.String(), Visible: true},
	}}
	scheduler, err := Setup(Dependencies{DB: db, ClaimDB: db, Frames: frames, Matcher: failingMatcher{}, Verifier: failingMatcher{}, Now: func() time.Time { return now }, Within: sqliteWithin(db)})
	if err != nil {
		t.Fatal(err)
	}
	worked, err := scheduler.RunDue(context.Background())
	if !worked || err != nil {
		t.Fatalf("transient matcher run = %t, %v", worked, err)
	}
	var got database.VideoPlan
	if err := db.First(&got, "id = ?", planID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != "BUILDING" || got.Attempts != 1 || !got.NextAttemptAt.Equal(now.Add(10*time.Second)) || got.ErrorCode != "VIDEO_PLAN_RETRYABLE" {
		t.Fatalf("transient matcher state: status=%s attempts=%d next=%s code=%s", got.Status, got.Attempts, got.NextAttemptAt, got.ErrorCode)
	}
}

func TestHandleRejectsMalformedSubmissionPermanently(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	scheduler, err := Setup(Dependencies{DB: db, Frames: framesStub{}, Matcher: matcherStub{}, Verifier: matcherStub{}})
	if err != nil {
		t.Fatal(err)
	}
	err = scheduler.Handle(context.Background(), db, eventsEnvelope("capture.video_submitted.v1", json.RawMessage(`{"submissionId":"bad"}`)))
	if !errors.Is(err, messaging.ErrPermanent) || !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("malformed message should be terminal: %v", err)
	}
}

func eventsEnvelope(eventType string, payload json.RawMessage, tenants ...identity.ID) events.RawEnvelope {
	tenant := identity.NewID()
	if len(tenants) > 0 {
		tenant = tenants[0]
	}
	return events.RawEnvelope{Type: eventType, TenantID: tenant, Payload: payload}
}

func schedulePlanTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"media", "capture", "inspections", "analysis"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		"CREATE TABLE media.video_preparation_jobs (id TEXT, tenant_id TEXT, media_id TEXT, submission_id TEXT, status TEXT, generation INTEGER, manifest_digest TEXT, pending_object_keys BLOB, next_attempt_at DATETIME, created_at DATETIME)",
		"CREATE TABLE media.video_scope_items (id TEXT, tenant_id TEXT, preparation_id TEXT, created_at DATETIME)",
		"CREATE TABLE media.media_objects (id TEXT, tenant_id TEXT, responsibility_id TEXT, media_kind TEXT)",
		"CREATE TABLE capture.submission_versions (id TEXT, tenant_id TEXT, draft_id TEXT, responsibility_id TEXT, version_number INTEGER, complete NUMERIC, payload BLOB, payload_digest TEXT, submitted_at DATETIME)",
		"CREATE TABLE capture.capture_drafts (id TEXT, tenant_id TEXT, responsibility_id TEXT, kind TEXT, media_kind TEXT)",
		"CREATE TABLE inspections.inspections (id TEXT, tenant_id TEXT, asset_id TEXT, media_kind TEXT)",
		"CREATE TABLE inspections.responsibilities (id TEXT, tenant_id TEXT, inspection_id TEXT)",
		"CREATE TABLE inspections.reference_snapshots (id TEXT, tenant_id TEXT, inspection_id TEXT, reference_version_id TEXT, comparison_mode TEXT, payload BLOB, created_at DATETIME)",
		"CREATE TABLE analysis.video_plans (id TEXT, tenant_id TEXT, inspection_id TEXT, submission_id TEXT, parent_plan_id TEXT, reference_version_id TEXT, origin_preparation_id TEXT, current_preparation_id TEXT, generation INTEGER, plan_digest TEXT, status TEXT, lease_token TEXT, lease_until DATETIME, attempts INTEGER, next_attempt_at DATETIME, error_code TEXT, client_mutation_id TEXT, expected_plan_id TEXT, sealed_at DATETIME, created_at DATETIME, UNIQUE(tenant_id,submission_id,generation))",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func createPlanRows(t *testing.T, db *gorm.DB, tenant, submissionID, preparationID identity.ID, status, manifest string) {
	t.Helper()
	mediaID := identity.NewID()
	if err := db.Exec("INSERT INTO media.video_preparation_jobs (id,tenant_id,media_id,submission_id,status,manifest_digest,pending_object_keys,next_attempt_at,created_at) VALUES (?,?,?,?,?,?,?,?,?)", preparationID, tenant, mediaID, submissionID, "READY", manifest, []byte("[]"), time.Now(), time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO analysis.video_plans (id,tenant_id,submission_id,current_preparation_id,generation,status,plan_digest,next_attempt_at) VALUES (?,?,?,?,?,?,?,?)", identity.NewID(), tenant, submissionID, preparationID, 1, status, "", time.Now()).Error; err != nil {
		t.Fatal(err)
	}
}

func seedReadySubmission(t *testing.T, db *gorm.DB, tenant, submissionID, preparationID identity.ID) {
	t.Helper()
	now := time.Now().UTC()
	inspectionID, responsibilityID, mediaID := identity.NewID(), identity.NewID(), identity.NewID()
	assetID := identity.NewID()
	if err := db.Exec("INSERT INTO inspections.inspections (id,tenant_id,asset_id,media_kind) VALUES (?,?,?,?)", inspectionID, tenant, assetID, "VIDEO").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO inspections.responsibilities (id,tenant_id,inspection_id) VALUES (?,?,?)", responsibilityID, tenant, inspectionID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO media.media_objects (id,tenant_id,responsibility_id,media_kind) VALUES (?,?,?,?)", mediaID, tenant, responsibilityID, "VIDEO").Error; err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"mediaKind": "VIDEO", "mediaId": mediaID})
	if err := db.Exec("INSERT INTO capture.submission_versions (id,tenant_id,draft_id,responsibility_id,version_number,complete,payload,payload_digest,submitted_at) VALUES (?,?,?,?,?,?,?,?,?)", submissionID, tenant, identity.NewID(), responsibilityID, 1, true, payload, "d", now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO media.video_preparation_jobs (id,tenant_id,media_id,submission_id,status,manifest_digest,pending_object_keys,next_attempt_at,created_at) VALUES (?,?,?,?,?,?,?,?,?)", preparationID, tenant, mediaID, submissionID, "READY", "d", []byte("[]"), now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO inspections.reference_snapshots (id,tenant_id,inspection_id,comparison_mode,payload,created_at) VALUES (?,?,?,?,?,?)", identity.NewID(), tenant, inspectionID, "CURRENT_ONLY", []byte(`{"comparisonMode":"CURRENT_ONLY"}`), now).Error; err != nil {
		t.Fatal(err)
	}
}

func lookupResponsibility(t *testing.T, db *gorm.DB) identity.ID {
	t.Helper()
	var row database.Responsibility
	if err := db.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row.ID
}
func lookupMedia(t *testing.T, db *gorm.DB) identity.ID {
	t.Helper()
	var row database.MediaObject
	if err := db.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row.ID
}
func idPtr(id identity.ID) *identity.ID { return &id }

func lookupPreparationMedia(t *testing.T, db *gorm.DB, preparationID identity.ID) identity.ID {
	t.Helper()
	var mediaText string
	if err := db.Raw("SELECT media_id FROM media.video_preparation_jobs WHERE id = ?", preparationID).Scan(&mediaText).Error; err != nil {
		t.Fatal(err)
	}
	mediaID, err := identity.ParseID(mediaText)
	if err != nil {
		t.Fatal(err)
	}
	return mediaID
}

func sqliteWithin(db *gorm.DB) func(context.Context, identity.ID, func(*gorm.DB) error) error {
	return func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
}

func insertVideoPlan(db *gorm.DB, plan database.VideoPlan) error {
	return db.Exec("INSERT INTO analysis.video_plans (id,tenant_id,submission_id,current_preparation_id,generation,status,plan_digest,attempts,next_attempt_at,lease_token) VALUES (?,?,?,?,?,?,?,?,?,?)", plan.ID, plan.TenantID, plan.SubmissionID, plan.CurrentPreparationID, plan.Generation, plan.Status, "", plan.Attempts, plan.NextAttemptAt, plan.LeaseToken).Error
}
