// Package accesstest provides an isolated PostgreSQL fixture for the access
// slices and the authorization boundary. It only runs when the isolated test
// database variables are set, so default test runs skip it.
package accesstest

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

// Env holds the privileged and runtime-role connections to the isolated
// database. Admin bypasses row-level security for seeding and assertions;
// Runtime is what the application uses and is bound by tenant isolation.
type Env struct {
	Admin   *gorm.DB
	Runtime *gorm.DB
}

// Open connects to the isolated database and applies every migration. It skips
// the test when the isolated URLs are not configured.
func Open(t testing.TB) Env {
	t.Helper()
	adminDSN, runtimeDSN := os.Getenv("INSPECTION_TEST_DATABASE_URL"), os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL")
	if adminDSN == "" || runtimeDSN == "" {
		t.Skip("INSPECTION_TEST_DATABASE_URL and INSPECTION_TEST_RUNTIME_DATABASE_URL not set")
	}
	admin, err := database.Open(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := (database.Migrator{DB: admin}).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	runtime, err := database.Open(runtimeDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, db := range []*gorm.DB{runtime, admin} {
			if sqlDB, err := db.DB(); err == nil {
				_ = sqlDB.Close()
			}
		}
	})
	return Env{Admin: admin, Runtime: runtime}
}

// Hierarchy is one seeded tenant with a canonical resource tree:
//
//	UnitA -> AssetA1 -> ProjectA1 -> InspectionA1 (in project)
//	UnitA -> AssetA2 -> InspectionA2 (no project)
//	UnitB -> AssetB1 -> InspectionB1
type Hierarchy struct {
	TenantID                                 identity.ID
	UnitA, UnitB                             identity.ID
	AssetA1, AssetA2, AssetB1                identity.ID
	ProjectA1                                identity.ID
	InspectionA1, InspectionA2, InspectionB1 identity.ID
}

// SeedTenant creates an ACTIVE tenant and its resource tree.
func (e Env) SeedTenant(t testing.TB) Hierarchy {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	h := Hierarchy{TenantID: identity.NewID(), UnitA: identity.NewID(), UnitB: identity.NewID(), AssetA1: identity.NewID(), AssetA2: identity.NewID(), AssetB1: identity.NewID(), ProjectA1: identity.NewID(), InspectionA1: identity.NewID(), InspectionA2: identity.NewID(), InspectionB1: identity.NewID()}
	create := func(model any) {
		t.Helper()
		if err := e.Admin.Create(model).Error; err != nil {
			t.Fatal(err)
		}
	}
	create(&database.Tenant{ID: h.TenantID, TenantID: h.TenantID, Name: "Imobiliária de teste", Language: "pt-BR", DefaultTimezone: "UTC", Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now})
	create(&database.BusinessUnit{ID: h.UnitA, TenantID: h.TenantID, Code: "A-" + h.UnitA.String()[:8], Name: "Unidade A", Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now})
	create(&database.BusinessUnit{ID: h.UnitB, TenantID: h.TenantID, Code: "B-" + h.UnitB.String()[:8], Name: "Unidade B", Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now})
	e.AddAsset(t, h.TenantID, h.UnitA, h.AssetA1, "Imóvel A1")
	e.AddAsset(t, h.TenantID, h.UnitA, h.AssetA2, "Imóvel A2")
	e.AddAsset(t, h.TenantID, h.UnitB, h.AssetB1, "Imóvel B1")
	create(&database.Project{ID: h.ProjectA1, TenantID: h.TenantID, BusinessUnitID: h.UnitA, AssetID: h.AssetA1, Name: "Obra A1", Status: "ACTIVE", Version: 1, IdempotencyKey: "project-" + h.ProjectA1.String(), CreatedAt: now, UpdatedAt: now})
	e.AddInspection(t, h.TenantID, h.UnitA, h.AssetA1, &h.ProjectA1, h.InspectionA1)
	e.AddInspection(t, h.TenantID, h.UnitA, h.AssetA2, nil, h.InspectionA2)
	e.AddInspection(t, h.TenantID, h.UnitB, h.AssetB1, nil, h.InspectionB1)
	return h
}

