//go:build integration

package prepare_video

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

func TestVideoWorkerImageCommitsSyntheticClipWithoutWakeupIT584IT610(t *testing.T) {
	runVideoWorkerImage(t, false)
}

func TestVideoWorkerCancelsModelCallAndPreservesJobIT587(t *testing.T) {
	runVideoWorkerImage(t, true)
}

func runVideoWorkerImage(t *testing.T, interruptModel bool) {
	t.Helper()
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_WORKER_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL runtime and worker URLs are required")
	}
	if _, err := os.Stat("/usr/local/bin/inspection-video-worker"); err != nil {
		t.Fatalf("target video-worker image is required: %v", err)
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "worker.mp4")
	command := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=96x64:rate=5:duration=1", "-threads", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate real video: %v: %s", err, output)
	}
	clip, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(clip)
	digest := hex.EncodeToString(sum[:])
	now := time.Now().UTC().Truncate(time.Microsecond)
	attemptID, mediaID, submissionID, preparationID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	objectKey := "qa-video/" + fixture.TenantID.String() + "/" + mediaID.String() + ".mp4"
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, objectKey, bytes.NewReader(clip), int64(len(clip)), "video/mp4"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = h.Store.Client.(objectstore.Deleter).Delete(context.Background(), h.Store.Bucket, objectKey)
	})
	err = h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "worker-" + mediaID.String(), PayloadDigest: digest, AdmittedAt: now, ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: objectKey, ContentType: "video/mp4", SHA256: digest, SizeBytes: int64(len(clip)), Status: "RECEIVED", MediaKind: "VIDEO", VideoAttemptID: &attemptID, RequirementKey: "video:main", IdempotencyKey: "worker-media-" + mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: fixture.TenantID, DraftID: fixture.DraftID, ResponsibilityID: fixture.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: json.RawMessage(`{}`), PayloadDigest: digest, SubmittedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, SubmissionID: &submissionID, Status: "QUEUED", Phase: "RECEIVED", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, PendingObjectKeys: []byte("[]"), CreatedAt: now}).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	modelEntered := make(chan struct{}, 1)
	modelCanceled := make(chan struct{}, 1)
	mux.HandleFunc("/__admin/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, request *http.Request) {
		if interruptModel {
			_, _ = io.Copy(io.Discard, request.Body)
			select {
			case modelEntered <- struct{}{}:
			default:
			}
			<-request.Context().Done()
			modelCanceled <- struct{}{}
			return
		}
		var body struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || len(body.Messages) < 2 {
			http.Error(w, "invalid model request", http.StatusBadRequest)
			return
		}
		var observations []map[string]any
		var contentItems []json.RawMessage
		if err := json.Unmarshal(body.Messages[1].Content, &contentItems); err != nil {
			http.Error(w, "invalid image request", http.StatusBadRequest)
			return
		}
		for _, raw := range contentItems {
			var item struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(raw, &item) != nil || !strings.HasPrefix(item.Text, "evidenceId=") {
				continue
			}
			id := strings.TrimPrefix(strings.SplitN(item.Text, ";", 2)[0], "evidenceId=")
			observations = append(observations, map[string]any{"frameId": id, "environmentCandidate": "kitchen", "componentLabel": "sink", "viewLabel": "wide", "visibility": "VISIBLE", "landmarks": []string{}})
		}
		content, _ := json.Marshal(map[string]any{"observations": observations, "unresolvedFrameIds": []string{}})
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "qa-scope", "model": "qa-scope", "choices": []any{map[string]any{"message": map[string]any{"content": string(content)}}}})
	})
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if server.Shutdown(shutdownCtx) != nil {
			_ = server.Close()
		}
	})
	port, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	workerPort := port.Addr().String()
	_ = port.Close()
	worker := exec.Command("/usr/local/bin/inspection-video-worker")
	worker.WaitDelay = time.Second
	worker.Env = append(os.Environ(),
		"INSPECTION_ENV=local", "INSPECTION_DATABASE_URL="+os.Getenv("INSPECTION_TEST_WORKER_DATABASE_URL"), "INSPECTION_RUNTIME_DATABASE_URL="+os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL"),
		"INSPECTION_RABBITMQ_URL="+os.Getenv("INSPECTION_TEST_RABBITMQ_URL"), "INSPECTION_MINIO_ENDPOINT="+os.Getenv("INSPECTION_TEST_MINIO_ENDPOINT"),
		"INSPECTION_MINIO_ACCESS_KEY="+os.Getenv("INSPECTION_TEST_MINIO_ACCESS_KEY"), "INSPECTION_MINIO_SECRET_KEY="+os.Getenv("INSPECTION_TEST_MINIO_SECRET_KEY"), "INSPECTION_MINIO_BUCKET="+os.Getenv("INSPECTION_TEST_MINIO_BUCKET"),
		"INSPECTION_LITELLM_URL=http://"+listener.Addr().String(), "INSPECTION_LLM_MODE=mock", "INSPECTION_IMAGE_VALIDATION_ENABLED=false", "INSPECTION_HTTP_ADDR="+workerPort,
		"INSPECTION_ALLOWED_ORIGINS=http://localhost:3000", "INSPECTION_OIDC_ISSUER=http://keycloak:8080/realms/inspection", "INSPECTION_OIDC_AUDIENCES=inspection-api", "INSPECTION_SUPER_ADMIN_ISSUER=qa", "INSPECTION_SUPER_ADMIN_SUBJECT=qa", "INSPECTION_SUPER_ADMIN_PASSWORD=qa",
	)
	var workerLog bytes.Buffer
	worker.Stdout, worker.Stderr = &workerLog, &workerLog
	if err := worker.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if worker.ProcessState == nil {
			_ = worker.Process.Kill()
			_ = worker.Wait()
		}
	})
	deadline := time.Now().Add(25 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		response, err := http.Get("http://" + workerPort + "/health")
		if err == nil {
			_ = response.Body.Close()
			ready = response.StatusCode == http.StatusOK
			if ready {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("worker did not become ready: %s", workerLog.String())
	}
	if interruptModel {
		select {
		case <-modelEntered:
		case <-time.After(20 * time.Second):
			t.Fatalf("worker did not reach the model call: %s", workerLog.String())
		}
		stopWorkerWithinBound(t, worker, &workerLog)
		select {
		case <-modelCanceled:
		case <-time.After(2 * time.Second):
			t.Fatal("model request was not canceled during worker shutdown")
		}
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			var job database.VideoPreparationJob
			if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, preparationID).Take(&job).Error; err != nil {
				return err
			}
			if job.Status == "READY" || job.ManifestDigest != "" || (job.Status != "QUEUED" && (job.Status != "PROCESSING" || job.LeaseExpiresAt == nil)) {
				t.Errorf("interrupted job is not recoverable: status=%s manifest=%q lease=%v", job.Status, job.ManifestDigest, job.LeaseExpiresAt)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return
	}
	completed := false
	for time.Now().Before(deadline) {
		var status, manifest string
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Model(&database.VideoPreparationJob{}).Select("status,manifest_digest").Where("tenant_id=? AND id=?", fixture.TenantID, preparationID).Row().Scan(&status, &manifest)
		}); err != nil {
			t.Fatal(err)
		}
		if status == "READY" && manifest != "" {
			completed = true
			break
		}
		if status == "FAILED" {
			t.Fatalf("worker failed to prepare clip: %s", workerLog.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !completed {
		t.Fatalf("worker did not commit synthetic clip: %s", workerLog.String())
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, preparationID).Take(&job).Error; err != nil {
			return err
		}
		var frames, scopes, preparedEvents, scopeCalls int64
		for _, check := range []struct {
			model any
			query string
			args  []any
			count *int64
		}{
			{&database.VideoFrame{}, "tenant_id=? AND preparation_id=?", []any{fixture.TenantID, preparationID}, &frames},
			{&database.VideoScopeItem{}, "tenant_id=? AND preparation_id=?", []any{fixture.TenantID, preparationID}, &scopes},
			{&database.OutboxIntent{}, "tenant_id=? AND type='media.video_prepared.v1' AND correlation_id=?", []any{fixture.TenantID, preparationID.String()}, &preparedEvents},
			{&database.LLMCallRecord{}, "tenant_id=? AND job_id=? AND comparison_mode='VIDEO_SCOPE' AND state='FINISHED'", []any{fixture.TenantID, preparationID}, &scopeCalls},
		} {
			if err := tx.Model(check.model).Where(check.query, check.args...).Count(check.count).Error; err != nil {
				return err
			}
		}
		if job.Attempts != 1 || frames == 0 || scopes != 1 || preparedEvents != 1 || scopeCalls != 1 {
			t.Errorf("polling produced attempts=%d frames=%d scopes=%d events=%d scopeCalls=%d", job.Attempts, frames, scopes, preparedEvents, scopeCalls)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stopWorkerWithinBound(t, worker, &workerLog)
}

func stopWorkerWithinBound(t *testing.T, worker *exec.Cmd, workerLog *bytes.Buffer) {
	t.Helper()
	if err := worker.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- worker.Wait() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("worker shutdown: %v: %s", err, workerLog.String())
		}
	case <-time.After(30 * time.Second):
		_ = worker.Process.Kill()
		t.Fatalf("worker exceeded shutdown bound: %s", workerLog.String())
	}
}
