// inspection-transcription-worker owns leased audio transcription jobs.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	processnote "inspection/services/inspection/internal/features/transcription/process_note"
	"inspection/services/inspection/internal/platform/config"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/observability"
	"inspection/services/inspection/internal/platform/operational"
	runtime "inspection/services/inspection/internal/platform/runtime"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "inspection-transcription-worker exited:", safeTranscriptionCode(err))
		os.Exit(1)
	}
}

// Safe exit codes printed by main. Details stay in the wrapped error only.
const (
	codeInvalidConfig     = "INVALID_TRANSCRIPTION_CONFIG"
	codeStartupFailed     = "STARTUP_FAILED"
	codeBrokerUnavailable = "BROKER_CONNECTION_LOST"
	codeWorkerStopped     = "WORKER_STOPPED"
)

// workerFailure attaches a stable, non-sensitive exit code to a worker error.
type workerFailure struct {
	code string
	err  error
}

func (f workerFailure) Error() string    { return f.code + ": " + f.err.Error() }
func (f workerFailure) Unwrap() error    { return f.err }
func (f workerFailure) SafeCode() string { return f.code }

// maximumLease keeps the lease below the five-minute busy-lease retry, so a
// redelivery never races a live lease.
const maximumLease = 5 * time.Minute

type transcriptionWorkerConfig struct {
	timeout time.Duration
	lease   time.Duration
}

func loadTranscriptionWorkerConfig() (transcriptionWorkerConfig, error) {
	profileKey := strings.TrimSpace(os.Getenv("INSPECTION_TRANSCRIPTION_PROFILE"))
	if profileKey != "" {
		if _, err := llm.ResolveTranscriptionProfile(profileKey); err != nil {
			return transcriptionWorkerConfig{}, workerFailure{code: codeInvalidConfig, err: errors.New("invalid INSPECTION_TRANSCRIPTION_PROFILE")}
		}
	}
	timeout, err := durationEnv("INSPECTION_TRANSCRIPTION_TIMEOUT", llm.DefaultTranscriptionTimeout)
	if err != nil || timeout <= 0 {
		return transcriptionWorkerConfig{}, workerFailure{code: codeInvalidConfig, err: errors.New("invalid INSPECTION_TRANSCRIPTION_TIMEOUT")}
	}
	// process_note owns the lease formula (detector + gateway + download +
	// persistence + margin); the default lease is exactly that minimum, and any
	// override must cover it while staying below the busy-lease retry.
	minimumLease := processnote.RequiredLease(processnote.DefaultSilenceDetectorTimeout, timeout)
	lease, err := durationEnv("INSPECTION_TRANSCRIPTION_LEASE", minimumLease)
	if err != nil || lease < minimumLease || lease >= maximumLease {
		return transcriptionWorkerConfig{}, workerFailure{code: codeInvalidConfig, err: errors.New("invalid INSPECTION_TRANSCRIPTION_LEASE")}
	}
	return transcriptionWorkerConfig{timeout: timeout, lease: lease}, nil
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	return time.ParseDuration(value)
}

func run() error {
	// Validate the worker's own settings first: an invalid profile or lease
	// must fail this process before it touches any shared dependency.
	workerCfg, err := loadTranscriptionWorkerConfig()
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := checkTranscriptionGateway(cfg.LiteLLMURL, cfg.LLMMode); err != nil {
		return fmt.Errorf("transcription worker gateway is not ready: %w", err)
	}
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(binary); err != nil {
			return fmt.Errorf("transcription worker requires %s", binary)
		}
	}
	workerDB, err := database.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	runtimeURL := cfg.RuntimeDatabaseURL
	if runtimeURL == "" {
		runtimeURL = cfg.DatabaseURL
	}
	runtimeDB := workerDB
	if runtimeURL != cfg.DatabaseURL {
		runtimeDB, err = database.Open(runtimeURL)
		if err != nil {
			return err
		}
		if sqlDB, dbErr := runtimeDB.DB(); dbErr == nil {
			defer sqlDB.Close()
		}
	}
	if err := ping(workerDB); err != nil {
		return fmt.Errorf("transcription worker database is not ready: %w", err)
	}
	if err := ping(runtimeDB); err != nil {
		return fmt.Errorf("transcription worker runtime database is not ready: %w", err)
	}

	minio, err := objectstore.NewMinIOWithRegion(cfg.MinIOEndpoint, cfg.MinIOAccessKey, cfg.MinIOSecretKey, cfg.MinIOSecure, cfg.MinIORegion)
	if err != nil {
		return err
	}
	audio, err := processnote.NewAudioStore(objectstore.Store{Bucket: cfg.MinIOBucket, Client: minio})
	if err != nil {
		return err
	}
	repository, err := processnote.NewRepository(processnote.RepositoryDependencies{DB: workerDB, Lease: workerCfg.lease})
	if err != nil {
		return err
	}
	ledger, err := processnote.NewAttemptLedger(runtimeDB, cfg.LLMMode)
	if err != nil {
		return err
	}
	metrics := observability.NewMetrics()
	apiKey := ""
	if cfg.LLMMode == "live" {
		apiKey = cfg.LiteLLMAPIKey
	}
	gateway := llm.HTTPTranscriptionGateway{BaseURL: cfg.LiteLLMURL, APIKey: apiKey, Client: &http.Client{Timeout: workerCfg.timeout}}
	silence, err := processnote.NewFFmpegSilenceDetector(processnote.DefaultSilenceDetectorTimeout, "-50dB")
	if err != nil {
		return err
	}
	processor := transcriptionJobProcessor{dependencies: processnote.Dependencies{Repository: repository, Audio: audio, Gateway: gateway, Ledger: ledger, Silence: silence}}

	connection, err := amqp.Dial(cfg.RabbitMQURL)
	if err != nil {
		return fmt.Errorf("transcription worker rabbitmq: %w", err)
	}
	defer connection.Close()
	channel, err := connection.Channel()
	if err != nil {
		return fmt.Errorf("transcription worker rabbitmq channel: %w", err)
	}
	defer channel.Close()
	contracts, err := messaging.TranscriptionQueueContracts()
	if err != nil {
		return err
	}
	if err := messaging.DeclareTopology(channel, contracts); err != nil {
		return fmt.Errorf("transcription worker rabbitmq topology: %w", err)
	}
	observer := observability.NewWorkerObserver(metrics, observability.NewJSONLLMLogger(os.Stdout, "inspection-transcription-worker", cfg.Environment, cfg.LLMMode))
	consumer := messaging.RabbitConsumer{Channel: channel, Contract: contracts[0], Handler: processor, ConsumerName: contracts[0].Name, Observer: observer}

	handler, err := newOperationalHandler(cfg, workerDB, runtimeDB, connection, channel, metrics)
	if err != nil {
		return err
	}
	// Compose/OCI stop_grace_period must stay above this bound: the effective
	// lease plus 5s to acknowledge the delivery and close connections.
	shutdownTimeout := workerCfg.lease + 5*time.Second
	if err := runtime.ServeWithBackground(cfg.HTTPAddress, handler, shutdownTimeout, func(ctx context.Context) error {
		return consumeUntilStopped(ctx, consumer.Run)
	}); err != nil {
		var coded workerFailure
		if errors.As(err, &coded) {
			return err
		}
		return workerFailure{code: codeWorkerStopped, err: err}
	}
	return nil
}

