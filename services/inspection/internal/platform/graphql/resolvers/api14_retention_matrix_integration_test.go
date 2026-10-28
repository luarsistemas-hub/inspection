//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/testfailure"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

func TestVV_IT_515RetentionMutationsPreserveOneExplicitVideoScope(t *testing.T) {
	fx := newAPI14RetentionFixture(t)
	query := `mutation {
		configureRetentionPolicy(input:{evidenceDays:1825,operationalDays:365,securityDays:90,clientMutationId:"vv-it-515-policy"}) { policy { evidenceDays operationalDays securityDays version } clientMutationId }
		recordDeletionRequest(input:{sourceMediaId:"` + fx.mediaID.String() + `",reason:"ordinary retention clock",clientMutationId:"vv-it-515-delete"}) { status requestId clientMutationId }
		applyLegalHold(input:{sourceMediaId:"` + fx.mediaID.String() + `",reason:"H1",clientMutationId:"vv-it-515-apply"}) { status clientMutationId }
		releaseLegalHold(input:{sourceMediaId:"` + fx.mediaID.String() + `",reason:"H1",clientMutationId:"vv-it-515-release"}) { status clientMutationId }
	}`
	response := executeAPI14Retention(t, fx, query, fx.admin, "", "")
	for _, expected := range []string{`"evidenceDays":1825`, `"operationalDays":365`, `"securityDays":90`, `"version":1`, `"status":"REQUESTED"`, `"status":"HELD"`, `"status":"RELEASED"`} {
		if !strings.Contains(response, expected) {
			t.Fatalf("retention mutation response missing %s: %s", expected, response)
		}
	}
	if strings.Contains(response, `"errors"`) {
		t.Fatalf("retention mutations returned errors: %s", response)
	}
	if err := fx.h.WithinTenant(context.Background(), fx.tenantID, func(tx *gorm.DB) error {
		var policy database.RetentionPolicy
		if err := tx.Where("tenant_id=?", fx.tenantID).Take(&policy).Error; err != nil {
			return err
		}
		if policy.EvidenceDays != 1825 || policy.OperationalDays != 365 || policy.SecurityDays != 90 || policy.Version != 1 {
			t.Errorf("retention policy payload changed: %+v", policy)
		}
		var deletion database.DeletionRequest
		if err := tx.Where("tenant_id=?", fx.tenantID).Take(&deletion).Error; err != nil {
			return err
		}
		if deletion.InspectionID != nil || deletion.OriginVersionID != nil || deletion.SourceMediaID == nil || *deletion.SourceMediaID != fx.mediaID || deletion.Status != "REQUESTED" {
			t.Errorf("deletion request did not preserve exactly the requested source scope: %+v", deletion)
		}
		var holds []database.LegalHold
		if err := tx.Where("tenant_id=?", fx.tenantID).Find(&holds).Error; err != nil {
			return err
		}
		if len(holds) != 1 || holds[0].InspectionID != nil || holds[0].OriginVersionID != nil || holds[0].SourceMediaID == nil || *holds[0].SourceMediaID != fx.mediaID || holds[0].Active {
			t.Errorf("H1 was not applied and released on exactly one video scope: %+v", holds)
		}
		var source database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id=?", fx.tenantID, fx.mediaID).Take(&source).Error; err != nil {
			return err
		}
		if source.Status != "RETAINED" || source.SourceExpiryAt == nil || !source.SourceExpiryAt.Equal(fx.sourceExpiry) || source.RetentionDays == nil || *source.RetentionDays != 30 {
			t.Errorf("hold mutations changed the existing source retention payload: status=%s expiry=%v", source.Status, source.SourceExpiryAt)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestVV_IT_516RetentionMutationsRequireAuthentication(t *testing.T) {
	fx := newAPI14RetentionFixture(t)
	for _, operation := range api14RetentionOperations(fx) {
		t.Run(operation.name, func(t *testing.T) {
			assertFixedOriginTransportError(t, executeAPI14Retention(t, fx, operation.query, context.Background(), "", ""), "UNAUTHENTICATED")
		})
	}
}

func TestVV_IT_517RetentionMutationsRequireManagementCapability(t *testing.T) {
	fx := newAPI14RetentionFixture(t)
	viewer := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.tenantID, Principal: requestctx.Principal{IdentityID: fx.identityID, TenantID: fx.tenantID, Roles: []string{auth.Viewer}}, CorrelationID: "vv-it-517"})
	for _, operation := range api14RetentionOperations(fx) {
		t.Run(operation.name, func(t *testing.T) {
			assertFixedOriginTransportError(t, executeAPI14Retention(t, fx, operation.query, viewer, "", ""), "FORBIDDEN")
		})
	}
	assertAPI14RetentionNoWrites(t, fx)
}

func TestVV_IT_518RetentionResourcesAreTenantScopedAndSingletonUsesActorScope(t *testing.T) {
	fx := newAPI14RetentionFixture(t)
	foreign, err := fx.h.SeedTask06Fixture(context.Background(), harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	foreignMediaID := identity.NewID()
	foreignOriginID := identity.NewID()
	if err := fx.h.WithinTenant(context.Background(), foreign.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: foreign.TenantID, MediaID: foreignMediaID, ScreeningStatus: "APPROVED", Status: "RETAINED", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}).Error; err != nil {
			return err
		}
		responsibilityID := identity.NewID()
		if err := tx.Create(&database.Responsibility{ID: responsibilityID, TenantID: foreign.TenantID, InspectionID: foreign.InspectionID, ParticipantID: foreign.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}).Error; err != nil {
			return err
		}
		return tx.Create(&database.OriginVersion{ID: foreignOriginID, TenantID: foreign.TenantID, OriginID: identity.NewID(), ResponsibilityID: &responsibilityID, VersionNumber: 1, Status: "ACTIVE", MediaKind: "VIDEO", IdempotencyKey: "vv-it-518-" + foreignOriginID.String(), CreatedAt: time.Now().UTC()}).Error
	}); err != nil {
		t.Fatal(err)
	}
	foreignScopes := []struct {
		name, inspection, source, origin string
	}{
		{"inspection", foreign.InspectionID.String(), "", ""},
		{"source", "", foreignMediaID.String(), ""},
		{"originVersion", "", "", foreignOriginID.String()},
	}
	for _, operation := range api14RetentionOperations(fx) {
		if operation.name == "configureRetentionPolicy" {
			continue
		}
		for _, scope := range foreignScopes {
			t.Run(operation.name+"/"+scope.name, func(t *testing.T) {
				query := api14RetentionMutation(operation.name, scope.inspection, scope.source, scope.origin, "vv-it-518-"+operation.name+scope.name)
				response := executeAPI14Retention(t, fx, query, fx.admin, "", "")
				assertFixedOriginTransportError(t, response, "NOT_FOUND")
				for _, id := range []string{foreign.TenantID.String(), foreign.InspectionID.String(), foreignMediaID.String(), foreignOriginID.String()} {
					if strings.Contains(response, id) {
						t.Fatalf("foreign tenant metadata leaked (%s): %s", id, response)
					}
				}
			})
		}
	}
	if err := fx.h.WithinTenant(context.Background(), fx.tenantID, func(tx *gorm.DB) error {
		return tx.Create(&database.RetentionPolicy{ID: identity.NewID(), TenantID: fx.tenantID, EvidenceDays: 111, OperationalDays: 22, SecurityDays: 3, Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := fx.h.WithinTenant(context.Background(), foreign.TenantID, func(tx *gorm.DB) error {
		return tx.Create(&database.RetentionPolicy{ID: identity.NewID(), TenantID: foreign.TenantID, EvidenceDays: 999, OperationalDays: 888, SecurityDays: 777, Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}).Error
	}); err != nil {
		t.Fatal(err)
	}
	response := executeAPI14Retention(t, fx, `query { retentionPolicies { nodes { evidenceDays operationalDays securityDays } } }`, fx.admin, "", "")
	if !strings.Contains(response, `"evidenceDays":111`) || strings.Contains(response, `"evidenceDays":999`) {
		t.Fatalf("tenant-only retention query did not derive scope from actor: %s", response)
	}
	injected := executeAPI14Retention(t, fx, `query { retentionPolicies(tenantId:"`+foreign.TenantID.String()+`") { nodes { evidenceDays } } }`, fx.admin, "", "")
	if !strings.Contains(injected, `"errors"`) || stringAPI14Data(injected) != "null" {
		t.Fatalf("schema accepted injected tenant selector: %s", injected)
	}
}

func TestVV_IT_519RetentionDatabaseUnavailableHasNoSuccessPayload(t *testing.T) {
	fx := newAPI14RetentionFixture(t)
	if err := testfailure.Install(fx.h.DB); err != nil {
		t.Fatal(err)
	}
	for _, operation := range api14RetentionOperations(fx) {
		t.Run(operation.name, func(t *testing.T) {
			response := executeAPI14Retention(t, fx, operation.query, fx.admin, operation.name, "dependency")
			assertFixedOriginTransportError(t, response, "DEPENDENCY_UNAVAILABLE")
		})
	}
}

func TestVV_IT_520RetentionInternalErrorsHideSecret(t *testing.T) {
	fx := newAPI14RetentionFixture(t)
	if err := testfailure.Install(fx.h.DB); err != nil {
		t.Fatal(err)
	}
	for _, operation := range api14RetentionOperations(fx) {
		t.Run(operation.name, func(t *testing.T) {
			response := executeAPI14Retention(t, fx, operation.query, fx.admin, operation.name, "internal")
			assertFixedOriginTransportError(t, response, "INTERNAL")
			if strings.Contains(response, testfailure.Secret) {
				t.Fatalf("internal adapter secret leaked: %s", response)
			}
		})
	}
}

func TestVV_IT_521RetentionRejectsInconsistentScopeWithoutWrites(t *testing.T) {
	fx := newAPI14RetentionFixture(t)
	invalid := []struct{ name, query string }{
		{"recordDeletionRequest/no-scope", `mutation { recordDeletionRequest(input:{reason:"invalid",clientMutationId:"vv-it-521-delete-none"}) { status } }`},
		{"recordDeletionRequest/two-scopes", `mutation { recordDeletionRequest(input:{inspectionId:"` + fx.inspectionID.String() + `",sourceMediaId:"` + fx.mediaID.String() + `",clientMutationId:"vv-it-521-delete-two"}) { status } }`},
		{"applyLegalHold/no-scope", `mutation { applyLegalHold(input:{reason:"invalid",clientMutationId:"vv-it-521-apply-none"}) { status } }`},
		{"applyLegalHold/two-scopes", `mutation { applyLegalHold(input:{inspectionId:"` + fx.inspectionID.String() + `",sourceMediaId:"` + fx.mediaID.String() + `",reason:"invalid",clientMutationId:"vv-it-521-apply-two"}) { status } }`},
		{"releaseLegalHold/no-scope", `mutation { releaseLegalHold(input:{reason:"invalid",clientMutationId:"vv-it-521-release-none"}) { status } }`},
		{"releaseLegalHold/two-scopes", `mutation { releaseLegalHold(input:{inspectionId:"` + fx.inspectionID.String() + `",sourceMediaId:"` + fx.mediaID.String() + `",reason:"invalid",clientMutationId:"vv-it-521-release-two"}) { status } }`},
	}
	for _, item := range invalid {
		t.Run(item.name, func(t *testing.T) {
			assertFixedOriginTransportError(t, executeAPI14Retention(t, fx, item.query, fx.admin, "", ""), "INVALID_INPUT")
		})
	}
	badPolicy := executeAPI14Retention(t, fx, `mutation { configureRetentionPolicy(input:{evidenceDays:0,operationalDays:365,clientMutationId:"vv-it-521-policy"}) { policy { version } } }`, fx.admin, "", "")
	assertFixedOriginTransportError(t, badPolicy, "INVALID_INPUT")
	assertAPI14RetentionNoWrites(t, fx)
}

func TestVV_IT_523SourceHoldRequestConflictsWithDeletionInProgress(t *testing.T) {
	fx := newAPI14RetentionFixture(t)
	if err := fx.h.WithinTenant(context.Background(), fx.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", fx.tenantID, fx.mediaID).Update("status", "DELETING").Error
	}); err != nil {
		t.Fatal(err)
	}
	query := api14RetentionMutation("applyLegalHold", "", fx.mediaID.String(), "", "vv-it-523-source-hold")
	response := executeAPI14Retention(t, fx, query, fx.admin, "", "")
	assertFixedOriginTransportError(t, response, "CONFLICT")
	if strings.Contains(response, `"status":"HELD"`) || strings.Contains(response, `"applyLegalHold":{`) {
		t.Fatalf("rejected source hold reported success: %s", response)
	}
	var holds int64
	if err := fx.h.WithinTenant(context.Background(), fx.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.LegalHold{}).Where("tenant_id=? AND source_media_id=?", fx.tenantID, fx.mediaID).Count(&holds).Error
	}); err != nil {
		t.Fatal(err)
	}
	if holds != 0 {
		t.Fatalf("source already DELETING acquired %d legal holds", holds)
	}
}

