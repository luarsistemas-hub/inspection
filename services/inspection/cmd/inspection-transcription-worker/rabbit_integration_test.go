//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	processnote "inspection/services/inspection/internal/features/transcription/process_note"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/config"
	"inspection/services/inspection/internal/platform/database/migrations"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

func TestTranscriptionRabbitConsumerIT376To377AndIT386To387(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatalf("IT-376/386/387 require disposable PostgreSQL, RabbitMQ, and MinIO: %v", err)
	}
	defer h.Close()
	if err := checkTranscriptionGateway(h.LiteLLMURL, "mock"); err != nil {
		t.Fatalf("IT-386 LiteLLM health: %v", err)
	}
	t.Run("IT-376 PostgreSQL claim fencing distinguishes active and terminal jobs", func(t *testing.T) {
		tenant, job := seedClaimState(t, ctx, h)
		repository, err := processnote.NewRepository(processnote.RepositoryDependencies{DB: h.DB, Within: h.WithinTenant, Lease: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		first, err := repository.Claim(ctx, job, tenant, 1)
		if err != nil {
			t.Fatalf("first claim: %v", err)
		}
		if _, err := repository.Claim(ctx, job, tenant, 1); !errors.Is(err, processnote.ErrLeaseActive) {
			t.Fatalf("valid lease must have its own sentinel: %v", err)
		}
		if err := h.WithinTenant(ctx, tenant, func(tx *gorm.DB) error {
			return tx.Exec("UPDATE transcription.jobs SET lease_deadline=now()-interval '1 second' WHERE tenant_id=? AND id=?", tenant, job).Error
		}); err != nil {
			t.Fatal(err)
		}
		second, err := repository.Claim(ctx, job, tenant, 1)
		if err != nil {
			t.Fatalf("reclaim after lease expiry: %v", err)
		}
		if err := repository.Complete(ctx, first, "", "stale transcript", false); !errors.Is(err, processnote.ErrStaleLease) {
			t.Fatalf("old fencing token must not complete: %v", err)
		}
		if err := repository.Complete(ctx, second, "", "", true); err != nil {
			t.Fatalf("complete current fenced claim: %v", err)
		}
		if _, err := repository.Claim(ctx, job, tenant, 1); !errors.Is(err, processnote.ErrNotClaimable) {
			t.Fatalf("completed job must be terminal/unclaimable: %v", err)
		}
	})
	profile := "GEMINI_2_5_FLASH_LITE"
	t.Setenv("INSPECTION_TRANSCRIPTION_PROFILE", profile)
	if _, err := loadTranscriptionWorkerConfig(); err != nil {
		t.Fatalf("IT-386 catalog profile should be accepted: %v", err)
	}

	// Production contract shape, isolated by a unique queue prefix AND routing
	// key so a locally running worker never receives these test deliveries.
	prefix := "it-transcription-" + identity.NewID().String() + "-"
	contracts, err := messaging.TranscriptionQueueContracts(prefix)
	if err != nil {
		t.Fatal(err)
	}
	contracts[0].RoutingKey = prefix + contracts[0].RoutingKey
	contract := contracts[0]
	channel, err := h.Rabbit.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	if err := messaging.DeclareTopology(channel, contracts); err != nil {
		t.Fatal(err)
	}
	defer deleteTestQueues(t, h, contract)
	workerConfig := config.Config{SchemaMin: migrations.LatestVersion(), SchemaMax: migrations.LatestVersion()}
	readyHandler, err := newOperationalHandler(workerConfig, h.DB, h.DB, h.Rabbit, channel, nil)
	if err != nil {
		t.Fatalf("IT-386 worker readiness composition: %v", err)
	}
	assertReady := func(label string) {
		t.Helper()
		for _, path := range []string{"/healthz", "/readyz"} {
			response := httptest.NewRecorder()
			readyHandler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
			if response.Code != 200 {
				t.Fatalf("%s %s returned %d: %s", label, path, response.Code, response.Body.String())
			}
		}
	}
	assertReady("IT-386")
	if got, err := channel.QueueInspect(contract.Name); err != nil || got.Consumers != 0 {
		t.Fatalf("queue should not report a consumer before composition: consumers=%d err=%v", got.Consumers, err)
	}

	// IT-376: real PostgreSQL repository and ledger. Two production consumers
	// share the queue; the duplicate arrives while the first lease is held.
	tenant, job, note := seedClaimStateWithNote(t, ctx, h)
	repository, err := processnote.NewRepository(processnote.RepositoryDependencies{DB: h.DB, Within: h.WithinTenant, Lease: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := processnote.NewAttemptLedger(h.DB, "mock")
	if err != nil {
		t.Fatal(err)
	}
	gateway := newBlockingGateway()
	outcomes := make(chan deliveryOutcome, 8)
	handler := recordingHandler{inner: transcriptionJobProcessor{dependencies: processnote.Dependencies{Repository: repository, Audio: bytesAudio{}, Gateway: gateway, Ledger: ledger}}, outcomes: outcomes}
	consumerCtx, consumerCancel := context.WithCancel(ctx)
	defer consumerCancel()
	done := make(chan error, 2)
	for _, name := range []string{"a", "b"} {
		consumerChannel, err := h.Rabbit.Channel()
		if err != nil {
			t.Fatal(err)
		}
		defer consumerChannel.Close()
		consumer := messaging.RabbitConsumer{Channel: consumerChannel, Contract: contract, Handler: handler, ConsumerName: prefix + "processor-" + name}
		go func() { done <- consumer.Run(consumerCtx) }()
	}
	if err := h.Eventually(ctx, func() (bool, error) {
		inspect, inspectErr := channel.QueueInspect(contract.Name)
		return inspectErr == nil && inspect.Consumers == 2, inspectErr
	}); err != nil {
		t.Fatalf("IT-386 production consumers did not register: %v", err)
	}

	envelope := events.Envelope[events.TranscriptionRequestedPayload]{ID: identity.NewID(), Type: "transcription.requested.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: tenant, CorrelationID: note.String(), Payload: events.TranscriptionRequestedPayload{NoteID: note, JobID: job, Generation: 1}}
	body, _ := json.Marshal(envelope)
	publication := messaging.Publication{RoutingKey: contract.RoutingKey, Body: body, CorrelationID: envelope.CorrelationID, Mandatory: true}
	if err := h.Publisher.PublishConfirmed(ctx, publication); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gateway.started:
	case <-ctx.Done():
		t.Fatal("IT-376 first delivery never reached the model gateway")
	}
	if err := h.Publisher.PublishConfirmed(ctx, publication); err != nil {
		t.Fatal(err)
	}
	if got := awaitOutcome(t, ctx, outcomes); !errors.Is(got.err, messaging.ErrLeaseBusy) {
		t.Fatalf("IT-376 duplicate under a live lease must be deferred as lease busy: ack=%v err=%v", got.ack, got.err)
	}
	if err := h.Eventually(ctx, func() (bool, error) {
		retry, inspectErr := channel.QueueInspect(contract.RetryNames[len(contract.RetryNames)-1])
		return inspectErr == nil && retry.Messages == 1, inspectErr
	}); err != nil {
		t.Fatalf("IT-376 busy lease was not sent to the five-minute retry queue: %v", err)
	}
	close(gateway.release)
	if got := awaitOutcome(t, ctx, outcomes); !got.ack || got.err != nil {
		t.Fatalf("IT-376 first delivery did not complete: ack=%v err=%v", got.ack, got.err)
	}
	if err := h.Publisher.PublishConfirmed(ctx, publication); err != nil {
		t.Fatal(err)
	}
	if got := awaitOutcome(t, ctx, outcomes); !got.ack || got.err != nil {
		t.Fatalf("IT-376 redelivery of a completed job must be acknowledged: ack=%v err=%v", got.ack, got.err)
	}
	status, revisions, calls := jobState(t, ctx, h, tenant, job, note)
	if status != "COMPLETED" || revisions != 1 || calls != 1 || gateway.calls.Load() != 1 {
		t.Fatalf("IT-376 want one durable completion and one model transport: status=%s revisions=%d ledgerCalls=%d gatewayCalls=%d", status, revisions, calls, gateway.calls.Load())
	}

	// IT-377: invalid envelope is rejected safely into the DLQ without mutation.
	invalidEnvelope := events.Envelope[events.TranscriptionRequestedPayload]{ID: identity.NewID(), Type: "transcription.requested.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: tenant, CorrelationID: note.String(), Payload: events.TranscriptionRequestedPayload{NoteID: note, Generation: 0}}
	invalidBody, _ := json.Marshal(invalidEnvelope)
	if err := h.Publisher.PublishConfirmed(ctx, messaging.Publication{RoutingKey: contract.RoutingKey, Body: invalidBody, Mandatory: true}); err != nil {
		t.Fatal(err)
	}
	if got := awaitOutcome(t, ctx, outcomes); !errors.Is(got.err, messaging.ErrPermanent) {
		t.Fatalf("IT-377 invalid envelope must be permanent: ack=%v err=%v", got.ack, got.err)
	}
	if err := h.Eventually(ctx, func() (bool, error) {
		inspect, inspectErr := channel.QueueInspect(contract.DLQName)
		return inspectErr == nil && inspect.Messages == 1, inspectErr
	}); err != nil {
		t.Fatalf("IT-377 invalid envelope did not reach DLQ: %v", err)
	}
	if status, revisions, calls := jobState(t, ctx, h, tenant, job, note); status != "COMPLETED" || revisions != 1 || calls != 1 {
		t.Fatalf("IT-377 invalid envelope mutated state: status=%s revisions=%d calls=%d", status, revisions, calls)
	}

	// IT-387: a worker process started with an unknown profile exits non-zero
	// with a safe code, while the consumers already serving the broker (the
	// stand-ins for API/visual workers) and their readiness stay untouched.
	stderr, exitCode := runWorkerProcess(t, "INSPECTION_TRANSCRIPTION_PROFILE=UNKNOWN", "INSPECTION_DATABASE_URL=", "INSPECTION_RABBITMQ_URL=", "INSPECTION_HTTP_ADDR=127.0.0.1:0")
	if exitCode != 1 || strings.TrimSpace(stderr) != "inspection-transcription-worker exited: "+codeInvalidConfig {
		t.Fatalf("IT-387 unknown profile startup: exit=%d stderr=%q", exitCode, stderr)
	}
	if inspect, err := channel.QueueInspect(contract.Name); err != nil || inspect.Consumers != 2 {
		t.Fatalf("IT-387 failed startup disturbed running consumers: consumers=%d err=%v", inspect.Consumers, err)
	}
	assertReady("IT-387")

	consumerCancel()
	for range 2 {
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal("Rabbit consumer did not stop")
		}
	}

	// Broker loss after readiness exits with a broker code, not a startup one.
	lostChannel, err := h.Rabbit.Channel()
	if err != nil {
		t.Fatal(err)
	}
	lostDone := make(chan error, 1)
	lostConsumer := messaging.RabbitConsumer{Channel: lostChannel, Contract: contract, Handler: handler, ConsumerName: prefix + "broker-loss"}
	go func() { lostDone <- consumeUntilStopped(context.Background(), lostConsumer.Run) }()
	if err := h.Eventually(ctx, func() (bool, error) {
		inspect, inspectErr := channel.QueueInspect(contract.Name)
		return inspectErr == nil && inspect.Consumers == 1, inspectErr
	}); err != nil {
		t.Fatalf("broker-loss consumer did not register: %v", err)
	}
	_ = lostChannel.Close()
	select {
	case err := <-lostDone:
		if safeTranscriptionCode(err) != codeBrokerUnavailable {
			t.Fatalf("broker channel loss must exit with %s, got %v", codeBrokerUnavailable, err)
		}
	case <-ctx.Done():
		t.Fatal("consumer did not report broker channel loss")
	}
}

type deliveryOutcome struct {
	ack bool
	err error
}

// recordingHandler reports each delivery outcome so the test can sequence
// duplicates deterministically.
type recordingHandler struct {
	inner    messaging.DeliveryHandler
	outcomes chan<- deliveryOutcome
}

func (r recordingHandler) Process(ctx context.Context, body []byte, generation int) (bool, error) {
	ack, err := r.inner.Process(ctx, body, generation)
	r.outcomes <- deliveryOutcome{ack: ack, err: err}
	return ack, err
}

func awaitOutcome(t *testing.T, ctx context.Context, outcomes <-chan deliveryOutcome) deliveryOutcome {
	t.Helper()
	select {
	case got := <-outcomes:
		return got
	case <-ctx.Done():
		t.Fatal("delivery outcome not observed")
		return deliveryOutcome{}
	}
}

func jobState(t *testing.T, ctx context.Context, h *harness.Harness, tenant, job, note identity.ID) (string, int64, int64) {
	t.Helper()
	var status string
	var revisions, calls int64
	if err := h.WithinTenant(ctx, tenant, func(tx *gorm.DB) error {
		if err := tx.Raw("SELECT status FROM transcription.jobs WHERE tenant_id=? AND id=?", tenant, job).Row().Scan(&status); err != nil {
			return err
		}
		if err := tx.Table("transcription.transcript_revisions").Where("tenant_id=? AND note_id=?", tenant, note).Count(&revisions).Error; err != nil {
			return err
		}
		return tx.Table("usage.llm_calls").Where("tenant_id=? AND job_id=? AND comparison_mode='TRANSCRIPTION'", tenant, job).Count(&calls).Error
	}); err != nil {
		t.Fatal(err)
	}
	return status, revisions, calls
}

func deleteTestQueues(t *testing.T, h *harness.Harness, contract messaging.QueueContract) {
	t.Helper()
	channel, err := h.Rabbit.Channel()
	if err != nil {
		t.Logf("cleanup channel: %v", err)
		return
	}
	defer channel.Close()
	for _, name := range append([]string{contract.Name, contract.DLQName}, contract.RetryNames[:]...) {
		if _, err := channel.QueueDelete(name, false, false, false); err != nil {
			t.Logf("cleanup queue %s: %v", name, err)
		}
	}
}

func seedClaimState(t *testing.T, ctx context.Context, h *harness.Harness) (identity.ID, identity.ID) {
	t.Helper()
	tenant, job, _ := seedClaimStateWithNote(t, ctx, h)
	return tenant, job
}

// seedClaimStateWithNote inserts one QUEUED note/job pair for a fresh tenant.
func seedClaimStateWithNote(t *testing.T, ctx context.Context, h *harness.Harness) (identity.ID, identity.ID, identity.ID) {
	t.Helper()
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{MediaKind: "VIDEO", RequirementKey: "room-1", SchemaVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	noteID, jobID := identity.NewID(), identity.NewID()
	profile := llm.TranscriptionProfiles["GEMINI_2_5_FLASH_LITE"]
	digest := strings.Repeat("a", 64)
	err = h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Exec(`INSERT INTO transcription.audio_notes (tenant_id,id,inspection_id,responsibility_id,client_note_id,requirement_key,consent_version,disclosure_version,target_kind,video_attempt_id,start_ms,end_ms,time_basis,duration_ms,content_sha256,content_type,size_bytes,status,anchor_status,audio_deletion_status,upload_key,upload_id) VALUES (?,?,?,?,?,'room-1','voice-v1','disclosure-v1','VIDEO_INTERVAL',?,500,1500,'CAPTURE_CLOCK',1000,?,'audio/webm',8,'QUEUED','VALID','PENDING','opaque/audio','upload-proof')`, fixture.TenantID, noteID, fixture.InspectionID, fixture.ResponsibilityID, identity.NewID(), fixture.VideoAttemptID, digest).Error; err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO transcription.jobs (tenant_id,id,note_id,generation,profile_key,profile_version,profile_digest,status,attempts) VALUES (?,?,?,1,?,?,?,'QUEUED',0)`, fixture.TenantID, jobID, noteID, profile.Key, profile.Version, digest).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture.TenantID, jobID, noteID
}

type bytesAudio struct{}

func (bytesAudio) Open(context.Context, string) (io.ReadSeekCloser, error) {
	return readSeekCloser{Reader: bytes.NewReader([]byte("audio"))}, nil
}

type readSeekCloser struct{ *bytes.Reader }

func (readSeekCloser) Close() error { return nil }

// blockingGateway holds the first transcription until the test releases it.
type blockingGateway struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingGateway() *blockingGateway {
	return &blockingGateway{started: make(chan struct{}), release: make(chan struct{})}
}

func (g *blockingGateway) Transcribe(ctx context.Context, _ llm.TranscriptionRequest) (llm.TranscriptionResult, error) {
	g.calls.Add(1)
	g.once.Do(func() { close(g.started) })
	select {
	case <-g.release:
	case <-ctx.Done():
		return llm.TranscriptionResult{}, ctx.Err()
	}
	return llm.TranscriptionResult{Transcript: "áudio de teste"}, nil
}
