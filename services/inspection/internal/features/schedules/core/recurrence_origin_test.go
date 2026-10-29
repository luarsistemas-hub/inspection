package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	inspectioncore "inspection/services/inspection/internal/features/inspections/core"
	createoccurrence "inspection/services/inspection/internal/features/inspections/create_occurrence"
	originresolve "inspection/services/inspection/internal/features/origins/resolve_reference"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	"inspection/services/inspection/internal/features/templates/catalog"
	templateresolve "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMaterializeFixedOriginResolvesActiveOriginForEachVideoAndPhotoOccurrence(t *testing.T) {
	for _, mediaKind := range []string{"VIDEO", "PHOTO"} {
		t.Run(mediaKind, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			sqlDB.SetMaxOpenConns(1)
			if err := db.Exec("ATTACH DATABASE ':memory:' AS schedules").Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec("ATTACH DATABASE ':memory:' AS platform").Error; err != nil {
				t.Fatal(err)
			}
			for _, statement := range []string{
				`CREATE TABLE schedules.schedules (id text PRIMARY KEY, tenant_id text, business_unit_id text, asset_id text, participant_id text, template_id text, template_version_id text, comparison_mode text, project_id text, stage_id text, reference_version_id text, r_rule text, timezone text, starts_at datetime, next_due_at datetime, deadline_minutes integer, reminder_offsets blob, status text, blocked_reason text, blocked_at datetime, version integer, idempotency_key text, created_at datetime, updated_at datetime)`,
				`CREATE TABLE schedules.occurrence_materializations (id text PRIMARY KEY, tenant_id text, schedule_id text, due_instant datetime, inspection_id text, event_id text, created_at datetime)`,
			} {
				if err := db.Exec(statement).Error; err != nil {
					t.Fatal(err)
				}
			}

			tenantID, assetID, participantID, templateID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
			unitID, personUnitID := identity.NewID(), identity.NewID()
			originID := identity.NewID()
			activeOriginID := originID
			definition, err := json.Marshal(catalog.TemplateDocument{SchemaVersion: catalog.SchemaVersion, IsVideo: mediaKind == "VIDEO", DefaultComparisonMode: catalog.FixedOrigin, AnalysisType: "REAL_ESTATE"})
			if err != nil {
				t.Fatal(err)
			}
			bus := mediator.New()
			registerQuery := func(message any, fn func(context.Context, any) (any, error)) {
				t.Helper()
				if err := bus.RegisterQuery(message, fn); err != nil {
					t.Fatal(err)
				}
			}
			registerQuery(assetget.Query{}, func(context.Context, any) (any, error) {
				return assetcore.View{Asset: database.Asset{ID: assetID, BusinessUnitID: unitID, Status: "ACTIVE"}}, nil
			})
			registerQuery(participantget.Query{}, func(context.Context, any) (any, error) {
				return participantcore.ParticipantView{Participant: database.Participant{ID: participantID, BusinessUnitID: personUnitID, Status: "ACTIVE"}, Selected: []identity.ID{identity.NewID()}}, nil
			})
			registerQuery(templateresolve.Query{}, func(context.Context, any) (any, error) {
				return templateresolve.Result{Version: database.TemplateVersion{ID: identity.NewID(), DefinitionJSON: definition}}, nil
			})
			originResolveCalls := 0
			registerQuery(originresolve.Query{}, func(_ context.Context, raw any) (any, error) {
				originResolveCalls++
				query := raw.(originresolve.Query)
				if query.AssetID != assetID || query.MediaKind != mediaKind || query.VersionID != nil {
					t.Fatalf("origin query = %+v, want asset %s, media kind %s, and no pinned version", query, assetID, mediaKind)
				}
				return originresolve.Result{Version: database.OriginVersion{ID: activeOriginID, MediaKind: mediaKind}}, nil
			})

			var occurrenceCommand createoccurrence.Command
			var resolvedByOccurrence identity.ID
			if err := bus.RegisterCommand(createoccurrence.Command{}, func(ctx context.Context, raw any) (any, error) {
				occurrenceCommand = raw.(createoccurrence.Command)
				if occurrenceCommand.ReferenceVersionID != nil {
					t.Fatalf("occurrence command pinned origin %s; want deferred resolution", *occurrenceCommand.ReferenceVersionID)
				}
				result, err := bus.Ask(ctx, originresolve.Query{TenantID: tenantID, AssetID: assetID, VersionID: occurrenceCommand.ReferenceVersionID, MediaKind: mediaKind})
				if err != nil {
					return nil, err
				}
				resolvedByOccurrence = result.(originresolve.Result).Version.ID
				return inspectioncore.View{Inspection: database.Inspection{ID: identity.NewID()}, Reference: database.ReferenceSnapshot{ReferenceVersionID: &resolvedByOccurrence}, EventID: identity.NewID()}, nil
			}); err != nil {
				t.Fatal(err)
			}

			within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
				return db.WithContext(ctx).Transaction(fn)
			}
			service := Service{DB: db, Bus: bus, Authorizer: auth.Authorizer{}, Within: within, Now: func() time.Time { return time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC) }}
			startsAt := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
			created, err := service.Create(principalContext(tenantID, auth.TenantAdmin), Input{TenantID: tenantID, AssetID: assetID, ParticipantID: participantID, TemplateID: templateID, ComparisonMode: catalog.FixedOrigin, RRule: "FREQ=DAILY", Timezone: "UTC", StartsAt: startsAt, DeadlineMinutes: 60, IdempotencyKey: "origin-rotation-" + mediaKind})
			if err != nil {
				t.Fatalf("create fixed-origin schedule: %v", err)
			}
			if created.ReferenceVersionID != nil {
				t.Fatalf("created schedule pinned origin %s; want nil", *created.ReferenceVersionID)
			}
			if originResolveCalls != 0 {
				t.Fatalf("creation resolved the active origin %d times; want deferred resolution", originResolveCalls)
			}
			if _, err := bus.Ask(context.Background(), originresolve.Query{TenantID: tenantID, AssetID: assetID, MediaKind: mediaKind}); err != nil {
				t.Fatalf("initial active origin %s was not resolvable: %v", originID, err)
			}
			activeOriginID = identity.NewID()
			materialized, err := service.MaterializeDue(principalContext(tenantID, auth.TenantAdmin), tenantID, time.Date(2026, 10, 18, 13, 0, 0, 0, time.UTC), 1)
			if err != nil {
				t.Fatalf("materialize after origin change: %v", err)
			}
			if len(materialized) != 1 || occurrenceCommand.ReferenceVersionID != nil {
				t.Fatalf("materializations=%d command reference=%v; want one and nil", len(materialized), occurrenceCommand.ReferenceVersionID)
			}
			if resolvedByOccurrence != activeOriginID || materialized[0].Inspection.Reference.ReferenceVersionID == nil || *materialized[0].Inspection.Reference.ReferenceVersionID != activeOriginID {
				t.Fatalf("materialized reference=%v, resolved=%s, active=%s; want current active origin", materialized[0].Inspection.Reference.ReferenceVersionID, resolvedByOccurrence, activeOriginID)
			}
			updated, err := service.Update(principalContext(tenantID, auth.TenantAdmin), UpdateInput{TenantID: tenantID, ScheduleID: created.ID, ExpectedVersion: created.Version + 1, RRule: "FREQ=DAILY", Timezone: "UTC", StartsAt: startsAt, DeadlineMinutes: 60})
			if err != nil {
				t.Fatalf("update tracking schedule: %v", err)
			}
			if updated.ReferenceVersionID != nil {
				t.Fatalf("updated schedule pinned origin %s; want nil", *updated.ReferenceVersionID)
			}
		})
	}
}