func TestVV_IT_522InspectionPurgeRequestRejectsLiveVideoPlanFrames(t *testing.T) {
	fx := newAPI14RetentionFixture(t)
	origin, err := fx.h.SeedVideoOriginFixture(context.Background(), harness.VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	var inspectionID, draftID identity.ID
	if err := fx.h.WithinTenant(context.Background(), origin.TenantID, func(tx *gorm.DB) error {
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND asset_id=?", origin.TenantID, origin.AssetID).Take(&inspection).Error; err != nil {
			return err
		}
		inspectionID = inspection.ID
		var draft database.CaptureDraft
		if err := tx.Where("tenant_id=? AND responsibility_id=?", origin.TenantID, origin.ResponsibilityID).Take(&draft).Error; err != nil {
			return err
		}
		draftID = draft.ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fx.tenantID, fx.inspectionID = origin.TenantID, inspectionID
	fx.admin = requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: origin.TenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: origin.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "vv-it-522"})
	planID := identity.NewID()
	submissionID := identity.NewID()
	now := time.Now().UTC()
	if err := fx.h.WithinTenant(context.Background(), fx.tenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: fx.tenantID, DraftID: draftID, ResponsibilityID: origin.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: []byte(`{}`), PayloadDigest: strings.Repeat("b", 64), SubmittedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlan{ID: planID, TenantID: fx.tenantID, InspectionID: fx.inspectionID, SubmissionID: submissionID, CurrentPreparationID: origin.PreparationID, Generation: 1, PlanDigest: strings.Repeat("a", 64), Status: "SEALED", NextAttemptAt: now, CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoEvidenceReference{ID: identity.NewID(), TenantID: fx.tenantID, PreparationID: origin.PreparationID, OwnerType: "VIDEO_PLAN", OwnerID: planID, CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	query := api14RetentionMutation("recordDeletionRequest", fx.inspectionID.String(), "", "", "vv-it-522-purge")
	response := executeAPI14Retention(t, fx, query, fx.admin, "", "")
	assertFixedOriginTransportError(t, response, "INVALID_STATE")
	if strings.Contains(response, `"status":"REQUESTED"`) || strings.Contains(response, `"recordDeletionRequest":{`) {
		t.Fatalf("rejected inspection purge request reported success: %s", response)
	}
	var requests int64
	if err := fx.h.WithinTenant(context.Background(), fx.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.DeletionRequest{}).Where("tenant_id=? AND inspection_id=?", fx.tenantID, fx.inspectionID).Count(&requests).Error
	}); err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatalf("inspection with live video plan frames created %d deletion requests", requests)
	}
}

