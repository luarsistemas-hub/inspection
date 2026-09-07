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
	getparticipant "inspection/services/inspection/internal/features/participants/get_participant"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	materializedue "inspection/services/inspection/internal/features/schedules/materialize_due"
	schedulereminders "inspection/services/inspection/internal/features/schedules/schedule_reminders"
	resolvetemplate "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/config"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/operational"
	"inspection/services/inspection/internal/platform/requestctx"
	process "inspection/services/inspection/internal/platform/runtime"
	"net/http"
	"os"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

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
		func() error {
			return createoccurrence.Setup(createoccurrence.Dependencies{DB: db, Bus: bus, Authorizer: internalAuthorizer})
		},
		func() error { return materializedue.Setup(materializedue.Dependencies{DB: db, Bus: bus}) },
		func() error { return schedulereminders.Setup(schedulereminders.Dependencies{DB: db, Bus: bus}) },
	}
	for _, setup := range setups {
		if err := setup(); err != nil {
			return err
		}
	}
	go runMaterializer(db, bus)
	mux := http.NewServeMux()
	if err := operational.Setup(mux, func(r *http.Request) error { return database.Compatible(r.Context(), db, cfg.SchemaMin, cfg.SchemaMax) }, cfg.MetricsToken); err != nil {
		return err
	}
	return process.Serve(cfg.HTTPAddress, mux, cfg.ShutdownTimeout)
}

func runMaterializer(db *gorm.DB, bus *mediator.Bus) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		now := time.Now().UTC()
		var tenantIDs []identity.ID
		if err := db.Raw("SELECT tenant_id FROM schedules.schedules WHERE status='ACTIVE' AND next_due_at<=? UNION SELECT tenant_id FROM schedules.reminder_plans WHERE status='PLANNED' AND remind_at<=? UNION SELECT tenant_id FROM inspections.inspections WHERE status IN ('COMPLETED','CANCELED','INVALIDATED') AND updated_at<=?", now, now, now.AddDate(-5, 0, 0)).Scan(&tenantIDs).Error; err != nil {
			log.Printf("scheduler tenant discovery failed: %v", err)
		} else {
			for _, tenantID := range tenantIDs {
				ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: tenantID, TenantID: tenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "scheduler-" + now.Format(time.RFC3339Nano), StartedAt: now})
				if _, err := bus.Send(ctx, materializedue.Command{TenantID: tenantID, Now: now, Limit: 100}); err != nil {
					log.Printf("scheduler materialization failed: %v", err)
				}
				if _, err := bus.Send(ctx, schedulereminders.Command{TenantID: tenantID, Now: now, Limit: 100}); err != nil {
					log.Printf("scheduler reminder dispatch failed: %v", err)
				}
				if err := emitRetentionDue(db, tenantID, now); err != nil {
					log.Printf("scheduler retention discovery failed: %v", err)
				}
			}
		}
		<-ticker.C
	}
}

func emitRetentionDue(db *gorm.DB, tenantID identity.ID, now time.Time) error {
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
