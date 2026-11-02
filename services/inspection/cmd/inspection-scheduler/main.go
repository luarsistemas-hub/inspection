package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"inspection/libs/identity"
	getasset "inspection/services/inspection/internal/features/assets/get_asset"
	counterpartycore "inspection/services/inspection/internal/features/counterparty/core"
	counterpartysweep "inspection/services/inspection/internal/features/counterparty/sweep_deadlines"
	createoccurrence "inspection/services/inspection/internal/features/inspections/create_occurrence"
	reminddeadlines "inspection/services/inspection/internal/features/notifications/remind_deadlines"
	notificationrequest "inspection/services/inspection/internal/features/notifications/request"
	originresolve "inspection/services/inspection/internal/features/origins/resolve_reference"
	getparticipant "inspection/services/inspection/internal/features/participants/get_participant"
	expiresources "inspection/services/inspection/internal/features/retention/expire_video_sources"
	processdeletions "inspection/services/inspection/internal/features/retention/process_deletion_requests"
	scheduleexpiry "inspection/services/inspection/internal/features/retention/schedule_expiry"
	materializedue "inspection/services/inspection/internal/features/schedules/materialize_due"
	schedulereminders "inspection/services/inspection/internal/features/schedules/schedule_reminders"
	resolvetemplate "inspection/services/inspection/internal/features/templates/resolve_template"
	expireaudio "inspection/services/inspection/internal/features/transcription/expire_audio"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/config"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/notifications"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/observability"
	"inspection/services/inspection/internal/platform/operational"
	"inspection/services/inspection/internal/platform/requestctx"
	process "inspection/services/inspection/internal/platform/runtime"
	"net/http"
	"os"

	"gorm.io/gorm"
)

const defaultVideoExpiryInterval = 5 * time.Minute

// defaultTranscriptionSweepInterval paces voice-note lease recovery, expiry,
// retry wakeups and audio cleanup; the scheduler loop ticks once per minute.
const defaultTranscriptionSweepInterval = time.Minute

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "inspection-scheduler:", err)
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	db, err := database.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	storeClient, err := objectstore.NewMinIOWithRegion(cfg.MinIOEndpoint, cfg.MinIOAccessKey, cfg.MinIOSecretKey, cfg.MinIOSecure, cfg.MinIORegion)
	if err != nil {
		return err
	}
	metrics := observability.NewMetrics()
	videoExpiry, err := expiresources.Setup(expiresources.Dependencies{DB: db, Store: objectstore.Store{Bucket: cfg.MinIOBucket, Client: storeClient}, Metrics: metrics})
	if err != nil {
		return err
	}
	deletionRequests, err := processdeletions.Setup(processdeletions.Dependencies{DB: db, Store: objectstore.Store{Bucket: cfg.MinIOBucket, Client: storeClient}})
	if err != nil {
		return err
	}
	transcriptionSweepInterval, err := transcriptionSweepIntervalFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	videoExpiryInterval, err := videoExpiryIntervalFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	transcriptionSweeps, err := expireaudio.Setup(expireaudio.Dependencies{DB: db, Store: objectstore.Store{Bucket: cfg.MinIOBucket, Client: storeClient}, Metrics: metrics})
	if err != nil {
		return err
	}
	deadlineNotifications, err := reminddeadlines.Setup(reminddeadlines.Dependencies{DB: db})
	if err != nil {
		return err
	}
	payloadCipher, err := notifications.NewPayloadCipher(cfg.Notification.ActivePayloadKey, cfg.Notification.PayloadKeys)
	if err != nil {
		return err
	}
	providerResolver, err := notifications.NewProviderResolver(notifications.Provider(cfg.Notification.WhatsAppProvider))
	if err != nil {
		return err
	}
	notificationService, err := notificationrequest.Setup(notificationrequest.Dependencies{DB: db, Providers: providerResolver, Payloads: payloadCipher, Metrics: metrics})
	if err != nil {
		return err
	}
	bus := mediator.New()
	internalAuthorizer := auth.Authorizer{}
	setups := []func() error{
		func() error {
			return getasset.Setup(getasset.Dependencies{DB: db, Bus: bus, Authorizer: internalAuthorizer})
		},
		func() error {
			return getparticipant.Setup(getparticipant.Dependencies{DB: db, Bus: bus, Authorizer: internalAuthorizer})
		},
		func() error { return resolvetemplate.Setup(resolvetemplate.Dependencies{DB: db, Bus: bus}) },
		func() error { return originresolve.Setup(originresolve.Dependencies{DB: db, Bus: bus}) },
		func() error {
			return createoccurrence.Setup(createoccurrence.Dependencies{DB: db, Bus: bus, Authorizer: internalAuthorizer})
		},
		func() error { return materializedue.Setup(materializedue.Dependencies{DB: db, Bus: bus}) },
		func() error {
			return counterpartysweep.Setup(counterpartysweep.Dependencies{Bus: bus, Service: counterpartycore.Service{DB: db}})
		},
		func() error {
			return schedulereminders.Setup(schedulereminders.Dependencies{DB: db, Bus: bus, Notifications: notificationService, CaptureBaseURL: cfg.CaptureOrigin})
		},
	}
	for _, setup := range setups {
		if err := setup(); err != nil {
			return err
		}
	}
	mux := http.NewServeMux()
	if err := operational.SetupWithMetrics(mux, func(r *http.Request) error { return database.Compatible(r.Context(), db, cfg.SchemaMin, cfg.SchemaMax) }, cfg.MetricsToken, metrics); err != nil {
		return err
	}
	return process.ServeWithBackground(cfg.HTTPAddress, mux, cfg.ShutdownTimeout, func(ctx context.Context) error {
		done := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			runMaterializer(ctx, db, bus, deadlineNotifications, videoExpiry, deletionRequests, metrics, videoExpiryInterval)
		}()
		// Voice-note scans run on their own loop so a slow storage provider or
		// database lock there never delays materialization or reminders.
		go func() {
			defer workers.Done()
			runTranscriptionSweepLoop(ctx, transcriptionSweeps, transcriptionSweepInterval)
		}()
		go func() {
			workers.Wait()
			close(done)
		}()
		<-ctx.Done()
		select {
		case <-done:
		case <-time.After(cfg.ShutdownTimeout):
			return fmt.Errorf("scheduler background work did not stop within %s", cfg.ShutdownTimeout)
		}
		return nil
	})
}

