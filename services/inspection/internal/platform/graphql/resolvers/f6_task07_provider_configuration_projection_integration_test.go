//go:build integration

package resolvers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/media/prepare_video"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/sensitivecontent"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

type gatedAPI08Decoder struct {
	inner   video.Decoder
	entered chan struct{}
	release chan struct{}
	phase   func() bool
	gate    sync.Once
	open    sync.Once
}

type api08SuccessfulScopeGateway struct{}

func (api08SuccessfulScopeGateway) CompleteStructured(_ context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
	observations := make([]map[string]any, 0, len(request.Images))
	for _, frame := range request.Images {
		observations = append(observations, map[string]any{"frameId": frame.EvidenceID, "environmentCandidate": "kitchen", "componentLabel": "sink", "viewLabel": "wide", "visibility": "VISIBLE", "landmarks": []string{}})
	}
	payload, err := json.Marshal(map[string]any{"observations": observations, "unresolvedFrameIds": []string{}})
	return llm.StructuredResult{JSON: payload}, err
}

func (d *gatedAPI08Decoder) Probe(ctx context.Context, path string, limits video.Limits) (video.Probe, error) {
	return d.inner.Probe(ctx, path, limits)
}

func (d *gatedAPI08Decoder) Decode(ctx context.Context, path string, limits video.Limits, yield func(video.Frame) error) error {
	if d.phase != nil && d.phase() {
		d.gate.Do(func() { close(d.entered) })
		select {
		case <-d.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return d.inner.Decode(ctx, path, limits, yield)
}

func (d *gatedAPI08Decoder) Playback(ctx context.Context, source, target string, limits video.Limits) error {
	return d.inner.Playback(ctx, source, target, limits)
}

func (d *gatedAPI08Decoder) releaseGate() {
	d.open.Do(func() { close(d.release) })
}

func deferUnrelatedAPI08PreparationJobs(t *testing.T, fx api08Fixture, target identity.ID) {
	t.Helper()
	var queued []database.VideoPreparationJob
	if err := fx.h.AdminDB.Where("status='QUEUED' AND id<>?", target).Find(&queued).Error; err != nil {
		t.Fatal(err)
	}
	deferredUntil := time.Now().UTC().Add(48 * time.Hour)
	var processing []database.VideoPreparationJob
	if err := fx.h.AdminDB.Where("status='PROCESSING' AND id<>? AND lease_expires_at<=?", target, deferredUntil).Find(&processing).Error; err != nil {
		t.Fatal(err)
	}
	if len(queued) == 0 && len(processing) == 0 {
		return
	}
	originalQueued := make(map[identity.ID]time.Time, len(queued))
	for _, job := range queued {
		originalQueued[job.ID] = job.NextAttemptAt
	}
	if err := fx.h.AdminDB.Model(&database.VideoPreparationJob{}).Where("status='QUEUED' AND id<>?", target).Update("next_attempt_at", deferredUntil).Error; err != nil {
		t.Fatal(err)
	}
	originalLeases := make(map[identity.ID]*time.Time, len(processing))
	for _, job := range processing {
		originalLeases[job.ID] = job.LeaseExpiresAt
	}
	if err := fx.h.AdminDB.Model(&database.VideoPreparationJob{}).Where("status='PROCESSING' AND id<>? AND lease_expires_at<=?", target, deferredUntil).Update("lease_expires_at", deferredUntil).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for id, next := range originalQueued {
			if err := fx.h.AdminDB.Model(&database.VideoPreparationJob{}).Where("id=?", id).Update("next_attempt_at", next).Error; err != nil {
				t.Errorf("restore deferred preparation %s: %v", id, err)
			}
		}
		for id, lease := range originalLeases {
			if err := fx.h.AdminDB.Model(&database.VideoPreparationJob{}).Where("id=?", id).Update("lease_expires_at", lease).Error; err != nil {
				t.Errorf("restore deferred lease %s: %v", id, err)
			}
		}
	})
}

