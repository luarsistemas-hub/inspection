package render_pdf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// memoryClient is a minimal in-memory object store; only Get/Put/Delete are used.
type memoryClient struct {
	mu      sync.Mutex
	objects map[string][]byte
	putErr  error
}

func (m *memoryClient) CreateMultipart(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (m *memoryClient) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "", nil
}
func (m *memoryClient) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (m *memoryClient) AbortMultipart(context.Context, string, string, string) error { return nil }
func (m *memoryClient) Head(context.Context, string, string) (objectstore.Head, error) {
	return objectstore.Head{}, nil
}
func (m *memoryClient) Get(_ context.Context, _, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.objects[key]
	if !ok {
		return nil, objectstore.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}
func (m *memoryClient) Put(_ context.Context, _, key string, reader io.Reader, _ int64, _ string) error {
	if m.putErr != nil {
		return m.putErr
	}
	data, _ := io.ReadAll(reader)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = data
	return nil
}
func (m *memoryClient) Delete(_ context.Context, _, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}
func (m *memoryClient) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.objects)
}

type testEnv struct {
	db       *gorm.DB
	client   *memoryClient
	executor *Executor
	clock    *time.Time
	tenantID identity.ID
	snapshot database.ReportSnapshot
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"reports", "inspections", "media", "messaging"} {
		if err := db.Exec(fmt.Sprintf("ATTACH DATABASE ':memory:' AS %s", schema)).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.AutoMigrate(&database.ReportSnapshot{}, &database.ReportArtifact{}); err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		`CREATE TABLE media.derivatives (id blob PRIMARY KEY, tenant_id blob, media_id blob, object_key text, kind text, sha256 text, content_type text, width integer, height integer, size_bytes integer, profile text, created_at datetime)`,
		`CREATE TABLE messaging.outbox (id blob PRIMARY KEY, tenant_id blob, type text, schema_version integer, payload blob, correlation_id text, causation_id text, status text, attempts integer, next_attempt_at datetime, claimed_at datetime, last_error text, published_at datetime, created_at datetime)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`CREATE TABLE reports.report_pdf_jobs (
		id blob PRIMARY KEY, tenant_id blob NOT NULL, snapshot_id blob NOT NULL, audience text NOT NULL, renderer_version integer NOT NULL,
		status text NOT NULL, attempts integer NOT NULL DEFAULT 0, next_attempt_at datetime NOT NULL,
		lease_token blob, lease_expires_at datetime, artifact_id blob, pending_object_key text NOT NULL DEFAULT '', last_error text NOT NULL DEFAULT '',
		created_at datetime, updated_at datetime, UNIQUE(tenant_id, snapshot_id, audience)
	)`).Error; err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	env := &testEnv{db: db, client: &memoryClient{objects: map[string][]byte{}}, clock: &clock, tenantID: identity.NewID()}
	store := objectstore.Store{Bucket: "private", Client: env.client}
	limits := DefaultLimits()
	executor, err := SetupExecutor(db, store, limits, func() time.Time { return *env.clock })
	if err != nil {
		t.Fatal(err)
	}
	env.executor = executor

	value := validPDFSnapshot()
	canonical, digest, err := reportcore.CanonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	inspectionID := identity.NewID()
	if err := db.Exec("CREATE TABLE inspections.inspections (id blob PRIMARY KEY, tenant_id blob NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("inspections.inspections").Create(map[string]any{"id": inspectionID, "tenant_id": env.tenantID}).Error; err != nil {
		t.Fatal(err)
	}
	env.snapshot = database.ReportSnapshot{ID: identity.NewID(), TenantID: env.tenantID, InspectionID: inspectionID, VersionNumber: 1, PDFRenderVersion: 1, JSONDigest: digest, CanonicalJSON: json.RawMessage(canonical), HTML: json.RawMessage(`""`), CreatedAt: clock}
	if err := db.Create(&env.snapshot).Error; err != nil {
		t.Fatal(err)
	}
	return env
}

func (e *testEnv) addJob(t *testing.T, audience, status string, attempts int) database.ReportPDFJob {
	t.Helper()
	job := database.ReportPDFJob{ID: identity.NewID(), TenantID: e.tenantID, SnapshotID: e.snapshot.ID, Audience: audience, RendererVersion: rendererVersion, Status: status, Attempts: attempts, NextAttemptAt: *e.clock, CreatedAt: *e.clock, UpdatedAt: *e.clock}
	if err := e.db.Table("reports.report_pdf_jobs").Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	return job
}

func (e *testEnv) job(t *testing.T, id identity.ID) database.ReportPDFJob {
	t.Helper()
	var job database.ReportPDFJob
	if err := e.db.Table("reports.report_pdf_jobs").Where("id=?", id).First(&job).Error; err != nil {
		t.Fatal(err)
	}
	return job
}

func (e *testEnv) outboxStatuses(t *testing.T) []string {
	t.Helper()
	var rows []database.OutboxIntent
	if err := e.db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	statuses := make([]string, 0, len(rows))
	for _, row := range rows {
		var payload struct {
			Payload struct{ Kind, Status string } `json:"payload"`
		}
		_ = json.Unmarshal(row.Payload, &payload)
		statuses = append(statuses, row.Type+":"+payload.Payload.Kind+":"+payload.Payload.Status)
	}
	return statuses
}

func TestExecutorPublishesArtifactAndReadyEventOnce(t *testing.T) {
	env := newTestEnv(t)
	job := env.addJob(t, "PDF_CUSTOMER", "QUEUED", 0)
	worked, err := env.executor.RunDue(context.Background())
	if err != nil || !worked {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
	got := env.job(t, job.ID)
	if got.Status != "READY" || got.ArtifactID == nil || got.LeaseToken != nil || got.PendingObjectKey != "" || got.Attempts != 1 {
		t.Fatalf("job after success = %+v", got)
	}
	var artifact database.ReportArtifact
	if err := env.db.First(&artifact, "snapshot_id=? AND kind='PDF_CUSTOMER'", env.snapshot.ID).Error; err != nil {
		t.Fatal(err)
	}
	if env.client.count() != 1 || !bytes.HasPrefix(env.client.objects[artifact.ObjectKey], []byte("%PDF-")) {
		t.Fatal("PDF object was not stored under the artifact key")
	}
	if statuses := env.outboxStatuses(t); len(statuses) != 1 || statuses[0] != "report.ready.v1:PDF_CUSTOMER:READY" {
		t.Fatalf("outbox = %v", statuses)
	}
	if worked, err := env.executor.RunDue(context.Background()); err != nil || worked {
		t.Fatalf("second run worked=%v err=%v", worked, err)
	}
}

func TestExecutorRetriesWithBackoffThenFailsTerminally(t *testing.T) {
	env := newTestEnv(t)
	env.client.putErr = errors.New("storage unavailable")
	job := env.addJob(t, "PDF", "QUEUED", 0)
	delays := []time.Duration{5 * time.Second, 30 * time.Second, 5 * time.Minute}
	for attempt, delay := range delays {
		if _, err := env.executor.RunDue(context.Background()); err != nil {
			t.Fatal(err)
		}
		got := env.job(t, job.ID)
		if got.Status != "QUEUED" || got.Attempts != attempt+1 || got.PendingObjectKey != "" || !got.NextAttemptAt.Equal(env.clock.Add(delay)) {
			t.Fatalf("attempt %d job = %+v", attempt+1, got)
		}
		if worked, _ := env.executor.RunDue(context.Background()); worked {
			t.Fatal("job ran before its backoff elapsed")
		}
		*env.clock = env.clock.Add(delay)
	}
	if _, err := env.executor.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := env.job(t, job.ID)
	if got.Status != "FAILED" || got.Attempts != 4 {
		t.Fatalf("terminal job = %+v", got)
	}
	if statuses := env.outboxStatuses(t); len(statuses) != 1 || statuses[0] != "report.ready.v1:PDF:FAILED" {
		t.Fatalf("outbox = %v", statuses)
	}
	if env.client.count() != 0 {
		t.Fatal("failed attempts left objects behind")
	}
}

func TestExecutorMarksInvalidSnapshotPermanentWithoutRetry(t *testing.T) {
	env := newTestEnv(t)
	if err := env.db.Model(&env.snapshot).Update("json_digest", "bad").Error; err != nil {
		t.Fatal(err)
	}
	job := env.addJob(t, "PDF", "QUEUED", 0)
	if _, err := env.executor.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := env.job(t, job.ID); got.Status != "FAILED" || got.LastError != "document_invalid_or_limit_exceeded" {
		t.Fatalf("job = %+v", got)
	}
}

func TestExecutorRecoversExpiredReservationAndRemovesPreviousObject(t *testing.T) {
	env := newTestEnv(t)
	job := env.addJob(t, "PDF", "PROCESSING", 1)
	stale := identity.NewID()
	env.client.objects["tenant/stale-object"] = []byte("%PDF-old")
	expired := env.clock.Add(-time.Second)
	if err := env.db.Table("reports.report_pdf_jobs").Where("id=?", job.ID).Updates(map[string]any{"lease_token": stale, "lease_expires_at": expired, "pending_object_key": "tenant/stale-object"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := env.executor.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := env.job(t, job.ID)
	if got.Status != "READY" || got.Attempts != 2 {
		t.Fatalf("job = %+v", got)
	}
	if _, ok := env.client.objects["tenant/stale-object"]; ok || env.client.count() != 1 {
		t.Fatal("interrupted attempt's object was not discarded")
	}
}

func TestExecutorPublishesNothingWithoutOwnedReservation(t *testing.T) {
	env := newTestEnv(t)
	job := env.addJob(t, "PDF", "PROCESSING", 1)
	live := identity.NewID()
	lease := env.clock.Add(time.Minute)
	if err := env.db.Table("reports.report_pdf_jobs").Where("id=?", job.ID).Updates(map[string]any{"lease_token": live, "lease_expires_at": lease}).Error; err != nil {
		t.Fatal(err)
	}
	env.client.objects["tenant/stale"] = []byte("%PDF-")
	stale := reservation{job: job, token: identity.NewID()}
	err := env.executor.finishSuccess(context.Background(), stale, env.snapshot, "tenant/stale", "digest", 5, 1)
	if !errors.Is(err, errNotPublished) {
		t.Fatalf("stale attempt err = %v", err)
	}
	if got := env.job(t, job.ID); got.Status != "PROCESSING" || got.ArtifactID != nil {
		t.Fatalf("stale attempt altered the job: %+v", got)
	}
	var artifacts int64
	env.db.Model(&database.ReportArtifact{}).Count(&artifacts)
	if artifacts != 0 || env.client.count() != 0 {
		t.Fatalf("stale attempt published: artifacts=%d objects=%d", artifacts, env.client.count())
	}
}

func TestExecutorTreatsPurgedJobAsCanceledDuringGeneration(t *testing.T) {
	env := newTestEnv(t)
	job := env.addJob(t, "PDF", "QUEUED", 0)
	claimed, handled, err := env.executor.reserve(context.Background())
	if err != nil || claimed == nil || !handled {
		t.Fatalf("reserve = %v %v %v", claimed, handled, err)
	}
	if err := env.db.Table("reports.report_pdf_jobs").Where("id=?", job.ID).Delete(&database.ReportPDFJob{}).Error; err != nil {
		t.Fatal(err)
	}
	env.client.objects["tenant/generated"] = []byte("%PDF-")
	if err := env.executor.finishSuccess(context.Background(), *claimed, env.snapshot, "tenant/generated", "digest", 5, 1); !errors.Is(err, errNotPublished) {
		t.Fatalf("finishSuccess err = %v", err)
	}
	if env.client.count() != 0 {
		t.Fatal("object of purged job was kept")
	}
	if err := env.executor.finishFailure(context.Background(), *claimed, errPermanentPDF, false, *env.clock); err != nil {
		t.Fatalf("finishFailure for purged job = %v", err)
	}
}

func TestExecutorFinalizesEvenWhenWorkContextIsCanceled(t *testing.T) {
	env := newTestEnv(t)
	job := env.addJob(t, "PDF", "QUEUED", 0)
	claimed, _, err := env.executor.reserve(context.Background())
	if err != nil || claimed == nil {
		t.Fatalf("reserve = %v %v", claimed, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := env.executor.finishFailure(ctx, *claimed, context.Canceled, true, *env.clock); err != nil {
		t.Fatal(err)
	}
	if got := env.job(t, job.ID); got.Status != "QUEUED" || got.LeaseToken != nil {
		t.Fatalf("job after canceled work context = %+v", got)
	}
}

func TestExecutorFailsExhaustedExpiredReservationWithEvent(t *testing.T) {
	env := newTestEnv(t)
	job := env.addJob(t, "PDF", "PROCESSING", 4)
	expired := env.clock.Add(-time.Second)
	if err := env.db.Table("reports.report_pdf_jobs").Where("id=?", job.ID).Updates(map[string]any{"lease_token": identity.NewID(), "lease_expires_at": expired}).Error; err != nil {
		t.Fatal(err)
	}
	if worked, err := env.executor.RunDue(context.Background()); err != nil || worked {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
	if got := env.job(t, job.ID); got.Status != "FAILED" || got.LastError != "attempts_exhausted" {
		t.Fatalf("job = %+v", got)
	}
	if statuses := env.outboxStatuses(t); len(statuses) != 1 || statuses[0] != "report.ready.v1:PDF:FAILED" {
		t.Fatalf("outbox = %v", statuses)
	}
}

func TestLoadDisplayAssetsVerifiesDigestOfBytesRead(t *testing.T) {
	env := newTestEnv(t)
	data := jpegBytes(t, 40, 30)
	mediaID := identity.NewID()
	key, hash := objectstore.DerivativeKey(env.tenantID, mediaID, "DISPLAY", data)
	env.client.objects[key] = data
	derivative := database.MediaDerivative{ID: identity.NewID(), TenantID: env.tenantID, MediaID: mediaID, ObjectKey: key, Kind: "DISPLAY", SHA256: hash, ContentType: "image/jpeg", Width: 40, Height: 30, SizeBytes: int64(len(data))}
	if err := env.db.Create(&derivative).Error; err != nil {
		t.Fatal(err)
	}
	value := validPDFSnapshot()
	value.Evidence = []reportcore.Evidence{{ID: mediaID.String(), RequirementKey: "roof", Role: "CURRENT", DisplayDigest: hash}}
	assets, err := env.executor.loadDisplayAssets(context.Background(), env.tenantID, value)
	if err != nil || len(assets["evidence-"+mediaID.String()+".jpg"]) != len(data) {
		t.Fatalf("assets = %v err = %v", assets, err)
	}
	// The stored object no longer matches the recorded digest although the
	// metadata row still claims it does.
	corrupted := append([]byte{}, data...)
	corrupted[len(corrupted)-3] ^= 0xFF
	env.client.objects[key] = corrupted
	if _, err := env.executor.loadDisplayAssets(context.Background(), env.tenantID, value); !errors.Is(err, errPermanentPDF) {
		t.Fatalf("corrupted image err = %v", err)
	}
}

func TestSnapshotEventForPurgedSnapshotIsAcknowledged(t *testing.T) {
	env := newTestEnv(t)
	handler, err := Setup(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"snapshotId": identity.NewID().String()})
	if err := handler(context.Background(), env.db, rawEnvelope(env.tenantID, payload)); err != nil {
		t.Fatalf("purged snapshot event err = %v", err)
	}
}
