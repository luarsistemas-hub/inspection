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

	"gorm.io/gorm"
)

func TestFollowActiveOriginKeepsCreatedOccurrenceReferenceAfterNextPromotion(t *testing.T) {
	db := scheduleTestDB(t)
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	tenantID, assetID, participantID, templateID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	unitID := identity.NewID()
	firstOrigin, secondOrigin := identity.NewID(), identity.NewID()
	activeOrigin := firstOrigin
	definition, err := json.Marshal(catalog.TemplateDocument{SchemaVersion: catalog.SchemaVersion, DefaultComparisonMode: catalog.FixedOrigin, AnalysisType: "REAL_ESTATE"})
	if err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	registerQuery := func(message any, handler func(context.Context, any) (any, error)) {
		t.Helper()
		if err := bus.RegisterQuery(message, handler); err != nil {
			t.Fatal(err)
		}
	}
	registerQuery(assetget.Query{}, func(context.Context, any) (any, error) {
		return assetcore.View{Asset: database.Asset{ID: assetID, BusinessUnitID: unitID, Status: "ACTIVE"}}, nil
	})
	registerQuery(participantget.Query{}, func(context.Context, any) (any, error) {
		return participantcore.ParticipantView{Participant: database.Participant{ID: participantID, BusinessUnitID: unitID, Status: "ACTIVE"}, Selected: []identity.ID{identity.NewID()}}, nil
	})
	registerQuery(templateresolve.Query{}, func(context.Context, any) (any, error) {
		return templateresolve.Result{Version: database.TemplateVersion{ID: identity.NewID(), DefinitionJSON: definition}}, nil
	})
	registerQuery(originresolve.Query{}, func(_ context.Context, raw any) (any, error) {
		query := raw.(originresolve.Query)
		if query.VersionID != nil {
			t.Fatalf("follow-active schedule pinned version %s", *query.VersionID)
		}
		return originresolve.Result{Version: database.OriginVersion{ID: activeOrigin, MediaKind: "PHOTO"}}, nil
	})
	var materializedReferences []*identity.ID
	if err := bus.RegisterCommand(createoccurrence.Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(createoccurrence.Command)
		resolved, err := bus.Ask(ctx, originresolve.Query{TenantID: tenantID, AssetID: assetID, VersionID: command.ReferenceVersionID, MediaKind: "PHOTO"})
		if err != nil {
			return nil, err
		}
		id := resolved.(originresolve.Result).Version.ID
		materializedReferences = append(materializedReferences, identity.Ptr(id))
		return inspectioncore.View{Inspection: database.Inspection{ID: identity.NewID()}, Reference: database.ReferenceSnapshot{ReferenceVersionID: &id}, EventID: identity.NewID()}, nil
	}); err != nil {
		t.Fatal(err)
	}
	within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
	service := Service{DB: db, Bus: bus, Authorizer: auth.Authorizer{}, Within: within, Now: func() time.Time { return now }}
	startsAt := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	schedule, err := service.Create(principalContext(tenantID, auth.TenantAdmin), Input{TenantID: tenantID, AssetID: assetID, ParticipantID: participantID, TemplateID: templateID, ComparisonMode: catalog.FixedOrigin, RRule: "FREQ=DAILY", Timezone: "UTC", StartsAt: startsAt, DeadlineMinutes: 60, IdempotencyKey: "origin-rotation-history"})
	if err != nil {
		t.Fatal(err)
	}
	firstBatch, err := service.MaterializeDue(principalContext(tenantID, auth.TenantAdmin), tenantID, time.Date(2026, 10, 18, 13, 0, 0, 0, time.UTC), 1)
	if err != nil || len(firstBatch) != 1 {
		t.Fatalf("first materialization count=%d err=%v", len(firstBatch), err)
	}
	activeOrigin = secondOrigin
	secondBatch, err := service.MaterializeDue(principalContext(tenantID, auth.TenantAdmin), tenantID, time.Date(2026, 10, 19, 13, 0, 0, 0, time.UTC), 1)
	if err != nil || len(secondBatch) != 1 {
		t.Fatalf("second materialization count=%d err=%v", len(secondBatch), err)
	}
	if len(materializedReferences) != 2 || materializedReferences[0] == nil || materializedReferences[1] == nil || *materializedReferences[0] != firstOrigin || *materializedReferences[1] != secondOrigin {
		t.Fatalf("occurrence references = %v; want first %s and second %s", materializedReferences, firstOrigin, secondOrigin)
	}
	if firstBatch[0].Inspection.Reference.ReferenceVersionID == nil || *firstBatch[0].Inspection.Reference.ReferenceVersionID != firstOrigin || secondBatch[0].Inspection.Reference.ReferenceVersionID == nil || *secondBatch[0].Inspection.Reference.ReferenceVersionID != secondOrigin {
		t.Fatalf("created occurrence references changed across promotions: first=%+v second=%+v schedule=%+v", firstBatch[0].Inspection.Reference, secondBatch[0].Inspection.Reference, schedule)
	}
}
