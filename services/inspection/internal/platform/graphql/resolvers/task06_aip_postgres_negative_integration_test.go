//go:build integration

package resolvers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	dashboardcore "inspection/services/inspection/internal/features/dashboard/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

// aipPGFixture is the PostgreSQL/RLS fixture of the negative GraphQL cases of
// autonomous-inspection-platform task 06. Every read goes through the
// non-privileged runtime role: a tenant-less query sees nothing, exactly as in
// production, so a resolver that forgets the tenant transaction fails here.
type aipPGFixture struct {
	h                                 *harness.Harness
	server                            *handler.Server
	a, b                              harness.Task06Fixture
	secondInspectionA                 identity.ID
	snapshotA, snapshotB              identity.ID
	deliveryA1, deliveryA2, deliveryB identity.ID
	pastTimestamp                     time.Time
}

type aipPGOutcome struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []struct {
		Message    string `json:"message"`
		Extensions struct {
			Code  string `json:"code"`
			Field string `json:"field"`
		} `json:"extensions"`
	} `json:"errors"`
}

func aipPGContext(tenantID identity.ID, roles []string, audience, product string, entitlements []string, scopes ...requestctx.Scope) context.Context {
	return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, CorrelationID: "aip-pg", Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: roles, Scopes: scopes, Audience: audience, Product: product, ProductEntitlements: entitlements}})
}

// dashboardActor is an internal user of the dashboard product.
func dashboardActor(tenantID identity.ID, role string, scopes ...requestctx.Scope) context.Context {
	return aipPGContext(tenantID, []string{role}, "inspection-dashboard", auth.DashboardProduct, []string{auth.DashboardProduct}, scopes...)
}

func newAIPPGFixture(t *testing.T) *aipPGFixture {
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
	a, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	fx := &aipPGFixture{h: h, a: a, b: b, secondInspectionA: identity.NewID(), snapshotA: identity.NewID(), snapshotB: identity.NewID(), deliveryA1: identity.NewID(), deliveryA2: identity.NewID(), deliveryB: identity.NewID(), pastTimestamp: time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)}
	now := time.Now().UTC().Truncate(time.Microsecond)
	var template database.Inspection
	if err := h.AdminDB.Where("tenant_id=? AND id=?", a.TenantID, a.InspectionID).Take(&template).Error; err != nil {
		t.Fatal(err)
	}
	seedTenant := func(f harness.Task06Fixture, snapshot identity.ID, second *identity.ID) {
		t.Helper()
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			canonical, _ := json.Marshal(map[string]any{"classification": "CRITICAL", "findings": []any{}, "context": map[string]any{"asset": map[string]any{"id": f.AssetID.String(), "name": "Imóvel " + f.TenantID.String(), "externalKey": "K", "address": "Rua"}, "participant": map[string]any{"id": f.ParticipantID.String(), "name": "Responsável"}, "template": map[string]any{"id": f.TemplateID.String(), "name": "Modelo", "version": 1}, "inspection": map[string]any{"generatedAt": now.Format(time.RFC3339Nano)}}})
			rows := []any{
				&database.DashboardInspection{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, ProjectID: &f.ProjectID, AssetID: f.AssetID, Classification: "CRITICAL", Status: "COMPLETED", Sequence: 2, UpdatedAt: now},
				&database.ReportSnapshot{ID: snapshot, TenantID: f.TenantID, InspectionID: f.InspectionID, Mode: "HISTORICAL", Classification: "CRITICAL", JSONDigest: "j", HTMLDigest: "h", VersionNumber: 1, CanonicalJSON: canonical, HTML: json.RawMessage(`"<p>interno</p>"`), CreatedAt: now},
				&database.ReportArtifact{ID: identity.NewID(), TenantID: f.TenantID, SnapshotID: snapshot, Kind: "PDF", ObjectKey: "tenant/" + f.TenantID.String() + "/reports/" + snapshot.String() + ".pdf", SHA256: "pdf", Status: "READY", CreatedAt: now},
			}
			if second != nil {
				inspection := template
				inspection.ID, inspection.SourceKey, inspection.CreatedAt, inspection.UpdatedAt = *second, "aip-pg-"+second.String(), now, now
				rows = append(rows, &inspection,
					&database.DashboardInspection{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: *second, ProjectID: &f.ProjectID, AssetID: f.AssetID, Classification: "ATTENTION", Status: "COMPLETED", Sequence: 3, UpdatedAt: now.Add(-time.Minute)})
			}
			for _, row := range rows {
				if err := tx.Create(row).Error; err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	seedTenant(a, fx.snapshotA, &fx.secondInspectionA)
	seedTenant(b, fx.snapshotB, nil)
	seedDelivery := func(f harness.Task06Fixture, id, inspection identity.ID, status, destination, lastError string, at time.Time) {
		t.Helper()
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			if err := tx.Create(&database.Delivery{ID: id, TenantID: f.TenantID, IntentID: identity.NewID(), InspectionID: &inspection, Status: status, LogicalTemplate: "critical-alert", SelectedProvider: "smtp", CreatedAt: at, UpdatedAt: at}).Error; err != nil {
				return err
			}
			return tx.Create(&database.ChannelAttempt{ID: identity.NewID(), TenantID: f.TenantID, DeliveryID: id, Channel: "EMAIL", Destination: destination, Status: status, Provider: "smtp", Attempts: 1, LastError: lastError, CreatedAt: at, UpdatedAt: at, NextAttemptAt: at}).Error
		}); err != nil {
			t.Fatal(err)
		}
	}
	seedDelivery(a, fx.deliveryA1, a.InspectionID, "FAILED", "ana.souza@example.test", "PROVIDER_REJECTED", now)
	seedDelivery(a, fx.deliveryA2, fx.secondInspectionA, "DELIVERED", "carla.dias@example.test", "", now.Add(-time.Minute))
	seedDelivery(b, fx.deliveryB, b.InspectionID, "DELIVERED", "bruno.lima@example.test", "", now)
	fx.server = handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: h.DB, Bus: mediator.New(), Store: h.Store}}))
	fx.server.SetErrorPresenter(graph.PresentError)
	return fx
}