func runMaterializer(ctx context.Context, db *gorm.DB, bus *mediator.Bus, deadlineNotifications func(context.Context, identity.ID, time.Time) error, videoExpiry *expiresources.Executor, deletionRequests *processdeletions.Executor, metrics *observability.Metrics, videoExpiryInterval time.Duration) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	var lastVideoExpiryRun time.Time
	for {
		if ctx.Err() != nil {
			return
		}
		now := time.Now().UTC()
		var tenantIDs []identity.ID
		// The second argument (terminal-before cutoff) is kept for signature
		// compatibility only: discovery compares each inspection with the
		// effective retention of its tenant, so a short policy is never skipped.
		if err := db.WithContext(ctx).Raw("SELECT pending.tenant_id FROM platform.discover_scheduler_tenants(?, ?, ?) AS pending(tenant_id)", now, now, now.Add(24*time.Hour)).Scan(&tenantIDs).Error; err != nil {
			log.Printf("scheduler tenant discovery failed: %v", err)
		} else {
			videoExpiryDue := dueEvery(lastVideoExpiryRun, now, videoExpiryInterval)
			if videoExpiryDue {
				metrics.ResetVideoExpirationLag()
			}
			for _, tenantID := range tenantIDs {
				if ctx.Err() != nil {
					return
				}
				tenantCtx := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: tenantID, TenantID: tenantID, Roles: []string{auth.TenantAdmin}, Internal: true}, CorrelationID: "scheduler-" + now.Format(time.RFC3339Nano), StartedAt: now})
				if _, err := bus.Send(tenantCtx, materializedue.Command{TenantID: tenantID, Now: now, Limit: 100}); err != nil {
					log.Printf("scheduler materialization failed: %v", err)
				}
				if _, err := bus.Send(tenantCtx, schedulereminders.Command{TenantID: tenantID, Now: now, Limit: 100}); err != nil {
					log.Printf("scheduler reminder dispatch failed: %v", err)
				}
				if _, err := bus.Send(tenantCtx, counterpartysweep.Command{TenantID: tenantID, Now: now, Limit: 100}); err != nil {
					log.Printf("counterparty deadline sweep failed: %v", err)
				}
				if err := emitRetentionDue(ctx, db, tenantID, now); err != nil {
					log.Printf("scheduler retention discovery failed: %v", err)
				}
				if videoExpiryDue {
					if _, err := deletionRequests.RunDue(tenantCtx, tenantID, processdeletions.DefaultBatchSize); err != nil {
						log.Printf("scheduler deletion request processing failed: %v", err)
					}
					if _, err := videoExpiry.RunDue(tenantCtx, tenantID, now, expiresources.DefaultBatchSize); err != nil {
						log.Printf("scheduler video source expiry failed: %v", err)
					}
					if _, err := videoExpiry.PurgeInactiveOriginBanks(tenantCtx, tenantID, expiresources.DefaultBatchSize); err != nil {
						log.Printf("scheduler inactive origin frame purge failed: %v", err)
					}
				}
				if err := deadlineNotifications(tenantCtx, tenantID, now); err != nil {
					log.Printf("scheduler deadline notification failed: %v", err)
				}
			}
			if videoExpiryDue {
				lastVideoExpiryRun = now
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// transcriptionSweeper is the slice of expireaudio.Executor used by the scheduler loop.
type transcriptionSweeper interface {
	DueTenants(ctx context.Context) ([]identity.ID, error)
	RunDue(ctx context.Context, tenantID identity.ID, limit int) (expireaudio.Counts, error)
}

// runTranscriptionSweepLoop runs one bounded voice-note pass immediately and
// then once per interval until ctx ends. Passes are sequential, so they never
// overlap, and each pass is cancelled when it outlives the interval. Tenant
// discovery is independent of the general scheduler discovery so a tenant with
// only transcription work is never skipped.
func runTranscriptionSweepLoop(ctx context.Context, sweeps transcriptionSweeper, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		runTranscriptionSweeps(ctx, sweeps, interval)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// runTranscriptionSweeps runs every voice-note scan for each due tenant within
// one overall timeout and logs only counters and tenant identifiers, never
// audio keys or transcripts. Work cut off by the timeout resumes next pass.
func runTranscriptionSweeps(ctx context.Context, sweeps transcriptionSweeper, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tenantIDs, err := sweeps.DueTenants(ctx)
	if err != nil {
		log.Printf("scheduler transcription tenant discovery failed: %v", err)
		return
	}
	for _, tenantID := range tenantIDs {
		if ctx.Err() != nil {
			return
		}
		counts, err := sweeps.RunDue(ctx, tenantID, expireaudio.DefaultBatchSize)
		if err != nil {
			log.Printf("scheduler transcription audio scan failed tenant=%s: %v", tenantID, err)
		}
		if counts.Total() > 0 {
			log.Printf("scheduler transcription audio scan tenant=%s recovered=%d exhausted=%d expired=%d requeued=%d deleted=%d cleanup_failed=%d", tenantID, counts.Recovered, counts.Exhausted, counts.Expired, counts.Requeued, counts.Deleted, counts.Failed)
		}
	}
}

// transcriptionSweepIntervalFromEnv validates the optional
// INSPECTION_TRANSCRIPTION_SWEEP_INTERVAL once at startup.
func transcriptionSweepIntervalFromEnv(getenv func(string) string) (time.Duration, error) {
	value := strings.TrimSpace(getenv("INSPECTION_TRANSCRIPTION_SWEEP_INTERVAL"))
	if value == "" {
		return defaultTranscriptionSweepInterval, nil
	}
	interval, err := time.ParseDuration(value)
	if err != nil || interval < time.Minute || interval > time.Hour {
		return 0, fmt.Errorf("invalid INSPECTION_TRANSCRIPTION_SWEEP_INTERVAL: use a duration between 1m and 1h")
	}
	return interval, nil
}

// videoExpiryIntervalFromEnv validates the optional source expiry scan interval.
func videoExpiryIntervalFromEnv(getenv func(string) string) (time.Duration, error) {
	value := strings.TrimSpace(getenv("INSPECTION_VIDEO_EXPIRY_INTERVAL"))
	if value == "" {
		return defaultVideoExpiryInterval, nil
	}
	interval, err := time.ParseDuration(value)
	if err != nil || interval < 10*time.Second || interval > 24*time.Hour {
		return 0, fmt.Errorf("invalid INSPECTION_VIDEO_EXPIRY_INTERVAL: use a duration between 10s and 24h")
	}
	return interval, nil
}

func dueEvery(last, now time.Time, interval time.Duration) bool {
	return last.IsZero() || now.Sub(last) >= interval
}

// emitRetentionDue queues the purge work of one tenant inside its tenant
// transaction; the slice owns the policy, hold and idempotency rules.
func emitRetentionDue(ctx context.Context, db *gorm.DB, tenantID identity.ID, now time.Time) error {
	_, err := scheduleexpiry.EmitDue(ctx, db, tenantID, now, scheduleexpiry.DefaultBatchSize)
	return err
}
