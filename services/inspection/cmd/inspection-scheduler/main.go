package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	getasset "inspection/services/inspection/internal/features/assets/get_asset"
	createoccurrence "inspection/services/inspection/internal/features/inspections/create_occurrence"
	reminddeadlines "inspection/services/inspection/internal/features/notifications/remind_deadlines"
	notificationrequest "inspection/services/inspection/internal/features/notifications/request"
	originresolve "inspection/services/inspection/internal/features/origins/resolve_reference"
	getparticipant "inspection/services/inspection/internal/features/participants/get_participant"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	expiresources "inspection/services/inspection/internal/features/retention/expire_video_sources"
	processdeletions "inspection/services/inspection/internal/features/retention/process_deletion_requests"
	materializedue "inspection/services/inspection/internal/features/schedules/materialize_due"
	schedulereminders "inspection/services/inspection/internal/features/schedules/schedule_reminders"
	resolvetemplate "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/config"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/notifications"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/observability"
	"inspection/services/inspection/internal/platform/operational"
	"inspection/services/inspection/internal/platform/requestctx"
	process "inspection/services/inspection/internal/platform/runtime"
	"net/http"
	"os"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const videoExpiryInterval = 5 * time.Minute

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
		go func() {
			defer close(done)
			runMaterializer(ctx, db, bus, deadlineNotifications, videoExpiry, deletionRequests, metrics)
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

func runMaterializer(ctx context.Context, db *gorm.DB, bus *mediator.Bus, deadlineNotifications func(context.Context, identity.ID, time.Time) error, videoExpiry *expiresources.Executor, deletionRequests *processdeletions.Executor, metrics *observability.Metrics) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	var lastVideoExpiryRun time.Time
	for {
		if ctx.Err() != nil {
			return
		}
		now := time.Now().UTC()
		var tenantIDs []identity.ID
		if err := db.WithContext(ctx).Raw("SELECT pending.tenant_id FROM platform.discover_scheduler_tenants(?, ?, ?) AS pending(tenant_id)", now, now.AddDate(-5, 0, 0), now.Add(24*time.Hour)).Scan(&tenantIDs).Error; err != nil {
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
				tenantCtx := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: tenantID, TenantID: tenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "scheduler-" + now.Format(time.RFC3339Nano), StartedAt: now})
				if _, err := bus.Send(tenantCtx, materializedue.Command{TenantID: tenantID, Now: now, Limit: 100}); err != nil {
					log.Printf("scheduler materialization failed: %v", err)
				}
				if _, err := bus.Send(tenantCtx, schedulereminders.Command{TenantID: tenantID, Now: now, Limit: 100}); err != nil {
					log.Printf("scheduler reminder dispatch failed: %v", err)
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

func dueEvery(last, now time.Time, interval time.Duration) bool {
	return last.IsZero() || now.Sub(last) >= interval
}

func emitRetentionDue(ctx context.Context, db *gorm.DB, tenantID identity.ID, now time.Time) error {
	db = db.WithContext(ctx)
	policy := retentioncore.DefaultPolicy()
	var configured database.RetentionPolicy
	if err := db.Where("tenant_id=?", tenantID).Order("version DESC").First(&configured).Error; err == nil {
		policy.EvidenceReports = time.Duration(configured.EvidenceDays) * 24 * time.Hour
		policy.Operational = time.Duration(configured.OperationalDays) * 24 * time.Hour
	} else if err != gorm.ErrRecordNotFound {
		return err
	}
	var inspections []database.Inspection
	if err := db.Where("tenant_id=? AND status IN ('COMPLETED','CANCELED','INVALIDATED')", tenantID).Where("updated_at <= ?", now.Add(-policy.EvidenceReports)).Find(&inspections).Error; err != nil {
		return err
	}
	for _, inspection := range inspections {
		var held int64
		if err := db.Model(&database.LegalHold{}).Where("tenant_id=? AND inspection_id=? AND active", tenantID, inspection.ID).Count(&held).Error; err != nil {
			return err
		}
		if held > 0 {
			continue
		}
		var count int64
		if err := db.Model(&database.PurgeRun{}).Where("tenant_id=? AND inspection_id=?", tenantID, inspection.ID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		eventID := identity.ID(uuid.NewSHA1(uuid.Nil, []byte("retention:"+tenantID.String()+":"+inspection.ID.String())))
		if err := messaging.AddOutbox(db, events.Envelope[map[string]any]{ID: eventID, Type: "retention.purge_due.v1", SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: inspection.ID, CorrelationID: "retention-" + inspection.ID.String(), Payload: map[string]any{"inspectionId": inspection.ID}}); err != nil {
			return err
		}
	}
	return nil
}