type api14RetentionFixture struct {
	h                                                            *harness.Harness
	tenantID, identityID, inspectionID, mediaID, originVersionID identity.ID
	draftID, responsibilityID                                    identity.ID
	sourceExpiry                                                 time.Time
	server                                                       *handler.Server
	admin                                                        context.Context
}

type api14RetentionOperation struct{ name, query string }

func newAPI14RetentionFixture(t *testing.T) api14RetentionFixture {
	t.Helper()
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	seed, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	mediaID, originVersionID := identity.NewID(), identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	sourceExpiry := now.Add(30 * 24 * time.Hour)
	retentionDays := 30
	if err := h.WithinTenant(ctx, seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: seed.TenantID, MediaID: mediaID, ScreeningStatus: "APPROVED", RetentionDays: &retentionDays, SourceExpiryAt: &sourceExpiry, Status: "RETAINED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		responsibilityID := identity.NewID()
		if err := tx.Create(&database.Responsibility{ID: responsibilityID, TenantID: seed.TenantID, InspectionID: seed.InspectionID, ParticipantID: seed.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.OriginVersion{ID: originVersionID, TenantID: seed.TenantID, OriginID: identity.NewID(), ResponsibilityID: &responsibilityID, VersionNumber: 1, Status: "ACTIVE", MediaKind: "VIDEO", IdempotencyKey: "vv-api14-" + originVersionID.String(), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	admin := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: seed.TenantID, Principal: requestctx.Principal{IdentityID: seed.IdentityID, TenantID: seed.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "vv-api14"})
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: h.DB, Bus: mediator.New()}}))
	server.SetErrorPresenter(graph.PresentError)
	return api14RetentionFixture{h: h, tenantID: seed.TenantID, identityID: seed.IdentityID, inspectionID: seed.InspectionID, mediaID: mediaID, originVersionID: originVersionID, draftID: seed.DraftID, responsibilityID: seed.ResponsibilityID, sourceExpiry: sourceExpiry, server: server, admin: admin}
}

