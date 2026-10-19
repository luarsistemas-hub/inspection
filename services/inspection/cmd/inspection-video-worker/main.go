// inspection-video-worker owns bounded video preparation and condition work.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"inspection/services/inspection/internal/contracts/events"
	buildrequest "inspection/services/inspection/internal/features/analysis/build_request"
	preparecomparisons "inspection/services/inspection/internal/features/analysis/prepare_video_comparisons"
	processcomparison "inspection/services/inspection/internal/features/analysis/process_comparison"
	scheduleplan "inspection/services/inspection/internal/features/analysis/schedule_video_plan"
	preparevideo "inspection/services/inspection/internal/features/media/prepare_video"
	evaluatecoverage "inspection/services/inspection/internal/features/recapture/evaluate_video_coverage"
	recordllmcall "inspection/services/inspection/internal/features/usage/record_llm_call"
	"inspection/services/inspection/internal/platform/config"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/observability"
	"inspection/services/inspection/internal/platform/operational"
	process "inspection/services/inspection/internal/platform/runtime"
	"inspection/services/inspection/internal/platform/sensitivecontent"
	"inspection/services/inspection/internal/platform/video"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "inspection-video-worker failed before readiness:", preparevideo.SafeCode(err))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	limits, err := video.LoadWorkerConfig()
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("video worker: ffmpeg is required")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return fmt.Errorf("video worker: ffprobe is required")
	}
	claimDB, err := database.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	runtimeURL := cfg.RuntimeDatabaseURL
	if runtimeURL == "" {
		runtimeURL = cfg.DatabaseURL
	}
	runtimeDB, err := database.Open(runtimeURL)
	if err != nil {
		return err
	}
	if err := pingDatabase(claimDB); err != nil {
		return fmt.Errorf("video worker database is not ready: %w", err)
	}
	if runtimeDB != claimDB {
		if err := pingDatabase(runtimeDB); err != nil {
			return fmt.Errorf("video worker runtime database is not ready: %w", err)
		}
	}
	if err := checkGateway(cfg.LiteLLMURL, cfg.LLMMode); err != nil {
		return fmt.Errorf("video worker gateway is not ready: %w", err)
	}
	metrics := observability.NewMetrics()
	ledger, err := recordllmcall.Setup(recordllmcall.Dependencies{DB: runtimeDB})
	if err != nil {
		return err
	}
	apiKey := ""
	if cfg.LLMMode == "live" {
		apiKey = cfg.LiteLLMAPIKey
	}
	logger := observability.NewJSONLLMLogger(os.Stdout, "inspection-video-worker", cfg.Environment, cfg.LLMMode)
	gateway := llm.ObservedGateway{Inner: llm.HTTPGateway{BaseURL: cfg.LiteLLMURL, APIKey: apiKey, Client: &http.Client{Timeout: cfg.ProviderTimeout}}, Metrics: metrics, Logger: logger, Mode: cfg.LLMMode, Ledger: ledger}
	minio, err := objectstore.NewMinIOWithRegion(cfg.MinIOEndpoint, cfg.MinIOAccessKey, cfg.MinIOSecretKey, cfg.MinIOSecure, cfg.MinIORegion)
	if err != nil {
		return err
	}
	store := objectstore.VideoStore{Store: objectstore.Store{Bucket: cfg.MinIOBucket, Client: minio}}
	detector := sensitivecontent.NewEmbeddedDetector()
	if !cfg.ImageValidationEnabled {
		detector = sensitivecontent.NewDisabledDetector()
		log.Printf("video worker: sensitive-content screening is explicitly disabled")
	}
	executor, err := preparevideo.Setup(preparevideo.Dependencies{DB: runtimeDB, ClaimDB: claimDB, Store: store, Decoder: video.FFmpeg{Threads: limits.DecoderThreads}, Detector: detector, Gateway: gateway, Now: time.Now, TempDir: "/var/lib/inspection-video-scratch", Limits: limits, Metrics: metrics})
	if err != nil {
		return err
	}
	requestBuilder, err := buildrequest.Setup(buildrequest.Dependencies{Store: store.Store})
	if err != nil {
		return err
	}
	processJob, err := processcomparison.Setup(processcomparison.Dependencies{Gateway: gateway, BuildRequest: requestBuilder, Now: time.Now, Metrics: metrics, Logger: logger, Mode: cfg.LLMMode})
	if err != nil {
		return err
	}
	imageSource := preparecomparisons.StoreFrameImages{DB: runtimeDB, Store: store.Store}
	matcher, err := preparecomparisons.NewGatewayMatcher(gateway, imageSource)
	if err != nil {
		return err
	}
	planScheduler, err := scheduleplan.Setup(scheduleplan.Dependencies{DB: runtimeDB, ClaimDB: claimDB, Frames: preparecomparisons.DBFrameReader{DB: runtimeDB}, Matcher: matcher, Verifier: matcher, Now: time.Now, Lease: limits.LeaseDuration, Metrics: metrics})
	if err != nil {
		return err
	}
	coverageEvaluator, err := evaluatecoverage.Setup(evaluatecoverage.Dependencies{DB: runtimeDB, ClaimDB: claimDB, Frames: preparecomparisons.DBFrameReader{DB: runtimeDB}, Matcher: matcher, Verifier: matcher, PlanRequests: scheduleplan.SupplementalRequester{}, Now: time.Now, Lease: limits.LeaseDuration})
	if err != nil {
		return err
	}
	connection, err := amqp.Dial(cfg.RabbitMQURL)
	if err != nil {
		return fmt.Errorf("video worker rabbitmq: %w", err)
	}
	defer connection.Close()
	channel, err := connection.Channel()
	if err != nil {
		return fmt.Errorf("video worker rabbitmq channel: %w", err)
	}
	defer channel.Close()
	legacyChannel, err := connection.Channel()
	if err != nil {
		return fmt.Errorf("video worker legacy rabbitmq channel: %w", err)
	}
	defer legacyChannel.Close()
	contract, err := messaging.NewQueueContract("inspection.video", "analysis.video_comparison_requested.v1", 1)
	if err != nil {
		return err
	}
	legacyContract, err := messaging.NewQueueContract("inspection-video-comparison", contract.RoutingKey, 1)
	if err != nil {
		return err
	}
	if err := declareVideoQueueMigration(channel, legacyChannel, contract, legacyContract); err != nil {
		return fmt.Errorf("video worker rabbitmq topology: %w", err)
	}
	for _, key := range []string{"media.video_prepared.v1", "capture.video_submitted.v1"} {
		if err := channel.QueueBind(contract.Name, key, messaging.Exchange, false, nil); err != nil {
			return fmt.Errorf("video worker event binding: %w", err)
		}
	}
	observer := observability.NewWorkerObserver(metrics, logger)
	consumer := newVideoWorkerConsumer(claimDB, events.DefaultRegistry(), legacyContract, func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		metrics.VideoWorkHeartbeat(time.Now().UTC())
		if envelope.Type == "analysis.video_comparison_requested.v1" {
			return processcomparison.VideoRequestHandler(processJob)(ctx, tx, envelope)
		}
		if envelope.Type == "media.video_prepared.v1" {
			err := coverageEvaluator.HandlePrepared(ctx, tx, envelope)
			if !errors.Is(err, evaluatecoverage.ErrNotApplicable) {
				return err
			}
			return planScheduler.Handle(ctx, tx, envelope)
		}
		return planScheduler.Handle(ctx, tx, envelope)
	}, observer)
	rabbit := messaging.RabbitConsumer{Channel: channel, Contract: contract, Handler: permittedDelivery{permit: executor, inner: consumer}, ConsumerName: contract.Name, Observer: observer}
	legacyRabbit := messaging.RabbitConsumer{Channel: legacyChannel, Contract: legacyContract, Handler: permittedDelivery{permit: executor, inner: consumer}, ConsumerName: legacyContract.Name, Observer: observer}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ready\n"))
	})
	if err := operational.SetupWithMetrics(mux, func(r *http.Request) error {
		return database.Compatible(r.Context(), runtimeDB, cfg.SchemaMin, cfg.SchemaMax)
	}, cfg.MetricsToken, metrics); err != nil {
		return err
	}
	log.Printf("inspection-video-worker ready: concurrency=1 decoder_threads=1 max_bytes=%d scratch_bytes=%d", limits.MaxBytes, limits.ScratchBytes)
	return process.ServeWithBackground(cfg.HTTPAddress, mux, limits.ShutdownTimeout, func(ctx context.Context) error {
		workCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		errCh := make(chan error, 6)
		go func() { errCh <- rabbit.Run(workCtx) }()
		go func() { errCh <- legacyRabbit.Run(workCtx) }()
		go func() { errCh <- runPreparationLoop(workCtx, executor, limits.PollInterval) }()
		go func() { errCh <- runPlanLoop(workCtx, executor, planScheduler, limits.PollInterval) }()
		go func() { errCh <- runCoverageLoop(workCtx, executor, coverageEvaluator, limits.PollInterval) }()
		go func() { errCh <- runVideoQueueMetricsLoop(workCtx, claimDB, metrics) }()
		completed := 0
		var firstErr error
		select {
		case <-ctx.Done():
		case err := <-errCh:
			completed = 1
			firstErr = err
		}
		cancel()
		deadline := time.NewTimer(limits.ShutdownTimeout * 9 / 10)
		defer deadline.Stop()
		for completed < 5 {
			select {
			case <-errCh:
				completed++
			case <-deadline.C:
				return fmt.Errorf("video worker background shutdown timed out")
			}
		}
		return firstErr
	})
}