// consumeUntilStopped labels a consumer exit that was not caused by shutdown
// as a broker loss, so the process exits non-zero with a precise safe code.
func consumeUntilStopped(ctx context.Context, consume func(context.Context) error) error {
	err := consume(ctx)
	if err == nil || ctx.Err() != nil {
		return err
	}
	return workerFailure{code: codeBrokerUnavailable, err: err}
}

func safeTranscriptionCode(err error) string {
	var coded interface{ SafeCode() string }
	if errors.As(err, &coded) {
		return coded.SafeCode()
	}
	var code interface{ ErrorCode() string }
	if errors.As(err, &code) {
		return code.ErrorCode()
	}
	return codeStartupFailed
}

func checkTranscriptionGateway(baseURL, mode string) error {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid gateway endpoint")
	}
	if mode == "live" {
		parsed.Path = "/health/liveliness"
	} else {
		parsed.Path = "/__admin/health"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	client := http.Client{Timeout: 3 * time.Second}
	request, err := http.NewRequest(http.MethodGet, parsed.String(), nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("gateway health returned %d", response.StatusCode)
	}
	return nil
}

func newOperationalHandler(cfg config.Config, workerDB, runtimeDB *gorm.DB, connection *amqp.Connection, channel *amqp.Channel, metrics *observability.Metrics) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ready\n"))
	})
	if err := operational.SetupWithMetrics(mux, func(r *http.Request) error {
		if connection == nil || channel == nil || connection.IsClosed() || channel.IsClosed() {
			return errors.New("rabbitmq unavailable")
		}
		if err := pingContext(r.Context(), workerDB); err != nil {
			return err
		}
		if err := pingContext(r.Context(), runtimeDB); err != nil {
			return err
		}
		return database.Compatible(r.Context(), runtimeDB, cfg.SchemaMin, cfg.SchemaMax)
	}, cfg.MetricsToken, metrics); err != nil {
		return nil, err
	}
	return mux, nil
}

func ping(db *gorm.DB) error { return pingContext(context.Background(), db) }

func pingContext(ctx context.Context, db *gorm.DB) error {
	conn, err := db.DB()
	if err != nil {
		return err
	}
	return conn.PingContext(ctx)
}

type transcriptionJobProcessor struct{ dependencies processnote.Dependencies }

func (p transcriptionJobProcessor) Process(ctx context.Context, body []byte, _ int) (bool, error) {
	envelope, err := events.DefaultRegistry().Validate(body)
	if err != nil {
		return false, messaging.ErrPermanent
	}
	if envelope.Type != "transcription.requested.v1" {
		return false, messaging.ErrPermanent
	}
	var payload events.TranscriptionRequestedPayload
	decoder := json.NewDecoder(strings.NewReader(string(envelope.Payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || payload.NoteID == (identity.ID{}) || payload.JobID == (identity.ID{}) || payload.Generation <= 0 {
		return false, messaging.ErrPermanent
	}
	// RabbitMQ cancels the consumer context on SIGTERM. Once a delivery starts,
	// its fenced lease (enforced inside process_note.Setup) bounds the work, so
	// shutdown does not become a synthetic provider cancellation. The shutdown
	// timeout (lease + 5s) waits for that bound.
	workCtx := context.WithoutCancel(ctx)
	err = processnote.Setup(workCtx, p.dependencies, payload.JobID, envelope.TenantID, payload.Generation)
	switch {
	case errors.Is(err, processnote.ErrLeaseActive):
		// Another worker owns a valid lease: revisit after the 5-minute retry.
		return false, messaging.ErrLeaseBusy
	case errors.Is(err, processnote.ErrNotClaimable), errors.Is(err, processnote.ErrStaleLease):
		// Terminal, completed, or superseded work: acknowledge the duplicate.
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

var _ messaging.DeliveryHandler = transcriptionJobProcessor{}