// AddAsset inserts one active asset under a business unit.
func (e Env) AddAsset(t testing.TB, tenantID, unitID, assetID identity.ID, name string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := e.Admin.Create(&database.Asset{ID: assetID, TenantID: tenantID, BusinessUnitID: unitID, SegmentVersionID: identity.NewID(), Name: name, ExternalKey: "asset-" + assetID.String(), Address: "Rua de teste", GeofenceMeters: 150, PolicyOverrides: json.RawMessage(`{}`), Status: "ACTIVE", Version: 1, IdempotencyKey: "asset-" + assetID.String(), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
}

// AddProject inserts one active project.
func (e Env) AddProject(t testing.TB, tenantID, unitID, assetID, projectID identity.ID) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := e.Admin.Create(&database.Project{ID: projectID, TenantID: tenantID, BusinessUnitID: unitID, AssetID: assetID, Name: "Obra " + projectID.String()[:8], Status: "ACTIVE", Version: 1, IdempotencyKey: "project-" + projectID.String(), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
}

// AddInspection inserts one scheduled inspection.
func (e Env) AddInspection(t testing.TB, tenantID, unitID, assetID identity.ID, projectID *identity.ID, inspectionID identity.ID) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := e.Admin.Create(&database.Inspection{ID: inspectionID, TenantID: tenantID, BusinessUnitID: unitID, AssetID: assetID, ParticipantID: identity.NewID(), TemplateID: identity.NewID(), TemplateVersionID: identity.NewID(), AnalysisPromptSnapshotID: identity.NewID(), ProjectID: projectID, Source: "MANUAL", SourceKey: "inspection-" + inspectionID.String(), Status: "PLANNED", MediaKind: "PHOTO", DueAt: now, DeadlineAt: now.Add(24 * time.Hour), ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{}`), Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
}

// Grant is one explicit scope for AddMembership.
type Grant struct {
	Kind       string
	ResourceID identity.ID
}

// AddMembership inserts a membership with the given product entitlements and
// explicit grants, owned by a fresh OIDC subject.
func (e Env) AddMembership(t testing.TB, tenantID identity.ID, role, status string, products []string, grants ...Grant) database.Membership {
	t.Helper()
	return e.AddMembershipFor(t, tenantID, "subject-"+identity.NewID().String(), role, status, products, grants...)
}

// AddMembershipFor inserts a membership for an explicit OIDC subject, so one
// identity can hold memberships in several tenants.
func (e Env) AddMembershipFor(t testing.TB, tenantID identity.ID, subject, role, status string, products []string, grants ...Grant) database.Membership {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	membership := database.Membership{ID: identity.NewID(), TenantID: tenantID, IdentityID: identity.NewDeterministicID("inspection/oidc-identity", "https://issuer.test\x00"+subject), Issuer: "https://issuer.test", Subject: subject, Name: "Pessoa " + role, Email: subject + "@example.test", Role: role, Status: status, InvitationStatus: "NONE", Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := e.Admin.Create(&membership).Error; err != nil {
		t.Fatal(err)
	}
	for _, product := range products {
		if err := e.Admin.Create(&database.ProductEntitlement{ID: identity.NewID(), TenantID: tenantID, MembershipID: membership.ID, Product: product, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, grant := range grants {
		if err := e.Admin.Create(&database.ResourceScope{ID: identity.NewID(), TenantID: tenantID, MembershipID: membership.ID, Kind: grant.Kind, ResourceID: grant.ResourceID, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return membership
}

// Archive marks one resource ARCHIVED through the privileged connection.
func (e Env) Archive(t testing.TB, table string, tenantID, id identity.ID) {
	t.Helper()
	if err := e.Admin.Exec("UPDATE "+table+" SET status='ARCHIVED' WHERE tenant_id=? AND id=?", tenantID, id).Error; err != nil {
		t.Fatal(err)
	}
}

// Count returns the number of rows of table for the tenant matching where.
func (e Env) Count(t testing.TB, table string, tenantID identity.ID, where string, args ...any) int64 {
	t.Helper()
	var count int64
	query := "SELECT count(*) FROM " + table + " WHERE tenant_id=?"
	if where != "" {
		query += " AND " + where
	}
	if err := e.Admin.Raw(query, append([]any{tenantID}, args...)...).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

// Authorizer returns the production authorizer wiring: current-state
// membership reload and hierarchy-aware scope resolution through the runtime
// role.
func (e Env) Authorizer() auth.Authorizer {
	return auth.Authorizer{Store: auth.GORMMembershipStore{DB: e.Runtime}, Scopes: auth.GORMScopeResolver{DB: e.Runtime}}
}

// Context returns a request context for membership as if the verified token
// carried audience (for example "inspection-admin"). Roles and entitlements are
// deliberately not copied: the authorizer must reload them.
func Context(membership database.Membership, audience string) context.Context {
	product := ""
	switch audience {
	case "inspection-admin":
		product = auth.AdminProduct
	case "inspection-dashboard":
		product = auth.DashboardProduct
	}
	return requestctx.WithMetadata(context.Background(), requestctx.Metadata{
		TenantID:      membership.TenantID,
		CorrelationID: "access-test",
		Principal:     requestctx.Principal{IdentityID: membership.IdentityID, TenantID: membership.TenantID, Audience: audience, Product: product},
	})
}