func (fx *aipPGFixture) run(t *testing.T, ctx context.Context, query string) (aipPGOutcome, string) {
	t.Helper()
	raw := executeTask06GraphQL(t, fx.server, query, ctx)
	var outcome aipPGOutcome
	if err := json.Unmarshal([]byte(raw), &outcome); err != nil {
		t.Fatalf("invalid GraphQL response: %v: %s", err, raw)
	}
	return outcome, raw
}

// foreign lists every identifier or value of tenant B that must never appear
// in a response served to tenant A.
func (fx *aipPGFixture) foreign() []string {
	return []string{fx.b.TenantID.String(), fx.b.InspectionID.String(), fx.b.ProjectID.String(), fx.b.AssetID.String(), fx.b.BusinessUnitID.String(), fx.snapshotB.String(), fx.deliveryB.String(), "bruno.lima@example.test", "b***@example.test"}
}

// foreignTo lists what the other tenant owns, whichever tenant the actor
// belongs to.
func (fx *aipPGFixture) foreignTo(ctx context.Context) []string {
	meta, _ := requestctx.FromContext(ctx)
	if meta.TenantID == fx.b.TenantID {
		return []string{fx.a.TenantID.String(), fx.a.InspectionID.String(), fx.secondInspectionA.String(), fx.a.ProjectID.String(), fx.a.AssetID.String(), fx.a.BusinessUnitID.String(), fx.snapshotA.String(), fx.deliveryA1.String(), fx.deliveryA2.String(), "ana.souza@example.test", "carla.dias@example.test"}
	}
	return fx.foreign()
}

// rejected asserts one stable error code with null data and no foreign or
// internal detail.
func (fx *aipPGFixture) rejected(t *testing.T, ctx context.Context, query, code, field string) {
	t.Helper()
	outcome, raw := fx.run(t, ctx, query)
	if len(outcome.Errors) == 0 {
		t.Fatalf("request was accepted, want %s: %s", code, raw)
	}
	if got := outcome.Errors[0]; got.Extensions.Code != code || (field != "" && got.Extensions.Field != field) {
		t.Fatalf("error = %s/%s, want %s/%s: %s", got.Extensions.Code, got.Extensions.Field, code, field, raw)
	}
	for _, value := range outcome.Data {
		if string(value) != "null" {
			t.Fatalf("rejected request returned data: %s", raw)
		}
	}
	for _, leak := range append(fx.foreign(), "pq:", "SQLSTATE", "gorm", "SELECT ", "uuid", "tenant_id") {
		if strings.Contains(raw, leak) {
			t.Fatalf("rejected response leaked %q: %s", leak, raw)
		}
	}
}

