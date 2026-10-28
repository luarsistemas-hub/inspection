package catalog_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	assetarchive "inspection/services/inspection/internal/features/assets/archive_asset"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	assetlist "inspection/services/inspection/internal/features/assets/list_assets"
	assetregister "inspection/services/inspection/internal/features/assets/register_asset"
	assetupdate "inspection/services/inspection/internal/features/assets/update_asset"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	participantdeactivate "inspection/services/inspection/internal/features/participants/deactivate_participant"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	participantlist "inspection/services/inspection/internal/features/participants/list_participants"
	participantchannels "inspection/services/inspection/internal/features/participants/set_delivery_channels"
	participantupsert "inspection/services/inspection/internal/features/participants/upsert_participant"
	participantverify "inspection/services/inspection/internal/features/participants/verify_channel"
	segmentactivate "inspection/services/inspection/internal/features/segments/activate_definition"
	segmentcore "inspection/services/inspection/internal/features/segments/core"
	segmentlist "inspection/services/inspection/internal/features/segments/list_definitions"
	segmentpublish "inspection/services/inspection/internal/features/segments/publish_definition"
	segmentresolve "inspection/services/inspection/internal/features/segments/resolve_definition"
	templateactivate "inspection/services/inspection/internal/features/templates/activate_template"
	"inspection/services/inspection/internal/features/templates/catalog"
	templatecore "inspection/services/inspection/internal/features/templates/core"
	templatelist "inspection/services/inspection/internal/features/templates/list_templates"
	templatepublish "inspection/services/inspection/internal/features/templates/publish_template"
	templateresolve "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/graphql/resolvers"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestCatalogSlicesIT021ToIT070(t *testing.T) {
	adminDSN, runtimeDSN := os.Getenv("INSPECTION_TEST_DATABASE_URL"), os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL")
	if adminDSN == "" || runtimeDSN == "" {
		t.Skip("catalog PostgreSQL integration requires INSPECTION_TEST_DATABASE_URL and INSPECTION_TEST_RUNTIME_DATABASE_URL")
	}
	admin, err := gorm.Open(postgres.Open(adminDSN), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := (database.Migrator{DB: admin}).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	runtimeDB, err := gorm.Open(postgres.Open(runtimeDSN), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	tenantID, unitID, identityID, membershipID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	seed := []any{&database.Tenant{ID: tenantID, TenantID: tenantID, Name: "Catalog tenant", Language: "pt-BR", DefaultTimezone: "America/Sao_Paulo", Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}, &database.BusinessUnit{ID: unitID, TenantID: tenantID, Code: "main-" + tenantID.String(), Name: "Main", Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}, &database.Membership{ID: membershipID, TenantID: tenantID, IdentityID: identityID, Issuer: "test", Subject: identityID.String(), Role: auth.TenantAdmin, Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}}
	for _, row := range seed {
		if err := admin.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identityID, MembershipID: membershipID, TenantID: tenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "catalog-integration", StartedAt: now})
	bus := mediator.New()
	authorizer := auth.Authorizer{Store: auth.GORMMembershipStore{DB: runtimeDB}}
	setups := []func() error{
		func() error {
			return participantupsert.Setup(participantupsert.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		}, func() error {
			return participantverify.Setup(participantverify.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		}, func() error {
			return participantchannels.Setup(participantchannels.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		}, func() error {
			return participantdeactivate.Setup(participantdeactivate.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		}, func() error {
			return participantget.Setup(participantget.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		}, func() error {
			return participantlist.Setup(participantlist.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return segmentpublish.Setup(segmentpublish.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		}, func() error {
			return segmentactivate.Setup(segmentactivate.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		}, func() error {
			return segmentlist.Setup(segmentlist.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		}, func() error { return segmentresolve.Setup(segmentresolve.Dependencies{DB: runtimeDB, Bus: bus}) },
		func() error {
			return templatepublish.Setup(templatepublish.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		}, func() error {
			return templateactivate.Setup(templateactivate.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		}, func() error {
			return templatelist.Setup(templatelist.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		}, func() error { return templateresolve.Setup(templateresolve.Dependencies{DB: runtimeDB, Bus: bus}) },
		func() error {
			return assetregister.Setup(assetregister.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		}, func() error {
			return assetupdate.Setup(assetupdate.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		}, func() error {
			return assetarchive.Setup(assetarchive.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		}, func() error {
			return assetget.Setup(assetget.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		}, func() error {
			return assetlist.Setup(assetlist.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		},
	}
	for _, setup := range setups {
		if err := setup(); err != nil {
			t.Fatal(err)
		}
	}

	participantRaw, err := bus.Send(ctx, participantupsert.Command{TenantID: tenantID, BusinessUnitID: unitID, Name: "Person", SegmentRole: "TENANT_PARTICIPANT", IdempotencyKey: "participant-" + tenantID.String(), Contacts: []participantcore.ContactInput{{Channel: "EMAIL", Value: "person@example.com"}, {Channel: "SMS", Value: "+5511999999999"}}})
	if err != nil {
		t.Fatal(err)
	}
	participant := participantRaw.(participantcore.ParticipantView)
	if len(participant.Contacts) != 2 {
		t.Fatal("participant contacts not stored")
	}
	replayed, err := bus.Send(ctx, participantupsert.Command{TenantID: tenantID, BusinessUnitID: unitID, Name: "Changed", SegmentRole: "OTHER", IdempotencyKey: "participant-" + tenantID.String()})
	if err != nil || replayed.(participantcore.ParticipantView).Participant.ID != participant.Participant.ID {
		t.Fatal("participant idempotency failed")
	}
	if _, err := bus.Send(ctx, participantchannels.Command{TenantID: tenantID, ParticipantID: participant.Participant.ID, ContactIDs: []identity.ID{participant.Contacts[0].ID}, ExpectedVersion: 1}); apperrorCode(err) != apperror.InvalidState {
		t.Fatalf("unverified channel accepted: %v", err)
	}
	if _, err := bus.Send(ctx, participantverify.Command{TenantID: tenantID, ContactID: participant.Contacts[0].ID, Success: true, IdempotencyKey: "verify-direct-" + tenantID.String()}); err != nil {
		t.Fatal(err)
	}
	selectedRaw, err := bus.Send(ctx, participantchannels.Command{TenantID: tenantID, ParticipantID: participant.Participant.ID, ContactIDs: []identity.ID{participant.Contacts[0].ID}, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if selectedRaw.(participantcore.ParticipantView).Participant.Version != 2 {
		t.Fatal("channel version not advanced")
	}
	if _, err := bus.Send(ctx, participantchannels.Command{TenantID: tenantID, ParticipantID: participant.Participant.ID, ContactIDs: []identity.ID{participant.Contacts[0].ID}, ExpectedVersion: 1}); err != nil {
		t.Fatalf("exact participant retry was not idempotent: %v", err)
	}
	if _, err := bus.Send(ctx, participantverify.Command{TenantID: tenantID, ContactID: participant.Contacts[1].ID, Success: true, IdempotencyKey: "verify-secondary-" + tenantID.String()}); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Send(ctx, participantchannels.Command{TenantID: tenantID, ParticipantID: participant.Participant.ID, ContactIDs: []identity.ID{participant.Contacts[1].ID}, ExpectedVersion: 1}); apperrorCode(err) != apperror.Conflict {
		t.Fatalf("different stale participant update accepted: %v", err)
	}

	schema := []byte(`{"type":"object","properties":{"kind":{"type":"string"},"identifier":{"type":"string","maxLength":200}},"required":["kind","identifier"],"additionalProperties":false}`)
	segmentRaw, err := bus.Send(ctx, segmentpublish.Command{TenantID: tenantID, Key: "property", Name: "Property", IdempotencyKey: "segment-" + tenantID.String(), SchemaJSON: schema, UISchemaJSON: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	segment := segmentRaw.(segmentcore.View)
	if _, err := bus.Send(ctx, segmentactivate.Command{TenantID: tenantID, VersionID: segment.Version.ID, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	document := catalog.TemplateDocument{SchemaVersion: 1, SegmentVersionID: segment.Version.ID.String(), ParticipantRoles: []string{"TENANT_PARTICIPANT"}, DefaultComparisonMode: catalog.ChecklistOnly, Requirements: []catalog.CaptureRequirement{{Key: "overview", Section: "general", Label: "Overview", EvidenceKind: "PHOTO", MinimumCount: 1, MaximumCount: 2, Required: true, DescriptionRequired: true, CaptureSourcePolicy: "CAMERA_DEFAULT"}}, AnalysisType: "REAL_ESTATE", Policy: catalog.Policy{GPSRequired: true, GeofenceMeters: 150, AllowGallery: true}}
	definitionJSON, _ := json.Marshal(document)
	templateRaw, err := bus.Send(ctx, templatepublish.Command{TenantID: tenantID, Key: "periodic", Name: "Periodic", IdempotencyKey: "template-" + tenantID.String(), DefinitionJSON: definitionJSON})
	if err != nil {
		t.Fatal(err)
	}
	template := templateRaw.(templatecore.View)
	if _, err := bus.Send(ctx, templateactivate.Command{TenantID: tenantID, VersionID: template.Version.ID, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	environmentDocument := catalog.TemplateDocument{SchemaVersion: catalog.EnvironmentSchemaVersion, SegmentVersionID: segment.Version.ID.String(), ParticipantRoles: []string{"TENANT_PARTICIPANT"}, DefaultComparisonMode: catalog.ChecklistOnly, CaptureLayout: catalog.EnvironmentsLayout, EnvironmentCapture: catalog.DefaultEnvironmentCapture(), SuggestedEnvironmentTypes: []string{"LIVING_ROOM", "KITCHEN"}, AnalysisType: "REAL_ESTATE", Policy: catalog.Policy{GPSRequired: true, GeofenceMeters: 150, AllowGallery: true}}
	environmentJSON, _ := json.Marshal(environmentDocument)
	environmentRaw, err := bus.Send(ctx, templatepublish.Command{TenantID: tenantID, Key: "environments", Name: "Environments", IdempotencyKey: "template-environments-" + tenantID.String(), DefinitionJSON: environmentJSON})
	if err != nil {
		t.Fatal(err)
	}
	environmentTemplate := environmentRaw.(templatecore.View)
	activatedRaw, err := bus.Send(ctx, templateactivate.Command{TenantID: tenantID, VersionID: environmentTemplate.Version.ID, ExpectedVersion: 1})
	if err != nil {
		t.Fatalf("schema v2 template activation failed: %v", err)
	}
	if activated := activatedRaw.(templatecore.View); activated.Version.Status != "ACTIVE" || activated.Version.SchemaVersion != catalog.EnvironmentSchemaVersion || activated.Template.ActiveVersionID == nil || *activated.Template.ActiveVersionID != environmentTemplate.Version.ID {
		t.Fatalf("schema v2 template was not activated: %+v", activated)
	}
	assetInput := assetcore.Input{TenantID: tenantID, BusinessUnitID: unitID, SegmentVersionID: segment.Version.ID, TemplateID: &template.Template.ID, Name: "Asset", ExternalKey: "asset-1", Address: "Street", IdempotencyKey: "asset-" + tenantID.String(), GeofenceMeters: 150, Attributes: map[string]any{"kind": "house", "identifier": "A1"}, Assignments: []assetcore.AssignmentInput{{ParticipantID: participant.Participant.ID, Role: "TENANT_PARTICIPANT"}}}
	assetRaw, err := bus.Send(ctx, assetregister.Command(assetInput))
	if err != nil {
		t.Fatal(err)
	}
	asset := assetRaw.(assetcore.View)
	if asset.Asset.Status != "ACTIVE" {
		t.Fatal("valid asset not active")
	}
	if replay, err := bus.Send(ctx, assetregister.Command(assetInput)); err != nil || replay.(assetcore.View).Asset.ID != asset.Asset.ID {
		t.Fatal("asset idempotency failed")
	}
	assetInput.Name = "Updated"
	updatedRaw, err := bus.Send(ctx, assetupdate.Command{AssetID: asset.Asset.ID, ExpectedVersion: 1, Input: assetInput})
	if err != nil {
		t.Fatal(err)
	}
	updated := updatedRaw.(assetcore.View)
	if updated.Asset.Version != 2 {
		t.Fatal("asset optimistic version not advanced")
	}
	if _, err := bus.Send(ctx, assetupdate.Command{AssetID: asset.Asset.ID, ExpectedVersion: 1, Input: assetInput}); err != nil {
		t.Fatalf("exact asset retry was not idempotent: %v", err)
	}
	staleAssetInput := assetInput
	staleAssetInput.Name = "Different"
	if _, err := bus.Send(ctx, assetupdate.Command{AssetID: asset.Asset.ID, ExpectedVersion: 1, Input: staleAssetInput}); apperrorCode(err) != apperror.Conflict {
		t.Fatalf("different stale asset update accepted: %v", err)
	}
	if _, err := bus.Ask(ctx, assetget.Query{TenantID: tenantID, AssetID: asset.Asset.ID}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &resolvers.Resolver{Bus: bus, DB: runtimeDB}}))
	server.SetErrorPresenter(graph.PresentError)
	var templateDefinition map[string]any
	_ = json.Unmarshal(definitionJSON, &templateDefinition)
	assetVariables := map[string]any{"businessUnitId": unitID.String(), "segmentVersionId": segment.Version.ID.String(), "templateId": template.Template.ID.String(), "name": "Asset", "externalKey": "asset-1", "address": "Street", "geofenceMeters": 150, "attributes": map[string]any{"kind": "house", "identifier": "A1"}, "policyOverrides": map[string]any{}, "assignments": []any{map[string]any{"participantId": participant.Participant.ID.String(), "role": "TENANT_PARTICIPANT"}}}
	successes := []struct {
		name, query string
		variables   map[string]any
	}{
		{"IT-397 participants", `query { participants { nodes { id } pageInfo { hasNextPage } } }`, nil}, {"IT-399 participant", `query($id:ID!){ participant(id:$id){ id } }`, map[string]any{"id": participant.Participant.ID.String()}}, {"IT-401 segmentDefinitions", `query { segmentDefinitions { nodes { id } pageInfo { hasNextPage } } }`, nil}, {"IT-403 templates", `query { templates { nodes { id } pageInfo { hasNextPage } } }`, nil}, {"IT-405 templateVersion", `query($id:ID!){ templateVersion(id:$id){ id canonicalDigest } }`, map[string]any{"id": template.Version.ID.String()}}, {"IT-407 assets", `query { assets { nodes { id } pageInfo { hasNextPage } } }`, nil}, {"IT-409 asset", `query($id:ID!){ asset(id:$id){ id status } }`, map[string]any{"id": asset.Asset.ID.String()}},
		{"IT-453 upsertParticipant", `mutation($input:UpsertParticipantInput!){ upsertParticipant(input:$input){ participant{id} userErrors{code} } }`, map[string]any{"input": map[string]any{"businessUnitId": unitID.String(), "name": "Person", "segmentRole": "TENANT_PARTICIPANT", "contacts": []any{}, "clientMutationId": "participant-" + tenantID.String()}}}, {"IT-455 verifyContact", `mutation($input:VerifyContactInput!){ verifyContact(input:$input){ contact{id verified} userErrors{code} } }`, map[string]any{"input": map[string]any{"contactId": participant.Contacts[0].ID.String(), "verified": true, "clientMutationId": "verify-graphql"}}}, {"IT-457 setDeliveryChannels", `mutation($input:SetDeliveryChannelsInput!){ setDeliveryChannels(input:$input){ participant{id version} userErrors{code} } }`, map[string]any{"input": map[string]any{"participantId": participant.Participant.ID.String(), "contactIds": []string{participant.Contacts[0].ID.String()}, "expectedVersion": 2, "clientMutationId": "channels-graphql"}}}, {"IT-459 publishSegmentDefinition", `mutation($input:PublishSegmentDefinitionInput!){ publishSegmentDefinition(input:$input){ definition{id} userErrors{code} } }`, map[string]any{"input": map[string]any{"key": "property", "name": "Property", "schema": map[string]any{"type": "object", "properties": map[string]any{"kind": map[string]any{"type": "string"}}, "required": []string{"kind"}}, "uiSchema": map[string]any{}, "clientMutationId": "segment-" + tenantID.String()}}}, {"IT-461 publishTemplateVersion", `mutation($input:PublishTemplateVersionInput!){ publishTemplateVersion(input:$input){ template{id} version{id} userErrors{code} } }`, map[string]any{"input": map[string]any{"key": "periodic", "name": "Periodic", "definition": templateDefinition, "clientMutationId": "template-" + tenantID.String()}}}, {"IT-463 activateTemplateVersion", `mutation($input:ActivateTemplateVersionInput!){ activateTemplateVersion(input:$input){ template{id} version{id status} userErrors{code} } }`, map[string]any{"input": map[string]any{"versionId": template.Version.ID.String(), "expectedVersion": 2, "clientMutationId": "activate-graphql"}}}, {"IT-467 registerAsset", `mutation($input:RegisterAssetInput!){ registerAsset(input:$input){ asset{id} userErrors{code} } }`, map[string]any{"input": map[string]any{"asset": assetVariables, "clientMutationId": "asset-" + tenantID.String()}}}, {"IT-469 updateAsset", `mutation($input:UpdateAssetInput!){ updateAsset(input:$input){ asset{id version} userErrors{code} } }`, map[string]any{"input": map[string]any{"assetId": asset.Asset.ID.String(), "expectedVersion": 2, "asset": assetVariables, "clientMutationId": "asset-update-graphql"}}}, {"IT-471 archiveAsset", `mutation($input:ArchiveAssetInput!){ archiveAsset(input:$input){ userErrors{code} } }`, map[string]any{"input": map[string]any{"assetId": asset.Asset.ID.String(), "expectedVersion": 3, "clientMutationId": "asset-archive-graphql"}}},
	}
	for _, test := range successes {
		t.Run(test.name, func(t *testing.T) {
			body := graphqlRequest(t, server, ctx, test.query, test.variables)
			if strings.Contains(body, `"errors"`) {
				t.Fatalf("GraphQL success failed: %s", body)
			}
		})
	}
	failures := []string{`query { participants { nodes{id} } }`, `query($id:ID!){ participant(id:$id){id} }`, `query { segmentDefinitions {nodes{id}} }`, `query { templates {nodes{id}} }`, `query($id:ID!){templateVersion(id:$id){id}}`, `query {assets {nodes{id}} }`, `query($id:ID!){asset(id:$id){id}}`}
	for index, query := range failures {
		t.Run("safe query failure", func(t *testing.T) {
			body := graphqlRequest(t, server, context.Background(), query, map[string]any{"id": identity.NewID().String()})
			if !strings.Contains(body, "UNAUTHENTICATED") || strings.Contains(body, "postgres") {
				t.Fatalf("unsafe failure %d: %s", index, body)
			}
		})
	}
	mutationFailures := []struct {
		name, query string
		variables   map[string]any
	}{
		{"IT-454", `mutation($input:UpsertParticipantInput!){upsertParticipant(input:$input){participant{id}}}`, map[string]any{"input": map[string]any{"businessUnitId": unitID.String(), "name": "Bad", "segmentRole": "ROLE", "contacts": []any{map[string]any{"channel": "EMAIL", "value": "bad"}}, "clientMutationId": "bad-participant"}}},
		{"IT-456", `mutation($input:VerifyContactInput!){verifyContact(input:$input){contact{id}}}`, map[string]any{"input": map[string]any{"contactId": "bad", "verified": true, "clientMutationId": "bad-verify"}}},
		{"IT-458", `mutation($input:SetDeliveryChannelsInput!){setDeliveryChannels(input:$input){participant{id}}}`, map[string]any{"input": map[string]any{"participantId": participant.Participant.ID.String(), "contactIds": []string{participant.Contacts[1].ID.String()}, "expectedVersion": 3, "clientMutationId": "bad-channels"}}},
		{"IT-460", `mutation($input:PublishSegmentDefinitionInput!){publishSegmentDefinition(input:$input){definition{id}}}`, map[string]any{"input": map[string]any{"key": "bad", "name": "Bad", "schema": map[string]any{"type": "array"}, "uiSchema": map[string]any{}, "clientMutationId": "bad-segment"}}},
		{"IT-462", `mutation($input:PublishTemplateVersionInput!){publishTemplateVersion(input:$input){template{id}}}`, map[string]any{"input": map[string]any{"key": "bad", "name": "Bad", "definition": map[string]any{"schemaVersion": 1}, "clientMutationId": "bad-template"}}},
		{"IT-464", `mutation($input:ActivateTemplateVersionInput!){activateTemplateVersion(input:$input){template{id}}}`, map[string]any{"input": map[string]any{"versionId": "bad", "expectedVersion": 1, "clientMutationId": "bad-activate"}}},
		{"IT-468", `mutation($input:RegisterAssetInput!){registerAsset(input:$input){asset{id}}}`, map[string]any{"input": map[string]any{"asset": map[string]any{"businessUnitId": unitID.String(), "segmentVersionId": segment.Version.ID.String(), "name": "Bad", "externalKey": "bad", "address": "Street", "geofenceMeters": 1, "attributes": map[string]any{"kind": "house", "identifier": "B"}, "policyOverrides": map[string]any{}, "assignments": []any{}}, "clientMutationId": "bad-asset"}}},
		{"IT-470", `mutation($input:UpdateAssetInput!){updateAsset(input:$input){asset{id}}}`, map[string]any{"input": map[string]any{"assetId": "bad", "expectedVersion": 1, "asset": assetVariables, "clientMutationId": "bad-update"}}},
		{"IT-472", `mutation($input:ArchiveAssetInput!){archiveAsset(input:$input){asset{id}}}`, map[string]any{"input": map[string]any{"assetId": "bad", "expectedVersion": 1, "clientMutationId": "bad-archive"}}},
	}
	for _, test := range mutationFailures {
		t.Run(test.name, func(t *testing.T) {
			body := graphqlRequest(t, server, ctx, test.query, test.variables)
			if !strings.Contains(body, `"errors"`) || (!strings.Contains(body, "INVALID_INPUT") && !strings.Contains(body, "INVALID_STATE") && !strings.Contains(body, "CONFLICT")) {
				t.Fatalf("expected stable safe failure: %s", body)
			}
			if strings.Contains(body, "postgres") || strings.Contains(body, "SELECT ") {
				t.Fatalf("internal detail leaked: %s", body)
			}
		})
	}
	if result, err := bus.Ask(ctx, assetlist.Query{TenantID: tenantID, First: 100}); err != nil || len(result.(assetlist.Result).Assets) != 1 {
		t.Fatal("asset list contract failed")
	}
	if _, err := bus.Send(ctx, participantdeactivate.Command{TenantID: tenantID, ParticipantID: participant.Participant.ID, ExpectedVersion: 2}); err != nil {
		t.Fatalf("participant deactivation failed: %v", err)
	}
	deactivatedRaw, err := bus.Ask(ctx, participantget.Query{TenantID: tenantID, ParticipantID: participant.Participant.ID})
	if err != nil || deactivatedRaw.(participantcore.ParticipantView).Participant.Status != "INACTIVE" {
		t.Fatalf("participant lifecycle not persisted: %v", err)
	}

	var verification database.ContactVerification
	if err := admin.Where("tenant_id = ? AND contact_id = ?", tenantID, participant.Contacts[0].ID).First(&verification).Error; err != nil {
		t.Fatal(err)
	}
	assertImmutableCatalogRows(t, admin, template.Version.ID, updated.Attributes.ID, verification.ID)
	assertTenantIsolation(t, admin, bus, ctx, asset.Asset.ID)
}

func TestVideoTemplateGraphQLTransportIT381ToIT388(t *testing.T) {
	adminDSN, runtimeDSN := os.Getenv("INSPECTION_TEST_DATABASE_URL"), os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL")
	if adminDSN == "" || runtimeDSN == "" {
		t.Skip("video template GraphQL integration requires INSPECTION_TEST_DATABASE_URL and INSPECTION_TEST_RUNTIME_DATABASE_URL")
	}
	admin, err := gorm.Open(postgres.Open(adminDSN), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := (database.Migrator{DB: admin}).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	runtimeDB, err := gorm.Open(postgres.Open(runtimeDSN), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	tenantID, identityID, membershipID, viewerIdentityID, viewerMembershipID, customerViewerIdentityID, customerViewerMembershipID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	for _, row := range []any{
		&database.Tenant{ID: tenantID, TenantID: tenantID, Name: "Video template tenant", Language: "pt-BR", DefaultTimezone: "America/Sao_Paulo", Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now},
		&database.Membership{ID: membershipID, TenantID: tenantID, IdentityID: identityID, Issuer: "test", Subject: identityID.String(), Role: auth.TenantAdmin, Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now},
		&database.Membership{ID: viewerMembershipID, TenantID: tenantID, IdentityID: viewerIdentityID, Issuer: "test", Subject: viewerIdentityID.String(), Role: auth.Viewer, Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now},
		&database.Membership{ID: customerViewerMembershipID, TenantID: tenantID, IdentityID: customerViewerIdentityID, Issuer: "test", Subject: customerViewerIdentityID.String(), Role: auth.CustomerViewer, Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now},
	} {
		if err := admin.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identityID, MembershipID: membershipID, TenantID: tenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "video-template-graphql", StartedAt: now})
	bus := mediator.New()
	authorizer := auth.Authorizer{Store: auth.GORMMembershipStore{DB: runtimeDB}}
	for _, setup := range []func() error{
		func() error {
			return segmentpublish.Setup(segmentpublish.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return segmentactivate.Setup(segmentactivate.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		},
		func() error { return segmentresolve.Setup(segmentresolve.Dependencies{DB: runtimeDB, Bus: bus}) },
		func() error {
			return templatepublish.Setup(templatepublish.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return templateactivate.Setup(templateactivate.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		},
		func() error {
			return templatelist.Setup(templatelist.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		},
		func() error { return templateresolve.Setup(templateresolve.Dependencies{DB: runtimeDB, Bus: bus}) },
	} {
		if err := setup(); err != nil {
			t.Fatal(err)
		}
	}
	segmentSchema := []byte(`{"type":"object","properties":{"kind":{"type":"string"}},"required":["kind"],"additionalProperties":false}`)
	segmentRaw, err := bus.Send(ctx, segmentpublish.Command{TenantID: tenantID, Key: "video-property", Name: "Video property", IdempotencyKey: "segment-" + tenantID.String(), SchemaJSON: segmentSchema, UISchemaJSON: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	segment := segmentRaw.(segmentcore.View)
	if _, err := bus.Send(ctx, segmentactivate.Command{TenantID: tenantID, VersionID: segment.Version.ID, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	definition := catalog.TemplateDocument{SchemaVersion: 1, SegmentVersionID: segment.Version.ID.String(), ParticipantRoles: []string{"TENANT_PARTICIPANT"}, DefaultComparisonMode: catalog.ChecklistOnly, Requirements: []catalog.CaptureRequirement{{Key: "overview", Section: "general", Label: "Overview", EvidenceKind: "VIDEO", MinimumCount: 1, MaximumCount: 1, Required: true, CaptureSourcePolicy: "CAMERA_DEFAULT"}}, AnalysisType: "REAL_ESTATE", Policy: catalog.Policy{GeofenceMeters: 150, AllowGallery: true}}
	definitionJSON, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	var definitionMap map[string]any
	if err := json.Unmarshal(definitionJSON, &definitionMap); err != nil {
		t.Fatal(err)
	}
	definitionMap["isVideo"] = true
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &resolvers.Resolver{Bus: bus, DB: runtimeDB}}))
	server.SetErrorPresenter(graph.PresentError)
	publishQuery := `mutation($input:PublishTemplateVersionInput!){publishTemplateVersion(input:$input){version{id isVideo mediaKind} userErrors{code}}}`
	body := graphqlRequest(t, server, ctx, publishQuery, map[string]any{"input": map[string]any{"key": "video-" + tenantID.String(), "name": "Video", "definition": definitionMap, "clientMutationId": "publish-" + tenantID.String()}})
	var published struct {
		Data struct {
			Payload struct {
				Version struct {
					ID        string `json:"id"`
					IsVideo   bool   `json:"isVideo"`
					MediaKind string `json:"mediaKind"`
				} `json:"version"`
			} `json:"publishTemplateVersion"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &published); err != nil || published.Data.Payload.Version.ID == "" || !published.Data.Payload.Version.IsVideo || published.Data.Payload.Version.MediaKind != "VIDEO" {
		t.Fatalf("published version did not expose immutable VIDEO modality: %s (decode error: %v)", body, err)
	}
	versionID := published.Data.Payload.Version.ID
	activateQuery := `mutation($input:ActivateTemplateVersionInput!){activateTemplateVersion(input:$input){version{id status mediaKind}}}`
	publishVideoVersion := func(name, mutationID string) string {
		body := graphqlRequest(t, server, ctx, publishQuery, map[string]any{"input": map[string]any{"key": "video-" + tenantID.String(), "name": name, "definition": definitionMap, "clientMutationId": mutationID}})
		var result struct {
			Data struct {
				Payload struct {
					Version struct {
						ID string `json:"id"`
					} `json:"version"`
				} `json:"publishTemplateVersion"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(body), &result); err != nil || result.Data.Payload.Version.ID == "" {
			t.Fatalf("could not publish %s template version: %s (decode error: %v)", name, body, err)
		}
		return result.Data.Payload.Version.ID
	}
	body = graphqlRequest(t, server, ctx, activateQuery, map[string]any{"input": map[string]any{"versionId": versionID, "expectedVersion": 1, "clientMutationId": "activate-" + tenantID.String()}})
	if strings.Contains(body, `"errors"`) || !strings.Contains(body, `"mediaKind":"VIDEO"`) {
		t.Fatalf("VIDEO version activation failed: %s", body)
	}
	body = graphqlRequest(t, server, ctx, `query($id:ID!){templateVersion(id:$id){id isVideo mediaKind}}`, map[string]any{"id": versionID})
	if strings.Contains(body, `"errors"`) || !strings.Contains(body, `"isVideo":true`) || !strings.Contains(body, `"mediaKind":"VIDEO"`) {
		t.Fatalf("template query did not retain VIDEO modality: %s", body)
	}
	secondVersionID := publishVideoVersion("Video version two", "publish-video-two-"+tenantID.String())
	body = graphqlRequest(t, server, ctx, activateQuery, map[string]any{"input": map[string]any{"versionId": secondVersionID, "expectedVersion": 2, "clientMutationId": "activate-video-two-" + tenantID.String()}})
	if strings.Contains(body, `"errors"`) {
		t.Fatalf("second VIDEO version activation failed: %s", body)
	}
	thirdVersionID := publishVideoVersion("Video version three", "publish-video-three-"+tenantID.String())
	body = graphqlRequest(t, server, ctx, activateQuery, map[string]any{"input": map[string]any{"versionId": thirdVersionID, "expectedVersion": 3, "clientMutationId": "activate-video-three-" + tenantID.String()}})
	if strings.Contains(body, `"errors"`) || !strings.Contains(body, `"mediaKind":"VIDEO"`) {
		t.Fatalf("VIDEO template activation at expectedVersion 3 failed: %s", body)
	}
	body = graphqlRequest(t, server, ctx, `query($id:ID!){templateVersion(id:$id){id isVideo mediaKind}}`, map[string]any{"id": thirdVersionID})
	if strings.Contains(body, `"errors"`) || !strings.Contains(body, `"isVideo":true`) || !strings.Contains(body, `"mediaKind":"VIDEO"`) {
		t.Fatalf("third template version did not preserve VIDEO modality: %s", body)
	}
	fourthVersionID := publishVideoVersion("Video version four", "publish-video-four-"+tenantID.String())
	body = graphqlRequest(t, server, ctx, activateQuery, map[string]any{"input": map[string]any{"versionId": fourthVersionID, "expectedVersion": 3, "clientMutationId": "stale-activate-" + tenantID.String()}})
	if !strings.Contains(body, "CONFLICT") || strings.Contains(body, `"version":{`) {
		t.Fatalf("stale template activation returned success-shaped data: %s", body)
	}
	foreignTenantID, foreignIdentityID, foreignMembershipID := identity.NewID(), identity.NewID(), identity.NewID()
	for _, row := range []any{
		&database.Tenant{ID: foreignTenantID, TenantID: foreignTenantID, Name: "Foreign video template tenant", Language: "pt-BR", DefaultTimezone: "UTC", Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now},
		&database.Membership{ID: foreignMembershipID, TenantID: foreignTenantID, IdentityID: foreignIdentityID, Issuer: "test", Subject: foreignIdentityID.String(), Role: auth.TenantAdmin, Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now},
	} {
		if err := admin.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	foreignCtx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: foreignTenantID, Principal: requestctx.Principal{IdentityID: foreignIdentityID, MembershipID: foreignMembershipID, TenantID: foreignTenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "video-template-foreign", StartedAt: now})
	foreignSegmentRaw, err := bus.Send(foreignCtx, segmentpublish.Command{TenantID: foreignTenantID, Key: "foreign-video-property", Name: "Foreign video property", IdempotencyKey: "foreign-segment-" + foreignTenantID.String(), SchemaJSON: segmentSchema, UISchemaJSON: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	foreignSegment := foreignSegmentRaw.(segmentcore.View)
	if _, err := bus.Send(foreignCtx, segmentactivate.Command{TenantID: foreignTenantID, VersionID: foreignSegment.Version.ID, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	foreignDefinition := definition
	foreignDefinition.SegmentVersionID = foreignSegment.Version.ID.String()
	foreignDefinitionJSON, err := json.Marshal(foreignDefinition)
	if err != nil {
		t.Fatal(err)
	}
	var foreignDefinitionMap map[string]any
	if err := json.Unmarshal(foreignDefinitionJSON, &foreignDefinitionMap); err != nil {
		t.Fatal(err)
	}
	foreignDefinitionMap["isVideo"] = true
	foreignTemplateRaw, err := bus.Send(foreignCtx, templatepublish.Command{TenantID: foreignTenantID, Key: "foreign-video-" + foreignTenantID.String(), Name: "Foreign video", DefinitionJSON: foreignDefinitionJSON, IdempotencyKey: "foreign-template-" + foreignTenantID.String()})
	if err != nil {
		t.Fatal(err)
	}
	foreignTemplate := foreignTemplateRaw.(templatecore.View)
	foreignVersionID := foreignTemplate.Version.ID.String()
	for _, tc := range []struct {
		name, query string
		variables   map[string]any
	}{
		{"foreign read", `query($id:ID!){templateVersion(id:$id){id}}`, map[string]any{"id": foreignVersionID}},
		{"foreign activation", activateQuery, map[string]any{"input": map[string]any{"versionId": foreignVersionID, "expectedVersion": 1, "clientMutationId": "foreign-activate-" + tenantID.String()}}},
		{"foreign segment association", publishQuery, map[string]any{"input": map[string]any{"key": "foreign-segment-association-" + tenantID.String(), "name": "Foreign segment association", "definition": foreignDefinitionMap, "clientMutationId": "foreign-segment-association-" + tenantID.String()}}},
	} {
		body = graphqlRequest(t, server, ctx, tc.query, tc.variables)
		if !strings.Contains(body, "NOT_FOUND") || strings.Contains(body, foreignVersionID) || strings.Contains(body, foreignSegment.Version.ID.String()) || strings.Contains(body, `"version":{`) {
			t.Fatalf("tenant A %s exposed or associated tenant B data: %s", tc.name, body)
		}
	}
	body = graphqlRequest(t, server, ctx, `query { templates { nodes { id key } } }`, nil)
	if strings.Contains(body, "foreign-video-"+foreignTenantID.String()) || strings.Contains(body, foreignVersionID) {
		t.Fatalf("tenant A template list exposed tenant B data: %s", body)
	}
	for _, tc := range []struct {
		name, query string
		variables   map[string]any
	}{
		{"publish", publishQuery, map[string]any{"input": map[string]any{"key": "unauth-" + tenantID.String(), "name": "Unauth", "definition": definitionMap, "clientMutationId": "unauth-" + tenantID.String()}}},
		{"activate", activateQuery, map[string]any{"input": map[string]any{"versionId": versionID, "expectedVersion": 2, "clientMutationId": "unauth-activate-" + tenantID.String()}}},
		{"templates", `query { templates { nodes { id } } }`, nil},
		{"templateVersion", `query($id:ID!){templateVersion(id:$id){id}}`, map[string]any{"id": versionID}},
	} {
		body = graphqlRequest(t, server, context.Background(), tc.query, tc.variables)
		if !strings.Contains(body, "UNAUTHENTICATED") || strings.Contains(body, `"publishTemplateVersion":{`) || strings.Contains(body, `"activateTemplateVersion":{`) || strings.Contains(body, `"templateVersion":{`) || strings.Contains(body, `"templates":{`) {
			t.Fatalf("unauthenticated %s operation was accepted: %s", tc.name, body)
		}
	}
	status, body := graphqlRequestStatus(t, server, ctx, `query { templates(tenantId:"00000000-0000-0000-0000-000000000002") { nodes { id } } }`, nil)
	if status != http.StatusUnprocessableEntity || !strings.Contains(body, "Unknown argument") || !strings.Contains(body, `"data":null`) {
		t.Fatalf("template tenant selector was not rejected by schema validation: %s", body)
	}
	viewerCtx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: viewerIdentityID, MembershipID: viewerMembershipID, TenantID: tenantID, Roles: []string{auth.Viewer}}, CorrelationID: "video-template-viewer", StartedAt: now})
	for _, tc := range []struct {
		name, query string
		variables   map[string]any
	}{
		{"publish", publishQuery, map[string]any{"input": map[string]any{"key": "forbidden-" + tenantID.String(), "name": "Forbidden", "definition": definitionMap, "clientMutationId": "forbidden-" + tenantID.String()}}},
		{"activate", activateQuery, map[string]any{"input": map[string]any{"versionId": versionID, "expectedVersion": 2, "clientMutationId": "forbidden-activate-" + tenantID.String()}}},
	} {
		body = graphqlRequest(t, server, viewerCtx, tc.query, tc.variables)
		if !strings.Contains(body, "FORBIDDEN") || strings.Contains(body, `"version":{`) {
			t.Fatalf("viewer %s operation was not rejected: %s", tc.name, body)
		}
	}
	customerViewerCtx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: customerViewerIdentityID, MembershipID: customerViewerMembershipID, TenantID: tenantID, Roles: []string{auth.CustomerViewer}}, CorrelationID: "video-template-customer-viewer", StartedAt: now})
	for _, tc := range []struct {
		name, query string
		variables   map[string]any
	}{
		{"templates", `query { templates { nodes { id } } }`, nil},
		{"templateVersion", `query($id:ID!){templateVersion(id:$id){id}}`, map[string]any{"id": versionID}},
	} {
		body = graphqlRequest(t, server, customerViewerCtx, tc.query, tc.variables)
		if !strings.Contains(body, "FORBIDDEN") || strings.Contains(body, `"templates":{`) || strings.Contains(body, `"templateVersion":{`) {
			t.Fatalf("customer viewer read through %s: %s", tc.name, body)
		}
	}
	invalidDefinition := make(map[string]any, len(definitionMap))
	for key, value := range definitionMap {
		invalidDefinition[key] = value
	}
	invalidDefinition["isVideo"] = "yes"
	body = graphqlRequest(t, server, ctx, publishQuery, map[string]any{"input": map[string]any{"key": "invalid-" + tenantID.String(), "name": "Invalid", "definition": invalidDefinition, "clientMutationId": "invalid-" + tenantID.String()}})
	if !strings.Contains(body, "INVALID_INPUT") || strings.Contains(body, `"version":{`) {
		t.Fatalf("invalid modality input returned success-shaped template data: %s", body)
	}
}

func TestCatalogSetupsRequireDependencies(t *testing.T) {
	setups := []func() error{
		func() error { return participantupsert.Setup(participantupsert.Dependencies{}) },
		func() error { return participantverify.Setup(participantverify.Dependencies{}) },
		func() error { return participantchannels.Setup(participantchannels.Dependencies{}) },
		func() error { return participantdeactivate.Setup(participantdeactivate.Dependencies{}) },
		func() error { return participantget.Setup(participantget.Dependencies{}) },
		func() error { return participantlist.Setup(participantlist.Dependencies{}) },
		func() error { return segmentpublish.Setup(segmentpublish.Dependencies{}) },
		func() error { return segmentactivate.Setup(segmentactivate.Dependencies{}) },
		func() error { return segmentlist.Setup(segmentlist.Dependencies{}) },
		func() error { return segmentresolve.Setup(segmentresolve.Dependencies{}) },
		func() error { return templatepublish.Setup(templatepublish.Dependencies{}) },
		func() error { return templateactivate.Setup(templateactivate.Dependencies{}) },
		func() error { return templatelist.Setup(templatelist.Dependencies{}) },
		func() error { return templateresolve.Setup(templateresolve.Dependencies{}) },
		func() error { return assetregister.Setup(assetregister.Dependencies{}) },
		func() error { return assetupdate.Setup(assetupdate.Dependencies{}) },
		func() error { return assetarchive.Setup(assetarchive.Dependencies{}) },
		func() error { return assetget.Setup(assetget.Dependencies{}) },
		func() error { return assetlist.Setup(assetlist.Dependencies{}) },
	}
	for index, setup := range setups {
		if err := setup(); err == nil {
			t.Fatalf("setup %d accepted missing dependencies", index)
		}
	}
}

func assertImmutableCatalogRows(t *testing.T, admin *gorm.DB, templateVersionID, attributeVersionID, contactID identity.ID) {
	t.Helper()
	checks := []struct {
		name  string
		model any
		id    identity.ID
		field string
		value any
	}{
		{"template version", &database.TemplateVersion{}, templateVersionID, "canonical_digest", "tampered"},
		{"asset attribute version", &database.AssetAttributeVersion{}, attributeVersionID, "canonical_digest", "tampered"},
		{"contact verification", &database.ContactVerification{}, contactID, "status", "TAMPERED"},
	}
	for _, check := range checks {
		if err := admin.Model(check.model).Where("id = ?", check.id).Update(check.field, check.value).Error; err == nil {
			t.Fatalf("%s was mutable", check.name)
		}
	}
}

func assertTenantIsolation(t *testing.T, admin *gorm.DB, bus *mediator.Bus, parent context.Context, foreignAssetID identity.ID) {
	t.Helper()
	tenantID, unitID, identityID, membershipID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	rows := []any{
		&database.Tenant{ID: tenantID, TenantID: tenantID, Name: "Isolated tenant", Language: "pt-BR", DefaultTimezone: "UTC", Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now},
		&database.BusinessUnit{ID: unitID, TenantID: tenantID, Code: "isolated-" + tenantID.String(), Name: "Isolated", Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now},
		&database.Membership{ID: membershipID, TenantID: tenantID, IdentityID: identityID, Issuer: "test", Subject: identityID.String(), Role: auth.TenantAdmin, Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now},
	}
	for _, row := range rows {
		if err := admin.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	meta, _ := requestctx.FromContext(parent)
	meta.TenantID = tenantID
	meta.Principal = requestctx.Principal{IdentityID: identityID, MembershipID: membershipID, TenantID: tenantID, Roles: []string{auth.TenantAdmin}}
	isolated := requestctx.WithMetadata(context.Background(), meta)
	if _, err := bus.Ask(isolated, assetget.Query{TenantID: tenantID, AssetID: foreignAssetID}); apperrorCode(err) != apperror.NotFound {
		t.Fatalf("cross-tenant asset was visible: %v", err)
	}
	listed, err := bus.Ask(isolated, participantlist.Query{TenantID: tenantID, First: 100})
	if err != nil || len(listed.(participantlist.Result).Participants) != 0 {
		t.Fatalf("cross-tenant participants were visible: %v", err)
	}
}

func graphqlRequest(t *testing.T, server http.Handler, ctx context.Context, query string, variables map[string]any) string {
	t.Helper()
	status, body := graphqlRequestStatus(t, server, ctx, query, variables)
	if status != http.StatusOK {
		t.Fatalf("GraphQL status %d: %s", status, body)
	}
	return body
}

func graphqlRequestStatus(t *testing.T, server http.Handler, ctx context.Context, query string, variables map[string]any) (int, string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
	request := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(payload)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response.Code, response.Body.String()
}

func apperrorCode(err error) apperror.Code {
	if err == nil {
		return ""
	}
	code, _, _ := apperror.Public(err)
	return code
}
