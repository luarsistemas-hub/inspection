//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"inspection/libs/identity"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	inspectioncore "inspection/services/inspection/internal/features/inspections/core"
	origincore "inspection/services/inspection/internal/features/origins/core"
	"inspection/services/inspection/internal/features/origins/resolve_reference"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	"inspection/services/inspection/internal/features/templates/catalog"
	templateresolve "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

func TestActivatedReadyVideoOriginSelectableForNewFixedOriginInspectionIT059(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	origin, err := h.SeedVideoOriginFixture(ctx, harness.VideoOriginFixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	activated, err := (origincore.Service{DB: h.DB, Within: h.WithinTenant}).ActivateCompleted(ctx, origin.TenantID, origin.ResponsibilityID)
	if err != nil || activated.Status != "ACTIVE" {
		t.Fatalf("activate prepared video origin: status=%s err=%v", activated.Status, err)
	}
	if err := h.AdminDB.Exec("INSERT INTO platform.video_admission (id, enabled, version, changed_at, reason, mutation_id, payload_hash) VALUES (1, TRUE, 1, ?, ?, ?, '') ON CONFLICT (id) DO UPDATE SET enabled=TRUE", time.Now().UTC(), "isolated IT-059 fixture", "it059").Error; err != nil {
		t.Fatalf("enable video admission in disposable integration database: %v", err)
	}
	var base database.Inspection
	var participant database.Participant
	var asset database.Asset
	var baseTemplate database.Template
	var member database.Membership
	if err := h.WithinTenant(ctx, origin.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", origin.TenantID, origin.AssetID).Take(&asset).Error; err != nil {
			return err
		}
		var responsibility database.Responsibility
		if err := tx.Where("tenant_id=? AND id=?", origin.TenantID, origin.ResponsibilityID).Take(&responsibility).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND id=?", origin.TenantID, responsibility.InspectionID).Take(&base).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND id=?", origin.TenantID, base.ParticipantID).Take(&participant).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND id=?", origin.TenantID, base.TemplateID).Take(&baseTemplate).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND role=?", origin.TenantID, auth.TenantAdmin).Take(&member).Error
	}); err != nil {
		t.Fatal(err)
	}
	templateID, templateVersionID, err := createVideoInspectionTemplate(ctx, h, harness.Task06Fixture{TenantID: origin.TenantID, AssetID: origin.AssetID, SegmentVersionID: baseTemplate.SegmentVersionID, IdentityID: member.IdentityID})
	if err != nil {
		t.Fatal(err)
	}
	asset.TemplateID = &templateID
	var version database.TemplateVersion
	if err := h.WithinTenant(ctx, origin.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", origin.TenantID, templateVersionID).Take(&version).Error
	}); err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := bus.RegisterQuery(assetget.Query{}, func(context.Context, any) (any, error) { return assetcore.View{Asset: asset}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(participantget.Query{}, func(context.Context, any) (any, error) {
		return participantcore.ParticipantView{Participant: participant, Selected: []identity.ID{identity.NewID()}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(templateresolve.Query{}, func(context.Context, any) (any, error) {
		return templateresolve.Result{Template: database.Template{ID: templateID, TenantID: origin.TenantID}, Version: version}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := resolve_reference.Setup(resolve_reference.Dependencies{DB: h.DB, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: h.DB, Bus: bus, InspectionService: inspectioncore.Service{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}}}))
	server.SetErrorPresenter(graph.PresentError)
	principal := requestctx.Principal{IdentityID: member.IdentityID, MembershipID: member.ID, TenantID: origin.TenantID, Roles: []string{auth.TenantAdmin}}
	operator := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: origin.TenantID, Principal: principal, CorrelationID: "it059"})
	now := time.Now().UTC()
	query := `mutation { createInspection(input:{assetId:"` + origin.AssetID.String() + `",participantId:"` + base.ParticipantID.String() + `",comparisonMode:FIXED_ORIGIN,referenceVersionId:"` + activated.ID.String() + `",dueAt:"` + now.Add(time.Hour).Format(time.RFC3339) + `",deadlineAt:"` + now.Add(24*time.Hour).Format(time.RFC3339) + `",reminderInstants:[],reason:"IT-059",clientMutationId:"IT-059"}) { inspection { id } } }`
	response := executeTask06GraphQL(t, server, query, operator)
	var payload struct {
		Data struct {
			Create struct {
				Inspection struct {
					ID string `json:"id"`
				} `json:"inspection"`
			} `json:"createInspection"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil || len(payload.Errors) != 0 || payload.Data.Create.Inspection.ID == "" {
		t.Fatalf("new FIXED_ORIGIN inspection could not select activated video origin: response=%s err=%v", response, err)
	}
	inspectionID, err := identity.ParseID(payload.Data.Create.Inspection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, origin.TenantID, func(tx *gorm.DB) error {
		var snapshot database.ReferenceSnapshot
		if err := tx.Where("tenant_id=? AND inspection_id=?", origin.TenantID, inspectionID).Take(&snapshot).Error; err != nil {
			return err
		}
		if snapshot.ComparisonMode != "FIXED_ORIGIN" || snapshot.ReferenceVersionID == nil || *snapshot.ReferenceVersionID != activated.ID {
			t.Errorf("inspection did not pin the activated origin: %+v", snapshot)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCreateInspectionRacesOriginActivationAndPinsOneCompleteVersionIT082(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	origin, err := h.SeedVideoOriginFixture(ctx, harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	version2ID, responsibility2ID, draft2ID := identity.NewID(), identity.NewID(), identity.NewID()
	media2ID, preparation2ID, frame2ID := identity.NewID(), identity.NewID(), identity.NewID()
	var first database.OriginVersion
	var base database.Inspection
	var participant database.Participant
	var asset database.Asset
	var baseTemplate database.Template
	var member database.Membership
	if err := h.WithinTenant(ctx, origin.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", origin.TenantID, origin.OriginVersionID).Take(&first).Error; err != nil {
			return err
		}
		var responsibility database.Responsibility
		if err := tx.Where("tenant_id=? AND id=?", origin.TenantID, origin.ResponsibilityID).Take(&responsibility).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND id=?", origin.TenantID, responsibility.InspectionID).Take(&base).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND id=?", origin.TenantID, base.ParticipantID).Take(&participant).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND id=?", origin.TenantID, origin.AssetID).Take(&asset).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND id=?", origin.TenantID, base.TemplateID).Take(&baseTemplate).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND role=?", origin.TenantID, auth.TenantAdmin).Take(&member).Error; err != nil {
			return err
		}
		version := database.OriginVersion{ID: version2ID, TenantID: origin.TenantID, OriginID: origin.OriginID, VersionNumber: 2, ResponsibilityID: responsibility2ID, SupersedesID: &first.ID, Status: "DRAFT", MediaKind: "VIDEO", IdempotencyKey: "it082-v2", CreatedAt: now, SubmittedAt: &now}
		draft := database.CaptureDraft{ID: draft2ID, TenantID: origin.TenantID, ResponsibilityID: responsibility2ID, Kind: "ORIGIN", MediaKind: "VIDEO", TemplateVersionID: base.TemplateVersionID, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[{"key":"video:main","required":true,"minimumCount":1,"maximumCount":1}]`), Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}
		media := database.MediaObject{ID: media2ID, TenantID: origin.TenantID, ResponsibilityID: responsibility2ID, ObjectKey: "it082/source-v2.mp4", ContentType: "video/mp4", SHA256: strings.Repeat("b", 64), SizeBytes: 100, Status: "READY", MediaKind: "VIDEO", IdempotencyKey: "it082-source-v2", RequirementKey: "video:main", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}
		preparation := database.VideoPreparationJob{ID: preparation2ID, TenantID: origin.TenantID, MediaID: media2ID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, ManifestDigest: strings.Repeat("c", 64), PendingObjectKeys: json.RawMessage(`[]`), CreatedAt: now}
		source := database.VideoSource{ID: identity.NewID(), TenantID: origin.TenantID, MediaID: media2ID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 96, Height: 64, FrameRate: 30, ScreeningStatus: "APPROVED", Status: "RETAINED", CreatedAt: now, UpdatedAt: now}
		frame := database.VideoFrame{ID: frame2ID, TenantID: origin.TenantID, SourceMediaID: media2ID, PreparationID: preparation2ID, PTSMicros: 2_000_000, SHA256: strings.Repeat("d", 64), ObjectKey: "it082/frame-v2.jpg", Width: 96, Height: 64, Role: "BASELINE", Quality: "GOOD", SizeBytes: 100, CreatedAt: now}
		scope := database.VideoScopeItem{ID: identity.NewID(), TenantID: origin.TenantID, PreparationID: preparation2ID, FrameID: frame2ID, EnvironmentCandidate: "bathroom-v2", ComponentLabel: "basin-v2", ViewLabel: "front", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now}
		for _, row := range []any{&version, &draft, &media, &preparation, &source, &frame, &scope} {
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Exec("INSERT INTO platform.video_admission (id, enabled, version, changed_at, reason, mutation_id, payload_hash) VALUES (1, TRUE, 1, ?, ?, ?, '') ON CONFLICT (id) DO UPDATE SET enabled=TRUE", now, "isolated IT-082 fixture", "it082").Error; err != nil {
		t.Fatal(err)
	}
	templateID, templateVersionID, err := createVideoInspectionTemplate(ctx, h, harness.Task06Fixture{TenantID: origin.TenantID, AssetID: origin.AssetID, SegmentVersionID: baseTemplate.SegmentVersionID, IdentityID: member.IdentityID})
	if err != nil {
		t.Fatal(err)
	}
	asset.TemplateID = &templateID
	var templateVersion database.TemplateVersion
	if err := h.WithinTenant(ctx, origin.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", origin.TenantID, templateVersionID).Take(&templateVersion).Error
	}); err != nil {
		t.Fatal(err)
	}
	assetStarted, finishAssetLookup := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var blockAssetLookup atomic.Bool
	bus := mediator.New()
	if err := bus.RegisterQuery(assetget.Query{}, func(context.Context, any) (any, error) {
		if blockAssetLookup.Load() {
			once.Do(func() { close(assetStarted) })
			<-finishAssetLookup
		}
		return assetcore.View{Asset: asset}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(participantget.Query{}, func(context.Context, any) (any, error) {
		return participantcore.ParticipantView{Participant: participant, Selected: []identity.ID{identity.NewID()}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(templateresolve.Query{}, func(context.Context, any) (any, error) {
		return templateresolve.Result{Template: database.Template{ID: templateID, TenantID: origin.TenantID}, Version: templateVersion}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := resolve_reference.Setup(resolve_reference.Dependencies{DB: h.DB, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	service := inspectioncore.Service{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}
	operator := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: origin.TenantID, Principal: requestctx.Principal{IdentityID: member.IdentityID, MembershipID: member.ID, TenantID: origin.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "it082"})
	pinnedV1, err := service.Create(operator, inspectioncore.CreateInput{TenantID: origin.TenantID, AssetID: origin.AssetID, ParticipantID: base.ParticipantID, ComparisonMode: catalog.FixedOrigin, Source: inspectioncore.SourceManual, SourceKey: "it060-pinned", Reason: "IT-060", DueAt: now.Add(time.Hour), DeadlineAt: now.Add(24 * time.Hour)})
	if err != nil {
		t.Fatalf("create V1 pinned to O1: %v", err)
	}
	if pinnedV1.Reference.ReferenceVersionID == nil || *pinnedV1.Reference.ReferenceVersionID != first.ID {
		t.Fatalf("V1 did not pin O1 before O2 activation: %+v", pinnedV1.Reference)
	}
	blockAssetLookup.Store(true)
	type result struct {
		view inspectioncore.View
		err  error
	}
	created := make(chan result, 1)
	go func() {
		created <- func() result {
			view, err := service.Create(operator, inspectioncore.CreateInput{TenantID: origin.TenantID, AssetID: origin.AssetID, ParticipantID: base.ParticipantID, ComparisonMode: catalog.FixedOrigin, Source: inspectioncore.SourceManual, SourceKey: "it082-create", Reason: "IT-082", DueAt: now.Add(time.Hour), DeadlineAt: now.Add(24 * time.Hour)})
			return result{view: view, err: err}
		}()
	}()
	<-assetStarted
	originService := origincore.Service{DB: h.DB, Within: h.WithinTenant, Now: func() time.Time { return now }}
	activationStart := make(chan struct{})
	activationResults := make(chan error, 2)
	for _, responsibilityID := range []identity.ID{origin.ResponsibilityID, responsibility2ID} {
		go func(id identity.ID) {
			<-activationStart
			_, activationErr := originService.ActivateCompleted(ctx, origin.TenantID, id)
			activationResults <- activationErr
		}(responsibilityID)
	}
	close(activationStart)
	for range 2 {
		if activationErr := <-activationResults; activationErr != nil {
			close(finishAssetLookup)
			t.Fatalf("race O1/O2 activation while createInspection is in progress: %v", activationErr)
		}
	}
	if err := h.WithinTenant(ctx, origin.TenantID, func(tx *gorm.DB) error {
		var activeCount int64
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND origin_id=? AND status='ACTIVE'", origin.TenantID, origin.OriginID).Count(&activeCount).Error; err != nil {
			return err
		}
		if activeCount != 1 {
			t.Fatalf("concurrent O1/O2 activation left %d active versions, want exactly one", activeCount)
		}
		var snapshot database.ReferenceSnapshot
		if err := tx.Where("tenant_id=? AND inspection_id=?", origin.TenantID, pinnedV1.Inspection.ID).Take(&snapshot).Error; err != nil {
			return err
		}
		if snapshot.ReferenceVersionID == nil || *snapshot.ReferenceVersionID != first.ID {
			t.Fatalf("V1 pin changed after O2 activation: %+v", snapshot)
		}
		return nil
	}); err != nil {
		close(finishAssetLookup)
		t.Fatal(err)
	}
	close(finishAssetLookup)
	out := <-created
	if out.err != nil {
		t.Fatalf("createInspection racing O1→O2 activation: %v", out.err)
	}
	if out.view.Reference.ReferenceVersionID == nil || *out.view.Reference.ReferenceVersionID != version2ID {
		t.Fatalf("inspection did not pin complete activated O2: reference=%+v", out.view.Reference)
	}
	var snapshot database.ReferenceSnapshot
	if err := h.WithinTenant(ctx, origin.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND inspection_id=?", origin.TenantID, out.view.Inspection.ID).Take(&snapshot).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Scope []struct {
			FrameID       identity.ID `json:"frameId"`
			SourceMediaID identity.ID `json:"sourceMediaId"`
			Component     string      `json:"componentLabel"`
		} `json:"scope"`
	}
	if err := json.Unmarshal(snapshot.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if snapshot.ReferenceVersionID == nil || *snapshot.ReferenceVersionID != version2ID || len(payload.Scope) != 1 || payload.Scope[0].FrameID != frame2ID || payload.Scope[0].SourceMediaID != media2ID || payload.Scope[0].Component != "basin-v2" || first.VideoPreparationID == nil {
		t.Fatalf("create/activation race persisted mixed reference lineage: reference=%+v payload=%s", snapshot, snapshot.Payload)
	}
}

func TestCreateInspectionFixedOriginWithoutPreparedOriginGraphQLIT079(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
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
	templateID, templateVersionID, err := createVideoInspectionTemplate(ctx, h, fixture)
	if err != nil {
		t.Fatal(err)
	}

	bus := mediator.New()
	if err := bus.RegisterQuery(assetget.Query{}, func(_ context.Context, raw any) (any, error) {
		query := raw.(assetget.Query)
		if query.TenantID != fixture.TenantID || query.AssetID != fixture.AssetID {
			t.Fatalf("unexpected asset lookup: %+v", query)
		}
		return assetcore.View{Asset: database.Asset{ID: fixture.AssetID, TenantID: fixture.TenantID, BusinessUnitID: fixture.BusinessUnitID, TemplateID: &templateID, Status: "ACTIVE", Version: 1}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(participantget.Query{}, func(_ context.Context, raw any) (any, error) {
		query := raw.(participantget.Query)
		if query.TenantID != fixture.TenantID || query.ParticipantID != fixture.ParticipantID {
			t.Fatalf("unexpected participant lookup: %+v", query)
		}
		return participantcore.ParticipantView{Participant: database.Participant{ID: fixture.ParticipantID, TenantID: fixture.TenantID, BusinessUnitID: fixture.BusinessUnitID, Status: "ACTIVE", Version: 1}, Selected: []identity.ID{identity.NewID()}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	document, err := json.Marshal(catalog.TemplateDocument{SchemaVersion: catalog.SchemaVersion, IsVideo: true, DefaultComparisonMode: catalog.FixedOrigin, AnalysisType: "REAL_ESTATE"})
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(templateresolve.Query{}, func(_ context.Context, raw any) (any, error) {
		query := raw.(templateresolve.Query)
		if query.TenantID != fixture.TenantID || query.TemplateID != templateID {
			t.Fatalf("unexpected template lookup: %+v", query)
		}
		return templateresolve.Result{Template: database.Template{ID: templateID, TenantID: fixture.TenantID}, Version: database.TemplateVersion{ID: templateVersionID, TenantID: fixture.TenantID, TemplateID: templateID, DefinitionJSON: document}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := resolve_reference.Setup(resolve_reference.Dependencies{DB: h.DB, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{
		DB: h.DB, Bus: bus,
		InspectionService: inspectioncore.Service{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}},
	}}))
	server.SetErrorPresenter(graph.PresentError)
	meta := requestctx.Metadata{TenantID: fixture.TenantID, Principal: requestctx.Principal{IdentityID: fixture.IdentityID, TenantID: fixture.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "it079"}
	requestContext := requestctx.WithMetadata(context.Background(), meta)
	query := `mutation { createInspection(input:{assetId:"` + fixture.AssetID.String() + `",participantId:"` + fixture.ParticipantID.String() + `",comparisonMode:FIXED_ORIGIN,dueAt:"2026-10-17T12:00:00Z",deadlineAt:"2026-10-18T12:00:00Z",reminderInstants:[],reason:"IT-079",clientMutationId:"IT-079"}) { inspection { id } userErrors { code field } clientMutationId } }`
	response := executeTask06GraphQL(t, server, query, requestContext)
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Extensions map[string]any `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(response), &envelope); err != nil {
		t.Fatalf("decode GraphQL response: %v; response=%s", err, response)
	}
	if len(envelope.Errors) != 1 || envelope.Errors[0].Extensions["code"] != "INVALID_INPUT" || envelope.Errors[0].Extensions["field"] != "referenceVersionId" {
		t.Fatalf("expected GraphQL INVALID_INPUT on referenceVersionId using the established error envelope, got %s", response)
	}
	if string(envelope.Data) != "null" {
		t.Fatalf("expected null mutation data, got %s", response)
	}
	assertIT079NoMutationSideEffects(t, ctx, h, fixture, "IT-079")
}

func createVideoInspectionTemplate(ctx context.Context, h *harness.Harness, fixture harness.Task06Fixture) (identity.ID, identity.ID, error) {
	return createVideoInspectionTemplateWithMode(ctx, h, fixture, catalog.FixedOrigin)
}

func createVideoInspectionTemplateWithMode(ctx context.Context, h *harness.Harness, fixture harness.Task06Fixture, mode catalog.ComparisonMode) (identity.ID, identity.ID, error) {
	document, err := json.Marshal(catalog.TemplateDocument{SchemaVersion: catalog.SchemaVersion, IsVideo: true, DefaultComparisonMode: mode, AnalysisType: "REAL_ESTATE"})
	if err != nil {
		return identity.ID{}, identity.ID{}, err
	}
	templateID, versionID := identity.NewID(), identity.NewID()
	err = h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		version := database.TemplateVersion{ID: versionID, TenantID: fixture.TenantID, TemplateID: templateID, VersionNumber: 1, SchemaVersion: catalog.SchemaVersion, DefinitionJSON: document, CanonicalDigest: strings.Repeat("0", 64), Status: "ACTIVE", IdempotencyKey: "it079-template-version", PublishedAt: now, CreatedBy: fixture.IdentityID}
		template := database.Template{ID: templateID, TenantID: fixture.TenantID, Key: "it079-video", Name: "IT-079 video template", SegmentVersionID: fixture.SegmentVersionID, ActiveVersionID: &versionID, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&template).Error; err != nil {
			return err
		}
		if err := tx.Create(&version).Error; err != nil {
			return err
		}
		return tx.Model(&database.Asset{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.AssetID).Update("template_id", templateID).Error
	})
	return templateID, versionID, err
}

func assertIT079NoMutationSideEffects(t *testing.T, ctx context.Context, h *harness.Harness, fixture harness.Task06Fixture, sourceKey string) {
	t.Helper()
	err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		checks := []struct {
			name  string
			model any
			query string
			args  []any
		}{
			{"inspection", &database.Inspection{}, "tenant_id=? AND source_key=?", []any{fixture.TenantID, sourceKey}},
			{"responsibility", &database.Responsibility{}, "tenant_id=? AND inspection_id IN (SELECT id FROM inspections.inspections WHERE tenant_id=? AND source_key=?)", []any{fixture.TenantID, fixture.TenantID, sourceKey}},
			{"policy snapshot", &database.PolicySnapshot{}, "tenant_id=? AND inspection_id IN (SELECT id FROM inspections.inspections WHERE tenant_id=? AND source_key=?)", []any{fixture.TenantID, fixture.TenantID, sourceKey}},
			{"reference snapshot", &database.ReferenceSnapshot{}, "tenant_id=? AND inspection_id IN (SELECT id FROM inspections.inspections WHERE tenant_id=? AND source_key=?)", []any{fixture.TenantID, fixture.TenantID, sourceKey}},
			{"capture draft", &database.CaptureDraft{}, "tenant_id=? AND responsibility_id IN (SELECT r.id FROM inspections.responsibilities r JOIN inspections.inspections i ON i.id=r.inspection_id WHERE i.tenant_id=? AND i.source_key=?)", []any{fixture.TenantID, fixture.TenantID, sourceKey}},
			{"invitation", &database.Invitation{}, "tenant_id=?", []any{fixture.TenantID}},
		}
		for _, check := range checks {
			var count int64
			if err := tx.Model(check.model).Where(check.query, check.args...).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return gorm.ErrRecordNotFound
			}
		}
		var fallbackCount int64
		if err := tx.Model(&database.ReferenceSnapshot{}).Where("tenant_id=? AND comparison_mode=? AND inspection_id IN (SELECT id FROM inspections.inspections WHERE tenant_id=? AND source_key=?)", fixture.TenantID, string(catalog.ChecklistOnly), fixture.TenantID, sourceKey).Count(&fallbackCount).Error; err != nil {
			return err
		}
		if fallbackCount != 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			t.Fatal("IT-079 created inspection-related rows or a CHECKLIST_ONLY fallback")
		}
		t.Fatalf("check IT-079 mutation side effects: %v", err)
	}
}
