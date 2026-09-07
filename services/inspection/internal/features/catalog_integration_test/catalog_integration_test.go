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
	templateprofile "inspection/services/inspection/internal/features/templates/publish_analysis_profile"
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
			return templateprofile.Setup(templateprofile.Dependencies{DB: runtimeDB, Bus: bus, Authorizer: authorizer})
		}, func() error {
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
	profileJSON := []byte(`{"schemaVersion":1,"modelAlias":"vision","promptVersion":"v1","outputSchema":{"type":"object"},"minimumConfidenceBps":5000}`)
	profileRaw, err := bus.Send(ctx, templateprofile.Command{TenantID: tenantID, Key: "default", IdempotencyKey: "profile-" + tenantID.String(), DefinitionJSON: profileJSON})
	if err != nil {
		t.Fatal(err)
	}
	profile := profileRaw.(database.AnalysisProfileVersion)
	document := catalog.TemplateDocument{SchemaVersion: 1, SegmentVersionID: segment.Version.ID.String(), ParticipantRoles: []string{"TENANT_PARTICIPANT"}, ComparisonMode: catalog.ChecklistOnly, Requirements: []catalog.CaptureRequirement{{Key: "overview", Section: "general", Label: "Overview", EvidenceKind: "PHOTO", MinimumCount: 1, MaximumCount: 2, Required: true, DescriptionRequired: true, CaptureSourcePolicy: "CAMERA_DEFAULT", ComparisonTarget: catalog.ChecklistOnly}}, ReportMode: "HISTORICAL", AnalysisProfile: profile.ID.String(), Policy: catalog.Policy{GPSRequired: true, GeofenceMeters: 150, AllowGallery: true}}
	definitionJSON, _ := json.Marshal(document)
	templateRaw, err := bus.Send(ctx, templatepublish.Command{TenantID: tenantID, Key: "periodic", Name: "Periodic", IdempotencyKey: "template-" + tenantID.String(), DefinitionJSON: definitionJSON})
	if err != nil {
		t.Fatal(err)
	}
	template := templateRaw.(templatecore.View)
	if _, err := bus.Send(ctx, templateactivate.Command{TenantID: tenantID, VersionID: template.Version.ID, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
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
		{"IT-453 upsertParticipant", `mutation($input:UpsertParticipantInput!){ upsertParticipant(input:$input){ participant{id} userErrors{code} } }`, map[string]any{"input": map[string]any{"businessUnitId": unitID.String(), "name": "Person", "segmentRole": "TENANT_PARTICIPANT", "contacts": []any{}, "clientMutationId": "participant-" + tenantID.String()}}}, {"IT-455 verifyContact", `mutation($input:VerifyContactInput!){ verifyContact(input:$input){ contact{id verified} userErrors{code} } }`, map[string]any{"input": map[string]any{"contactId": participant.Contacts[0].ID.String(), "verified": true, "clientMutationId": "verify-graphql"}}}, {"IT-457 setDeliveryChannels", `mutation($input:SetDeliveryChannelsInput!){ setDeliveryChannels(input:$input){ participant{id version} userErrors{code} } }`, map[string]any{"input": map[string]any{"participantId": participant.Participant.ID.String(), "contactIds": []string{participant.Contacts[0].ID.String()}, "expectedVersion": 2, "clientMutationId": "channels-graphql"}}}, {"IT-459 publishSegmentDefinition", `mutation($input:PublishSegmentDefinitionInput!){ publishSegmentDefinition(input:$input){ definition{id} userErrors{code} } }`, map[string]any{"input": map[string]any{"key": "property", "name": "Property", "schema": map[string]any{"type": "object", "properties": map[string]any{"kind": map[string]any{"type": "string"}}, "required": []string{"kind"}}, "uiSchema": map[string]any{}, "clientMutationId": "segment-" + tenantID.String()}}}, {"IT-461 publishTemplateVersion", `mutation($input:PublishTemplateVersionInput!){ publishTemplateVersion(input:$input){ template{id} version{id} userErrors{code} } }`, map[string]any{"input": map[string]any{"key": "periodic", "name": "Periodic", "definition": templateDefinition, "clientMutationId": "template-" + tenantID.String()}}}, {"IT-463 activateTemplateVersion", `mutation($input:ActivateTemplateVersionInput!){ activateTemplateVersion(input:$input){ template{id} version{id status} userErrors{code} } }`, map[string]any{"input": map[string]any{"versionId": template.Version.ID.String(), "expectedVersion": 2, "clientMutationId": "activate-graphql"}}}, {"IT-465 publishAnalysisProfile", `mutation($input:PublishAnalysisProfileInput!){ publishAnalysisProfile(input:$input){ profile{id} userErrors{code} } }`, map[string]any{"input": map[string]any{"key": "default", "definition": map[string]any{"schemaVersion": 1, "modelAlias": "vision", "promptVersion": "v1", "outputSchema": map[string]any{"type": "object"}, "minimumConfidenceBps": 5000}, "clientMutationId": "profile-" + tenantID.String()}}}, {"IT-467 registerAsset", `mutation($input:RegisterAssetInput!){ registerAsset(input:$input){ asset{id} userErrors{code} } }`, map[string]any{"input": map[string]any{"asset": assetVariables, "clientMutationId": "asset-" + tenantID.String()}}}, {"IT-469 updateAsset", `mutation($input:UpdateAssetInput!){ updateAsset(input:$input){ asset{id version} userErrors{code} } }`, map[string]any{"input": map[string]any{"assetId": asset.Asset.ID.String(), "expectedVersion": 2, "asset": assetVariables, "clientMutationId": "asset-update-graphql"}}}, {"IT-471 archiveAsset", `mutation($input:ArchiveAssetInput!){ archiveAsset(input:$input){ userErrors{code} } }`, map[string]any{"input": map[string]any{"assetId": asset.Asset.ID.String(), "expectedVersion": 3, "clientMutationId": "asset-archive-graphql"}}},
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
		{"IT-466", `mutation($input:PublishAnalysisProfileInput!){publishAnalysisProfile(input:$input){profile{id}}}`, map[string]any{"input": map[string]any{"key": "bad", "definition": map[string]any{}, "clientMutationId": "bad-profile"}}},
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
		func() error { return templateprofile.Setup(templateprofile.Dependencies{}) },
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
	payload, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
	request := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(payload)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GraphQL status %d: %s", response.Code, response.Body.String())
	}
	return response.Body.String()
}

func apperrorCode(err error) apperror.Code {
	if err == nil {
		return ""
	}
	code, _, _ := apperror.Public(err)
	return code
}
