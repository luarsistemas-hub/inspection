//go:build integration

package resolvers

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestTask01ScopeGraphQLTransportPostgres(t *testing.T) {
	t.Run("pinned scope confirm success stale and frozen", func(t *testing.T) {
		fx := newInspectionModeGraphQLFixtureWithSchemaAndVideo(t, false, catalog.ChecklistOnly, catalog.DescriptiveSchemaVersion, false)
		inspectionID := createTask01ScopeInspection(t, fx, "task01-scope-transport")
		read := executeTask06GraphQL(t, fx.server, fmt.Sprintf(`query { inspection(id:%q) { id scope { profile rooms { key } version digest } } }`, inspectionID), fx.ctx)
		if !containsAll(read, `"profile":"PROPERTY_COMPONENTS"`, `"version":1`, `"digest":`) {
			t.Fatalf("IT-003 pinned scope read failed: %s", read)
		}
		confirm := func(profile string, expected int, id string) string {
			return executeTask06GraphQL(t, fx.server, fmt.Sprintf(`mutation { confirmInspectionScope(input:{inspectionId:%q,scopeOverride:{profile:%q,selections:[]},expectedVersion:%d,clientMutationId:%q}) { scope userErrors { code field message } clientMutationId } }`, inspectionID, profile, expected, id), fx.ctx)
		}
		updated := confirm("FURNISHED_PROPERTY", 1, "task01-scope-confirm")
		if !containsAll(updated, `"userErrors":[]`, `"profile":"FURNISHED_PROPERTY"`) {
			t.Fatalf("IT-003 confirmInspectionScope failed: %s", updated)
		}
		stale := confirm("PROPERTY_COMPONENTS", 1, "task01-scope-stale")
		if !containsAll(stale, `"code":"CONFLICT"`, `"field":"expectedVersion"`) {
			t.Fatalf("IT-030 stale scope revision was not rejected: %s", stale)
		}
		if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
			var responsibility database.Responsibility
			if err := tx.Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, inspectionID).Take(&responsibility).Error; err != nil {
				return err
			}
			var draft database.CaptureDraft
			if err := tx.Where("tenant_id=? AND responsibility_id=?", fx.seed.TenantID, responsibility.ID).Take(&draft).Error; err != nil {
				return err
			}
			return tx.Create(&database.SubmissionVersion{ID: identity.NewID(), TenantID: fx.seed.TenantID, DraftID: draft.ID, ResponsibilityID: responsibility.ID, VersionNumber: 1, Complete: true, Payload: []byte(`{}`), PayloadDigest: strings.Repeat("b", 64), SubmittedAt: time.Now().UTC()}).Error
		}); err != nil {
			t.Fatal(err)
		}
		frozen := confirm("PROPERTY_COMPONENTS", 2, "task01-scope-frozen")
		if !containsAll(frozen, `"code":"INVALID_STATE"`, "início da captura") {
			t.Fatalf("scope change after submission was not rejected: %s", frozen)
		}
	})

	t.Run("legacy inspection synthesizes undefined scope", func(t *testing.T) {
		fx := newInspectionModeGraphQLFixtureWithSchemaAndVideo(t, false, catalog.ChecklistOnly, catalog.EnvironmentSchemaVersion, false)
		inspectionID := createTask01ScopeInspection(t, fx, "task01-scope-legacy")
		if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, inspectionID).Delete(&database.InspectionScopeSnapshot{}).Error
		}); err != nil {
			t.Fatal(err)
		}
		response := executeTask06GraphQL(t, fx.server, fmt.Sprintf(`query { inspection(id:%q) { id scope { profile rooms { key } version digest } } }`, inspectionID), fx.ctx)
		if !containsAll(response, `"profile":"UNDEFINED"`, `"rooms":[]`, `"version":1`, `"digest":`) {
			t.Fatalf("legacy scope should be synthesized as UNDEFINED (Escopo não definido): %s", response)
		}
	})

	t.Run("tenant B cannot read or confirm tenant A scope", func(t *testing.T) {
		fx := newInspectionModeGraphQLFixtureWithSchemaAndVideo(t, false, catalog.ChecklistOnly, catalog.DescriptiveSchemaVersion, false)
		inspectionID := createTask01ScopeInspection(t, fx, "task01-scope-tenant-a")
		other, err := fx.h.SeedTask06Fixture(context.Background(), harness.Task06FixtureOptions{})
		if err != nil {
			t.Fatal(err)
		}
		tenantB := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: other.TenantID, Principal: requestctx.Principal{IdentityID: other.IdentityID, TenantID: other.TenantID, Roles: []string{auth.TenantAdmin}, ProductEntitlements: auth.ProductsForRole(auth.TenantAdmin)}})
		read := executeTask06GraphQL(t, fx.server, fmt.Sprintf(`query { inspection(id:%q) { id scope { profile digest } } }`, inspectionID), tenantB)
		if !strings.Contains(read, `"inspection":null`) {
			t.Fatalf("tenant B read tenant A inspection scope: %s", read)
		}
		confirm := executeTask06GraphQL(t, fx.server, fmt.Sprintf(`mutation { confirmInspectionScope(input:{inspectionId:%q,scopeOverride:{profile:"FURNISHED_PROPERTY",selections:[]},expectedVersion:1,clientMutationId:"task01-scope-cross-tenant"}) { scope userErrors { code field } } }`, inspectionID), tenantB)
		if !containsAll(confirm, `"code":"NOT_FOUND"`, `"field":"inspectionId"`) {
			t.Fatalf("tenant B confirmation did not hide tenant A scope: %s", confirm)
		}
	})
}

func createTask01ScopeInspection(t *testing.T, fx fixedOriginGraphQLFixture, clientID string) string {
	t.Helper()
	query := fmt.Sprintf(`mutation { createInspection(input:{assetId:%q,participantId:%q,templateId:%q,comparisonMode:CHECKLIST_ONLY,dueAt:"2026-10-17T12:00:00Z",deadlineAt:"2026-10-18T12:00:00Z",reminderInstants:[],reason:%q,clientMutationId:%q}) { inspection { id scope { profile version digest } } userErrors { code field message } } }`, fx.seed.AssetID, fx.seed.ParticipantID, fx.templateID, clientID, clientID)
	response := executeTask06GraphQL(t, fx.server, query, fx.ctx)
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"userErrors":[]`) {
		t.Fatalf("scope inspection setup failed: %s", response)
	}
	start := strings.Index(response, `"id":"`)
	if start < 0 {
		t.Fatalf("scope inspection ID missing: %s", response)
	}
	start += len(`"id":"`)
	end := strings.Index(response[start:], `"`)
	if end < 0 {
		t.Fatalf("scope inspection ID malformed: %s", response)
	}
	return response[start : start+end]
}