func (fx *aipPGFixture) summary(t *testing.T, ctx context.Context, arguments string) (total, critical, attention int) {
	t.Helper()
	outcome, raw := fx.run(t, ctx, `query { dashboardSummary`+arguments+` { total critical attention } }`)
	if len(outcome.Errors) != 0 {
		t.Fatalf("dashboardSummary failed: %s", raw)
	}
	var data struct{ Total, Critical, Attention int }
	if err := json.Unmarshal(outcome.Data["dashboardSummary"], &data); err != nil {
		t.Fatal(err)
	}
	for _, leak := range fx.foreignTo(ctx) {
		if strings.Contains(raw, leak) {
			t.Fatalf("summary leaked %q: %s", leak, raw)
		}
	}
	return data.Total, data.Critical, data.Attention
}

func (fx *aipPGFixture) triageIDs(t *testing.T, ctx context.Context, arguments string) []string {
	t.Helper()
	outcome, raw := fx.run(t, ctx, `query { triageInspections`+arguments+` { nodes { inspectionId } } }`)
	if len(outcome.Errors) != 0 {
		t.Fatalf("triageInspections failed: %s", raw)
	}
	var data struct {
		Nodes []struct {
			InspectionID string `json:"inspectionId"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(outcome.Data["triageInspections"], &data); err != nil {
		t.Fatal(err)
	}
	for _, leak := range fx.foreignTo(ctx) {
		if strings.Contains(raw, leak) {
			t.Fatalf("triage leaked %q: %s", leak, raw)
		}
	}
	ids := make([]string, 0, len(data.Nodes))
	for _, node := range data.Nodes {
		ids = append(ids, node.InspectionID)
	}
	return ids
}

// cursorFor builds a syntactically valid triage cursor whose last field is id.
func aipPGCursor(bucket int, at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d|%s|%s", bucket, at.UTC().Format(time.RFC3339Nano), id)))
}

// deniedProofs are principals that must be refused by every dashboard read.
func (fx *aipPGFixture) deniedProofs() map[string]struct {
	ctx  context.Context
	code string
} {
	tenant := fx.a.TenantID
	return map[string]struct {
		ctx  context.Context
		code string
	}{
		"no credentials":           {context.Background(), "UNAUTHENTICATED"},
		"customer viewer":          {dashboardActor(tenant, auth.CustomerViewer), "FORBIDDEN"},
		"role outside the product": {dashboardActor(tenant, auth.Auditor), "FORBIDDEN"},
		"unknown role":             {dashboardActor(tenant, "SUPPORT"), "FORBIDDEN"},
		"admin audience":           {aipPGContext(tenant, []string{auth.Manager}, "inspection-admin", auth.DashboardProduct, []string{auth.DashboardProduct}), "FORBIDDEN"},
		"admin product":            {aipPGContext(tenant, []string{auth.Manager}, "inspection-dashboard", auth.AdminProduct, []string{auth.AdminProduct}), "FORBIDDEN"},
		"revoked entitlement":      {aipPGContext(tenant, []string{auth.Manager}, "inspection-dashboard", auth.DashboardProduct, nil), "FORBIDDEN"},
	}
}

// IT-428 on PostgreSQL with the runtime role.
func TestAIP_PostgresIT428DashboardSummaryRejectsInvalidProofScopeInputAndState(t *testing.T) {
	fx := newAIPPGFixture(t)
	query := `query { dashboardSummary { total critical attention normal pending invalidated } }`
	for name, proof := range fx.deniedProofs() {
		t.Run("proof/"+name, func(t *testing.T) { fx.rejected(t, proof.ctx, query, proof.code, "") })
	}
	admin := dashboardActor(fx.a.TenantID, auth.TenantAdmin)
	for name, arguments := range map[string]string{
		"project is not an identifier":       `(projectId:"not-a-uuid")`,
		"business unit is not an identifier": `(businessUnitId:"0000")`,
		"empty project":                      `(projectId:"")`,
	} {
		field := "projectId"
		if strings.Contains(arguments, "businessUnitId") {
			field = "businessUnitId"
		}
		t.Run("input/"+name, func(t *testing.T) {
			fx.rejected(t, admin, `query { dashboardSummary`+arguments+` { total } }`, "INVALID_INPUT", field)
		})
	}
	t.Run("scope", func(t *testing.T) {
		if total, critical, attention := fx.summary(t, admin, ""); total != 2 || critical != 1 || attention != 1 {
			t.Fatalf("tenant administrator summary = %d/%d/%d, want 2/1/1", total, critical, attention)
		}
		if total, _, _ := fx.summary(t, dashboardActor(fx.a.TenantID, auth.Manager), ""); total != 0 {
			t.Fatalf("a manager without grants saw %d inspections", total)
		}
		granted := dashboardActor(fx.a.TenantID, auth.Manager, requestctx.Scope{Kind: "INSPECTION", ID: fx.a.InspectionID})
		if total, critical, attention := fx.summary(t, granted, ""); total != 1 || critical != 1 || attention != 0 {
			t.Fatalf("a manager granted one inspection saw %d/%d/%d, want 1/1/0", total, critical, attention)
		}
		foreignGrant := dashboardActor(fx.a.TenantID, auth.Manager, requestctx.Scope{Kind: "INSPECTION", ID: fx.b.InspectionID}, requestctx.Scope{Kind: "ASSET", ID: fx.b.AssetID}, requestctx.Scope{Kind: "PROJECT", ID: fx.b.ProjectID})
		if total, _, _ := fx.summary(t, foreignGrant, ""); total != 0 {
			t.Fatalf("grants naming tenant B resources exposed %d inspections to tenant A", total)
		}
		for _, filter := range []string{`(projectId:"` + fx.b.ProjectID.String() + `")`, `(businessUnitId:"` + fx.b.BusinessUnitID.String() + `")`, `(projectId:"` + identity.NewID().String() + `")`} {
			if total, _, _ := fx.summary(t, admin, filter); total != 0 {
				t.Fatalf("filter %s returned %d rows", filter, total)
			}
		}
		if total, _, _ := fx.summary(t, dashboardActor(fx.b.TenantID, auth.TenantAdmin), ""); total != 1 {
			t.Fatalf("tenant B summary = %d, want only its own inspection", total)
		}
	})
	t.Run("state", func(t *testing.T) {
		if err := fx.h.AdminDB.Model(&database.DashboardInspection{}).Where("tenant_id=? AND inspection_id=?", fx.a.TenantID, fx.secondInspectionA).Update("invalidated", true).Error; err != nil {
			t.Fatal(err)
		}
		outcome, raw := fx.run(t, admin, query)
		if len(outcome.Errors) != 0 {
			t.Fatal(raw)
		}
		var data struct{ Critical, Attention, Normal, Invalidated int }
		if err := json.Unmarshal(outcome.Data["dashboardSummary"], &data); err != nil {
			t.Fatal(err)
		}
		if data.Critical != 1 || data.Attention != 0 || data.Invalidated != 1 {
			t.Fatalf("an invalidated inspection still counts as a valid indicator: %+v", data)
		}
	})
}

// IT-430 on PostgreSQL with the runtime role.
func TestAIP_PostgresIT430TriageInspectionsRejectsInvalidProofScopeInputAndState(t *testing.T) {
	fx := newAIPPGFixture(t)
	query := `query { triageInspections(first:5) { nodes { inspectionId } } }`
	for name, proof := range fx.deniedProofs() {
		t.Run("proof/"+name, func(t *testing.T) { fx.rejected(t, proof.ctx, query, proof.code, "") })
	}
	admin := dashboardActor(fx.a.TenantID, auth.TenantAdmin)
	t.Run("input", func(t *testing.T) {
		for name, arguments := range map[string]string{
			"unknown classification":   `(classification:"SEVERE")`,
			"lowercase classification": `(classification:"critical")`,
			"cursor is not base64":     `(after:"%%%")`,
			"cursor has no fields":     `(after:"` + base64.RawURLEncoding.EncodeToString([]byte("abc")) + `")`,
			"cursor bucket is unknown": `(after:"` + aipPGCursor(7, fx.pastTimestamp, fx.a.InspectionID.String()) + `")`,
			"cursor time is invalid":   `(after:"` + base64.RawURLEncoding.EncodeToString([]byte("0|yesterday|"+fx.a.InspectionID.String())) + `")`,
			"cursor id is not a UUID":  `(after:"` + aipPGCursor(0, fx.pastTimestamp, "not-a-uuid") + `")`,
			"cursor id is injected":    `(after:"` + aipPGCursor(0, fx.pastTimestamp, "x' OR '1'='1") + `")`,
		} {
			t.Run(name, func(t *testing.T) {
				fx.rejected(t, admin, `query { triageInspections`+arguments+` { nodes { inspectionId } } }`, "INVALID_INPUT", "")
			})
		}
	})
	t.Run("scope", func(t *testing.T) {
		if ids := fx.triageIDs(t, admin, "(first:10)"); len(ids) != 2 {
			t.Fatalf("tenant administrator triage = %v, want both tenant A inspections", ids)
		}
		if ids := fx.triageIDs(t, dashboardActor(fx.a.TenantID, auth.Viewer), "(first:10)"); len(ids) != 0 {
			t.Fatalf("a viewer without grants saw %v", ids)
		}
		granted := dashboardActor(fx.a.TenantID, auth.Viewer, requestctx.Scope{Kind: "INSPECTION", ID: fx.secondInspectionA})
		if ids := fx.triageIDs(t, granted, "(first:10)"); len(ids) != 1 || ids[0] != fx.secondInspectionA.String() {
			t.Fatalf("a viewer granted one inspection saw %v", ids)
		}
		// A cursor positioned before every row cannot widen a scoped actor's view.
		wide := aipPGCursor(0, time.Now().UTC().Add(24*time.Hour), identity.NewID().String())
		if ids := fx.triageIDs(t, granted, `(first:10,after:"`+wide+`")`); len(ids) != 1 || ids[0] != fx.secondInspectionA.String() {
			t.Fatalf("a cursor widened the scoped view: %v", ids)
		}
		// A cursor minted for tenant B is only a position: tenant B rows never appear.
		foreignCursor := dashboardcore.EncodeTriageCursor(dashboardcore.Projection{InspectionID: fx.b.InspectionID.String(), Classification: "CRITICAL", UpdatedAt: time.Now().UTC().Add(time.Hour)})
		for _, id := range fx.triageIDs(t, admin, `(first:10,after:"`+foreignCursor+`")`) {
			if id == fx.b.InspectionID.String() {
				t.Fatal("a tenant B cursor surfaced a tenant B inspection")
			}
		}
		if ids := fx.triageIDs(t, dashboardActor(fx.b.TenantID, auth.TenantAdmin), "(first:10)"); len(ids) != 1 || ids[0] != fx.b.InspectionID.String() {
			t.Fatalf("tenant B triage = %v", ids)
		}
	})
	t.Run("state", func(t *testing.T) {
		if err := fx.h.AdminDB.Model(&database.DashboardInspection{}).Where("tenant_id=? AND inspection_id=?", fx.a.TenantID, fx.secondInspectionA).Update("invalidated", true).Error; err != nil {
			t.Fatal(err)
		}
		if ids := fx.triageIDs(t, admin, "(first:10)"); len(ids) != 1 || ids[0] != fx.a.InspectionID.String() {
			t.Fatalf("an invalidated inspection is still triaged: %v", ids)
		}
		if ids := fx.triageIDs(t, admin, `(first:10,classification:"NORMAL")`); len(ids) != 0 {
			t.Fatalf("classification filter returned %v", ids)
		}
	})
}

// IT-434 on PostgreSQL with the runtime role.
func TestAIP_PostgresIT434NotificationDeliveriesRejectsInvalidProofScopeInputAndState(t *testing.T) {
	fx := newAIPPGFixture(t)
	query := `query { notificationDeliveries(first:5) { nodes { id } } }`
	proofs := fx.deniedProofs()
	// Deliveries are an internal operational read: the audience is not part
	// of the contract, so only the credential and the role are asserted.
	for name, proof := range proofs {
		if name == "admin audience" || name == "admin product" || name == "revoked entitlement" {
			continue
		}
		t.Run("proof/"+name, func(t *testing.T) { fx.rejected(t, proof.ctx, query, proof.code, "") })
	}
	admin := dashboardActor(fx.a.TenantID, auth.TenantAdmin)
	t.Run("input", func(t *testing.T) {
		for name, after := range map[string]string{
			"cursor is not base64":    "%%%",
			"cursor has no fields":    base64.RawURLEncoding.EncodeToString([]byte("abc")),
			"cursor time is invalid":  base64.RawURLEncoding.EncodeToString([]byte("yesterday|" + fx.deliveryA1.String())),
			"cursor id is not a UUID": base64.RawURLEncoding.EncodeToString([]byte(fx.pastTimestamp.Format(time.RFC3339Nano) + "|not-a-uuid")),
		} {
			t.Run(name, func(t *testing.T) {
				fx.rejected(t, admin, `query { notificationDeliveries(first:5,after:"`+after+`") { nodes { id } } }`, "INVALID_INPUT", "")
			})
		}
	})
	listed := func(ctx context.Context, arguments string) []string {
		t.Helper()
		outcome, raw := fx.run(t, ctx, `query { notificationDeliveries`+arguments+` { nodes { id inspectionId recipientMasked failureCode channels { id channel status } } } }`)
		if len(outcome.Errors) != 0 {
			t.Fatalf("notificationDeliveries failed: %s", raw)
		}
		for _, leak := range append(fx.foreignTo(ctx), "ana.souza@example.test", "carla.dias@example.test", "bruno.lima@example.test") {
			if strings.Contains(raw, leak) {
				t.Fatalf("delivery list leaked %q: %s", leak, raw)
			}
		}
		var data struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		}
		if err := json.Unmarshal(outcome.Data["notificationDeliveries"], &data); err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(data.Nodes))
		for _, node := range data.Nodes {
			ids = append(ids, node.ID)
		}
		return ids
	}
	t.Run("scope", func(t *testing.T) {
		if ids := listed(admin, "(first:10)"); len(ids) != 2 {
			t.Fatalf("tenant administrator deliveries = %v, want both tenant A deliveries", ids)
		}
		if ids := listed(dashboardActor(fx.a.TenantID, auth.Viewer), "(first:10)"); len(ids) != 0 {
			t.Fatalf("a viewer without grants saw deliveries %v", ids)
		}
		granted := dashboardActor(fx.a.TenantID, auth.Manager, requestctx.Scope{Kind: "INSPECTION", ID: fx.secondInspectionA})
		if ids := listed(granted, "(first:10)"); len(ids) != 1 || ids[0] != fx.deliveryA2.String() {
			t.Fatalf("a manager granted one inspection saw deliveries %v, want only %s", ids, fx.deliveryA2)
		}
		foreignCursor := dashboardcore.EncodeCursor(dashboardcore.Projection{InspectionID: fx.deliveryB.String(), UpdatedAt: time.Now().UTC().Add(time.Hour)})
		for _, id := range listed(admin, `(first:10,after:"`+foreignCursor+`")`) {
			if id == fx.deliveryB.String() {
				t.Fatal("a tenant B cursor surfaced a tenant B delivery")
			}
		}
		if ids := listed(dashboardActor(fx.b.TenantID, auth.TenantAdmin), "(first:10)"); len(ids) != 1 || ids[0] != fx.deliveryB.String() {
			t.Fatalf("tenant B deliveries = %v", ids)
		}
	})
	t.Run("state", func(t *testing.T) {
		// Safe projection of a failed delivery: masked recipient and a stable
		// failure code, resolved inside the tenant transaction.
		outcome, raw := fx.run(t, admin, `query { notificationDeliveries(first:10) { nodes { id status recipientMasked failureCode } } }`)
		if len(outcome.Errors) != 0 {
			t.Fatal(raw)
		}
		var data struct {
			Nodes []struct {
				ID, Status      string
				RecipientMasked *string
				FailureCode     *string
			} `json:"nodes"`
		}
		if err := json.Unmarshal(outcome.Data["notificationDeliveries"], &data); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, node := range data.Nodes {
			if node.ID != fx.deliveryA1.String() {
				continue
			}
			found = true
			if node.Status != "FAILED" || node.RecipientMasked == nil || *node.RecipientMasked == "" || strings.Contains(*node.RecipientMasked, "ana.souza") {
				t.Fatalf("failed delivery recipient is not masked: %+v", node)
			}
			if node.FailureCode == nil || *node.FailureCode != "PROVIDER_REJECTED" {
				t.Fatalf("failed delivery has no failure code under the runtime role: %+v", node)
			}
		}
		if !found {
			t.Fatalf("failed delivery missing: %s", raw)
		}
	})
}

// A tenant-less read through the runtime role proves the fixture is real:
// forced RLS hides every row that a resolver does not fetch inside a tenant
// transaction.
func TestAIP_PostgresRuntimeRoleSeesNothingWithoutTenantContext(t *testing.T) {
	fx := newAIPPGFixture(t)
	var inspections, deliveries int64
	if err := fx.h.DB.Model(&database.Inspection{}).Count(&inspections).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.h.DB.Model(&database.Delivery{}).Count(&deliveries).Error; err != nil {
		t.Fatal(err)
	}
	if inspections != 0 || deliveries != 0 {
		t.Fatalf("runtime role read %d inspections and %d deliveries without a tenant context", inspections, deliveries)
	}
}

// Cross-tenant reads of the dashboard and report resolvers: a tenant A actor
// referencing tenant B's identifiers learns nothing, and the stable
// not-found code is the same as for an identifier that never existed.
func TestAIP_PostgresCrossTenantDashboardAndReportResolversRevealNothing(t *testing.T) {
	fx := newAIPPGFixture(t)
	admin := dashboardActor(fx.a.TenantID, auth.TenantAdmin)
	for name, query := range map[string]string{
		"report":          `query { report(inspectionId:"` + fx.b.InspectionID.String() + `") { id } }`,
		"reportDownload":  `query { reportDownload(snapshotId:"` + fx.snapshotB.String() + `") { snapshotId url } }`,
		"projectTimeline": `query { projectTimeline(projectId:"` + fx.b.ProjectID.String() + `") { projectId } }`,
	} {
		t.Run(name, func(t *testing.T) {
			fx.rejected(t, admin, query, "NOT_FOUND", "")
			// The response for a foreign identifier equals the one for an unknown one.
			unknown := strings.NewReplacer(fx.b.InspectionID.String(), identity.NewID().String(), fx.snapshotB.String(), identity.NewID().String(), fx.b.ProjectID.String(), identity.NewID().String()).Replace(query)
			foreign, _ := fx.run(t, admin, query)
			missing, _ := fx.run(t, admin, unknown)
			if len(foreign.Errors) == 0 || len(missing.Errors) == 0 || foreign.Errors[0].Message != missing.Errors[0].Message || foreign.Errors[0].Extensions.Field != missing.Errors[0].Extensions.Field {
				t.Fatalf("foreign and unknown identifiers are distinguishable: %+v vs %+v", foreign.Errors, missing.Errors)
			}
		})
	}
	scopedElsewhere := dashboardActor(fx.a.TenantID, auth.Manager, requestctx.Scope{Kind: "INSPECTION", ID: fx.secondInspectionA})
	for name, query := range map[string]string{
		"report outside the grant":         `query { report(inspectionId:"` + fx.a.InspectionID.String() + `") { id } }`,
		"reportDownload outside the grant": `query { reportDownload(snapshotId:"` + fx.snapshotA.String() + `") { snapshotId url } }`,
	} {
		t.Run(name, func(t *testing.T) { fx.rejected(t, scopedElsewhere, query, "NOT_FOUND", "") })
	}
	// The same documents are served to their own tenant.
	if outcome, raw := fx.run(t, admin, `query { report(inspectionId:"`+fx.a.InspectionID.String()+`") { id version } reportDownload(snapshotId:"`+fx.snapshotA.String()+`") { snapshotId status } }`); len(outcome.Errors) != 0 {
		t.Fatalf("tenant A cannot read its own report: %s", raw)
	}
	other := dashboardActor(fx.b.TenantID, auth.TenantAdmin)
	fx.rejected(t, other, `query { report(inspectionId:"`+fx.a.InspectionID.String()+`") { id } }`, "NOT_FOUND", "")
}