func TestVV_IT_149ProviderConfigurationFailureIsSafeAndRetryableProjection(t *testing.T) {
	fx := newAPI08Fixture(t)
	failed := seedRealAPI08VideoForExecutor(t, fx)
	deferUnrelatedAPI08PreparationJobs(t, fx, failed.preparationID)
	logs := captureAPI08Logs(t)
	executor, err := prepare_video.Setup(prepare_video.Dependencies{DB: fx.h.DB, ClaimDB: fx.h.AdminDB, Store: objectstore.VideoStore{Store: fx.h.Store}, Decoder: video.FFmpeg{Threads: 1}, Detector: sensitivecontent.NewDisabledDetector(), Gateway: api08CredentialFailureGateway{}, Now: time.Now, TempDir: os.TempDir(), Limits: video.WorkerConfig{Concurrency: 1, DecoderThreads: 1, MaxBytes: 1 << 30, ScratchBytes: 4 << 30, RequiredFreeBytes: 8 << 30, StepTimeout: time.Minute, LeaseDuration: 120 * time.Second, HeartbeatInterval: 30 * time.Second, PollInterval: 2 * time.Second, ShutdownTimeout: 30 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(45 * time.Second)
	for {
		var job database.VideoPreparationJob
		if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, failed.preparationID).Take(&job).Error; err != nil {
			t.Fatal(err)
		}
		if job.Status == "FAILED" {
			if job.ErrorCode != "PROVIDER_CONFIGURATION" {
				t.Fatalf("executor failure code=%q, want PROVIDER_CONFIGURATION", job.ErrorCode)
			}
			break
		}
		handled, runErr := executor.RunDue(fx.ctx)
		if runErr != nil {
			t.Fatalf("run real preparation executor: %v", runErr)
		}
		if !handled {
			var pending database.VideoPreparationJob
			if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, failed.preparationID).Take(&pending).Error; err != nil {
				t.Fatalf("RunDue returned handled=false for fixture job %s; load status/error_code: %v", failed.preparationID, err)
			}
			t.Fatalf("RunDue returned handled=false for fixture job %s (status=%s error_code=%s)", failed.preparationID, pending.Status, pending.ErrorCode)
		}
		select {
		case <-deadline:
			t.Fatalf("executor did not reach failing gateway; status=%s phase=%s", job.Status, job.Phase)
		default:
		}
	}
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture,
		`query { videoPreparations(status:["FAILED"]) { nodes { mediaId preparationId status stage errorCode retryable sourceAvailable } } }`, fx.ctx, "", "")
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"errorCode":"PROVIDER_CONFIGURATION"`) || !strings.Contains(response, `"retryable":true`) || !strings.Contains(response, failed.mediaID.String()) {
		t.Fatalf("safe provider failure projection = %s", response)
	}
	retried := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture,
		api08Query(fx, "retryVideoPreparation", failed.mediaID, 4, "", "vv-it-149-retry"), fx.ctx, "", "")
	if strings.Contains(retried, `"errors"`) || !strings.Contains(retried, `"status":"QUEUED"`) || !strings.Contains(retried, `"preparationVersion":5`) {
		t.Fatalf("provider configuration recovery action = %s", retried)
	}
	assertNoAPI08ProviderSecret(t, fx, failed, logs.String(), response, retried)
}

// captureAPI08Logs redirects the standard and slog loggers for one test.
func captureAPI08Logs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	previousSlog := slog.Default()
	previousWriter := log.Writer()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug})))
	log.SetOutput(&buffer)
	t.Cleanup(func() {
		slog.SetDefault(previousSlog)
		log.SetOutput(previousWriter)
	})
	return &buffer
}

// assertNoAPI08ProviderSecret searches persisted text columns of the job and
// media, the tenant outbox, captured logs and GraphQL responses for the
// provider error detail injected by api08CredentialFailureGateway.
func assertNoAPI08ProviderSecret(t *testing.T, fx api08Fixture, prep api08Preparation, logs string, responses ...string) {
	t.Helper()
	var job, media, outbox string
	if err := fx.h.AdminDB.Raw("SELECT row_to_json(j)::text FROM media.video_preparation_jobs j WHERE tenant_id=? AND id=?", fx.seed.TenantID, prep.preparationID).Scan(&job).Error; err != nil || job == "" {
		t.Fatalf("load preparation job text columns: %v", err)
	}
	if err := fx.h.AdminDB.Raw("SELECT row_to_json(m)::text FROM media.media_objects m WHERE tenant_id=? AND id=?", fx.seed.TenantID, prep.mediaID).Scan(&media).Error; err != nil || media == "" {
		t.Fatalf("load media text columns: %v", err)
	}
	if err := fx.h.AdminDB.Raw("SELECT coalesce(string_agg(row_to_json(o)::text, E'\\n'), '') FROM messaging.outbox o WHERE tenant_id=?", fx.seed.TenantID).Scan(&outbox).Error; err != nil {
		t.Fatalf("load tenant outbox: %v", err)
	}
	sources := map[string]string{"preparation job": job, "media": media, "outbox": outbox, "logs": logs}
	for index, response := range responses {
		sources["GraphQL response "+strconv.Itoa(index+1)] = response
	}
	for source, text := range sources {
		for _, secret := range []string{"qa-secret-123", "authentication failed"} {
			if strings.Contains(text, secret) {
				t.Errorf("provider credential detail %q leaked in %s: %s", secret, source, text)
			}
		}
	}
}

type api08CredentialFailureGateway struct{}

func (api08CredentialFailureGateway) CompleteStructured(context.Context, llm.StructuredRequest) (llm.StructuredResult, error) {
	return llm.StructuredResult{}, llm.NewError(llm.CodeAuthentication, 401, errors.New("qa-secret-123 authentication failed"))
}

func seedRealAPI08VideoForExecutor(t *testing.T, fx api08Fixture) api08Preparation {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credential-failure.mp4")
	command := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=96x64:rate=5:duration=1", "-threads", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create video for credential failure: %v: %s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mediaID, preparationID, attemptID := identity.NewID(), identity.NewID(), identity.NewID()
	objectKey := "vv-it-149/" + mediaID.String() + ".mp4"
	if err := fx.h.Store.Client.Put(context.Background(), fx.h.Store.Bucket, objectKey, bytes.NewReader(data), int64(len(data)), "video/mp4"); err != nil {
		t.Fatalf("store video for credential failure: %v", err)
	}
	digest := sha256.Sum256(data)
	contentDigest := hex.EncodeToString(digest[:])
	now := time.Now().UTC()
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fx.seed.TenantID, ResponsibilityID: fx.seed.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "vv-it-149-attempt", PayloadDigest: contentDigest, AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fx.seed.TenantID, ResponsibilityID: fx.seed.ResponsibilityID, ObjectKey: objectKey, ContentType: "video/mp4", SHA256: contentDigest, SizeBytes: int64(len(data)), Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "vv-it-149-media", AttentionItems: []byte("[]"), Flags: []byte("[]"), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fx.seed.TenantID, MediaID: mediaID, Status: "QUEUED", Phase: "RECEIVED", PreparationVersion: 4, Generation: 1, NextAttemptAt: now, SourceSHA256: contentDigest, ProfileDigest: video.DefaultProfile().Digest(), PendingObjectKeys: []byte("[]"), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fx.h.Store.Client.(objectstore.Deleter).Delete(context.Background(), fx.h.Store.Bucket, objectKey); err != nil {
			t.Errorf("remove credential failure video fixture: %v", err)
		}
	})
	return api08Preparation{mediaID: mediaID, preparationID: preparationID, manifest: contentDigest}
}

// TestVV_IT_147CaptureAndOperationsReadEveryPreparationPhase checks both
// GraphQL projections at RECEIVED, EXTRACTING and READY.
func TestVV_IT_147CaptureAndOperationsReadEveryPreparationPhase(t *testing.T) {
	fx := newAPI08Fixture(t)
	prep := seedRealAPI08VideoForExecutor(t, fx)
	deferUnrelatedAPI08PreparationJobs(t, fx, prep.preparationID)
	invitations, credentials := captureSessionFixture(t, fx.seed.TenantID, fx.seed.ResponsibilityID)
	captureServer := captureGraphQLServerWithDB(fx.bus, invitations, fx.h.DB)
	assertPhase := func(wantStatus, wantStage string) {
		t.Helper()
		capture := executeCaptureGraphQL(t, captureServer, `query { videoPreparation(mediaId:"`+prep.mediaID.String()+`") { mediaId status stage } }`, fx.seed.TenantID, credentials, true)
		operations := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture,
			`query { videoPreparations { nodes { mediaId status stage } } }`, fx.ctx, "", "")
		var captureData struct {
			Data struct {
				Preparation struct{ MediaID, Status, Stage string } `json:"videoPreparation"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(capture), &captureData); err != nil {
			t.Fatalf("decode Capture projection: %v; response=%s", err, capture)
		}
		if captureData.Data.Preparation.MediaID != prep.mediaID.String() || captureData.Data.Preparation.Status != wantStatus || captureData.Data.Preparation.Stage != wantStage {
			t.Errorf("Capture preparation projection=%+v, want media=%s %s/%s", captureData.Data.Preparation, prep.mediaID, wantStatus, wantStage)
		}
		var operationsData struct {
			Data struct {
				Preparations struct {
					Nodes []struct{ MediaID, Status, Stage string } `json:"nodes"`
				} `json:"videoPreparations"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(operations), &operationsData); err != nil {
			t.Fatalf("decode operations projection: %v; response=%s", err, operations)
		}
		found := false
		for _, node := range operationsData.Data.Preparations.Nodes {
			if node.MediaID == prep.mediaID.String() {
				found = true
				if node.Status != wantStatus || node.Stage != wantStage {
					t.Errorf("operations node=%+v, want %s/%s", node, wantStatus, wantStage)
				}
			}
		}
		if !found {
			t.Errorf("operations projection omitted fixture media %s: %s", prep.mediaID, operations)
		}
	}
	assertPhase("QUEUED", "RECEIVED")
	decoder := &gatedAPI08Decoder{inner: video.FFmpeg{Threads: 1}, entered: make(chan struct{}), release: make(chan struct{})}
	decoder.phase = func() bool {
		var job database.VideoPreparationJob
		return fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, prep.preparationID).Take(&job).Error == nil && job.Status == "PROCESSING" && job.Phase == "EXTRACTING"
	}
	t.Cleanup(decoder.releaseGate)
	executor, err := prepare_video.Setup(prepare_video.Dependencies{DB: fx.h.DB, ClaimDB: fx.h.AdminDB, Store: objectstore.VideoStore{Store: fx.h.Store}, Decoder: decoder, Detector: sensitivecontent.NewDisabledDetector(), Gateway: api08SuccessfulScopeGateway{}, Now: time.Now, TempDir: os.TempDir(), Limits: video.WorkerConfig{Concurrency: 1, DecoderThreads: 1, MaxBytes: 1 << 30, ScratchBytes: 4 << 30, RequiredFreeBytes: 8 << 30, StepTimeout: time.Minute, LeaseDuration: 120 * time.Second, HeartbeatInterval: 30 * time.Second, PollInterval: 2 * time.Second, ShutdownTimeout: 30 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		handled, runErr := executor.RunDue(fx.ctx)
		if runErr == nil && !handled {
			runErr = errors.New("RunDue returned handled=false with fixture preparation pending")
		}
		done <- runErr
	}()
	select {
	case <-decoder.entered:
	case <-time.After(20 * time.Second):
		t.Fatal("executor did not reach gated frame extraction")
	}
	assertPhase("PROCESSING", "EXTRACTING")
	decoder.releaseGate()
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("complete preparation: %v", runErr)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("executor did not complete fixture preparation")
	}
	assertPhase("READY", "READY")
}