func api14RetentionOperations(fx api14RetentionFixture) []api14RetentionOperation {
	return []api14RetentionOperation{
		{"configureRetentionPolicy", `mutation { configureRetentionPolicy(input:{evidenceDays:1825,operationalDays:365,securityDays:90,clientMutationId:"vv-api14-policy"}) { policy { version } } }`},
		{"recordDeletionRequest", api14RetentionMutation("recordDeletionRequest", "", fx.mediaID.String(), "", "vv-api14-delete")},
		{"applyLegalHold", api14RetentionMutation("applyLegalHold", "", fx.mediaID.String(), "", "vv-api14-apply")},
		{"releaseLegalHold", api14RetentionMutation("releaseLegalHold", "", fx.mediaID.String(), "", "vv-api14-release")},
	}
}

func api14RetentionMutation(operation, inspection, source, origin, clientID string) string {
	fields := []string{}
	if inspection != "" {
		fields = append(fields, `inspectionId:"`+inspection+`"`)
	}
	if source != "" {
		fields = append(fields, `sourceMediaId:"`+source+`"`)
	}
	if origin != "" {
		fields = append(fields, `originVersionId:"`+origin+`"`)
	}
	if operation != "recordDeletionRequest" {
		fields = append(fields, `reason:"vv-api14"`)
	}
	fields = append(fields, `clientMutationId:"`+clientID+`"`)
	selection := "status"
	if operation == "recordDeletionRequest" {
		selection += " requestId"
	}
	return `mutation { ` + operation + `(input:{` + strings.Join(fields, ",") + `}) { ` + selection + ` } }`
}

func executeAPI14Retention(t *testing.T, fx api14RetentionFixture, query string, ctx context.Context, operation, failure string) string {
	t.Helper()
	w := httptest.NewRecorder()
	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctx)
	if operation != "" {
		req.Header.Set(testfailure.HeaderOperation, operation)
		req.Header.Set(testfailure.HeaderFailure, failure)
		req = testfailure.WithRequest(req, "test")
	}
	fx.server.ServeHTTP(w, req)
	return w.Body.String()
}

func assertAPI14RetentionNoWrites(t *testing.T, fx api14RetentionFixture) {
	t.Helper()
	if err := fx.h.WithinTenant(context.Background(), fx.tenantID, func(tx *gorm.DB) error {
		for _, item := range []struct {
			name  string
			model any
		}{{"retention policies", &database.RetentionPolicy{}}, {"deletion requests", &database.DeletionRequest{}}, {"legal holds", &database.LegalHold{}}} {
			var count int64
			if err := tx.Model(item.model).Where("tenant_id=?", fx.tenantID).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				t.Errorf("unexpected %s mutation: count=%d", item.name, count)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func stringAPI14Data(response string) string {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &envelope); err != nil {
		return ""
	}
	return string(envelope.Data)
}
