//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
)

func adminProductActor(tenantID identity.ID, role string) context.Context {
	return aipPGContext(tenantID, []string{role}, "inspection-admin", auth.AdminProduct, []string{auth.AdminProduct})
}

func (fx *aipPGFixture) policyRows(t *testing.T, tenantID identity.ID) []database.RetentionPolicy {
	t.Helper()
	var rows []database.RetentionPolicy
	if err := fx.h.AdminDB.Where("tenant_id=?", tenantID).Order("version ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

// IT-301 through GraphQL: the save is rejected before any write.
func TestAIP_PostgresIT301ConfigureRetentionPolicyRejectsDisallowedPeriods(t *testing.T) {
	fx := newAIPPGFixture(t)
	admin := adminProductActor(fx.a.TenantID, auth.TenantAdmin)
	for name, input := range map[string]struct{ input, field string }{
		"zero evidence":         {`evidenceDays:0,operationalDays:365`, "evidenceDays"},
		"negative evidence":     {`evidenceDays:-1,operationalDays:365`, "evidenceDays"},
		"evidence overflow":     {`evidenceDays:200000,operationalDays:365`, "evidenceDays"},
		"negative operational":  {`evidenceDays:1825,operationalDays:-30`, "operationalDays"},
		"negative security":     {`evidenceDays:1825,operationalDays:365,securityDays:-1`, "securityDays"},
		"operational overflow":  {`evidenceDays:1825,operationalDays:999999`, "operationalDays"},
		"security above limits": {`evidenceDays:1825,operationalDays:365,securityDays:99999`, "securityDays"},
	} {
		t.Run(name, func(t *testing.T) {
			fx.rejected(t, admin, `mutation { configureRetentionPolicy(input:{`+input.input+`,clientMutationId:"it301"}) { policy { version } } }`, "INVALID_INPUT", input.field)
		})
	}
	if rows := fx.policyRows(t, fx.a.TenantID); len(rows) != 0 {
		t.Fatalf("rejected saves wrote %d policy rows", len(rows))
	}
}

// IT-302 through GraphQL: without a custom policy the platform defaults are
// shown, and a saved policy replaces them as a new visible version.
func TestAIP_PostgresIT302RetentionPoliciesShowDefaultsThenVersions(t *testing.T) {
	fx := newAIPPGFixture(t)
	admin := adminProductActor(fx.a.TenantID, auth.TenantAdmin)
	read := func() []struct{ EvidenceDays, OperationalDays, SecurityDays, Version int } {
		t.Helper()
		outcome, raw := fx.run(t, admin, `query { retentionPolicies { nodes { evidenceDays operationalDays securityDays version } } }`)
		if len(outcome.Errors) != 0 {
			t.Fatal(raw)
		}
		var data struct {
			Nodes []struct{ EvidenceDays, OperationalDays, SecurityDays, Version int } `json:"nodes"`
		}
		if err := json.Unmarshal(outcome.Data["retentionPolicies"], &data); err != nil {
			t.Fatal(err)
		}
		return data.Nodes
	}
	if nodes := read(); len(nodes) != 1 || nodes[0].EvidenceDays != 1825 || nodes[0].OperationalDays != 365 || nodes[0].SecurityDays != 0 || nodes[0].Version != 1 {
		t.Fatalf("defaults shown = %+v, want five years / one year / secrets at their deadline", nodes)
	}
	if rows := fx.policyRows(t, fx.a.TenantID); len(rows) != 0 {
		t.Fatalf("reading the defaults wrote %d rows", len(rows))
	}
	for i, days := range []int{1000, 1200} {
		query := `mutation { configureRetentionPolicy(input:{evidenceDays:` + itoa(days) + `,operationalDays:200,securityDays:30,clientMutationId:"it302-` + itoa(i) + `"}) { policy { version evidenceDays } } }`
		if outcome, raw := fx.run(t, admin, query); len(outcome.Errors) != 0 || !strings.Contains(raw, `"version":`+itoa(i+1)) {
			t.Fatalf("save %d failed or skipped a version: %s", i+1, raw)
		}
	}
	nodes := read()
	if len(nodes) != 2 || nodes[0].Version != 2 || nodes[0].EvidenceDays != 1200 || nodes[1].Version != 1 || nodes[1].EvidenceDays != 1000 {
		t.Fatalf("policy history (newest first) = %+v", nodes)
	}
	// Another tenant keeps the platform defaults.
	other := adminProductActor(fx.b.TenantID, auth.TenantAdmin)
	outcome, raw := fx.run(t, other, `query { retentionPolicies { nodes { evidenceDays version } } }`)
	if len(outcome.Errors) != 0 || !strings.Contains(raw, `"evidenceDays":1825`) || strings.Contains(raw, "1200") {
		t.Fatalf("tenant B did not keep the defaults: %s", raw)
	}
}

// IT-304: only the tenant administrator changes the policy; reading a policy
// or requesting deletion stays denied to roles without management rights.
func TestAIP_PostgresIT304NonAdministratorsCannotChangeRetention(t *testing.T) {
	fx := newAIPPGFixture(t)
	for _, role := range []string{auth.Manager, auth.Employee, auth.Viewer, auth.CustomerViewer, auth.Auditor, "SUPPORT"} {
		actor := adminProductActor(fx.a.TenantID, role)
		t.Run("configure/"+role, func(t *testing.T) {
			fx.rejected(t, actor, `mutation { configureRetentionPolicy(input:{evidenceDays:30,operationalDays:30,clientMutationId:"it304"}) { policy { version } } }`, "FORBIDDEN", "")
		})
	}
	for _, role := range []string{auth.Employee, auth.Viewer, auth.CustomerViewer} {
		actor := adminProductActor(fx.a.TenantID, role)
		for name, query := range map[string]string{
			"deletion": `mutation { recordDeletionRequest(input:{inspectionId:"` + fx.a.InspectionID.String() + `",reason:"x",clientMutationId:"it304"}) { status } }`,
			"hold":     `mutation { applyLegalHold(input:{inspectionId:"` + fx.a.InspectionID.String() + `",reason:"x",clientMutationId:"it304"}) { status } }`,
			"release":  `mutation { releaseLegalHold(input:{inspectionId:"` + fx.a.InspectionID.String() + `",reason:"x",clientMutationId:"it304"}) { status } }`,
		} {
			t.Run(name+"/"+role, func(t *testing.T) { fx.rejected(t, actor, query, "FORBIDDEN", "") })
		}
	}
	fx.rejected(t, context.Background(), `mutation { configureRetentionPolicy(input:{evidenceDays:30,operationalDays:30,clientMutationId:"it304"}) { policy { version } } }`, "UNAUTHENTICATED", "")
	if rows := fx.policyRows(t, fx.a.TenantID); len(rows) != 0 {
		t.Fatalf("denied callers wrote %d policy rows", len(rows))
	}
	var requests, holds int64
	if err := fx.h.AdminDB.Model(&database.DeletionRequest{}).Where("tenant_id=?", fx.a.TenantID).Count(&requests).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.h.AdminDB.Model(&database.LegalHold{}).Where("tenant_id=?", fx.a.TenantID).Count(&holds).Error; err != nil {
		t.Fatal(err)
	}
	if requests != 0 || holds != 0 {
		t.Fatalf("denied callers created %d deletion requests and %d holds", requests, holds)
	}
	// The administrator is not affected by the denials.
	if outcome, raw := fx.run(t, adminProductActor(fx.a.TenantID, auth.TenantAdmin), `mutation { configureRetentionPolicy(input:{evidenceDays:365,operationalDays:90,clientMutationId:"it304-ok"}) { policy { version } } }`); len(outcome.Errors) != 0 {
		t.Fatalf("administrator denied: %s", raw)
	}
}

func itoa(value int) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