func runVideoQueueMetricsLoop(ctx context.Context, db *gorm.DB, metrics *observability.Metrics) error {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	type queueRow struct {
		Stage, Status string
		Depth         int64
		Oldest        *time.Time
	}
	for {
		var rows []queueRow
		err := db.WithContext(ctx).Table("media.video_preparation_jobs").
			Select("phase AS stage, status, count(*) AS depth, min(created_at) AS oldest").
			Where("status IN ?", []string{"QUEUED", "PROCESSING", "FAILED"}).
			Group("phase, status").Scan(&rows).Error
		if err != nil && ctx.Err() == nil {
			log.Printf("video worker metrics refresh failed")
		} else {
			now := time.Now().UTC()
			metrics.VideoWorkHeartbeat(now)
			for _, row := range rows {
				status := strings.ToLower(row.Status)
				if status == "processing" {
					status = "running"
				}
				age := time.Duration(0)
				if row.Oldest != nil {
					age = now.Sub(*row.Oldest)
				}
				metrics.VideoQueue(row.Stage, status, row.Depth, age)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func runCoverageLoop(ctx context.Context, permit *preparevideo.Executor, evaluator *evaluatecoverage.Evaluator, pollInterval time.Duration) error {
	for {
		worked, err := permit.WithPermit(ctx, func(ctx context.Context) (bool, error) { return evaluator.RunDue(ctx) })
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("video coverage step failed: code=VIDEO_COVERAGE_STEP_FAILED")
		}
		if worked {
			continue
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func newVideoWorkerConsumer(db *gorm.DB, registry *events.Registry, legacy messaging.QueueContract, handle func(context.Context, *gorm.DB, events.RawEnvelope) error, observer observability.ConsumerObserver) messaging.Consumer {
	return messaging.Consumer{DB: db, Registry: registry, Name: legacy.Name, Handle: handle, Observer: observer}
}

func runPlanLoop(ctx context.Context, permit *preparevideo.Executor, scheduler *scheduleplan.Scheduler, pollInterval time.Duration) error {
	for {
		worked, err := permit.WithPermit(ctx, func(ctx context.Context) (bool, error) { return scheduler.RunDue(ctx) })
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("video plan step failed: code=VIDEO_PLAN_STEP_FAILED")
		}
		if worked {
			continue
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func runPreparationLoop(ctx context.Context, executor *preparevideo.Executor, pollInterval time.Duration) error {
	for {
		worked, err := executor.RunDue(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			logPreparationFailure(err)
		}
		if worked {
			continue
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func logPreparationFailure(err error) {
	log.Printf("video preparation step failed: code=%s", preparevideo.SafeCode(err))
}

func pingDatabase(db interface{ DB() (*sql.DB, error) }) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return sqlDB.PingContext(ctx)
}

func checkGateway(baseURL, mode string) error {
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
